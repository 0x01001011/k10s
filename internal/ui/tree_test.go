package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	return &treeWalk{
		rel:    graph,
		status: &statusLookup{src: mock.New(""), cache: map[string]*statusTable{}},
		seen:   map[string]bool{},
		budget: budget,
	}
}

// walkFrom mirrors treeFrom's own setup, including seeding the root into the
// visited set — the part a test that called node() directly would skip.
func walkFrom(w *treeWalk, kind, name string, depth int) []treeRow {
	root := domain.Ref{Kind: kind, Name: name, Loaded: true}
	w.seen[refKey(root)] = true
	return flattenTree(w.node(root, depth), nil, "")
}

func render(w *treeWalk, kind, name string, depth int) string {
	rows := walkFrom(w, kind, name, depth)
	return treeText(rows, treeNote(rows, w.truncated))
}

// ---------------------------------------------------------------------------
// the walk
// ---------------------------------------------------------------------------

// A ref that points back at an ancestor must not be walked again. Kargo's
// stage->stage edge resolves in both directions, and so does ownership: a
// Deployment lists its ReplicaSets, each of which lists that Deployment
// straight back. A per-branch visited set would bounce between them until the
// depth counter ran out.
func TestTreeWalkCycleIsFinite(t *testing.T) {
	graph := fakeRelated{
		"stage/dev":     {{Kind: "stage", Name: "staging", Rel: "field", Loaded: true}},
		"stage/staging": {{Kind: "stage", Name: "dev", Rel: "field", Loaded: true}},
	}
	got := render(newTestWalk(graph, treeMaxNodes), "stage", "dev", treeDepth)

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
	got := render(newTestWalk(graph, treeMaxNodes), "stage", "dev", treeDepth)
	if n := strings.Count(got, "stage/dev"); n != 1 {
		t.Errorf("root drawn %d times, want 1:\n%s", n, got)
	}
}

// Depth bounds the CHAIN: a pipeline or an ownership chain longer than
// treeDepth stops rather than following every hop to the far end of the
// cluster. X on the node under the cursor is how you go further.
func TestTreeWalkStopsAtDepth(t *testing.T) {
	graph := fakeRelated{
		"n/a": {{Kind: "n", Name: "b", Rel: "field", Loaded: true}},
		"n/b": {{Kind: "n", Name: "c", Rel: "field", Loaded: true}},
		"n/c": {{Kind: "n", Name: "d", Rel: "field", Loaded: true}},
		"n/d": {{Kind: "n", Name: "e", Rel: "field", Loaded: true}},
	}
	got := render(newTestWalk(graph, treeMaxNodes), "n", "a", 3)

	if strings.Contains(got, "n/e") {
		t.Errorf("walked past depth 3:\n%s", got)
	}
	for _, want := range []string{"n/a", "n/b", "n/c", "n/d"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s within depth 3:\n%s", want, got)
		}
	}
}

// The budget bounds the WHOLE tree. A Node reached from a pod lists every pod
// on it; an AppProject lists every Application it governs. Either must stop
// and say so, because a tree that quietly stops is indistinguishable from a
// cluster that really is that small.
func TestTreeWalkBudgetTruncatesAndSaysSo(t *testing.T) {
	var kids []domain.Ref
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		kids = append(kids, domain.Ref{Kind: "app", Name: n, Rel: "field", Loaded: true})
	}
	w := newTestWalk(fakeRelated{"proj/platform": kids}, 2)
	got := render(w, "proj", "platform", treeDepth)

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
	got := render(newTestWalk(graph, treeMaxNodes), "stage", "dev", treeDepth)

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
	rows := walkFrom(newTestWalk(graph, treeMaxNodes), "root", "r", treeDepth)

	want := []string{
		"root/r",
		"├─ a/one   via field",
		"│  └─ c/deep   via field",
		"└─ b/two   via field",
		"   └─ d/last   via field",
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, r := range rows {
		if got := r.prefix + r.plain(); got != want[i] {
			t.Errorf("row %d:\n got %q\nwant %q", i, got, want[i])
		}
	}
}

// The namespace is carried only where it CHANGES. A pod's tree stays in one
// namespace and an ArgoCD one crosses constantly, so repeating the root's
// namespace on every line would bury the one line where it matters.
func TestTreeRowPrintsOnlyForeignNamespaces(t *testing.T) {
	same := treeRowOf(&treeNode{ref: domain.Ref{Kind: "app", Namespace: "argocd", Name: "web"}}, "", nil, "argocd")
	if same.ns != "" {
		t.Errorf("root namespace repeated: %q", same.ns)
	}
	other := treeRowOf(&treeNode{ref: domain.Ref{Kind: "po", Namespace: "prod", Name: "web-0"}}, "", nil, "argocd")
	if other.ns != "prod" {
		t.Errorf("foreign namespace = %q, want prod", other.ns)
	}
}

// Neighbours arrive in informer-cache order, which is not stable between
// calls. Two looks at the same tree must produce the same text, or diffing
// them is useless.
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
	a := render(newTestWalk(forward, treeMaxNodes), "root", "r", treeDepth)
	b := render(newTestWalk(reversed, treeMaxNodes), "root", "r", treeDepth)
	if a != b {
		t.Errorf("same graph rendered two ways:\n%s\n---\n%s", a, b)
	}
}

func TestTreeEmptyExplainsItself(t *testing.T) {
	got := render(newTestWalk(fakeRelated{}, treeMaxNodes), "stage", "lonely", treeDepth)
	if !strings.Contains(got, "No declared relationships resolved") {
		t.Errorf("empty tree gave no explanation:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

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
	if len(got) != 1 || !strings.Contains(got[0].text, "Unhealthy") {
		t.Fatalf("cells = %+v, want one carrying Unhealthy", got)
	}
	if got[0].level != "error" {
		t.Errorf("level = %q, want error", got[0].level)
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
	if got := s.of(domain.Ref{Kind: "k", Name: "x", Loaded: true}); len(got) != treeStatusCells {
		t.Errorf("rendered %d graded cells, want %d", len(got), treeStatusCells)
	}
}

// A row's colour is its WORST cell, not its first. An Application whose SYNC
// says Synced and whose HEALTH says Degraded is a failing Application, and
// colouring by the first column would paint it green.
func TestWorstLevelTakesTheWorstCell(t *testing.T) {
	cells := []treeCell{{text: "+ Synced", level: "ok"}, {text: "x Degraded", level: "error"}, {text: "! Slow", level: "warn"}}
	if got := worstLevel(cells); got != "error" {
		t.Errorf("worstLevel = %q, want error", got)
	}
	if got := worstLevel([]treeCell{{level: "ok"}}); got != "" {
		t.Errorf("an all-ok row has no severity to paint, got %q", got)
	}
}

// The note is the line read before any row. It has to state the healthy case
// out loud: a blank header and a header that failed to render look the same.
func TestTreeNoteCountsWhatMatters(t *testing.T) {
	rows := []treeRow{
		{ref: domain.Ref{Name: "a", Loaded: true}, worst: "error"},
		{ref: domain.Ref{Name: "b", Loaded: true}, worst: "warn"},
		{ref: domain.Ref{Name: "c", Loaded: true}},
		{ref: domain.Ref{Kind: "po"}},
	}
	got := treeNote(rows, false)
	if !strings.Contains(got, "2 of 3 need attention") {
		t.Errorf("note = %q, want it to count 2 of 3", got)
	}
	if !strings.Contains(got, "1 not loaded") {
		t.Errorf("note = %q, want it to count the unloaded kind", got)
	}

	healthy := treeNote([]treeRow{{ref: domain.Ref{Name: "a", Loaded: true}}}, false)
	if !strings.Contains(healthy, "none failing") {
		t.Errorf("healthy note = %q, want it to say so out loud", healthy)
	}
}

// ---------------------------------------------------------------------------
// the panel
// ---------------------------------------------------------------------------

func treeModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t, mock.New(domain.DemoContext+"-prod"))
	rows := []treeRow{
		{prefix: "", ref: domain.Ref{Kind: "pods", Name: "web-0", Loaded: true}, kind: "po"},
		{prefix: "├─ ", ref: domain.Ref{Kind: "pods", Name: "web-1", Rel: "ownerRef", Loaded: true}, kind: "po", worst: "error",
			cells: []treeCell{{text: "x CrashLoopBackOff", level: "error"}}},
		{prefix: "└─ ", ref: domain.Ref{Kind: "configmaps", Rel: "field"}, kind: "cm"},
	}
	m.showTreeRows("tree po/web-0", treeNote(rows, false), rows)
	return m
}

// The cursor starts on the first NEIGHBOUR. Row 0 is the object you already
// had selected, so starting there would make the first arrow press the only
// useful one.
func TestTreeOpensOnTheFirstNeighbour(t *testing.T) {
	m := treeModel(t)
	if m.mode != modeTree {
		t.Fatalf("mode = %v, want modeTree", m.mode)
	}
	if m.treeIdx != 1 {
		t.Errorf("cursor at %d, want 1", m.treeIdx)
	}
}

func TestTreeCursorStaysInRange(t *testing.T) {
	m := treeModel(t)
	m.treeMove(-100)
	if m.treeIdx != 0 {
		t.Errorf("cursor at %d after moving far up, want 0", m.treeIdx)
	}
	m.treeMove(1000)
	if want := len(m.treeRows) - 1; m.treeIdx != want {
		t.Errorf("cursor at %d after moving far down, want %d", m.treeIdx, want)
	}
}

// Enter is the whole "and then what". Reading that a pod is unhealthy is half
// an answer; landing on its table with that row selected is the other half,
// and every action k10s has then applies without the tree reimplementing one.
func TestTreeEnterGoesToTheObject(t *testing.T) {
	m := treeModel(t)
	m.treeIdx = 1
	if _, handled := m.treeKey("enter"); !handled {
		t.Fatal("enter was not claimed by the tree")
	}
	if m.mode != modeTable {
		t.Errorf("mode = %v, want modeTable", m.mode)
	}
	if m.curKind().Key != "pods" {
		t.Errorf("landed on %q, want pods", m.curKind().Key)
	}
}

// An endpoint with no object still navigates: going to that kind is what
// starts its informer, which is exactly what "open this kind to resolve"
// asks for.
func TestTreeEnterOnUnloadedKindOpensThatKind(t *testing.T) {
	m := treeModel(t)
	m.treeIdx = 2
	m.treeKey("enter")
	if m.curKind().Key != "configmaps" {
		t.Errorf("landed on %q, want configmaps", m.curKind().Key)
	}
}

// X re-roots the walk on the node under the cursor. This is why three hops is
// enough: the walk follows you instead of having to be widened.
func TestTreeXRerootsOnTheCursor(t *testing.T) {
	m := treeModel(t)
	m.treeIdx = 1
	cmd, handled := m.treeKey("X")
	if !handled {
		t.Fatal("X was not claimed by the tree")
	}
	if cmd == nil {
		t.Fatal("X produced no walk")
	}
	msg, ok := cmd().(treeResultMsg)
	if !ok {
		t.Fatalf("X produced %T, want treeResultMsg", cmd())
	}
	if !strings.Contains(msg.title, "web-1") {
		t.Errorf("re-rooted on %q, want a walk from web-1", msg.title)
	}
}

// An unloaded endpoint has no object to walk from, and says so instead of
// opening an empty tree.
func TestTreeXOnUnloadedEndpointRefuses(t *testing.T) {
	m := treeModel(t)
	m.treeIdx = 2
	cmd, _ := m.treeKey("X")
	if cmd != nil {
		t.Error("walked from an endpoint with no object")
	}
	if !strings.Contains(m.toast, "nothing to walk") {
		t.Errorf("toast = %q, want it to say why", m.toast)
	}
}

// Keys the tree does not claim must fall through, or opening a tree would
// take the terminal hostage: no :commands, no palette, no theme switch.
func TestTreeLeavesOtherKeysAlone(t *testing.T) {
	m := treeModel(t)
	for _, k := range []string{"q", ":", "d", "ctrl+y"} {
		if _, handled := m.treeKey(k); handled {
			t.Errorf("the tree swallowed %q", k)
		}
	}
}

func TestTreeEscReturnsToTheTable(t *testing.T) {
	m := treeModel(t)
	m.Update(key("esc"))
	if m.mode != modeTable {
		t.Errorf("mode = %v after esc, want modeTable", m.mode)
	}
}

// The panel paints from the severity the walk resolved, not from a substring
// match on the rendered line — that is the reason modeTree exists rather than
// reusing the text panel. Asserting on the escape bytes is the only way to
// see it.
func TestTreeBodyPaintsFailuresInTheErrorColour(t *testing.T) {
	m := treeModel(t)
	m.treeIdx = 0 // keep the failing row off the cursor, which recolours it
	th := m.th()
	body := strings.Join(m.treeBody(100, 12), "\n")

	// Compared against paint's own output rather than the theme's hex string:
	// paint emits resolved RGB escapes, so searching for "#f7768e" would pass
	// vacuously by never matching anything, failing OR passing.
	wantCell := paint(th.Bg, th.Err, false, "x CrashLoopBackOff")
	if !strings.Contains(body, wantCell) {
		t.Errorf("the failing cell is not painted in the error colour:\n%q", body)
	}
	wantName := paint(th.Bg, th.Err, false, "po/web-1")
	if !strings.Contains(body, wantName) {
		t.Errorf("the failing row's NAME is not painted in the error colour — scanning the left edge is how a failure is found:\n%q", body)
	}
	if !strings.Contains(body, paint(th.Bg, th.Border, false, "├─ ")) {
		t.Errorf("the spine is not painted as a border:\n%q", body)
	}
}

// The cursor row takes the selection background outright. Enter acts on the
// row under the cursor, so "which row am I on" has to be answerable at a
// glance — a per-run colouring kept under SelBg is unreadable on half the
// themes.
func TestTreeBodyHighlightsTheCursorRow(t *testing.T) {
	m := treeModel(t)
	m.treeIdx = 1
	th := m.th()
	lines := m.treeBody(100, 12)

	var cursorLine string
	for _, ln := range lines {
		if strings.Contains(ln, "web-1") {
			cursorLine = ln
		}
	}
	if cursorLine == "" {
		t.Fatal("the cursor row was not drawn")
	}
	if !strings.Contains(cursorLine, string(selBGProbe(th.SelBg))) {
		t.Errorf("the cursor row does not carry the selection background:\n%q", cursorLine)
	}
	// And the row NOT under the cursor must not, or every row would look
	// selected.
	for _, ln := range lines {
		if strings.Contains(ln, "web-0") && strings.Contains(ln, string(selBGProbe(th.SelBg))) {
			t.Errorf("an unselected row carries the selection background:\n%q", ln)
		}
	}
}

// selBGProbe renders a known string on the selection background, so a test can
// look for the escape bytes paint actually emits rather than the theme's hex
// string — which never appears in the output at all.
func selBGProbe(bg lipgloss.Color) string {
	full := paint(bg, bg, false, "\x00")
	pre, _, _ := strings.Cut(full, "\x00")
	return pre
}

// ---------------------------------------------------------------------------
// end to end, on the demo
// ---------------------------------------------------------------------------

// The demo is where this feature gets its screenshot, so the shape it shows
// has to be the real one: a Kargo pipeline read top-down in a single frame,
// with the warehouse it starts from named even though the demo cannot load it.
func TestDemoKargoPipelineTree(t *testing.T) {
	m := newTestModel(t, mock.New(domain.DemoContext+"-prod"))
	cmd := m.treeFrom("kargo-stages", "", "dev")
	if cmd == nil {
		t.Fatal("the demo backend no longer resolves relationships")
	}
	msg, ok := cmd().(treeResultMsg)
	if !ok {
		t.Fatalf("got %T, want treeResultMsg", cmd())
	}
	got := treeText(msg.rows, msg.note)

	for _, want := range []string{
		"stage/dev", "stage/staging", "stage/prod",
		"Unhealthy", "(not loaded", "need attention",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("demo pipeline tree missing %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "stage/dev"); n != 1 {
		t.Errorf("demo root drawn %d times, want 1:\n%s", n, got)
	}
}

// X on the table opens the tree for real, through Update — the path a user
// actually takes, including the async hop the headless renderer relies on.
func TestXOpensTheTreeFromTheTable(t *testing.T) {
	m := newTestModel(t, mock.New(domain.DemoContext+"-prod"))
	m.gotoKind("kargo-stages", "")

	var cmd tea.Cmd
	_, cmd = m.Update(key("X"))
	if cmd == nil {
		t.Fatal("X produced no command")
	}
	msg := cmd()
	if !IsAsyncMsg(msg) {
		t.Fatal("the tree result is not an async msg — just shot could not resolve it")
	}
	m.Update(msg)
	if m.mode != modeTree {
		t.Fatalf("mode = %v, want modeTree", m.mode)
	}
	if len(m.treeRows) == 0 {
		t.Error("the tree opened empty")
	}
}
