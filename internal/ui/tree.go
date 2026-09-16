package ui

import (
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/domain"
)

// The tree is the multi-hop companion to R (related), and exists because one
// hop is the wrong shape for the two tools that need it most.
//
// A Kargo pipeline IS a graph — warehouse, then stages feeding stages — and
// reading it one hop at a time means holding the shape in your head while you
// navigate. An ArgoCD Application is the root of a fan-out. In both cases the
// question is "what does this reach, and which part of it is unhealthy",
// which a list of neighbours answers only after several keystrokes and some
// mental bookkeeping.
//
// So: walk the same declared edges Related walks, several hops, and print the
// result with each node's own graded cells beside it. The judgement the
// operator was making by hand — following the chain, remembering statuses —
// is the thing being removed.

// treeDepth is how many hops out the walk goes.
//
// Three, because that is the depth at which the shipped packs stop saying
// anything new: warehouse → stage → stage covers a promotion pipeline, and
// appproject → application → workload covers an ArgoCD install. A fourth hop
// on a real cluster is mostly pods, which the table already lists better.
const treeDepth = 3

// treeMaxNodes bounds the whole tree, not one level of it.
//
// The bound is on the total because the fan-out is unbounded in exactly one
// direction that matters: an AppProject with two hundred Applications would
// otherwise render two hundred lines nobody reads, built by two hundred
// cache scans, in a panel that shows forty. Truncation is reported rather
// than silent — a tree that quietly stops is indistinguishable from a cluster
// that really is that small.
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
	status   string
	children []*treeNode
}

// showTree walks the selected object's declared edges several hops out and
// renders the result in the text panel that already serves describe and YAML.
func (m *Model) showTree() tea.Cmd {
	rel, ok := m.src.(domain.Related)
	if !ok {
		m.toast = "✗ this backend has no relationships"
		return nil
	}
	kind, ns, name := m.curKind().Key, m.curNamespace(), m.curName()
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
	title := "tree " + m.curKind().Short + "/" + name
	return m.runFetch(title, func() (string, error) {
		w := &treeWalk{
			rel:    rel,
			status: &statusLookup{src: src, cl: cl, cache: map[string]*statusTable{}},
			seen:   map[string]bool{},
			budget: treeMaxNodes,
		}
		root := domain.Ref{Kind: kind, Namespace: ns, Name: name, Loaded: true}
		// The root is marked seen before the walk, not during it: Related
		// excludes an object from its own neighbours, but a grandchild is
		// free to point back at it, and a root that appeared again as its
		// own descendant would read as a cycle in the cluster rather than a
		// bug in the renderer.
		w.seen[refKey(root)] = true
		return renderTreeView(w.node(root, treeDepth), shorts, ns, w.truncated), nil
	})
}

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
// ran out.
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
// two nodes: an Application found both by label from a pod and by field from
// its AppProject is one Application.
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
// Reusing the table is the point: severity lives in the lens pack, the pack
// is applied when rows are formatted, and asking the same question a second
// way would be a second grading path to keep in agreement with the first.
type statusLookup struct {
	src   domain.Source
	cl    domain.CellLevels
	cache map[string]*statusTable
}

// statusTable is one kind's table, kept for the life of one walk.
//
// Rows copies the whole table on every call, so a tree of forty nodes over
// four kinds would otherwise copy four tables forty times. The cache makes it
// four.
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

// of returns the graded cells of one object, already glyphed, or "" when the
// object carries nothing worth grading.
func (s *statusLookup) of(ref domain.Ref) string {
	if !ref.Loaded || ref.Name == "" {
		return ""
	}
	t := s.table(ref.Kind, ref.Namespace)
	// A table with no NAMESPACE column is already scoped to the namespace it
	// was asked for, and its rows carry no namespace to compare against —
	// so matching on one would fail every lookup outside ns=all. This is the
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
	return ""
}

// cells picks the graded values off one row.
//
// Grading goes through the same cellLevel the table uses, so a pack's
// declared severity wins and everything else falls back to the builtin status
// vocabulary — which is what gives a plain pod a status on a line where the
// lens pack has nothing to say. "subtle" is a colour rather than a severity
// and is not glyphed, so it drops out here exactly as it does in the table.
func (s *statusLookup) cells(kind string, t *statusTable, row []string) string {
	var out []string
	for i, v := range row {
		if i >= len(t.cols) || i == t.id.nameIdx || i == t.id.nsIdx || v == "" {
			continue
		}
		lvl := ""
		if s.cl != nil {
			lvl = s.cl.CellLevel(kind, t.cols[i], v)
		}
		g := severityGlyph(cellLevel(lvl, v))
		if g == "" {
			continue
		}
		out = append(out, g+v)
		if len(out) == treeStatusCells {
			break
		}
	}
	return strings.Join(out, "  ")
}

// ---------------------------------------------------------------------------
// render
// ---------------------------------------------------------------------------

func renderTreeView(root *treeNode, shorts map[string]string, rootNS string, truncated bool) string {
	var b strings.Builder
	b.WriteString(treeLine(root, shorts, rootNS))
	b.WriteByte('\n')
	writeTreeChildren(&b, root, "", shorts, rootNS)
	if len(root.children) == 0 {
		b.WriteString("\nNo declared relationships resolved for this object.\n\n" +
			"Relationships come from the lens pack's edges. An edge whose\n" +
			"far side has never been opened reads as not loaded, not as absent.\n")
	}
	if truncated {
		b.WriteString("\n… truncated at " + strconv.Itoa(treeMaxNodes) +
			" objects. Narrow the namespace, or press R for one hop.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeTreeChildren draws the box-drawing spine.
//
// The continuation prefix is built as the recursion descends rather than
// reconstructed per line, so a deep branch costs one string concat per level
// instead of one per node.
func writeTreeChildren(b *strings.Builder, n *treeNode, prefix string, shorts map[string]string, rootNS string) {
	for i, c := range n.children {
		branch, cont := "├─ ", "│  "
		if i == len(n.children)-1 {
			branch, cont = "└─ ", "   "
		}
		b.WriteString(prefix)
		b.WriteString(branch)
		b.WriteString(treeLine(c, shorts, rootNS))
		b.WriteByte('\n')
		writeTreeChildren(b, c, prefix+cont, shorts, rootNS)
	}
}

// treeLine is one object: what it is, how it is, and why it is on this line.
//
// The namespace is printed only when it differs from the one the walk started
// in. An ArgoCD tree crosses namespaces constantly — the Application lives in
// argocd and its workloads live everywhere — and printing the same namespace
// on every line of a single-namespace pipeline would bury the one line where
// it changes.
func treeLine(n *treeNode, shorts map[string]string, rootNS string) string {
	r := n.ref
	kind := r.Kind
	if s, ok := shorts[r.Kind]; ok && s != "" {
		kind = s
	}
	var b strings.Builder
	b.WriteString(kind)
	if r.Name == "" {
		// The endpoint is a kind, not an object: no pack-declared kind serves
		// it, or nobody has opened it. Either way the fix is the same and the
		// line says so instead of showing an empty name.
		b.WriteString("   (not loaded — open this kind to resolve)")
		return b.String()
	}
	b.WriteString("/")
	b.WriteString(r.Name)
	if r.Namespace != "" && r.Namespace != rootNS {
		b.WriteString("  ")
		b.WriteString(r.Namespace)
	}
	if n.status != "" {
		b.WriteString("   ")
		b.WriteString(n.status)
	}
	if r.Rel != "" {
		b.WriteString("   via ")
		b.WriteString(r.Rel)
	}
	return b.String()
}
