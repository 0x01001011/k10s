package k8s

import (
	"testing"
	"time"

	"github.com/0x01001011/k10s/internal/domain"
	apiextfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

var appsGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

func app(ns, name, sync, health string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name":              name,
			"namespace":         ns,
			"creationTimestamp": time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		},
		"spec": map[string]any{"project": "default"},
		"status": map[string]any{
			"sync":   map[string]any{"status": sync, "revision": "6f4c1b2a9e8d7c6b5a49382716"},
			"health": map[string]any{"status": health},
		},
	}}
}

// newTestStoreWithLenses builds a Store whose discovery serves the given
// group/versions, so the lens gate can actually open.
//
// The scheme is a FRESH runtime.NewScheme() per call, never the package-global
// scheme.Scheme: NewSimpleDynamicClientWithCustomListKinds MUTATES the scheme
// it is handed (it registers each list kind), so using the global one would
// leak these registrations into every other test in the package.
func newTestStoreWithLenses(t *testing.T, groupVersions []string, objs ...runtime.Object) *Store {
	t.Helper()
	// Never read the developer's real ~/.k10s/lenses.
	t.Setenv("K10S_LENS_DIR", t.TempDir())

	res := make([]*metav1.APIResourceList, 0, len(groupVersions))
	for _, gv := range groupVersions {
		res = append(res, &metav1.APIResourceList{GroupVersion: gv})
	}

	cs := fake.NewSimpleClientset()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{appsGVR: "ApplicationList"},
		objs...,
	)
	c := &Client{
		RestConfig:     &rest.Config{Host: "https://fake"},
		Clientset:      cs,
		Dynamic:        dyn,
		Metrics:        metricsfake.NewSimpleClientset(),
		CurrentContext: "test-context",
	}
	if len(groupVersions) > 0 {
		// Resources is promoted from the embedded *testing.Fake; it is not a
		// field of FakeDiscovery and cannot be set in its literal.
		c.Discovery = &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{Resources: res}}
	}
	s, err := newStoreFrom(c, apiextfake.NewSimpleClientset())
	if err != nil {
		t.Fatalf("newStoreFrom: %v", err)
	}
	t.Cleanup(s.Close)
	waitLens(t, s)
	return s
}

func waitLens(t *testing.T, s *Store) {
	t.Helper()
	select {
	case <-s.lensReady:
	case <-time.After(5 * time.Second):
		t.Fatal("lens gate did not finish within 5s")
	}
}

func lensKeySet(ks []domain.Kind) map[string]bool {
	out := map[string]bool{}
	for _, k := range ks {
		out[k.Key] = true
	}
	return out
}

// A cluster without the operator must show no lens kinds at all — and must
// not have asked the cluster anything to find that out.
func TestLensKindsHiddenWithoutDiscovery(t *testing.T) {
	s := newTestStoreWithLenses(t, nil)
	got, want := s.Kinds(), Kinds()
	if len(got) != len(want) {
		t.Fatalf("nil discovery must yield exactly the builtin kinds: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Key != want[i].Key {
			t.Fatalf("kind %d = %q, want %q", i, got[i].Key, want[i].Key)
		}
	}
	if dyn, ok := s.c.Dynamic.(*dynamicfake.FakeDynamicClient); ok {
		if n := len(dyn.Actions()); n != 0 {
			t.Errorf("a gated-off cluster issued %d dynamic actions, want 0: %v", n, dyn.Actions())
		}
	}
}

func TestLensKindsAppearWhenServed(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"})
	all := s.Kinds()
	builtins := Kinds()

	// Append-only: every builtin keeps its index, so a selection cannot move
	// under the user when the gate lands mid-session.
	if len(all) <= len(builtins) {
		t.Fatalf("no lens kinds appeared: %d kinds", len(all))
	}
	for i := range builtins {
		if all[i].Key != builtins[i].Key {
			t.Fatalf("builtin at %d moved: %q became %q", i, builtins[i].Key, all[i].Key)
		}
	}

	k := lensKeySet(all)
	for _, want := range []string{"argocd-apps", "argocd-appsets", "argocd-projects"} {
		if !k[want] {
			t.Errorf("lens kind %q missing after the gate opened", want)
		}
	}
	// A pack whose group/version is NOT served must stay hidden.
	for _, unwanted := range []string{"cnpg-clusters", "lh-volumes", "kargo-stages", "traefik-routes"} {
		if k[unwanted] {
			t.Errorf("lens kind %q appeared although its API group is not served", unwanted)
		}
	}

	// The three argocd kinds must be contiguous, so internal/ui draws one
	// group header rather than three.
	first, last := -1, -1
	for i, kind := range all {
		if kind.Group == "ArgoCD" {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || last-first+1 != 3 {
		t.Errorf("ArgoCD kinds are not contiguous: first=%d last=%d", first, last)
	}
}

func TestLensGVRForResolvesLensKeys(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"})
	gvr, namespaced, err := s.gvrFor("argocd-apps")
	if err != nil {
		t.Fatalf("gvrFor: %v", err)
	}
	if gvr != appsGVR {
		t.Errorf("gvr = %v, want %v", gvr, appsGVR)
	}
	if !namespaced {
		t.Error("argocd-apps is namespaced: true in the pack")
	}
	if _, _, err := s.gvrFor("no-such-kind"); err == nil {
		t.Error("an unknown kind must still error")
	}
}

// Opening one lens kind must watch exactly that GVR — not every kind in the
// pack, and not anything else.
func TestOpeningOneLensKindWatchesOnlyThatGVR(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("default", "a-app", "Synced", "Healthy"))

	s.Rows("argocd-apps", domain.AllNamespaces)

	if !s.isStarted("argocd-apps", domain.AllNamespaces) {
		t.Fatal("the opened kind has no informer")
	}
	for _, other := range []string{"argocd-appsets", "argocd-projects", "pods", "deployments"} {
		if s.isStarted(other, domain.AllNamespaces) {
			t.Errorf("opening argocd-apps also started %q", other)
		}
	}
}

// Drawing a sidebar badge must never open a watch. This is what keeps a
// cluster running five operators from paying for kinds nobody opened.
func TestLensRowCountStartsNoInformer(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("default", "a-app", "Synced", "Healthy"))

	for _, k := range s.Kinds() {
		if _, ok := s.lensKindFor(k.Key); !ok {
			continue
		}
		if got := s.RowCount(k.Key, domain.AllNamespaces); got != domain.CountUnknown {
			t.Errorf("RowCount(%q) = %d before the kind was opened, want CountUnknown", k.Key, got)
		}
		if s.isStarted(k.Key, domain.AllNamespaces) {
			t.Errorf("RowCount(%q) opened an informer", k.Key)
		}
	}
}

// Worst-first, then A→Z within a rank. The whole reason severity exists.
func TestLensSeveritySortsWorstFirst(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("default", "z-app", "OutOfSync", "Degraded"),
		app("default", "a-app", "OutOfSync", "Degraded"),
		app("default", "m-app", "Synced", "Progressing"),
		app("default", "b-app", "Synced", "Healthy"),
	)
	syncStore(t, s, "argocd-apps")

	_, rows := s.Rows("argocd-apps", "default")
	if len(rows) != 4 {
		t.Fatalf("want 4 rows, got %d: %v", len(rows), rows)
	}
	want := []string{"a-app", "z-app", "m-app", "b-app"}
	for i, w := range want {
		if rows[i][0] != w {
			t.Fatalf("row %d = %q, want %q (full order: %v)", i, rows[i][0], w, rowNames(rows))
		}
	}
}

// The row that motivated the worst-across-columns rule: SYNC is declared
// before HEALTH in the argocd pack, so a first-column-wins rule sorts a
// Synced-but-Degraded Application as healthy — the exact row an operator most
// needs to see, buried at the bottom.
func TestLensSeverityTakesTheWorstColumnNotTheFirst(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("default", "aaa-fine", "Synced", "Healthy"),
		app("default", "zzz-degraded", "Synced", "Degraded"),
	)
	syncStore(t, s, "argocd-apps")

	_, rows := s.Rows("argocd-apps", "default")
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %v", rows)
	}
	if rows[0][0] != "zzz-degraded" {
		t.Errorf("a Synced-but-Degraded app must sort first, got %v", rowNames(rows))
	}
}

// An empty severity cell must not be graded. ArgoCD's OPERATION column is
// empty on any app that is not mid-sync and its table defaults to unknown, so
// grading empties would rank every healthy row as "needs a look".
func TestLensEmptySeverityCellIsNotGraded(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("default", "b-healthy", "Synced", "Healthy"),
		app("default", "a-healthy", "Synced", "Healthy"),
	)
	syncStore(t, s, "argocd-apps")

	// Both rows have an empty OPERATION. If that were graded as unknown both
	// would share rank 2 — still equal, so assert the stronger property: they
	// rank as OK, which means plain alphabetical order survives.
	_, rows := s.Rows("argocd-apps", "default")
	if len(rows) != 2 || rows[0][0] != "a-healthy" {
		t.Errorf("healthy rows should stay alphabetical, got %v", rowNames(rows))
	}
	lk, ok := s.lensKindFor("argocd-apps")
	if !ok {
		t.Fatal("argocd-apps not registered")
	}
	for _, r := range s.lensRows(lk, "default") {
		if r.rank != 3 {
			t.Errorf("row %v ranked %d, want 3 (ok) — an empty OPERATION cell was graded", r.row, r.rank)
		}
	}
}

// Under :ns all the NAMESPACE column is prepended, and worst-first must still
// win — namespace grouping must not re-impose itself over severity.
func TestLensSeveritySurvivesAllNamespaces(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("zeta", "healthy-app", "Synced", "Healthy"),
		app("alpha", "broken-app", "OutOfSync", "Degraded"),
	)
	syncStore(t, s, "argocd-apps")

	cols, rows := s.Rows("argocd-apps", domain.AllNamespaces)
	if cols[0] != "NAMESPACE" {
		t.Fatalf("ns=all must prepend NAMESPACE, got %v", cols)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %v", rows)
	}
	if rows[0][1] != "broken-app" {
		t.Errorf("degraded row did not float to the top: %v", rows)
	}
}

func TestLensRowRendersEveryDeclaredColumn(t *testing.T) {
	s := newTestStoreWithLenses(t, []string{"argoproj.io/v1alpha1"},
		app("default", "a-app", "Synced", "Healthy"))
	syncStore(t, s, "argocd-apps")

	cols, rows := s.Rows("argocd-apps", "default")
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %v", rows)
	}
	if len(rows[0]) != len(cols) {
		t.Fatalf("row is %d wide but there are %d columns: %v vs %v", len(rows[0]), len(cols), rows[0], cols)
	}
	cell := map[string]string{}
	for i, c := range cols {
		cell[c] = rows[0][i]
	}
	if cell["SYNC"] != "Synced" || cell["HEALTH"] != "Healthy" {
		t.Errorf("status columns wrong: %v", cell)
	}
	if cell["PROJECT"] != "default" {
		t.Errorf("PROJECT = %q, want %q", cell["PROJECT"], "default")
	}
	// truncate: 7 on the revision.
	if len([]rune(cell["REVISION"])) != 7 {
		t.Errorf("REVISION = %q, want 7 runes", cell["REVISION"])
	}
	// OPERATION has no value on this object: an empty cell, not a panic and
	// not a short row.
	if cell["OPERATION"] != "" {
		t.Errorf("OPERATION = %q, want an empty cell", cell["OPERATION"])
	}
}

// A cluster-scoped lens kind has no namespace to filter on. Pushing its rows
// through applyNamespace compared ns "-" against "default" and discarded
// every one — an empty table in every namespace view, next to a sidebar badge
// that counted them correctly.
func TestClusterScopedLensKindRendersItsRows(t *testing.T) {
	const clusterScopedPack = `
name: csp
requires: [cs.example.com/v1]
kinds:
  - key: cs-things
    name: ClusterThings
    short: cst
    group: CS
    gvr: cs.example.com/v1/things
    namespaced: false
    columns: [{header: NAME, path: .metadata.name}]
`
	gvr := schema.GroupVersionResource{Group: "cs.example.com", Version: "v1", Resource: "things"}
	mk := func(name string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "cs.example.com/v1",
			"kind":       "Thing",
			"metadata":   map[string]any{"name": name},
		}}
	}
	s := lensStoreWithPack(t, clusterScopedPack, gvr, "ThingList", mk("b-thing"), mk("a-thing"))
	syncStore(t, s, "cs-things")

	// Every namespace view must show them, exactly as a builtin
	// cluster-scoped kind (pvs, nodes, clusterroles) does.
	for _, ns := range []string{"", "default", "kube-system", domain.AllNamespaces} {
		cols, rows := s.Rows("cs-things", ns)
		if len(rows) != 2 {
			t.Errorf("ns=%q: got %d rows, want 2", ns, len(rows))
			continue
		}
		if cols[0] == "NAMESPACE" {
			t.Errorf("ns=%q: a cluster-scoped kind must not gain a NAMESPACE column: %v", ns, cols)
		}
		if rows[0][0] != "a-thing" {
			t.Errorf("ns=%q: rows not sorted: %v", ns, rowNames(rows))
		}
	}

	// And the badge must agree with the table rather than contradict it.
	if got := s.RowCount("cs-things", "default"); got != 2 {
		t.Errorf("RowCount = %d, want 2 to match the table", got)
	}
}

func rowNames(rows [][]string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r[0])
	}
	return out
}

// syncStore opens a kind and waits for its initial list, so assertions see
// data rather than an empty cache. Production never waits like this.
func syncStore(t *testing.T, s *Store, kind string) {
	t.Helper()
	s.ensure(kind, domain.AllNamespaces)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.SyncedFor(kind, domain.AllNamespaces) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("informer for %q did not sync", kind)
}
