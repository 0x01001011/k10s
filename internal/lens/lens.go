// Package lens loads declarative packs that teach k10s one ecosystem
// operator each — which custom resources it owns, what belongs in a table
// row, and which daily actions apply. ArgoCD, CNPG, Longhorn, Kargo and
// Traefik are five YAML files against one mechanism, not five features.
//
// The schema and the reasoning behind it are in docs/lenses.md. This
// package parses and validates; it never talks to a cluster. Resolving a
// GVR, gating on discovery and running an action all live in internal/k8s,
// so a pack can be checked without a kubeconfig.
package lens

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/client-go/util/jsonpath"
	"sigs.k8s.io/yaml"
)

// Level is a column's severity bucket. It drives both the row colour and
// the sort comparator, so the order is load-bearing: a kind with any
// severity column sorts worst-first, which is the whole point (k9s #3589,
// sorting by STATUS scatters CrashLoopBackOff among Running, was closed as
// not planned).
type Level int

const (
	LevelOK Level = iota
	LevelWarn
	LevelError
	LevelUnknown
)

// Rank orders rows worst-first: errors, warnings, unknown, then healthy.
// Unknown sits above OK because "cannot tell" deserves a look, and below
// Error because it is not yet a fact.
func (l Level) Rank() int {
	switch l {
	case LevelError:
		return 0
	case LevelWarn:
		return 1
	case LevelUnknown:
		return 2
	default:
		return 3
	}
}

func (l Level) String() string {
	switch l {
	case LevelError:
		return "error"
	case LevelWarn:
		return "warn"
	case LevelUnknown:
		return "unknown"
	default:
		return "ok"
	}
}

// Severity maps observed field values onto levels. Default catches
// everything unlisted, which is not a convenience: CNPG's .status.phase is
// a human sentence ("Cluster is unrecoverable and needs manual
// intervention"), so enumerating every value is a losing game — one value
// is ok and the rest are warn.
type Severity struct {
	OK      []string `json:"ok"`
	Warn    []string `json:"warn"`
	Error   []string `json:"error"`
	Unknown []string `json:"unknown"`
	Default string   `json:"default"`
}

// Level classifies one observed value.
func (s Severity) Level(v string) Level {
	for _, m := range []struct {
		vals []string
		lvl  Level
	}{{s.OK, LevelOK}, {s.Warn, LevelWarn}, {s.Error, LevelError}, {s.Unknown, LevelUnknown}} {
		for _, want := range m.vals {
			if want == v {
				return m.lvl
			}
		}
	}
	switch s.Default {
	case "ok":
		return LevelOK
	case "warn":
		return LevelWarn
	case "error":
		return LevelError
	}
	return LevelUnknown
}

// Column is one table column: a JSONPath plus how to render what it finds.
type Column struct {
	Header string `json:"header"`
	Path   string `json:"path"`
	// Format is age, bytes, int, bool, or empty for the verbatim string.
	Format string `json:"format"`
	// Truncate cuts to N runes. Git revisions want 7.
	Truncate int `json:"truncate"`
	// Severity names a table in the pack's Severities map.
	Severity string `json:"severity"`
}

// Kind is one resource type a pack contributes to the Resources pane.
type Kind struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Short      string   `json:"short"`
	Group      string   `json:"group"`
	GVR        string   `json:"gvr"`
	Namespaced bool     `json:"namespaced"`
	Columns    []Column `json:"columns"`
	Actions    []string `json:"actions"`
}

// Action verbs. Five cover every daily action across all five operators,
// and none of them shells out or needs the operator's own CLI or HTTP API.
const (
	VerbAnnotate    = "annotate"
	VerbPatch       = "patch"
	VerbStatusPatch = "status-patch"
	VerbCreate      = "create"
	VerbDelete      = "delete"
)

// Confirm levels. Typed is reserved for failover and destructive actions:
// approval fatigue is a documented failure mode, and prompting on
// everything trains operators to confirm without reading.
const (
	ConfirmNone  = ""
	ConfirmModal = "true"
	ConfirmTyped = "typed"
)

// Action is one declarative mutation. Templates may use .Name, .Namespace,
// .Context, .Now (RFC3339) and .Selected.
type Action struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Verb    string `json:"verb"`
	Confirm string `json:"confirm"`

	// Annotations is the annotate verb's payload. An empty value removes
	// the key — that is how un-fencing and un-suspending work.
	Annotations map[string]string `json:"annotations"`
	// Patch is the merge patch for patch and status-patch.
	Patch map[string]any `json:"patch"`
	// Template is the object body for create.
	Template map[string]any `json:"template"`
	// Target redirects the write to a sibling object instead of the
	// selected row, named "group/version/resource". The object keeps the
	// selected row's name and namespace, which is exactly Longhorn's
	// attach/detach: the VolumeAttachment CR is named identically to the
	// Volume, so attaching is a patch on a different kind, same name.
	Target string `json:"target"`

	// Ack is a JSONPath the controller writes back once it has handled the
	// request. It is why this is a mechanism and not five buttons: three of
	// the five operators publish one, so "write, watch the ack, drop the
	// spinner" is shared code.
	Ack             string `json:"ack"`
	RetryOnConflict bool   `json:"retryOnConflict"`

	// Notice is free text the confirm modal shows verbatim. It exists
	// because some warnings cannot be derived from the verb: that syncing
	// an ArgoCD Application by writing .operation bypasses argocd-rbac-cm
	// is a fact about Argo, not about patching. Without a field the
	// disclosure could only live in a YAML comment, which the parser
	// discards, or as a Go constant keyed on an action id — a string
	// asserting it equals itself.
	Notice string `json:"notice"`

	// RequiresSelection marks an action whose .Selected genuinely cannot
	// fall back to the object's own name. Most can: fencing CNPG instance
	// "my-db" is the same string either way. Backing up a Longhorn volume
	// needs a *snapshot* name, and re-verifying a Kargo stage needs a
	// verification id — neither is the row's name, and writing one anyway
	// would target the wrong object.
	RequiresSelection bool `json:"requiresSelection"`

	// RefuseWhen blocks the action when any listed path is present and
	// non-empty on the object. The controller-side rules that make an
	// action illegal are not derivable from the verb — ArgoCD refuses a
	// sync while .operation is set, and k10s should refuse before the
	// round trip rather than surfacing the server's rejection.
	RefuseWhen []Refusal `json:"refuseWhen"`
}

// Refusal is one precondition: if Path resolves to a non-empty value, the
// action is refused with Reason.
type Refusal struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Edge declares a navigable relationship. The table is data because
// ownerRefs cannot express the hops that matter — pod to CNPG Cluster, PVC
// to Longhorn Volume — the same conclusion Headlamp reached when it made
// Resource Map relationships plugin-defined in v0.45.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Via is label, ownerRef, annotation, or field.
	Via string `json:"via"`
	Key string `json:"key"`
}

// Edge traversal kinds.
const (
	ViaLabel      = "label"
	ViaOwnerRef   = "ownerRef"
	ViaAnnotation = "annotation"
	ViaField      = "field"
)

// Pack is one loaded lens file.
type Pack struct {
	Name string `json:"name"`
	// Requires gates the whole pack on discovery: every group/version
	// listed must be served, or none of its kinds appear.
	Requires   []string            `json:"requires"`
	Severities map[string]Severity `json:"severities"`
	Kinds      []Kind              `json:"kinds"`
	Actions    []Action            `json:"actions"`
	Edges      []Edge              `json:"edges"`

	// Source is the file this came from, for diagnostics. Empty for
	// builtins parsed from the embedded set.
	Source string `json:"-"`
}

// Action finds a declared action by id.
func (p Pack) Action(id string) (Action, bool) {
	for _, a := range p.Actions {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// builtinActions are ids any kind may reference without declaring them —
// they are k10s's own, served by the existing action dispatch.
var builtinActions = map[string]bool{
	"describe": true, "yaml": true, "edit": true, "delete": true,
	"logs": true, "events": true,
}

// Parse decodes and validates one pack. src names the file in errors.
func Parse(data []byte, src string) (Pack, error) {
	var p Pack
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Pack{}, fmt.Errorf("%s: %w", src, err)
	}
	p.Source = src
	if err := p.validate(); err != nil {
		return Pack{}, fmt.Errorf("%s: %w", src, err)
	}
	return p, nil
}

func (p Pack) validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("pack has no name")
	}
	for _, gv := range p.Requires {
		if err := validGroupVersion(gv); err != nil {
			return fmt.Errorf("lens %q: requires %q: %w", p.Name, gv, err)
		}
	}
	for _, a := range p.Actions {
		if err := a.validate(); err != nil {
			return fmt.Errorf("lens %q: %w", p.Name, err)
		}
	}
	if len(p.Kinds) == 0 {
		return fmt.Errorf("lens %q: declares no kinds", p.Name)
	}
	seen := map[string]bool{}
	for _, k := range p.Kinds {
		if err := p.validateKind(k); err != nil {
			return fmt.Errorf("lens %q: %w", p.Name, err)
		}
		if seen[k.Key] {
			return fmt.Errorf("lens %q: duplicate kind key %q", p.Name, k.Key)
		}
		seen[k.Key] = true
	}
	for _, e := range p.Edges {
		if err := e.validate(); err != nil {
			return fmt.Errorf("lens %q: %w", p.Name, err)
		}
	}
	return nil
}

func (p Pack) validateKind(k Kind) error {
	if strings.TrimSpace(k.Key) == "" {
		return errors.New("kind has no key")
	}
	if _, _, _, err := ParseGVR(k.GVR); err != nil {
		return fmt.Errorf("kind %q: gvr %q: %w", k.Key, k.GVR, err)
	}
	if len(k.Columns) == 0 {
		return fmt.Errorf("kind %q: declares no columns", k.Key)
	}
	for _, c := range k.Columns {
		if strings.TrimSpace(c.Header) == "" {
			return fmt.Errorf("kind %q: column has no header", k.Key)
		}
		if err := ValidPath(c.Path); err != nil {
			return fmt.Errorf("kind %q: column %q: %w", k.Key, c.Header, err)
		}
		switch c.Format {
		case "", "age", "bytes", "int", "bool":
		default:
			return fmt.Errorf("kind %q: column %q: unknown format %q", k.Key, c.Header, c.Format)
		}
		if c.Severity != "" {
			if _, ok := p.Severities[c.Severity]; !ok {
				return fmt.Errorf("kind %q: column %q: undeclared severity table %q", k.Key, c.Header, c.Severity)
			}
		}
	}
	for _, id := range k.Actions {
		if builtinActions[id] {
			continue
		}
		if _, ok := p.Action(id); !ok {
			return fmt.Errorf("kind %q: unknown action %q", k.Key, id)
		}
	}
	return nil
}

func (a Action) validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("action has no id")
	}
	switch a.Verb {
	case VerbAnnotate:
		if len(a.Annotations) == 0 {
			return fmt.Errorf("action %q: annotate with no annotations", a.ID)
		}
	case VerbPatch, VerbStatusPatch:
		if len(a.Patch) == 0 {
			return fmt.Errorf("action %q: %s with no patch", a.ID, a.Verb)
		}
	case VerbCreate:
		if len(a.Template) == 0 {
			return fmt.Errorf("action %q: create with no template", a.ID)
		}
	case VerbDelete:
	default:
		return fmt.Errorf("action %q: unknown verb %q", a.ID, a.Verb)
	}
	switch a.Confirm {
	case ConfirmNone, ConfirmModal, ConfirmTyped:
	default:
		return fmt.Errorf("action %q: unknown confirm %q", a.ID, a.Confirm)
	}
	if a.Ack != "" {
		if err := ValidPath(a.Ack); err != nil {
			return fmt.Errorf("action %q: ack: %w", a.ID, err)
		}
	}
	if a.Target != "" {
		if _, _, _, err := ParseGVR(a.Target); err != nil {
			return fmt.Errorf("action %q: target %q: %w", a.ID, a.Target, err)
		}
	}
	for _, r := range a.RefuseWhen {
		if err := ValidPath(r.Path); err != nil {
			return fmt.Errorf("action %q: refuseWhen: %w", a.ID, err)
		}
		if strings.TrimSpace(r.Reason) == "" {
			return fmt.Errorf("action %q: refuseWhen %q has no reason", a.ID, r.Path)
		}
	}
	return nil
}

func (e Edge) validate() error {
	for _, gvr := range []string{e.From, e.To} {
		if _, _, _, err := ParseGVR(gvr); err != nil {
			return fmt.Errorf("edge %s->%s: %q: %w", e.From, e.To, gvr, err)
		}
	}
	switch e.Via {
	case ViaLabel, ViaOwnerRef, ViaAnnotation, ViaField:
	default:
		return fmt.Errorf("edge %s->%s: unknown via %q", e.From, e.To, e.Via)
	}
	if e.Via != ViaOwnerRef && strings.TrimSpace(e.Key) == "" {
		return fmt.Errorf("edge %s->%s: via %s needs a key", e.From, e.To, e.Via)
	}
	return nil
}

// ParseGVR splits "group/version/resource" — or "version/resource" for core
// types, which have no group. Returning the parts as strings keeps this
// package free of apimachinery; internal/k8s builds the real GVR.
func ParseGVR(s string) (group, version, resource string, err error) {
	parts := strings.Split(s, "/")
	switch len(parts) {
	case 2:
		group, version, resource = "", parts[0], parts[1]
	case 3:
		group, version, resource = parts[0], parts[1], parts[2]
	default:
		return "", "", "", errors.New(`want "group/version/resource" or "version/resource"`)
	}
	if version == "" || resource == "" {
		return "", "", "", errors.New("empty version or resource")
	}
	return group, version, resource, nil
}

func validGroupVersion(s string) error {
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New(`want "group/version"`)
	}
	return nil
}

// ValidPath reports whether a column or ack JSONPath parses. The k8s
// parser wants a braced template, the schema does not — callers write
// ".status.health.status" and we wrap it here so the YAML stays readable.
func ValidPath(p string) error {
	if strings.TrimSpace(p) == "" {
		return errors.New("empty path")
	}
	jp := jsonpath.New("lens")
	if err := jp.Parse(Braced(p)); err != nil {
		return fmt.Errorf("bad jsonpath %q: %w", p, err)
	}
	return nil
}

// Braced wraps a bare path in the {} the k8s jsonpath parser expects,
// leaving an already-braced one alone.
func Braced(p string) string {
	if strings.HasPrefix(p, "{") {
		return p
	}
	return "{" + p + "}"
}

// Load reads every pack from the embedded builtins and then from dir,
// where a user pack overrides a builtin of the same name.
//
// A pack that fails to parse is skipped and reported, never fatal: one bad
// file must not cost the user the other four lenses. This matches how
// internal/plugin treats a broken plugin file.
func Load(dir string) (packs []Pack, errs []error) {
	byName := map[string]Pack{}
	for _, p := range builtins() {
		byName[p.Name] = p
	}
	for _, p := range loadDir(dir, &errs) {
		byName[p.Name] = p
	}
	for _, p := range byName {
		packs = append(packs, p)
	}
	sort.Slice(packs, func(i, j int) bool { return packs[i].Name < packs[j].Name })
	return packs, errs
}

func loadDir(dir string, errs *[]error) []Pack {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No lens directory is the normal case, not a problem to report.
		if !errors.Is(err, fs.ErrNotExist) {
			*errs = append(*errs, err)
		}
		return nil
	}
	var out []Pack
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			*errs = append(*errs, err)
			continue
		}
		p, err := Parse(data, path)
		if err != nil {
			*errs = append(*errs, err)
			continue
		}
		out = append(out, p)
	}
	return out
}

func isYAML(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}
