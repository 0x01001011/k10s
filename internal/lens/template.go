package lens

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// Vars are the values an action's templates may reference.
type Vars struct {
	Name      string
	Namespace string
	Context   string
	Now       string // RFC3339
	// Selected is the highlighted sub-row: an instance, a revision, a
	// snapshot. It defaults to Name, because for most actions the row's own
	// name is exactly the right target.
	Selected string
	// Params are the action's collected parameters, reached in templates as
	// {{.Params.<name>}}. Always non-nil after Action.Fill.
	Params map[string]string
}

// ErrSelectedRequired means the action declares requiresSelection but nothing
// was selected. It exists so an action that needs a real sub-row refuses
// rather than silently targeting the wrong object — fencing CNPG instance ""
// or re-verifying Kargo with an empty id both look like success and do
// nothing at all.
var ErrSelectedRequired = errors.New("this action needs a selected instance, and nothing is selected")

// resolve fills in the defaults. Selected falls back to Name: for most
// actions the row's own name is the right target, and an action for which
// that is NOT true says so with requiresSelection.
func (v Vars) resolve() Vars {
	if v.Selected == "" {
		v.Selected = v.Name
	}
	// missingkey=error fires on a nil map too, so an action with no params
	// would otherwise fail to render the moment any template mentioned
	// .Params at all.
	if v.Params == nil {
		v.Params = map[string]string{}
	}
	return v
}

// Fill returns v with every declared param present: the caller's value where
// they supplied one, the param's default everywhere else.
//
// It copies rather than writes through, because the UI holds one parameter map
// per open form and re-renders the preview on every keystroke — filling in
// place would make a default indistinguishable from something the operator
// typed, and there would be no way back to "untouched".
func (a Action) Fill(v Vars) Vars {
	out := make(map[string]string, len(a.Params)+len(v.Params))
	for k, val := range v.Params {
		out[k] = val
	}
	for _, p := range a.Params {
		if out[p.Name] == "" {
			out[p.Name] = p.Default
		}
	}
	v.Params = out
	return v
}

// Render expands one template string. An unknown variable is an error, never
// a silent "<no value>": a half-rendered annotation is worse than a refusal.
func Render(s string, v Vars) (string, error) {
	t, err := template.New("lens").Option("missingkey=error").Parse(s)
	if err != nil {
		return "", fmt.Errorf("template %q: %w", s, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, v.resolve()); err != nil {
		return "", fmt.Errorf("template %q: %w", s, err)
	}
	return b.String(), nil
}

// RenderTree walks a decoded YAML tree and expands every string leaf, leaving
// numbers and booleans as they are. Used for an action's patch and template
// bodies, which are arbitrary object shapes.
func RenderTree(node any, v Vars) (any, error) {
	switch n := node.(type) {
	case string:
		return Render(n, v)
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, val := range n {
			// Keys are templated too: Kargo's approvedFor is keyed by the
			// Stage name, which comes from .Selected.
			key, err := Render(k, v)
			if err != nil {
				return nil, err
			}
			r, err := RenderTree(val, v)
			if err != nil {
				return nil, err
			}
			out[key] = r
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(n))
		for _, e := range n {
			r, err := RenderTree(e, v)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, nil
	default:
		return node, nil
	}
}

// CheckReady reports why an action cannot even be OFFERED, or nil.
//
// It is deliberately narrower than Check: an unfilled parameter is not a
// reason to grey out the button, it is the reason the button opens a form.
// Disabling on it would make every parameterised action permanently
// unreachable, since nothing can fill a form that never opens.
func (a Action) CheckReady(v Vars) error {
	if a.RequiresSelection && v.Selected == "" {
		return ErrSelectedRequired
	}
	return nil
}

// Check reports why an action cannot run right now, or nil.
//
// Parameters are checked here rather than only in the form, because the form
// is not the only caller: a key press fires the action directly when it has
// nothing to ask, and a pack edited to add a required param must not turn that
// into a write with an empty value.
func (a Action) Check(v Vars) error {
	if err := a.CheckReady(v); err != nil {
		return err
	}
	for _, p := range a.Params {
		val := v.Params[p.Name]
		if val == "" {
			if p.Required {
				return fmt.Errorf("%s is required", p.label())
			}
			continue
		}
		// An empty Options list is not a closed list — it is a field with no
		// suggestions, or one whose suggestions come from a cluster this
		// check cannot reach.
		if !p.AllowFree && len(p.Options) > 0 && !p.hasOption(val) {
			return fmt.Errorf("%s: %q is not one of its allowed values", p.label(), val)
		}
		if p.Type == ParamInt {
			if _, err := strconv.Atoi(val); err != nil {
				return fmt.Errorf("%s: %q is not a whole number", p.label(), val)
			}
		}
	}
	return nil
}

// label is what to call the param when refusing. The declared label reads
// better in a sentence; the name is the fallback and is never empty.
func (p Param) label() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Name
}

// Kubectl is the equivalent command, for the confirm modal to show.
//
// It exists to be checked: an operator about to fence a database has to be
// able to read what the button will do and recognise it. That makes fidelity
// a correctness property rather than a nicety — the rendered command must
// describe the same mutation the request performs.
func Kubectl(a Action, resource, ns, name string, v Vars) string {
	// Fill, not just resolve: an untouched parameter has to render its
	// DEFAULT, because that is the value the request will carry. Rendering it
	// empty would show a manifest the server never receives, and leaving the
	// key absent would fail the template outright — mid-form, which is the
	// preview's normal state, not an error.
	v = a.Fill(v).resolve()
	target := resource
	if a.Target != "" {
		if _, _, r, err := ParseGVR(a.Target); err == nil {
			target = r
		}
	}
	nsFlag := ""
	if ns != "" {
		nsFlag = " -n " + ns
	}

	switch a.Verb {
	case VerbAnnotate:
		keys := make([]string, 0, len(a.Annotations))
		for k := range a.Annotations {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			val, err := Render(a.Annotations[k], v)
			if err != nil {
				val = a.Annotations[k]
			}
			if val == "" {
				// kubectl's REMOVAL syntax. Rendering "key=" here would
				// describe setting the annotation to the empty string, a
				// different mutation from the one the button performs — and
				// un-fencing is exactly this case.
				parts = append(parts, k+"-")
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%s", k, val))
		}
		return fmt.Sprintf("kubectl annotate %s/%s%s %s --overwrite", target, name, nsFlag, strings.Join(parts, " "))

	case VerbPatch:
		return fmt.Sprintf("kubectl patch %s/%s%s --type merge -p '%s'", target, name, nsFlag, renderJSON(a.Patch, v))

	case VerbStatusPatch:
		return fmt.Sprintf("kubectl patch %s/%s%s --subresource status --type merge -p '%s'", target, name, nsFlag, renderJSON(a.Patch, v))

	case VerbCreate:
		return fmt.Sprintf("kubectl create%s -f - <<'EOF'\n%s\nEOF", nsFlag, renderYAMLish(a.Template, v))

	case VerbDelete:
		return fmt.Sprintf("kubectl delete %s/%s%s", target, name, nsFlag)
	}
	return ""
}

func renderJSON(tree map[string]any, v Vars) string {
	r, err := RenderTree(tree, v)
	if err != nil {
		return "{}"
	}
	b, err := json.Marshal(r)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// renderYAMLish prints a create body as indented JSON, which is valid YAML and
// avoids pulling in a YAML marshaller for one display string.
func renderYAMLish(tree map[string]any, v Vars) string {
	r, err := RenderTree(tree, v)
	if err != nil {
		return "{}"
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
