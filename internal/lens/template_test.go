package lens

import (
	"strings"
	"testing"
)

var testVars = Vars{
	Name:      "my-db",
	Namespace: "data",
	Context:   "prod",
	Now:       "2026-09-14T10:00:00Z",
	Params:    map[string]string{"instance": "my-db-2"},
}

func TestRenderVars(t *testing.T) {
	cases := map[string]string{
		"{{.Name}}":                "my-db",
		"{{.Namespace}}":           "data",
		"{{.Context}}":             "prod",
		"{{.Now}}":                 "2026-09-14T10:00:00Z",
		"{{.Params.instance}}":     "my-db-2",
		`["{{.Params.instance}}"]`: `["my-db-2"]`,
	}
	for in, want := range cases {
		got, err := Render(in, testVars)
		if err != nil {
			t.Fatalf("Render(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("Render(%q) = %q, want %q", in, got, want)
		}
	}
}

// A typo'd variable must fail loudly. Rendering it as "<no value>" would put
// that literal string into a cluster annotation.
func TestRenderRejectsAnUnknownVariable(t *testing.T) {
	_, err := Render("{{.Nope}}", testVars)
	if err == nil {
		t.Fatal("an unknown variable must be an error")
	}
	if !strings.Contains(err.Error(), "Nope") {
		t.Errorf("error should name the bad field: %v", err)
	}
}

// A parameter nobody filled renders EMPTY, not the row's own name. That
// fallback is what the single .Selected slot did, and it is why an action
// that forgot to opt out wrote the cluster's name where an instance belonged
// — fencing nothing, on the wrong object, looking like it worked.
func TestAnUnfilledParamDoesNotFallBackToTheName(t *testing.T) {
	a := Action{Params: []Param{{Name: "instance"}}}
	got, err := Render("{{.Params.instance}}", a.Fill(Vars{Name: "my-app"}))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("unfilled param = %q, want empty", got)
	}
}

// A template naming a parameter the action never declared is an ERROR, not an
// empty string. missingkey=error is what makes that true, and it is the same
// guarantee .Name and .Namespace already had: a typo must not reach a cluster
// annotation as silence.
func TestAnUndeclaredParamIsAnError(t *testing.T) {
	a := Action{Params: []Param{{Name: "instance"}}}
	if _, err := Render("{{.Params.instanace}}", a.Fill(Vars{Name: "my-app"})); err == nil {
		t.Error("a misspelled parameter rendered without error")
	}
}

func TestCheckRefusesWhenARequiredParamIsEmpty(t *testing.T) {
	a := Action{
		ID: "x", Verb: VerbCreate,
		Params:   []Param{{Name: "snapshot", Label: "snapshot", Required: true, AllowFree: true}},
		Template: map[string]any{"a": "{{.Params.snapshot}}"},
	}
	if err := a.Check(a.Fill(Vars{Name: "my-vol"})); err == nil {
		t.Error("Check with nothing filled in = nil, want a refusal")
	}
	filled := Vars{Name: "my-vol", Params: map[string]string{"snapshot": "snap-1"}}
	if err := a.Check(a.Fill(filled)); err != nil {
		t.Errorf("Check with the param filled = %v, want nil", err)
	}
	// An action that asks for nothing is never refused.
	b := Action{ID: "y", Verb: VerbAnnotate, Annotations: map[string]string{"k": "v"}}
	if err := b.Check(Vars{Name: "my-vol"}); err != nil {
		t.Errorf("Check on an ordinary action = %v, want nil", err)
	}
}

func TestRenderTreeLeavesNonStrings(t *testing.T) {
	in := map[string]any{
		"str":   "{{.Name}}",
		"num":   float64(3),
		"bool":  true,
		"list":  []any{"{{.Namespace}}", float64(7)},
		"inner": map[string]any{"deep": "{{.Params.instance}}"},
	}
	out, err := RenderTree(in, testVars)
	if err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	m := out.(map[string]any)
	if m["str"] != "my-db" {
		t.Errorf("str = %v", m["str"])
	}
	if _, ok := m["num"].(float64); !ok {
		t.Errorf("num changed type: %T", m["num"])
	}
	if _, ok := m["bool"].(bool); !ok {
		t.Errorf("bool changed type: %T", m["bool"])
	}
	l := m["list"].([]any)
	if l[0] != "data" {
		t.Errorf("list[0] = %v", l[0])
	}
	if _, ok := l[1].(float64); !ok {
		t.Errorf("list[1] changed type: %T", l[1])
	}
	if m["inner"].(map[string]any)["deep"] != "my-db-2" {
		t.Errorf("inner.deep = %v", m["inner"])
	}
}

// Kargo's approvedFor is keyed by the Stage name, which comes from a param —
// so map KEYS must expand, not just values.
func TestRenderTreeExpandsMapKeys(t *testing.T) {
	in := map[string]any{"approvedFor": map[string]any{"{{.Params.instance}}": map[string]any{"approvedAt": "{{.Now}}"}}}
	out, err := RenderTree(in, testVars)
	if err != nil {
		t.Fatal(err)
	}
	af := out.(map[string]any)["approvedFor"].(map[string]any)
	if _, ok := af["my-db-2"]; !ok {
		t.Errorf("map key was not expanded: %v", af)
	}
}

func TestKubectlEquivalent(t *testing.T) {
	cases := []struct {
		name   string
		action Action
		want   []string
	}{
		{
			name:   "annotate",
			action: Action{Verb: VerbAnnotate, Annotations: map[string]string{"cnpg.io/hibernation": "on"}},
			want:   []string{"kubectl annotate clusters/my-db -n data cnpg.io/hibernation=on --overwrite"},
		},
		{
			// The divergence that matters: an empty rendered value REMOVES
			// the annotation. "key=" would set it to the empty string, a
			// different mutation — and un-fencing is exactly this case.
			name:   "annotate with an empty value removes the key",
			action: Action{Verb: VerbAnnotate, Annotations: map[string]string{"cnpg.io/fencedInstances": ""}},
			want:   []string{"cnpg.io/fencedInstances-"},
		},
		{
			name:   "status-patch",
			action: Action{Verb: VerbStatusPatch, Patch: map[string]any{"status": map[string]any{"targetPrimary": "{{.Params.instance}}"}}},
			want:   []string{"--subresource status", "--type merge", `"targetPrimary":"my-db-2"`},
		},
		{
			name:   "patch",
			action: Action{Verb: VerbPatch, Patch: map[string]any{"spec": map[string]any{"allowScheduling": false}}},
			want:   []string{"kubectl patch clusters/my-db -n data --type merge", `"allowScheduling":false`},
		},
		{
			name:   "delete redirects to the target kind",
			action: Action{Verb: VerbDelete, Target: "longhorn.io/v1beta2/volumeattachments"},
			want:   []string{"kubectl delete volumeattachments/my-db -n data"},
		},
		{
			name:   "create",
			action: Action{Verb: VerbCreate, Template: map[string]any{"kind": "Backup"}},
			want:   []string{"kubectl create -n data -f -", `"kind": "Backup"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Kubectl(tc.action, "clusters", "data", "my-db", testVars)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("Kubectl = %q\n  missing %q", got, w)
				}
			}
		})
	}
}

// The preview exists to be checked against, so it has to show the values the
// operator actually chose. A command rendered from defaults while the request
// carries their edits describes a different mutation from the one about to
// happen — which is the one thing the preview must never do.
func TestKubectlRendersParameterValues(t *testing.T) {
	fence := Action{
		Verb:   VerbAnnotate,
		Params: []Param{{Name: "instance", Required: true}},
		Annotations: map[string]string{
			"cnpg.io/fencedInstances": `["{{.Params.instance}}"]`,
		},
	}
	got := Kubectl(fence, "clusters", "data", "my-db", Vars{
		Name: "my-db", Params: map[string]string{"instance": "my-db-2"},
	})
	if !strings.Contains(got, `cnpg.io/fencedInstances=["my-db-2"]`) {
		t.Errorf("Kubectl = %q, want the chosen instance", got)
	}

	backup := Action{
		Verb: VerbCreate,
		Params: []Param{
			{Name: "method", Default: "barmanObjectStore"},
			{Name: "target"},
		},
		Template: map[string]any{
			"kind": "Backup",
			"spec": map[string]any{"method": "{{.Params.method}}"},
		},
	}
	got = Kubectl(backup, "backups", "data", "my-db", Vars{Name: "my-db"})
	// An untouched parameter renders its DEFAULT, not an empty string: the
	// preview would otherwise show a manifest the server never receives.
	if !strings.Contains(got, `"method": "barmanObjectStore"`) {
		t.Errorf("Kubectl = %q, want the declared default", got)
	}
	if strings.Contains(got, "<no value>") || strings.Contains(got, "{{") {
		t.Errorf("Kubectl leaked a template: %q", got)
	}
}

// A required parameter nobody has filled renders empty rather than exploding.
// The preview is shown WHILE the form is being filled, so half-filled is its
// normal state; Check is what refuses to send it.
func TestKubectlToleratesAnUnfilledParameter(t *testing.T) {
	a := Action{
		Verb:        VerbAnnotate,
		Params:      []Param{{Name: "instance", Required: true}},
		Annotations: map[string]string{"cnpg.io/fencedInstances": `["{{.Params.instance}}"]`},
	}
	got := Kubectl(a, "clusters", "data", "my-db", Vars{Name: "my-db"})
	if strings.Contains(got, "<no value>") || strings.Contains(got, "{{") {
		t.Errorf("Kubectl leaked a template for an unfilled param: %q", got)
	}
}

// A cluster-scoped kind has no namespace, so the command must not claim one.
func TestKubectlOmitsNamespaceWhenClusterScoped(t *testing.T) {
	a := Action{Verb: VerbPatch, Patch: map[string]any{"spec": map[string]any{"x": "y"}}}
	got := Kubectl(a, "clusterthings", "", "thing-1", Vars{Name: "thing-1"})
	if strings.Contains(got, " -n ") {
		t.Errorf("cluster-scoped command carries a namespace: %q", got)
	}
}

// The displayed command and the request must describe the same mutation. This
// walks every shipped action rather than a hand-picked one, so a future verb
// cannot drift the two apart silently.
func TestKubectlRendersForEveryShippedAction(t *testing.T) {
	packs, _ := Builtins()
	for _, p := range packs {
		for _, a := range p.Actions {
			v := Vars{Name: "obj", Namespace: "ns", Context: "ctx", Now: "2026-09-14T10:00:00Z"}
			got := Kubectl(a, "things", "ns", "obj", v)
			if got == "" {
				t.Errorf("lens %q action %q renders no kubectl line", p.Name, a.ID)
				continue
			}
			if strings.Contains(got, "<no value>") || strings.Contains(got, "{{") {
				t.Errorf("lens %q action %q leaked a template into the modal: %q", p.Name, a.ID, got)
			}
			// An annotate action with an empty value must use removal
			// syntax, never "key=".
			for k, raw := range a.Annotations {
				rendered, err := Render(raw, v)
				if err == nil && rendered == "" && !strings.Contains(got, k+"-") {
					t.Errorf("lens %q action %q removes %q but the command says %q", p.Name, a.ID, k, got)
				}
			}
		}
	}
}
