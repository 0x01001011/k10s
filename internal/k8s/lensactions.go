package k8s

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
func (s *Store) lensVars(ns, name, selected string) lens.Vars {
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
	v := s.lensVars(ns, name, selected)
	out := make([]domain.LensActionSpec, 0, len(lk.kind.Allowed))
	for _, id := range lk.kind.Allowed {
		a, ok := lk.pack.Action(id)
		if !ok {
			continue // a builtin verb; the existing dispatch owns it
		}
		spec := domain.LensActionSpec{
			ID:      a.ID,
			Label:   a.Label,
			Confirm: a.Confirm,
			Notice:  a.Notice,
			AckPath: a.Ack,
			Kubectl: lens.Kubectl(a, lk.gvr.Resource, ns, name, v),
		}
		if err := a.Check(v); err != nil {
			spec.Disabled, spec.DisabledWhy = true, err.Error()
			spec.NeedsSelection = errors.Is(err, lens.ErrSelectedRequired)
		} else if why := s.lensRefusal(lk, a, ns, name); why != "" {
			spec.Disabled, spec.DisabledWhy = true, why
		}
		out = append(out, spec)
	}
	return out
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
func (s *Store) LensAction(kind, ns, name, id, selected string) (string, error) {
	lk, ok := s.lensKindFor(kind)
	if !ok {
		return "", fmt.Errorf("unknown kind %q", kind)
	}
	a, ok := lk.pack.Action(id)
	if !ok {
		return "", fmt.Errorf("lens %q has no action %q", lk.pack.Name, id)
	}
	v := s.lensVars(ns, name, selected)
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
