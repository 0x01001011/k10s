package ui

import (
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/domain"
)

// The tree is the multi-hop companion to R (related), and exists because one
// hop is the wrong shape for anything whose model is a graph.
//
// A Kargo pipeline IS a graph — warehouse, then stages feeding stages. So is
// a Pod: it is owned by a ReplicaSet owned by a Deployment, mounts claims
// bound to volumes, reads ConfigMaps and Secrets, and runs on a Node. Reading
// that one hop at a time means holding the shape in your head while you
// navigate, and re-reading each object's status as you go.
//
// So: walk the declared edges several hops, print the result with each node's
// own graded cells beside it, and make it a place you can act from rather
// than a page you read. The judgement the operator was making by hand —
// following the chain, remembering statuses, typing the next view's name — is
// the thing being removed.

// treeDepth is how many hops out the walk goes.
//
// Three, because that is where the shipped packs stop saying anything new:
// pod → replicaset → deployment is the whole ownership chain, and
// warehouse → stage → stage is a promotion pipeline. A fourth hop from a pod
// reaches the other pods of the same Deployment, which the table lists
// better. X on the node under the cursor re-roots the walk, so depth is a
// default rather than a ceiling.
const treeDepth = 3

// treeMaxNodes bounds the whole tree, not one level of it.
//
// The bound is on the total because the fan-out is unbounded in the
// directions that matter most: a Node reached from a pod lists every pod on
// it, and an AppProject lists every Application it governs. Either would
// otherwise render hundreds of lines nobody reads, built by hundreds of cache
// scans, into a panel that shows forty. Truncation is reported rather than
// silent — a tree that quietly stops is indistinguishable from a cluster that
// really is that small.
const treeMaxNodes = 120

// treeStatusCells is how many graded cells one line carries.
//
// Three fits ArgoCD's SYNC/HEALTH/OPERATION, which is the widest thing worth
// showing. More would wrap the line, and a wrapped tree loses the indentation
// that makes it a tree.
const treeStatusCells = 3

// treeNode is one object in the walk, with the children it reaches.
type treeNode struct {
	ref      domain.Ref
	status   []treeCell
	children []*treeNode
}

// treeCell is one graded value: what it says, and how bad it is.
//
// The level travels with the text rather than being re-derived at paint time,
// because grading costs a pack lookup per cell and the view runs on every
// keystroke — the same reason the table resolves severity once per row build.
type treeCell struct{ text, level string }

// treeRow is one drawn line, flattened out of the walk.
//
// Flattening happens once, off the render path, because a panel needs random
// access: the cursor moves by index and the viewport slices, and neither
// should re-walk a tree to find the third visible line.
type treeRow struct {
	prefix string
	ref    domain.Ref
	kind   string
	ns     string
	cells  []treeCell
	// worst is the row's own severity, used to colour the name. A failing
	// object should be findable by scanning names down the left edge, not
	// only by reading the status column on every line.
	worst string
}

// ---------------------------------------------------------------------------
// entry
// ---------------------------------------------------------------------------

// showTree walks the selected object's declared edges several hops out and
// opens the result as its own panel.
func (m *Model) showTree() tea.Cmd {
	return m.treeFrom(m.curKind().Key, m.curNamespace(), m.curName())
}

// treeFrom is the walk from an arbitrary object, which is what lets X inside
// the tree re-root it on the node under the cursor. Drilling in that way is
// why treeDepth can stay small: three hops from wherever you are beats thirty
// from where you started.
func (m *Model) treeFrom(kind, ns, name string) tea.Cmd {
	rel, ok := m.src.(domain.Related)
	if !ok {
		m.toast = "✗ this backend has no relationships"
		return nil
	}
	if name == "" || name == "-" {
		return nil
	}
	// Everything the walk needs is resolved HERE, on the update goroutine,
	// and captured by value. The fetch runs as a tea.Cmd, so reaching back
	// into the Model from inside it would read fields the update loop is free
	// to be writing — the same reason showRelated resolves its arguments up
	// front.
	src := m.src
	cl, _ := m.src.(domain.CellLevels)
	kinds := src.Kinds()
	shorts := make(map[string]string, len(kinds))
	for _, k := range kinds {
		shorts[k.Key] = k.Short
	}
	title := "tree " + shortOr(shorts, kind) + "/" + name
	m.toast = "… " + title
	m.startBusy(title)
	return func() tea.Msg {
		w := &treeWalk{
			rel:    rel,
			status: &statusLookup{src: src, cl: cl, cache: map[string]*statusTable{}},
			seen:   map[string]bool{},
			budget: treeMaxNodes,
		}
		root := domain.Ref{Kind: kind, Namespace: ns, Name: name, Loaded: true}
		// The root is marked seen before the walk, not during it: Related
		// excludes an object from its own neighbours, but a grandchild is
		// free to point back at it, and a root that appeared again as its own
		// descendant would read as a cycle in the cluster rather than a bug
		// in the renderer.
		w.seen[refKey(root)] = true
		rows := flattenTree(w.node(root, treeDepth), shorts, ns)
		return treeResultMsg{title: title, rows: rows, note: treeNote(rows, w.truncated)}
	}
}

func shortOr(shorts map[string]string, kind string) string {
	if s, ok := shorts[kind]; ok && s != "" {
		return s
	}
	return kind
}

// ---------------------------------------------------------------------------
// walk
// ---------------------------------------------------------------------------

// treeWalk carries the state one walk needs: what it has already placed, and
// how much room is left.
type treeWalk struct {
	rel       domain.Related
	status    *statusLookup
	seen      map[string]bool
	budget    int
	truncated bool
}

// node resolves one object and, while there is depth and budget left, the
// objects it reaches.
func (w *treeWalk) node(ref domain.Ref, depth int) *treeNode {
	n := &treeNode{ref: ref, status: w.status.of(ref)}
	if depth <= 0 {
		return n
	}
	refs, err := w.rel.Related(ref.Kind, ref.Namespace, ref.Name)
	if err != nil {
		// A neighbour that cannot be resolved is not a failure of the tree.
		// The node stands with what is known about it, which is more useful
		// than replacing the whole view with one edge's error.
		return n
	}
	sortRefs(refs)
	for _, r := range refs {
		if !w.take(refKey(r)) {
			continue
		}
		// An endpoint nobody has opened is a leaf by definition: there is no
		// cache to walk into, and recursing would spend budget producing
		// another copy of the same "not loaded" line.
		if !r.Loaded {
			n.children = append(n.children, &treeNode{ref: r})
			continue
		}
		n.children = append(n.children, w.node(r, depth-1))
	}
	return n
}

// take claims a node, reporting whether it is the first sighting and whether
// there was room for it.
//
// seen is global to the walk rather than per-branch, which is what makes a
// cycle finite: Kargo's stage→stage edge resolves in BOTH directions, so
// staging lists dev upstream and dev lists staging downstream, and a
// per-branch visited set would bounce between them until the depth counter
// ran out. Ownership has the same shape — a Deployment lists its ReplicaSets,
// each of which lists that Deployment straight back.
func (w *treeWalk) take(key string) bool {
	if w.seen[key] {
		return false
	}
	if w.budget <= 0 {
		w.truncated = true
		return false
	}
	w.seen[key] = true
	w.budget--
	return true
}

// refKey identifies an object, deliberately WITHOUT its relationship.
//
// Two edges reaching the same object are two reasons to draw one node, not
// two nodes: a Secret mounted as a volume AND named in imagePullSecrets is
// one Secret, and core.yaml declares both of those paths.
func refKey(r domain.Ref) string {
	return r.Kind + "\x00" + r.Namespace + "\x00" + r.Name
}

// sortRefs fixes the order neighbours appear in.
//
// The backend scans informer caches, whose listing order is not stable across
// calls, so without this the same tree renders differently each time it is
// opened — which makes the diff between two looks at a pipeline useless.
func sortRefs(refs []domain.Ref) {
	sort.Slice(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

// statusLookup reads a neighbour's graded cells out of the table the backend
// already builds for that neighbour's kind.
//
// Reusing the table is the point: severity lives in the lens pack, the pack is
// applied when rows are formatted, and asking the same question a second way
// would be a second grading path to keep in agreement with the first. It is
// also what gives a plain Pod a status — the builtin kinds carry no pack, and
// the table's own status vocabulary grades them.
type statusLookup struct {
	src   domain.Source
	cl    domain.CellLevels
	cache map[string]*statusTable
}

// statusTable is one kind's table, kept for the life of one walk.
//
// Rows copies the whole table on every call, so a tree of forty pods would
// otherwise copy the Pods table forty times. The cache makes it once.
type statusTable struct {
	cols []string
	rows [][]string
	id   rowIdent
}

func (s *statusLookup) table(kind, ns string) *statusTable {
	key := kind + "\x00" + ns
	if t, ok := s.cache[key]; ok {
		return t
	}
	cols, rows := s.src.Rows(kind, ns)
	t := &statusTable{cols: cols, rows: rows, id: identFor(kind, cols)}
	s.cache[key] = t
	return t
}

// of returns the graded cells of one object, or nil when the object carries
// nothing worth grading.
func (s *statusLookup) of(ref domain.Ref) []treeCell {
	if !ref.Loaded || ref.Name == "" {
		return nil
	}
	t := s.table(ref.Kind, ref.Namespace)
	// A table with no NAMESPACE column is already scoped to the namespace it
	// was asked for, and its rows carry no namespace to compare against — so
	// matching on one would fail every lookup outside ns=all. This is the
	// ordinary case: every single-namespace view is one.
	wantNS := ref.Namespace
	if t.id.nsIdx < 0 {
		wantNS = ""
	}
	for _, row := range t.rows {
		if !t.id.is(row, wantNS, ref.Name) {
			continue
		}
		return s.cells(ref.Kind, t, row)
	}
	return nil
}

// cells picks the graded values off one row.
//
// Grading goes through the same cellLevel the table uses, so a pack's declared
// severity wins and everything else falls back to the builtin status
// vocabulary. "subtle" is a colour rather than a severity and is not glyphed,
// so it drops out here exactly as it does in the table.
func (s *statusLookup) cells(kind string, t *statusTable, row []string) []treeCell {
	var out []treeCell
	for i, v := range row {
		if i >= len(t.cols) || i == t.id.nameIdx || i == t.id.nsIdx || v == "" {
			continue
		}
		lvl := ""
		if s.cl != nil {
			lvl = s.cl.CellLevel(kind, t.cols[i], v)
		}
		lvl = cellLevel(lvl, v)
		g := severityGlyph(lvl)
		if g == "" {
			continue
		}
		out = append(out, treeCell{text: g + v, level: lvl})
		if len(out) == treeStatusCells {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// flatten
// ---------------------------------------------------------------------------

// flattenTree turns the walk into the lines the panel draws.
//
// The spine is built as the recursion descends rather than reconstructed per
// line, so a deep branch costs one string concat per level instead of one per
// node.
func flattenTree(root *treeNode, shorts map[string]string, rootNS string) []treeRow {
	out := []treeRow{treeRowOf(root, "", shorts, rootNS)}
	appendKids(&out, root, "", shorts, rootNS)
	return out
}

func appendKids(out *[]treeRow, n *treeNode, prefix string, shorts map[string]string, rootNS string) {
	for i, c := range n.children {
		branch, cont := "├─ ", "│  "
		if i == len(n.children)-1 {
			branch, cont = "└─ ", "   "
		}
		*out = append(*out, treeRowOf(c, prefix+branch, shorts, rootNS))
		appendKids(out, c, prefix+cont, shorts, rootNS)
	}
}

// treeRowOf is one object: what it is, how it is, and why it is on this line.
//
// The namespace is carried only when it differs from the one the walk started
// in. A pod's tree crosses namespaces rarely and an ArgoCD one constantly —
// printing the namespace on every line of the first would bury the one line
// where the second changes.
func treeRowOf(n *treeNode, prefix string, shorts map[string]string, rootNS string) treeRow {
	r := treeRow{
		prefix: prefix,
		ref:    n.ref,
		kind:   shortOr(shorts, n.ref.Kind),
		cells:  n.status,
		worst:  worstLevel(n.status),
	}
	if n.ref.Namespace != "" && n.ref.Namespace != rootNS {
		r.ns = n.ref.Namespace
	}
	return r
}

// worstLevel is the row's severity: the worst of its graded cells.
//
// Worst, not first. An Application whose SYNC says Synced and whose HEALTH
// says Degraded is a failing Application, and colouring it by the first
// column would paint it green — the same rule the table's own sort follows.
func worstLevel(cells []treeCell) string {
	rank := map[string]int{"unknown": 1, "warn": 2, "error": 3}
	worst, best := "", 0
	for _, c := range cells {
		if r := rank[c.level]; r > best {
			worst, best = c.level, r
		}
	}
	return worst
}

// treeNote is the one line above the tree that answers "is anything wrong
// here" before the operator has read a single row.
//
// A count, not a verdict: the tree cannot know whether a warning matters, only
// how many there are. "none failing" is stated out loud rather than left as an
// absence, because a silent header and a header that has not rendered look the
// same.
func treeNote(rows []treeRow, truncated bool) string {
	bad, unloaded := 0, 0
	for _, r := range rows {
		switch {
		case r.ref.Name == "":
			unloaded++
		case r.worst == "error" || r.worst == "warn":
			bad++
		}
	}
	shown := len(rows) - unloaded
	var parts []string
	switch {
	case bad > 0:
		parts = append(parts, strconv.Itoa(bad)+" of "+strconv.Itoa(shown)+" need attention")
	case shown > 0:
		parts = append(parts, strconv.Itoa(shown)+" objects, none failing")
	}
	if unloaded > 0 {
		parts = append(parts, strconv.Itoa(unloaded)+" not loaded")
	}
	if truncated {
		parts = append(parts, "truncated at "+strconv.Itoa(treeMaxNodes))
	}
	return strings.Join(parts, " · ")
}

// treeEmptyHelp is what a tree with no neighbours says, and it says what R
// says because the cause is the same: "nothing here" on its own reads as a
// cluster with no relationships rather than a kind nobody has opened.
var treeEmptyHelp = []string{
	"",
	"No declared relationships resolved for this object.",
	"",
	"Relationships come from the lens packs' edges — core.yaml for the",
	"built-in kinds, one pack per operator for the rest. An edge whose far",
	"side has never been opened reads as not loaded, not as absent.",
}

// treeText renders the rows as plain text, for the export file and for tests
// that assert on shape rather than on colour.
func treeText(rows []treeRow, note string) string {
	var b strings.Builder
	if note != "" {
		b.WriteString(note)
		b.WriteString("\n\n")
	}
	for _, r := range rows {
		b.WriteString(r.prefix)
		b.WriteString(r.plain())
		b.WriteByte('\n')
	}
	if len(rows) <= 1 {
		b.WriteString(strings.Join(treeEmptyHelp, "\n"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// plain is one line without its spine or any colour.
func (r treeRow) plain() string {
	if r.ref.Name == "" {
		return r.kind + "   (not loaded — open this kind to resolve)"
	}
	var b strings.Builder
	b.WriteString(r.kind)
	b.WriteString("/")
	b.WriteString(r.ref.Name)
	if r.ns != "" {
		b.WriteString("  ")
		b.WriteString(r.ns)
	}
	for _, c := range r.cells {
		b.WriteString("   ")
		b.WriteString(c.text)
	}
	if r.ref.Rel != "" {
		b.WriteString("   via ")
		b.WriteString(r.ref.Rel)
	}
	return b.String()
}
