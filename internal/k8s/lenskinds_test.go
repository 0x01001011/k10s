package k8s

import (
	"strings"
	"testing"

	"github.com/0x01001011/k10s/internal/lens"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	discoveryfake "k8s.io/client-go/discovery/fake"
	k8stesting "k8s.io/client-go/testing"
)

// Packs are built from literal YAML rather than the shipped builtins: a pack
// edit must not be able to break the mechanism's own tests.
func mustPack(t *testing.T, y string) lens.Pack {
	t.Helper()
	p, err := lens.Parse([]byte(y), "test.yaml")
	if err != nil {
		t.Fatalf("parse test pack: %v", err)
	}
	return p
}

const twoRequirePack = `
name: two
requires: [a.example.com/v1, b.example.com/v1]
kinds:
  - key: two-things
    name: Things
    short: tw
    group: Two
    gvr: a.example.com/v1/things
    columns: [{header: NAME, path: .metadata.name}]
`

const noRequirePack = `
name: ungated
kinds:
  - key: ungated-things
    name: Ungated
    short: ug
    group: Ungated
    gvr: c.example.com/v1/things
    columns: [{header: NAME, path: .metadata.name}]
`

const argoLikePack = `
name: argolike
requires: [argoproj.io/v1alpha1]
severities:
  health:
    ok: [Healthy]
    error: [Degraded]
    default: warn
kinds:
  - key: al-apps
    name: Applications
    short: alapp
    group: ArgoLike
    gvr: argoproj.io/v1alpha1/applications
    namespaced: true
    columns:
      - {header: NAME, path: .metadata.name}
      - {header: HEALTH, path: .status.health.status, severity: health}
    actions: [describe, al-sync]
  - key: al-projects
    name: Projects
    short: alproj
    group: ArgoLike
    gvr: argoproj.io/v1alpha1/appprojects
    namespaced: true
    columns: [{header: NAME, path: .metadata.name}]
actions:
  - id: al-sync
    label: Sync
    verb: patch
    patch: {operation: {}}
`

func TestGatePacksNeedsEveryRequire(t *testing.T) {
	two := mustPack(t, twoRequirePack)
	ungated := mustPack(t, noRequirePack)
	packs := []lens.Pack{two, ungated}

	only := map[string]bool{"a.example.com/v1": true}
	got := gatePacks(packs, only, nil)
	if len(got) != 1 || got[0].Name != "ungated" {
		t.Fatalf("a half-served pack must be dropped; got %v", packNames(got))
	}

	both := map[string]bool{"a.example.com/v1": true, "b.example.com/v1": true}
	got = gatePacks(packs, both, nil)
	if len(got) != 2 {
		t.Fatalf("both served: want 2 packs, got %v", packNames(got))
	}

	// A pack with no requires is never gated off.
	if got = gatePacks([]lens.Pack{ungated}, map[string]bool{}, nil); len(got) != 1 {
		t.Fatalf("a pack with no requires must survive an empty served set; got %v", packNames(got))
	}
}

func TestBuildLensRegCompilesKindsInPackOrder(t *testing.T) {
	served := map[string]bool{"argoproj.io/v1alpha1": true}
	reg, errs := buildLensReg(gatePacks([]lens.Pack{mustPack(t, argoLikePack)}, served, nil), builtinTaken())
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(reg.order) != 2 {
		t.Fatalf("want 2 kinds, got %d", len(reg.order))
	}
	// Contiguous and in declared order, so internal/ui emits one group header.
	if reg.order[0].Key != "al-apps" || reg.order[1].Key != "al-projects" {
		t.Fatalf("kinds out of declared order: %v", reg.order)
	}
	k := reg.order[0]
	if k.Group != "ArgoLike" || !k.Namespaced {
		t.Errorf("kind metadata wrong: %+v", k)
	}
	if strings.Join(k.Cols, ",") != "NAME,HEALTH" {
		t.Errorf("Cols = %v, want the column headers", k.Cols)
	}
	if strings.Join(k.Allowed, ",") != "describe,al-sync" {
		t.Errorf("Allowed = %v, want the kind's actions", k.Allowed)
	}
	lk, ok := reg.byKey["al-apps"]
	if !ok {
		t.Fatal("byKey missing al-apps")
	}
	if lk.gvr.Group != "argoproj.io" || lk.gvr.Version != "v1alpha1" || lk.gvr.Resource != "applications" {
		t.Errorf("gvr = %v", lk.gvr)
	}
	if _, ok := reg.byGVR[lk.gvr]; !ok {
		t.Error("byGVR missing the applications gvr")
	}
}

// A pack that would shadow a builtin key or alias is dropped WHOLE — dropping
// one kind would leave that pack's edges and actions pointing at nothing.
func TestBuildLensRegDropsCollidingPack(t *testing.T) {
	cases := map[string]string{
		"key collides with a builtin kind": `
name: bad
kinds:
  - key: pods
    name: Nope
    short: zz
    group: Bad
    gvr: x.example.com/v1/nopes
    columns: [{header: NAME, path: .metadata.name}]
`,
		"short collides with a builtin alias": `
name: bad
kinds:
  - key: bad-things
    name: Nope
    short: po
    group: Bad
    gvr: x.example.com/v1/nopes
    columns: [{header: NAME, path: .metadata.name}]
`,
		"key collides with the encoded-CR namespace": `
name: bad
kinds:
  - key: "cr|x|v1|nopes|1"
    name: Nope
    short: zz
    group: Bad
    gvr: x.example.com/v1/nopes
    columns: [{header: NAME, path: .metadata.name}]
`,
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			good := mustPack(t, noRequirePack)
			reg, errs := buildLensReg([]lens.Pack{mustPack(t, y), good}, builtinTaken())
			if len(errs) == 0 {
				t.Fatal("a colliding pack must be reported, not dropped silently")
			}
			for _, k := range reg.order {
				if k.Key != "ungated-things" {
					t.Errorf("colliding pack leaked kind %q", k.Key)
				}
			}
			if len(reg.order) != 1 {
				t.Fatalf("the good pack must survive; got %v", reg.order)
			}
		})
	}
}

// Two lens packs claiming the same key is the same hazard as colliding with a
// builtin, and it is the one a user hits by installing two community packs.
func TestBuildLensRegDropsSecondPackClaimingTheSameKey(t *testing.T) {
	a := mustPack(t, noRequirePack)
	b := mustPack(t, strings.Replace(noRequirePack, "name: ungated", "name: ungated-two", 1))
	reg, errs := buildLensReg([]lens.Pack{a, b}, builtinTaken())
	if len(errs) == 0 {
		t.Fatal("the second pack claiming ungated-things must be reported")
	}
	if len(reg.order) != 1 {
		t.Fatalf("want exactly one surviving kind, got %v", reg.order)
	}
}

// Every existing test in this package builds a Client with a nil Discovery.
// servedGroupVersions must treat that as "nothing served" rather than panic.
func TestServedGroupVersionsTolerateNilDiscovery(t *testing.T) {
	if got := servedGroupVersions(nil); len(got) != 0 {
		t.Fatalf("nil discovery must gate everything off, got %v", got)
	}
}

func TestServedGroupVersionsReadsTheFake(t *testing.T) {
	// Resources is promoted from the embedded *testing.Fake — it is NOT a
	// field of FakeDiscovery, and Go forbids setting a promoted field in a
	// composite literal.
	d := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{
		Resources: []*metav1.APIResourceList{
			{GroupVersion: "argoproj.io/v1alpha1"},
			{GroupVersion: "v1"},
		},
	}}
	var iface discovery.DiscoveryInterface = d
	got := servedGroupVersions(iface)
	if !got["argoproj.io/v1alpha1"] {
		t.Errorf("argoproj.io/v1alpha1 not reported served: %v", got)
	}
	if !got["v1"] {
		t.Errorf("core v1 not reported served: %v", got)
	}
}

func packNames(ps []lens.Pack) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

// The group/version gate alone admits the wrong operator, and the case is
// real: Argo Workflows, Rollouts and Events all serve argoproj.io/v1alpha1
// without ever installing the Application CRD. A cluster running Rollouts
// and no ArgoCD would otherwise get a sidebar of kinds whose every LIST is
// a 404.
func TestGatePacksChecksResourcesNotJustGroupVersions(t *testing.T) {
	packs := []lens.Pack{mustPack(t, argoLikePack)}
	served := map[string]bool{"argoproj.io/v1alpha1": true}

	rollouts := func(string) map[string]bool {
		return map[string]bool{"rollouts": true, "analysisruns": true}
	}
	if got := gatePacks(packs, served, rollouts); len(got) != 0 {
		t.Errorf("a pack whose resources are absent was admitted: %v", packNames(got))
	}

	argocd := func(string) map[string]bool {
		return map[string]bool{"applications": true, "appprojects": true}
	}
	if got := gatePacks(packs, served, argocd); len(got) != 1 {
		t.Errorf("a pack whose resources ARE served was dropped: %v", packNames(got))
	}

	// All of them, not any: a pack is one coherent view of one operator, and
	// half of it is a table of errors.
	partial := func(string) map[string]bool { return map[string]bool{"applications": true} }
	if got := gatePacks(packs, served, partial); len(got) != 0 {
		t.Errorf("a pack missing one of its resources was admitted: %v", packNames(got))
	}
}

// Discovery that answers nothing is not evidence of absence. Closing the
// gate on it would delete a working view over a transient API hiccup.
func TestGatePacksKeepsThePackWhenResourceDiscoverySaysNothing(t *testing.T) {
	packs := []lens.Pack{mustPack(t, argoLikePack)}
	served := map[string]bool{"argoproj.io/v1alpha1": true}
	silent := func(string) map[string]bool { return nil }
	if got := gatePacks(packs, served, silent); len(got) != 1 {
		t.Errorf("a failed resource lookup gated off a pack that passed the first gate: %v", packNames(got))
	}
}

const gvrTwinPack = `
name: twin
requires: [argoproj.io/v1alpha1]
kinds:
  - key: twin-apps
    name: Twin Apps
    short: twapp
    group: Twin
    gvr: argoproj.io/v1alpha1/applications
    columns: [{header: NAME, path: .metadata.name}]
`

// Two packs may legitimately view one resource through different columns.
// Which of them an edge resolves to must not depend on load order, and
// byGVR's own comment promises the first.
func TestByGVRKeepsTheFirstPackNotTheLast(t *testing.T) {
	first := mustPack(t, argoLikePack)
	second := mustPack(t, gvrTwinPack)
	reg, errs := buildLensReg([]lens.Pack{first, second}, builtinTaken())
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	gvr := schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
	lk, ok := reg.byGVR[gvr]
	if !ok {
		t.Fatal("no kind registered for the shared GVR")
	}
	if lk.pack.Name != "argolike" {
		t.Errorf("byGVR resolved to pack %q, want the first one (%q)", lk.pack.Name, "argolike")
	}
}

// Every shipped pack must survive the whole pipeline — parse, then compile
// into the registry without colliding with a builtin kind OR with another
// pack. A collision drops the offending pack WHOLE and silently; the only
// evidence at runtime is LensErr, which nobody reads. This is the regression
// that must never ship again, so it is asserted on the real builtins rather
// than on test YAML.
func TestEveryShippedPackBuildsIntoTheRegistry(t *testing.T) {
	packs, parseErrs := lens.Builtins()
	for _, err := range parseErrs {
		t.Errorf("builtin pack failed to parse: %v", err)
	}
	if len(packs) == 0 {
		t.Fatal("no builtin packs embedded")
	}

	reg, errs := buildLensReg(packs, builtinTaken())
	for _, err := range errs {
		t.Errorf("builtin pack rejected by the registry: %v", err)
	}
	if len(reg.packs) != len(packs) {
		t.Fatalf("registry kept %d of %d shipped packs", len(reg.packs), len(packs))
	}
	for _, p := range packs {
		for _, k := range p.Kinds {
			if _, ok := reg.byKey[k.Key]; !ok {
				t.Errorf("pack %q kind %q did not reach the registry", p.Name, k.Key)
			}
		}
	}
}
