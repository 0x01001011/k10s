package lens

import (
	"strings"
	"testing"
)

// paramPack wraps an action body in the smallest pack that validates, so each
// case below reads as the thing it is testing and nothing else.
func paramPack(action string) string {
	return `
name: demo
actions:
` + action + `
kinds:
  - key: demo-things
    gvr: demo.example.com/v1/things
    columns:
      - {header: NAME, path: .metadata.name}
    actions: [demo-act]
`
}

const instanceParamAction = `  - id: demo-act
    label: Fence instance
    verb: annotate
    confirm: typed
    confirmValue: "{{.Params.instance}}"
    params:
      - name: instance
        label: instance
        required: true
        optionsFrom: .status.instanceNames
      - name: reason
        label: reason
        default: maintenance
    annotations:
      demo.example.com/fenced: '["{{.Params.instance}}"]'
`

func TestParamsParseOntoAnAction(t *testing.T) {
	p, err := Parse([]byte(paramPack(instanceParamAction)), "demo.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a, ok := p.Action("demo-act")
	if !ok {
		t.Fatal("demo-act not found")
	}
	if len(a.Params) != 2 {
		t.Fatalf("params = %+v, want 2", a.Params)
	}
	if a.Params[0].Name != "instance" || !a.Params[0].Required {
		t.Errorf("param 0 = %+v", a.Params[0])
	}
	if a.Params[0].OptionsFrom != ".status.instanceNames" {
		t.Errorf("optionsFrom = %q", a.Params[0].OptionsFrom)
	}
	if a.Params[1].Default != "maintenance" {
		t.Errorf("param 1 default = %q", a.Params[1].Default)
	}
	if a.ConfirmValue != "{{.Params.instance}}" {
		t.Errorf("confirmValue = %q", a.ConfirmValue)
	}
}

// A parameter is reached as {{.Params.<name>}}, so a name that is not a legal
// Go template field is a parse error the pack author must see now — not a
// render failure at the moment they press the key on a production database.
func TestParamsRejectNamesTemplatesCannotReach(t *testing.T) {
	cases := []struct {
		name, yaml, want string
	}{
		{
			name: "hyphen is not a template field",
			yaml: paramPack(`  - id: demo-act
    verb: delete
    params: [{name: target-instance}]
`),
			want: "target-instance",
		},
		{
			name: "empty name",
			yaml: paramPack(`  - id: demo-act
    verb: delete
    params: [{name: ""}]
`),
			want: "param has no name",
		},
		{
			name: "duplicate names",
			yaml: paramPack(`  - id: demo-act
    verb: delete
    params: [{name: a}, {name: a}]
`),
			want: "duplicate param",
		},
		{
			name: "unknown type",
			yaml: paramPack(`  - id: demo-act
    verb: delete
    params: [{name: a, type: colour}]
`),
			want: "colour",
		},
		{
			name: "optionsFrom is neither a path nor a gvr",
			yaml: paramPack(`  - id: demo-act
    verb: delete
    params: [{name: a, optionsFrom: "nonsense"}]
`),
			want: "optionsFrom",
		},
		{
			name: "fixed default is not among the options",
			yaml: paramPack(`  - id: demo-act
    verb: delete
    params:
      - name: a
        default: zzz
        options: [{value: x}, {value: y}]
`),
			want: "zzz",
		},
		{
			name: "confirmValue must be a template",
			yaml: paramPack(`  - id: demo-act
    verb: delete
    confirmValue: "{{.Params.unclosed"
`),
			want: "confirmValue",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.yaml), "demo.yaml")
			if err == nil {
				t.Fatal("Parse accepted it")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// A GVR in optionsFrom is the "list the related objects of this kind" form.
// It has to survive validation alongside the JSONPath form.
func TestParamsAcceptAGVRAsAnOptionSource(t *testing.T) {
	_, err := Parse([]byte(paramPack(`  - id: demo-act
    verb: delete
    params: [{name: source, optionsFrom: postgresql.cnpg.io/v1/backups}]
`)), "demo.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func demoAction(t *testing.T) Action {
	t.Helper()
	p, err := Parse([]byte(paramPack(instanceParamAction)), "demo.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a, _ := p.Action("demo-act")
	return a
}

// Fill is where a declared default becomes a value. Without it every template
// referencing an untouched param renders empty, and an annotation carrying an
// empty instance name looks like success while fencing nothing.
func TestParamsFillAppliesDefaults(t *testing.T) {
	a := demoAction(t)

	got := a.Fill(Vars{Name: "db", Params: map[string]string{"instance": "db-2"}})
	if got.Params["instance"] != "db-2" {
		t.Errorf("caller value was overwritten: %q", got.Params["instance"])
	}
	if got.Params["reason"] != "maintenance" {
		t.Errorf("default not applied: reason = %q", got.Params["reason"])
	}

	// Fill must not mutate the caller's map: the UI holds one per open form
	// and re-renders the preview on every keystroke.
	in := map[string]string{}
	a.Fill(Vars{Name: "db", Params: in})
	if len(in) != 0 {
		t.Errorf("Fill mutated the caller's map: %+v", in)
	}
}

func TestParamsRenderInTemplates(t *testing.T) {
	a := demoAction(t)
	v := a.Fill(Vars{Name: "db", Params: map[string]string{"instance": "db-2"}})

	got, err := Render(a.Annotations["demo.example.com/fenced"], v)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != `["db-2"]` {
		t.Errorf("rendered %q, want [\"db-2\"]", got)
	}
}

// Required means required. An action that fires with an empty required param
// writes a request the server happily accepts and that does nothing.
func TestParamsCheckRefusesAMissingRequiredValue(t *testing.T) {
	a := demoAction(t)

	err := a.Check(a.Fill(Vars{Name: "db"}))
	if err == nil {
		t.Fatal("Check accepted an empty required param")
	}
	if !strings.Contains(err.Error(), "instance") {
		t.Errorf("error %q does not name the param", err)
	}

	if err := a.Check(a.Fill(Vars{Name: "db", Params: map[string]string{"instance": "db-2"}})); err != nil {
		t.Errorf("Check rejected a filled param: %v", err)
	}
}

// A value outside a closed option list is a typo the pack said could not
// happen. Accepting it would write it to the cluster anyway.
func TestParamsCheckRefusesAValueOutsideAClosedList(t *testing.T) {
	p, err := Parse([]byte(paramPack(`  - id: demo-act
    verb: delete
    params:
      - name: method
        options: [{value: barmanObjectStore}, {value: volumeSnapshot}]
`)), "demo.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a, _ := p.Action("demo-act")

	err = a.Check(a.Fill(Vars{Name: "db", Params: map[string]string{"method": "typo"}}))
	if err == nil {
		t.Fatal("Check accepted a value the pack does not allow")
	}
	if !strings.Contains(err.Error(), "typo") {
		t.Errorf("error %q does not name the offending value", err)
	}

	// allowFree reopens it, for the genuinely open-ended fields — a restore
	// target timestamp cannot be enumerated.
	p2, err := Parse([]byte(paramPack(`  - id: demo-act
    verb: delete
    params:
      - name: method
        allowFree: true
        options: [{value: barmanObjectStore}]
`)), "demo.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	a2, _ := p2.Action("demo-act")
	if err := a2.Check(a2.Fill(Vars{Name: "db", Params: map[string]string{"method": "anything"}})); err != nil {
		t.Errorf("allowFree still refused a free value: %v", err)
	}
}
