package lens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodPack = `
name: demo
requires:
  - demo.example.com/v1
severities:
  health:
    ok: [Healthy]
    error: [Degraded]
    default: warn
kinds:
  - key: demo-things
    name: Things
    short: thing
    group: Demo
    gvr: demo.example.com/v1/things
    namespaced: true
    columns:
      - {header: NAME, path: .metadata.name}
      - {header: HEALTH, path: .status.health, severity: health}
      - {header: AGE, path: .metadata.creationTimestamp, format: age}
    actions: [describe, demo-poke]
actions:
  - id: demo-poke
    label: Poke
    verb: annotate
    confirm: "true"
    ack: .status.lastHandledRefresh
    annotations:
      demo.example.com/poke: "{{.Now}}"
edges:
  - from: v1/pods
    to: demo.example.com/v1/things
    via: label
    key: demo.example.com/thing
`

func TestParseAcceptsAWellFormedPack(t *testing.T) {
	p, err := Parse([]byte(goodPack), "demo.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Name != "demo" || p.Source != "demo.yaml" {
		t.Fatalf("name/source = %q/%q", p.Name, p.Source)
	}
	if len(p.Kinds) != 1 || p.Kinds[0].GVR != "demo.example.com/v1/things" {
		t.Fatalf("kinds = %+v", p.Kinds)
	}
	if len(p.Kinds[0].Columns) != 3 {
		t.Fatalf("columns = %+v", p.Kinds[0].Columns)
	}
	a, ok := p.Action("demo-poke")
	if !ok {
		t.Fatal("demo-poke not found")
	}
	if a.Verb != VerbAnnotate || a.Confirm != ConfirmModal {
		t.Fatalf("action = %+v", a)
	}
	if a.Ack != ".status.lastHandledRefresh" {
		t.Fatalf("ack = %q", a.Ack)
	}
	if len(p.Edges) != 1 || p.Edges[0].Via != ViaLabel {
		t.Fatalf("edges = %+v", p.Edges)
	}
}

// Each validation failure gets its own case, because a schema whose errors
// don't name the offending field is a schema people give up on.
func TestParseRejectsAndSaysWhy(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "no name",
			yaml: "kinds: []",
			want: "pack has no name",
		},
		{
			name: "bad gvr",
			yaml: `
name: bad
kinds:
  - key: k
    gvr: justaresource
    columns:
      - {header: NAME, path: .metadata.name}
`,
			want: `gvr "justaresource"`,
		},
		{
			name: "unknown verb",
			yaml: `
name: bad
actions:
  - id: a
    verb: teleport
kinds:
  - key: k
    gvr: g/v1/r
    columns:
      - {header: NAME, path: .metadata.name}
`,
			want: `unknown verb "teleport"`,
		},
		{
			name: "unparseable jsonpath",
			yaml: `
name: bad
kinds:
  - key: k
    gvr: g/v1/r
    columns:
      - {header: NAME, path: ".spec[?(@.x ==)]"}
`,
			want: "bad jsonpath",
		},
		{
			name: "action id not declared",
			yaml: `
name: bad
kinds:
  - key: k
    gvr: g/v1/r
    columns:
      - {header: NAME, path: .metadata.name}
    actions: [nope]
`,
			want: `unknown action "nope"`,
		},
		{
			name: "severity table not declared",
			yaml: `
name: bad
kinds:
  - key: k
    gvr: g/v1/r
    columns:
      - {header: H, path: .status.h, severity: ghost}
`,
			want: `undeclared severity table "ghost"`,
		},
		{
			name: "unknown format",
			yaml: `
name: bad
kinds:
  - key: k
    gvr: g/v1/r
    columns:
      - {header: H, path: .status.h, format: hexadecimal}
`,
			want: `unknown format "hexadecimal"`,
		},
		{
			name: "no kinds",
			yaml: "name: bad",
			want: "declares no kinds",
		},
		{
			name: "annotate with no annotations",
			yaml: `
name: bad
actions:
  - id: a
    verb: annotate
kinds:
  - key: k
    gvr: g/v1/r
    columns:
      - {header: NAME, path: .metadata.name}
`,
			want: "annotate with no annotations",
		},
		{
			name: "unknown edge via",
			yaml: `
name: bad
kinds:
  - key: k
    gvr: g/v1/r
    columns:
      - {header: NAME, path: .metadata.name}
edges:
  - {from: v1/pods, to: g/v1/r, via: telepathy, key: x}
`,
			want: `unknown via "telepathy"`,
		},
		{
			name: "duplicate kind key",
			yaml: `
name: bad
kinds:
  - key: k
    gvr: g/v1/r
    columns: [{header: NAME, path: .metadata.name}]
  - key: k
    gvr: g/v1/other
    columns: [{header: NAME, path: .metadata.name}]
`,
			want: `duplicate kind key "k"`,
		},
		{
			name: "requires is not group/version",
			yaml: `
name: bad
requires: [justagroup]
kinds:
  - key: k
    gvr: g/v1/r
    columns: [{header: NAME, path: .metadata.name}]
`,
			want: `requires "justagroup"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml), "bad.yaml")
			if err == nil {
				t.Fatal("want an error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
			// Every error names the file, or the user cannot find it.
			if !strings.Contains(err.Error(), "bad.yaml") {
				t.Fatalf("error %q does not name the source file", err)
			}
		})
	}
}

// One malformed file must not cost the user their other lenses — the same
// rule internal/plugin follows.
func TestLoadSkipsABrokenPackAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "good.yaml", goodPack)
	write(t, dir, "broken.yaml", "name: broken\nkinds: [{key: k, gvr: nope}]")

	packs, errs := Load(dir)
	if len(errs) != 1 {
		t.Fatalf("want exactly one error, got %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "broken.yaml") {
		t.Fatalf("error does not name the broken file: %v", errs[0])
	}
	if !hasPack(packs, "demo") {
		t.Fatal("the good pack was dropped along with the broken one")
	}
	// The builtins survive a broken user file too.
	if !hasPack(packs, "argocd") {
		t.Fatal("builtins missing after a broken user pack")
	}
}

func TestLoadLetsAUserPackOverrideABuiltin(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "argocd.yaml", strings.Replace(goodPack, "name: demo", "name: argocd", 1))

	packs, errs := Load(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	var found int
	for _, p := range packs {
		if p.Name == "argocd" {
			found++
			if p.Source == "builtin/argocd.yaml" {
				t.Fatal("builtin won over the user's pack")
			}
		}
	}
	if found != 1 {
		t.Fatalf("argocd appears %d times, want 1", found)
	}
}

func TestLoadTreatsAMissingDirectoryAsNormal(t *testing.T) {
	packs, errs := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if len(errs) != 0 {
		t.Fatalf("a missing lens dir is not a problem to report: %v", errs)
	}
	if len(packs) == 0 {
		t.Fatal("builtins should still load")
	}
}

// The shipped packs are the whole point of the mechanism; a typo in
// one of them is a build-time bug, not a user's problem.
func TestBuiltinPacksAllParse(t *testing.T) {
	packs, errs := Builtins()
	for _, err := range errs {
		t.Errorf("builtin pack failed to parse: %v", err)
	}
	for _, want := range []string{
		"argocd", "cnpg", "fleet", "k3s-helm", "k3s-upgrade",
		"kargo", "longhorn", "rancher", "traefik", "victoriametrics",
	} {
		if !hasPack(packs, want) {
			t.Errorf("builtin pack %q missing", want)
		}
	}
}

// Every builtin must be discovery-gated. A pack without `requires` would
// put its kinds in the Resources pane on every cluster, including ones
// where the operator is not installed.
func TestBuiltinPacksAreDiscoveryGated(t *testing.T) {
	packs, _ := Builtins()
	for _, p := range packs {
		if len(p.Requires) == 0 {
			t.Errorf("lens %q declares no requires — it would appear on every cluster", p.Name)
		}
	}
}

// An action that templates .Selected must say so, unless the row's own name
// is genuinely a valid value. Nothing else catches the case where .Selected
// silently falls back to the wrong string: a CNPG instance is "my-db-2", not
// "my-db", and fencing the wrong name looks like it worked.
func TestActionsTemplatingSelectedAreMarked(t *testing.T) {
	packs, _ := Builtins()
	for _, p := range packs {
		for _, a := range p.Actions {
			if !mentionsSelected(a) || a.RequiresSelection {
				continue
			}
			t.Errorf("lens %q action %q templates {{.Selected}} but is not marked requiresSelection — "+
				"if the row's own name really is a valid value here, say so in a comment and mark it anyway",
				p.Name, a.ID)
		}
	}
}

func mentionsSelected(a Action) bool {
	found := false
	walk := func(s string) {
		if strings.Contains(s, ".Selected") {
			found = true
		}
	}
	for _, v := range a.Annotations {
		walk(v)
	}
	var tree func(any)
	tree = func(n any) {
		switch v := n.(type) {
		case string:
			walk(v)
		case map[string]any:
			for _, e := range v {
				tree(e)
			}
		case []any:
			for _, e := range v {
				tree(e)
			}
		}
	}
	tree(a.Patch)
	tree(a.Template)
	return found
}

// The ArgoCD RBAC bypass must reach the modal as DATA. Keyed on a Go constant
// it would be a test asserting a string equals itself; as a YAML comment the
// parser discards it entirely.
func TestArgocdSyncCarriesTheRBACDisclosure(t *testing.T) {
	packs, _ := Builtins()
	var argocd Pack
	for _, p := range packs {
		if p.Name == "argocd" {
			argocd = p
		}
	}
	for _, id := range []string{"argocd-sync", "argocd-rollback"} {
		a, ok := argocd.Action(id)
		if !ok {
			t.Fatalf("%s missing from the argocd pack", id)
		}
		if !strings.Contains(a.Notice, "argocd-rbac-cm") {
			t.Errorf("%s notice does not disclose the argocd-rbac-cm bypass: %q", id, a.Notice)
		}
		if len(a.RefuseWhen) == 0 {
			t.Errorf("%s declares no preconditions; the spec requires refusing while .operation is set", id)
		}
	}
}

func TestParseValidatesRefuseWhen(t *testing.T) {
	bad := map[string]string{
		"refuseWhen has no reason": `
name: bad
actions:
  - id: a
    verb: annotate
    annotations: {x: "1"}
    refuseWhen:
      - {path: .operation}
kinds:
  - key: k
    gvr: g/v1/r
    columns: [{header: NAME, path: .metadata.name}]
`,
		"bad jsonpath": `
name: bad
actions:
  - id: a
    verb: annotate
    annotations: {x: "1"}
    refuseWhen:
      - {path: ".spec[?(@.x ==)]", reason: nope}
kinds:
  - key: k
    gvr: g/v1/r
    columns: [{header: NAME, path: .metadata.name}]
`,
	}
	for name, y := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(y), "bad.yaml"); err == nil {
				t.Fatal("want an error, got none")
			}
		})
	}
}

func TestSeverityLevel(t *testing.T) {
	s := Severity{OK: []string{"Healthy"}, Error: []string{"Degraded"}, Default: "warn"}
	for in, want := range map[string]Level{
		"Healthy":  LevelOK,
		"Degraded": LevelError,
		// The CNPG case: an unlisted human sentence is a warning, not a
		// crash and not a silent OK.
		"Cluster is unrecoverable and needs manual intervention": LevelWarn,
		"": LevelWarn,
	} {
		if got := s.Level(in); got != want {
			t.Errorf("Level(%q) = %v, want %v", in, got, want)
		}
	}

	// With no default, an unlisted value is unknown rather than healthy.
	bare := Severity{OK: []string{"Healthy"}}
	if got := bare.Level("Whatever"); got != LevelUnknown {
		t.Errorf("Level with no default = %v, want unknown", got)
	}
}

// Worst-first is the whole reason severity exists: k9s sorts STATUS
// alphabetically and scatters CrashLoopBackOff among Running.
func TestLevelRankPutsTroubleFirst(t *testing.T) {
	order := []Level{LevelError, LevelWarn, LevelUnknown, LevelOK}
	for i := 1; i < len(order); i++ {
		if order[i-1].Rank() >= order[i].Rank() {
			t.Fatalf("%v does not rank before %v", order[i-1], order[i])
		}
	}
}

func TestParseGVR(t *testing.T) {
	cases := map[string][3]string{
		"argoproj.io/v1alpha1/applications": {"argoproj.io", "v1alpha1", "applications"},
		"v1/pods":                           {"", "v1", "pods"},
	}
	for in, want := range cases {
		g, v, r, err := ParseGVR(in)
		if err != nil {
			t.Fatalf("ParseGVR(%q): %v", in, err)
		}
		if g != want[0] || v != want[1] || r != want[2] {
			t.Errorf("ParseGVR(%q) = %q/%q/%q, want %v", in, g, v, r, want)
		}
	}
	for _, bad := range []string{"pods", "a/b/c/d", "/pods", "v1/"} {
		if _, _, _, err := ParseGVR(bad); err == nil {
			t.Errorf("ParseGVR(%q) accepted a malformed gvr", bad)
		}
	}
}

func TestBracedLeavesAnAlreadyBracedPathAlone(t *testing.T) {
	if got := Braced(".metadata.name"); got != "{.metadata.name}" {
		t.Errorf("Braced bare = %q", got)
	}
	if got := Braced("{.metadata.name}"); got != "{.metadata.name}" {
		t.Errorf("Braced braced = %q", got)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasPack(packs []Pack, name string) bool {
	for _, p := range packs {
		if p.Name == name {
			return true
		}
	}
	return false
}
