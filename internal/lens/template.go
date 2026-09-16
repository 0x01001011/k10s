package lens

import (
	"encoding/json"
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
	// Params are the action's collected parameters, reached in templates as
	// {{.Params.<name>}}. Always non-nil after Action.Fill.
	Params map[string]string
}

// resolve fills in the defaults.
func (v Vars) resolve() Vars {
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
		if out[p.Name] != "" {
			continue
		}
		// A default is a template like every other string in a pack. The
		// restore form's "{{.Name}}-restore" is the case that proves it:
		// copied verbatim, the braces reach the manifest and the preview.
		//
		// Rendered against v BEFORE the params exist, so a default may use
		// .Name or .Namespace but not another parameter — which would need an
		// evaluation order the schema does not express.
		def, err := Render(p.Default, v)
		if err != nil {
			def = p.Default
		}
		out[p.Name] = def
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
			// Stage name, which comes from {{.Params.stage}}.
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

// Prune drops empty-string leaves from a rendered create body, and then any
// map left empty by that.
//
// An optional parameter that nobody filled renders to "". Sending it is not
// the same as omitting it: bootstrap.recovery.recoveryTarget with an empty
// targetTime is a recovery target CNPG must interpret, and a Cluster carrying
// one never finishes bootstrapping. Omission is what "I did not choose a
// recovery point" actually means.
//
// Only create bodies are pruned. An annotate action uses the empty string
// deliberately — it is how un-fencing removes a key — and a patch may need to
// write one.
func Prune(node any) any {
	switch n := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, val := range n {
			p := Prune(val)
			if p == nil {
				continue
			}
			out[k] = p
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]any, 0, len(n))
		for _, e := range n {
			if p := Prune(e); p != nil {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		if n == "" {
			return nil
		}
		return n
	default:
		return node
	}
}

// Check reports why an action cannot run right now, or nil.
//
// Parameters are checked here rather than only in the form, because the form
// is not the only caller: a key press fires the action directly when it has
// nothing to ask, and a pack edited to add a required param must not turn that
// into a write with an empty value.
func (a Action) Check(v Vars) error {
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
	// Pruned to match what Create actually sends. A preview showing a field
	// the request omits is a preview of a different mutation.
	r = Prune(r)
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
