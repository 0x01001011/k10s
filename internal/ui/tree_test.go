package ui

import (
	"strings"
	"testing"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/mock"
)

// fakeRelated is a relationship graph written out by hand, so the walk can be
// pushed into the shapes a real cluster only occasionally produces: a cycle,
// a fan-out wider than the budget, a chain deeper than the limit.
type fakeRelated map[string][]domain.Ref

func (f fakeRelated) Related(kind, ns, name string) ([]domain.Ref, error) {
	return append([]domain.Ref(nil), f[kind+"/"+name]...), nil
}

// fakeLevels grades by value, standing in for a lens pack's severity table.
// It is needed wherever the value is pack vocabulary — "Unhealthy" and
// "Synced" mean nothing to the builtin status colours, which is exactly why
// packs declare severities at all.
type fakeLevels map[string]string

func (f fakeLevels) CellLevel(kind, column, value string) string { return f[value] }

// newTestWalk builds a walk over graph, graded by the demo backend. The demo
// has no rows for the synthetic kinds these tests use, so every node comes
// back ungraded — which is what keeps the assertions about SHAPE rather than
// about some fixture's statuses.
func newTestWalk(graph fakeRelated, budget int) *treeWalk {
	src := mock.New("")
	return &treeWalk{
		rel:    graph,
		status: &statusLookup{src: src, cache: map[string]*statusTable{}},
		seen:   map[string]bool{},
		budget: budget,
	}
}

// walkFrom mirrors showTree's own setup, including seeding the root into the
// visited set — the part a test that called node() directly would skip.
func walkFrom(w *treeWalk, kind, name string, depth int) *treeNode {
	root := domain.Ref{Kind: kind, Name: name, Loaded: true}
	w.seen[refKey(root)] = true
	return w.node(root, depth)
}

// A ref that points back at an ancestor must not be walked again. Kargo's
// stage->stage edge resolves in both directions, so dev lists staging
// downstream while staging lists dev upstream: a per-branch visited set would
// bounce between them until the depth counter ran out, drawing the same two
// stages over and over.
func TestTreeWalkCycleIsFinite(t *testing.T) {
	graph := fakeRelated{
		"stage/dev":     {{Kind: "stage", Name: "staging", Rel: "field", Loaded: true}},
		"stage/staging": {{Kind: "stage", Name: "dev", Rel: "field", Loaded: true}},
	}
	w := newTestWalk(graph, treeMaxNodes)
	got := renderTreeView(walkFrom(w, "stage", "dev", treeDepth), nil, "", w.truncated)

	if n := strings.Count(got, "stage/dev"); n != 1 {
		t.Errorf("stage/dev appears %d times, want 1:\n%s", n, got)
	}
	if n := strings.Count(got, "stage/staging"); n != 1 {
		t.Errorf("stage/staging appears %d times, want 1:\n%s", n, got)
	}
}

// The root is seeded into the visited set before the walk, so a grandchild
// pointing back at it draws nothing. Without that the root reappears as its
// own descendant, which reads as a cycle in the cluster rather than a bug in
// the renderer.
func TestTreeWalkRootIsNotItsOwnDescendant(t *testing.T) {
	graph := fakeRelated{
		"stage/dev":     {{Kind: "stage", Name: "staging", Rel: "field", Loaded: true}},
		"stage/staging": {{Kind: "stage", Name: "prod", Rel: "field", Loaded: true}},
		"stage/prod":    {{Kind: "stage", Name: "dev", Rel: "field", Loaded: true}},
	}
	w := newTestWalk(graph, treeMaxNodes)
	got := renderTreeView(walkFrom(w, "stage", "dev", treeDepth), nil, "", w.truncated)

	if n := strings.Count(got, "stage/dev"); n != 1 {
		t.Errorf("root drawn %d times, want 1:\n%s", n, got)
	}
}

// Depth bounds the CHAIN: a pipeline longer than treeDepth stops rather than
// following every hop to the far end of the cluster.
func TestTreeWalkStopsAtDepth(t *testing.T) {
	graph := fakeRelated{
		"n/a": {{Kind: "n", Name: "b", Rel: "field", Loaded: true}},
		"n/b": {{Kind: "n", Name: "c", Rel: "field", Loaded: true}},
		"n/c": {{Kind: "n", Name: "d", Rel: "field", Loaded: true}},
		"n/d": {{Kind: "n", Name: "e", Rel: "field", Loaded: true}},
	}
	w := newTestWalk(graph, treeMaxNodes)
	got := renderTreeView(walkFrom(w, "n", "a", 3), nil, "", w.truncated)

	if strings.Contains(got, "n/e") {
		t.Errorf("walked past depth 3:\n%s", got)
	}
	for _, want := range []string{"n/a", "n/b", "n/c", "n/d"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s within depth 3:\n%s", want, got)
		}
	}
}

// The budget bounds the WHOLE tree. An AppProject with hundreds of
// Applications must stop and say so, because a tree that quietly stops is
// indistinguishable from a cluster that really is that small.
func TestTreeWalkBudgetTruncatesAndSaysSo(t *testing.T) {
	var kids []domain.Ref
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		kids = append(kids, domain.Ref{Kind: "app", Name: n, Rel: "field", Loaded: true})
	}
	w := newTestWalk(fakeRelated{"proj/platform": kids}, 2)
	got := renderTreeView(walkFrom(w, "proj", "platform", treeDepth), nil, "", w.truncated)

	if !w.truncated {
		t.Fatal("budget exhausted but truncated was not set")
	}
	if !strings.Contains(got, "truncated at") {
		t.Errorf("truncation not reported:\n%s", got)
	}
	if n := strings.Count(got, "app/"); n != 2 {
		t.Errorf("drew %d children, want 2 (the budget):\n%s", n, got)
	}
}

// An endpoint nobody has opened is a leaf that says why, rather than an empty
// name or a silent omission: "not loaded" and "does not exist" are different
// answers and only one of them is fixed by opening that kind.
func TestTreeNotLoadedEndpointIsALeafThatSaysWhy(t *testing.T) {
	graph := fakeRelated{
		"stage/dev": {{Kind: "kargo-warehouses", Rel: "field", Loaded: false}},
		// Never reached: an unloaded endpoint is not walked into.
		"kargo-warehouses/": {{Kind: "stage", Name: "ghost", Rel: "field", Loaded: true}},
	}
	w := newTestWalk(graph, treeMaxNodes)
	got := renderTreeView(walkFrom(w, "stage", "dev", treeDepth), nil, "", w.truncated)

	if !strings.Contains(got, "kargo-warehouses   (not loaded") {
		t.Errorf("unloaded endpoint not marked:\n%s", got)
	}
	if strings.Contains(got, "ghost") {
		t.Errorf("walked into an unloaded endpoint:\n%s", got)
	}
}

// The spine has to be a tree: a child of a non-last sibling keeps the
// vertical, a child of the last one does not. Get this wrong and the
// indentation stops carrying the structure, which is the whole point.
func TestTreeRendersBoxDrawingSpine(t *testing.T) {
	graph := fakeRelated{
		"root/r": {
			{Kind: "a", Name: "one", Rel: "field", Loaded: true},
			{Kind: "b", Name: "two", Rel: "field", Loaded: true},
		},
		"a/one": {{Kind: "c", Name: "deep", Rel: "field", Loaded: true}},
		"b/two": {{Kind: "d", Name: "last", Rel: "field", Loaded: true}},
	}
	w := newTestWalk(graph, treeMaxNodes)
	got := strings.Split(renderTreeView(walkFrom(w, "root", "r", treeDepth), nil, "", w.truncated), "\n")

	want := []string{
		"root/r",
		"├─ a/one   via field",
		"│  └─ c/deep   via field",
		"└─ b/two   via field",
		"   └─ d/last   via field",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

// The namespace is printed only where it CHANGES. An ArgoCD tree crosses
// namespaces constantly, and repeating the same one on every line of a
// single-namespace pipeline buries the one line where it matters.
func TestTreeLinePrintsOnlyForeignNamespaces(t *testing.T) {
	same := treeLine(&treeNode{ref: domain.Ref{Kind: "app", Namespace: "argocd", Name: "web"}}, nil, "argocd")
	if same != "app/web" {
		t.Errorf("root namespace repeated: %q, want %q", same, "app/web")
	}
	other := treeLine(&treeNode{ref: domain.Ref{Kind: "po", Namespace: "prod", Name: "web-0"}}, nil, "argocd")
	if !strings.Contains(other, "prod") {
		t.Errorf("foreign namespace dropped: %q", other)
	}
}

// Neighbours arrive in informer-cache order, which is not stable between
// calls. Two looks at the same pipeline must produce the same text, or
// diffing them is useless.
func TestTreeOrderIsStable(t *testing.T) {
	forward := fakeRelated{"root/r": {
		{Kind: "a", Name: "x", Rel: "field", Loaded: true},
		{Kind: "a", Name: "y", Rel: "field", Loaded: true},
		{Kind: "b", Name: "z", Rel: "field", Loaded: true},
	}}
	reversed := fakeRelated{"root/r": {
		{Kind: "b", Name: "z", Rel: "field", Loaded: true},
		{Kind: "a", Name: "y", Rel: "field", Loaded: true},
		{Kind: "a", Name: "x", Rel: "field", Loaded: true},
	}}
	w1, w2 := newTestWalk(forward, treeMaxNodes), newTestWalk(reversed, treeMaxNodes)
	a := renderTreeView(walkFrom(w1, "root", "r", treeDepth), nil, "", false)
	b := renderTreeView(walkFrom(w2, "root", "r", treeDepth), nil, "", false)
	if a != b {
		t.Errorf("same graph rendered two ways:\n%s\n---\n%s", a, b)
	}
}

// An object with no resolved neighbours gets the same explanation R gives,
// because the cause is the same: "nothing here" on its own reads as a cluster
// with no relationships rather than a kind nobody has opened.
func TestTreeEmptyExplainsItself(t *testing.T) {
	w := newTestWalk(fakeRelated{}, treeMaxNodes)
	got := renderTreeView(walkFrom(w, "stage", "lonely", treeDepth), nil, "", false)
	if !strings.Contains(got, "No declared relationships resolved") {
		t.Errorf("empty tree gave no explanation:\n%s", got)
	}
}

// A table with no NAMESPACE column carries no namespace to compare against,
// and Rows already scoped it to the namespace asked for. Matching on one
// anyway failed EVERY status lookup outside ns=all, which left every node in
// a single-namespace view ungraded — the exact thing the tree exists to show.
func TestStatusLookupMatchesWhenTableHasNoNamespaceColumn(t *testing.T) {
	cols := []string{"NAME", "HEALTH", "AGE"}
	s := &statusLookup{cl: fakeLevels{"Unhealthy": "error"}, cache: map[string]*statusTable{
		"kargo-stages\x00kargo-demo": {
			cols: cols,
			rows: [][]string{{"prod", "Unhealthy", "22d"}},
			id:   identFor("kargo-stages", cols),
		},
	}}
	got := s.of(domain.Ref{Kind: "kargo-stages", Namespace: "kargo-demo", Name: "prod", Loaded: true})
	if !strings.Contains(got, "Unhealthy") {
		t.Errorf("status = %q, want it to carry Unhealthy", got)
	}
	if !strings.HasPrefix(got, severityGlyph("error")) {
		t.Errorf("status = %q, want the error glyph %q", got, severityGlyph("error"))
	}
}

// One line carries at most treeStatusCells graded values. More wraps the
// line, and a wrapped tree loses the indentation that makes it a tree.
func TestStatusLookupCapsGradedCells(t *testing.T) {
	cols := []string{"NAME", "A", "B", "C", "D"}
	s := &statusLookup{cache: map[string]*statusTable{
		"k\x00": {
			cols: cols,
			rows: [][]string{{"x", "Failed", "Failed", "Failed", "Failed"}},
			id:   identFor("k", cols),
		},
	}}
	got := s.of(domain.Ref{Kind: "k", Name: "x", Loaded: true})
	if n := strings.Count(got, "Failed"); n != treeStatusCells {
		t.Errorf("rendered %d graded cells, want %d: %q", n, treeStatusCells, got)
	}
}

// The demo is where this feature gets its screenshot, so the shape it shows
// has to be the real one: a Kargo pipeline read top-down in a single frame,
// with the warehouse it starts from named even though the demo cannot load it.
func TestDemoKargoPipelineTree(t *testing.T) {
	src := mock.New(domain.DemoContext + "-prod")
	rel, ok := any(src).(domain.Related)
	if !ok {
		t.Fatal("the demo backend no longer resolves relationships")
	}
	cl, _ := any(src).(domain.CellLevels)
	w := &treeWalk{
		rel:    rel,
		status: &statusLookup{src: src, cl: cl, cache: map[string]*statusTable{}},
		seen:   map[string]bool{},
		budget: treeMaxNodes,
	}
	got := renderTreeView(walkFrom(w, "kargo-stages", "dev", treeDepth), nil, "", w.truncated)

	for _, want := range []string{
		"kargo-stages/dev",
		"kargo-stages/staging",
		"kargo-stages/prod",
		"Unhealthy",
		"(not loaded",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("demo pipeline tree missing %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "kargo-stages/dev"); n != 1 {
		t.Errorf("demo root drawn %d times, want 1:\n%s", n, got)
	}
}
