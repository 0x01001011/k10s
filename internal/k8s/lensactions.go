package k8s

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/lens"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/jsonpath"
	"k8s.io/client-go/util/retry"
)

const lensActionTimeout = 15 * time.Second

// lensVars builds the template values for one object.
func (s *Store) lensVars(ns, name, selected string, params map[string]string) lens.Vars {
	ctxName := ""
	if s.c != nil {
		ctxName = s.c.CurrentContext
	}
	return lens.Vars{
		Name:      name,
		Namespace: ns,
		Context:   ctxName,
		Now:       time.Now().UTC().Format(time.RFC3339),
		Selected:  selected,
		Params:    params,
	}
}

// LensActions describes the verbs available on one lens row.
//
// An action the user cannot currently run is returned DISABLED with a reason
// rather than omitted: a button that vanishes is a mystery, and one that fires
// against the wrong object is worse.
func (s *Store) LensActions(kind, ns, name, selected string) []domain.LensActionSpec {
	lk, ok := s.lensKindFor(kind)
	if !ok {
		return nil
	}
	v := s.lensVars(ns, name, selected, nil)
	// Read the object ONCE for the whole pane rather than per parameter: the
	// pane is rebuilt whenever the selection moves, and every action on a
	// CNPG cluster sources its instance list from the same object.
	obj := s.cachedLensObject(lk, ns, name)
	out := make([]domain.LensActionSpec, 0, len(lk.kind.Allowed))
	for _, id := range lk.kind.Allowed {
		a, ok := lk.pack.Action(id)
		if !ok {
			continue // a builtin verb; the existing dispatch owns it
		}
		spec := domain.LensActionSpec{
			ID:           a.ID,
			Label:        a.Label,
			Confirm:      a.Confirm,
			Notice:       a.Notice,
			AckPath:      a.Ack,
			ConfirmValue: a.ConfirmValue,
			Kubectl:      lens.Kubectl(a, lk.gvr.Resource, ns, name, v),
			Params:       s.lensParamSpecs(lk, a, obj, ns, name),
		}
		// CheckReady, not Check: an unfilled parameter is not a reason to
		// disable the button, it is the reason the button opens a form.
		if err := a.CheckReady(v); err != nil {
			spec.Disabled, spec.DisabledWhy = true, err.Error()
			spec.NeedsSelection = errors.Is(err, lens.ErrSelectedRequired)
		} else if why := s.lensRefusal(lk, a, ns, name); why != "" {
			spec.Disabled, spec.DisabledWhy = true, why
		}
		out = append(out, spec)
	}
	return out
}

// lensParamSpecs turns an action's declared parameters into what the UI draws,
// resolving every live option source here so the UI never parses a JSONPath.
func (s *Store) lensParamSpecs(lk *lensKind, a lens.Action, obj *unstructured.Unstructured, ns, name string) []domain.LensParamSpec {
	if len(a.Params) == 0 {
		return nil
	}
	out := make([]domain.LensParamSpec, 0, len(a.Params))
	for _, p := range a.Params {
		spec := domain.LensParamSpec{
			Name:      p.Name,
			Label:     p.Label,
			Type:      p.Type,
			Default:   p.Default,
			AllowFree: p.AllowFree,
			Required:  p.Required,
		}
		if spec.Label == "" {
			spec.Label = p.Name
		}
		for _, o := range p.Options {
			spec.Options = append(spec.Options, domain.LensOption{Value: o.Value, Note: o.Note})
		}
		if p.OptionsFrom != "" {
			opts, note := s.lensOptions(lk, p, obj, ns, name)
			spec.Options = append(spec.Options, opts...)
			spec.OptionsNote = note
		}
		out = append(out, spec)
	}
	return out
}

// lensOptions resolves one live option source. The note is the explanation for
// an empty list, which is never nothing: "no instances" and "that kind has not
// been opened" send the operator to different places.
func (s *Store) lensOptions(lk *lensKind, p lens.Param, obj *unstructured.Unstructured, ns, name string) ([]domain.LensOption, string) {
	if !strings.HasPrefix(p.OptionsFrom, ".") && !strings.HasPrefix(p.OptionsFrom, "{") {
		return s.lensRelatedOptions(lk, p, ns, name)
	}
	if obj == nil {
		return nil, "this object is not loaded, so its values cannot be listed"
	}
	vals := lensPathList(obj.Object, p.OptionsFrom)
	if len(vals) == 0 {
		return nil, ""
	}
	out := make([]domain.LensOption, 0, len(vals))
	for _, v := range vals {
		out = append(out, domain.LensOption{Value: v, Note: lensOptionNote(obj.Object, v)})
	}
	return out, ""
}

// lensOptionNote is the free text shown beside a suggestion.
//
// It is CNPG-shaped and deliberately so: instancesReportedState is the one
// place in any shipped pack that says which candidate is the primary, and
// picking the primary is the difference between fencing a replica and taking
// the database down. A pack-authored note template would be a second
// templating surface for one fact.
func lensOptionNote(obj map[string]any, value string) string {
	st, ok, _ := unstructured.NestedMap(obj, "status", "instancesReportedState")
	if !ok {
		return ""
	}
	e, ok := st[value].(map[string]any)
	if !ok {
		return ""
	}
	if primary, _ := e["isPrimary"].(bool); primary {
		return "primary"
	}
	return "replica"
}

// lensPathList resolves a path to a list of strings: the elements of an array,
// or the SORTED keys of a map.
//
// Sorted, because a map's iteration order is random per frame — an option list
// that reshuffles while the operator reads it is one they cannot trust enough
// to arrow through.
func lensPathList(obj map[string]any, path string) []string {
	// Walked by hand rather than through client-go's jsonpath, which renders
	// to text: a list would arrive as "[a b c]" and a map as its Go syntax,
	// and splitting that back apart would break on any value with a space.
	// Option sources are plain field paths, so the walk is the simpler tool.
	cur := any(obj)
	for _, seg := range strings.Split(strings.Trim(path, "{}."), ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[seg]
		if !ok {
			return nil
		}
	}
	switch v := cur.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return append([]string(nil), v...)
	case map[string]any:
		out := make([]string, 0, len(v))
		for k := range v {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	return nil
}

// lensRelatedOptions lists the names of related objects of one kind, reusing
// the pack's declared edges rather than a query language of its own: "which
// Backups belong to this Cluster" is already stated in the pack, and Related
// already walks it.
func (s *Store) lensRelatedOptions(lk *lensKind, p lens.Param, ns, name string) ([]domain.LensOption, string) {
	want, known := s.kindKeyForGVR(p.OptionsFrom)
	if !known {
		return nil, "no view is declared for " + p.OptionsFrom
	}
	refs, err := s.Related(lk.kind.Key, ns, name)
	if err != nil {
		return nil, err.Error()
	}
	var (
		out      []domain.LensOption
		unloaded bool
	)
	for _, r := range refs {
		if r.Kind != want {
			continue
		}
		if !r.Loaded {
			// Related reports an unopened kind as one unnamed, unloaded ref.
			unloaded = true
			continue
		}
		out = append(out, domain.LensOption{
			Value: r.Name,
			Note:  s.lensRelatedNote(want, r.Namespace, r.Name),
		})
	}
	if len(out) == 0 {
		if unloaded {
			return nil, "open " + want + " to list them"
		}
		return nil, ""
	}
	// Newest first. A restore form whose first suggestion is the oldest backup
	// invites picking it, and the most recent recoverable point is almost
	// always the one wanted.
	sort.Slice(out, func(i, j int) bool {
		return s.lensCreated(want, ns, out[i].Value).After(s.lensCreated(want, ns, out[j].Value))
	})
	return out, ""
}

// lensRelatedNote describes one suggestion: its phase, which for a Backup is
// the difference between a restore that works and one that cannot.
func (s *Store) lensRelatedNote(kind, ns, name string) string {
	obj, _ := s.cachedObjectByName(kind, ns, name)
	if obj == nil {
		return ""
	}
	phase, _, _ := unstructured.NestedString(obj, "status", "phase")
	return phase
}

// lensCreated is one object's creation time, or the zero time when it cannot
// be read — which sorts it last, where an object nobody can describe belongs.
func (s *Store) lensCreated(kind, ns, name string) time.Time {
	obj, _ := s.cachedObjectByName(kind, ns, name)
	if obj == nil {
		return time.Time{}
	}
	ts, _, _ := unstructured.NestedString(obj, "metadata", "creationTimestamp")
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

// lensRefusal evaluates the action's declared preconditions against the cached
// object. ArgoCD refuses a sync while .operation is set; saying so here costs
// a map lookup and saves a round trip that would come back as a server error
// the user has to interpret.
func (s *Store) lensRefusal(lk *lensKind, a lens.Action, ns, name string) string {
	if len(a.RefuseWhen) == 0 {
		return ""
	}
	obj := s.cachedLensObject(lk, ns, name)
	if obj == nil {
		return ""
	}
	for _, r := range a.RefuseWhen {
		if lensPathHasValue(obj.Object, r.Path) {
			return r.Reason
		}
	}
	return ""
}

// cachedLensObject reads one object from an ALREADY-RUNNING informer. It never
// calls ensure: describing what a button would do must not open a watch.
func (s *Store) cachedLensObject(lk *lensKind, ns, name string) *unstructured.Unstructured {
	s.infMu.Lock()
	desired := s.desiredInformerScope(lk.kind.Key, []string{ns})
	scope := s.accessScopeLocked(lk.kind.Key, desired)
	// Bail out BEFORE touching the factory. ForResource registers the
	// informer on it, and DynamicSharedInformerFactory.Start launches every
	// informer registered — so registering here would mean the next ensure()
	// on this namespace silently starts a watch nobody opened, with no
	// watch-error handler and no entry in s.started to make it visible.
	if !s.started[informerKey{kind: lk.kind.Key, namespace: scope}] {
		s.infMu.Unlock()
		return nil
	}
	lister := s.dynFactoryLocked(scope).ForResource(lk.gvr).Lister()
	s.infMu.Unlock()
	if lister == nil {
		return nil
	}
	var (
		obj any
		err error
	)
	if lk.kind.Namespaced && ns != "" && ns != domain.AllNamespaces {
		obj, err = lister.ByNamespace(ns).Get(name)
	} else {
		obj, err = lister.Get(name)
	}
	if err != nil {
		return nil
	}
	u, _ := obj.(*unstructured.Unstructured)
	return u
}

// lensPathHasValue reports whether path resolves to something non-empty.
//
// A fresh jsonpath is parsed per call rather than reusing the row builder's
// compiled paths: those are not safe for concurrent use, and this runs from a
// tea.Cmd goroutine.
func lensPathHasValue(obj map[string]any, path string) bool {
	jp := jsonpath.New("refuse")
	jp.AllowMissingKeys(true)
	if err := jp.Parse(lens.Braced(path)); err != nil {
		return false
	}
	var buf bytes.Buffer
	if err := jp.Execute(&buf, obj); err != nil {
		return false
	}
	v := buf.String()
	return v != "" && v != "<nil>" && v != "false"
}

// LensAction runs one declarative verb.
//
// Server errors are returned VERBATIM. Kargo's promote rejection arrives as a
// webhook message naming the virtual `promote` verb, and wrapping it in
// "lens: %v" would bury the one sentence that says what permission is missing.
func (s *Store) LensAction(kind, ns, name, id, selected string, params map[string]string) (string, error) {
	lk, ok := s.lensKindFor(kind)
	if !ok {
		return "", fmt.Errorf("unknown kind %q", kind)
	}
	a, ok := lk.pack.Action(id)
	if !ok {
		return "", fmt.Errorf("lens %q has no action %q", lk.pack.Name, id)
	}
	// Fill BEFORE Check, so a parameter the operator left alone is judged on
	// its default rather than reported as missing.
	v := a.Fill(s.lensVars(ns, name, selected, params))
	if err := a.Check(v); err != nil {
		return "", err
	}
	if why := s.lensRefusal(lk, a, ns, name); why != "" {
		return "", fmt.Errorf("%s", why)
	}

	ctx, cancel := context.WithTimeout(context.Background(), lensActionTimeout)
	defer cancel()

	ri, err := s.lensResource(lk, a, ns)
	if err != nil {
		return "", err
	}

	run := func() error {
		switch a.Verb {
		case lens.VerbAnnotate:
			body, err := annotationPatchBody(a.Annotations, v)
			if err != nil {
				return err
			}
			_, err = ri.Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{})
			return err

		case lens.VerbPatch:
			body, err := lensPatchBody(a.Patch, v)
			if err != nil {
				return err
			}
			_, err = ri.Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{})
			return err

		case lens.VerbStatusPatch:
			body, err := lensPatchBody(a.Patch, v)
			if err != nil {
				return err
			}
			// Exactly "status", no leading slash.
			_, err = ri.Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{}, "status")
			return err

		case lens.VerbCreate:
			tree, err := lens.RenderTree(a.Template, v)
			if err != nil {
				return err
			}
			m, ok := tree.(map[string]any)
			if !ok {
				return fmt.Errorf("action %q: template is not an object", a.ID)
			}
			_, err = ri.Create(ctx, &unstructured.Unstructured{Object: m}, metav1.CreateOptions{})
			return err

		case lens.VerbDelete:
			return ri.Delete(ctx, name, metav1.DeleteOptions{})
		}
		return fmt.Errorf("action %q: unknown verb %q", a.ID, a.Verb)
	}

	if a.RetryOnConflict {
		// CNPG's promote runs under an optimistic lock: the operator writes
		// the same status while we do.
		if err := retry.RetryOnConflict(retry.DefaultRetry, run); err != nil {
			return "", err
		}
	} else if err := run(); err != nil {
		return "", err
	}
	return ackWant(a, v), nil
}

// LensPreview renders the equivalent kubectl command for the parameters
// currently in the form.
//
// Separate from the Kubectl string on LensActionSpec, which is computed once
// per selection: this one changes on every keystroke, and a preview that lags
// the form describes a mutation other than the one about to happen.
func (s *Store) LensPreview(kind, ns, name, id, selected string, params map[string]string) string {
	lk, ok := s.lensKindFor(kind)
	if !ok {
		return ""
	}
	a, ok := lk.pack.Action(id)
	if !ok {
		return ""
	}
	return lens.Kubectl(a, lk.gvr.Resource, ns, name, s.lensVars(ns, name, selected, params))
}

// ackWant is the value the controller is expected to echo into the ack field.
//
// The acknowledging operators all work the same way: the annotation carries a
// token (a timestamp) and the controller copies it into a status field once it
// has handled the request. So the token we wrote IS what to wait for.
//
// Anything else — a patch, a create, or an annotate carrying more than one key
// with no single token — returns "", and LensAck falls back to presence.
func ackWant(a lens.Action, v lens.Vars) string {
	if a.Ack == "" || a.Verb != lens.VerbAnnotate || len(a.Annotations) != 1 {
		return ""
	}
	for _, raw := range a.Annotations {
		val, err := lens.Render(raw, v)
		if err != nil {
			return ""
		}
		return val
	}
	return ""
}

// lensResource resolves where the write lands: the selected row's own GVR, or
// the action's target GVR keeping the row's name and namespace.
//
// That second case is Longhorn attach/detach, where the VolumeAttachment CR is
// named identically to the Volume — so "patch a different kind, same name" is
// the whole mechanism.
func (s *Store) lensResource(lk *lensKind, a lens.Action, ns string) (dynamic.ResourceInterface, error) {
	gvr, namespaced := lk.gvr, lk.kind.Namespaced
	if a.Target != "" {
		g, ver, res, err := lens.ParseGVR(a.Target)
		if err != nil {
			return nil, fmt.Errorf("action %q: target %q: %w", a.ID, a.Target, err)
		}
		// A target kind keeps the selected row's scope: these are sibling
		// objects describing the same thing.
		gvr = schema.GroupVersionResource{Group: g, Version: ver, Resource: res}
	}
	if s.c == nil || s.c.Dynamic == nil {
		return nil, fmt.Errorf("no dynamic client")
	}
	if namespaced && ns != "" && ns != domain.AllNamespaces {
		return s.c.Dynamic.Resource(gvr).Namespace(ns), nil
	}
	if namespaced {
		return s.c.Dynamic.Resource(gvr).Namespace(metav1.NamespaceDefault), nil
	}
	return s.c.Dynamic.Resource(gvr), nil
}

// annotationPatchBody builds the merge patch that sets or removes annotations.
//
// An annotation whose rendered value is EMPTY is written as JSON null, which
// removes the key. That is how un-fencing and waking a hibernated cluster
// work, and it is why the kubectl line for that case must read "key-" rather
// than "key=".
func annotationPatchBody(ann map[string]string, v lens.Vars) ([]byte, error) {
	out := make(map[string]any, len(ann))
	for k, raw := range ann {
		val, err := lens.Render(raw, v)
		if err != nil {
			return nil, err
		}
		if val == "" {
			out[k] = nil
			continue
		}
		out[k] = val
	}
	return json.Marshal(map[string]any{"metadata": map[string]any{"annotations": out}})
}

func lensPatchBody(patch map[string]any, v lens.Vars) ([]byte, error) {
	tree, err := lens.RenderTree(patch, v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(tree)
}

// LensAck reports whether the controller has acknowledged the write.
//
// It reads the informer cache — zero API calls — and parses a fresh jsonpath
// per call, so it is safe to run from the ack poll's goroutine without
// touching the row builder's compiled paths.
func (s *Store) LensAck(kind, ns, name, id, want string) (bool, error) {
	lk, ok := s.lensKindFor(kind)
	if !ok {
		return true, nil
	}
	a, ok := lk.pack.Action(id)
	if !ok || a.Ack == "" {
		// Nothing to wait for: done the moment the request returned.
		return true, nil
	}
	obj := s.cachedLensObject(lk, ns, name)
	if obj == nil {
		return false, nil
	}
	if want == "" {
		// No token to match on — presence is the best available signal.
		return lensPathHasValue(obj.Object, a.Ack), nil
	}
	// The controller echoes our token back. Anything else in the field is a
	// PREVIOUS request's acknowledgement, which must not clear this spinner.
	return lensPathValue(obj.Object, a.Ack) == want, nil
}

// lensPathValue resolves a path to its rendered string, or "" .
func lensPathValue(obj map[string]any, path string) string {
	jp := jsonpath.New("ack")
	jp.AllowMissingKeys(true)
	if err := jp.Parse(lens.Braced(path)); err != nil {
		return ""
	}
	var buf bytes.Buffer
	if err := jp.Execute(&buf, obj); err != nil {
		return ""
	}
	return buf.String()
}
