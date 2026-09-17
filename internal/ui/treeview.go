package ui

import (
	"strconv"
	"strings"

	"github.com/0x01001011/k10s/internal/domain"
)

// The nested owner tree (T46).
//
// T42 gives pods one level of collapsible headers over a flat row slice, and
// that is the right default: it keeps sort, filter, row numbering and
// selection working exactly as they did. This is the other thing — a real
// Deployment → ReplicaSet → Pod tree, where every node is an object you can
// select and act on, and the parents carry their own status.
//
// Not to be confused with tree.go, which walks lens-declared edges into the
// read-only text panel. That one answers "what does this custom resource
// reach"; this one answers "what is the shape of this namespace", from
// built-in ownership, in the table, with a cursor.
//
// It is opt-in (`T`) and it comes second on purpose. The cheap version had to
// be seen on a real frame first, because most of what people want from "show
// me the tree" turns out to be answered by grouping — and the part that is
// not is the part that costs: a tree needs the ReplicaSet and Deployment
// objects, which means informers that opening the Pods table deliberately
// does not start. Those watches are started HERE, on the keypress, and the
// toast says so. Opening Pods still watches exactly one kind.

// treeBudget bounds the whole tree, not one level of it.
//
// Three hundred nodes is well past the point where the shape is still
// readable. Beyond it the tree refuses to open rather than truncating: a tree
// that quietly stops is indistinguishable from a cluster that really is that
// small — the same reasoning as treeMaxNodes in tree.go.
const treeBudget = 300

// treeRow is one node: an object, its depth, and the cells worth showing.
type treeRow struct {
	ref    domain.Ref
	depth  int
	name   string
	status string
	// ancestor marks a node kept only to hold the shape during a filter. It
	// did not match, so it is dimmed and cannot be selected — otherwise a
	// filtered tree would quietly offer rows that are not results.
	ancestor bool
	// last says this node is the final child of its parent, which is what
	// picks └─ over ├─.
	last bool
	// ancestorLast records, for each level above this node, whether THAT
	// ancestor was its parent's final child. Without it the prefix is drawn
	// from the node's own last-ness alone, and a pod under the last
	// ReplicaSet of a Deployment gets a `│` continuation line running down
	// past a branch that already ended.
	ancestorLast []bool
}

// selectable reports whether the cursor may rest here.
func (t treeRow) selectable() bool { return !t.ancestor }

// treeOpen reports whether the tree is showing.
func (m *Model) treeOpen() bool { return len(m.tree) > 0 }

// errTreeTooBig is returned rather than truncating, so the refusal is
// something an operator can read.
type treeTooBigError struct{}

func (treeTooBigError) Error() string { return "tree too large" }

var errTreeTooBig = treeTooBigError{}

// buildOwnerTree assembles Deployment → ReplicaSet → Pod for the current
// namespace.
//
// Everything is read through Rows(), which is what starts the informers — and
// is why this runs on a keypress rather than on a frame. Called once per
// toggle, never per render.
func (m *Model) buildOwnerTree() ([]treeRow, error) {
	ns := m.namespace

	podCols, podRows := m.src.Rows("pods", ns)
	rsCols, rsRows := m.src.Rows("replicasets", ns)
	depCols, depRows := m.src.Rows("deployments", ns)

	if len(podRows)+len(rsRows)+len(depRows) > treeBudget {
		return nil, errTreeTooBig
	}

	// The owner is a meta cell, so it sits just past the columns.
	podOwner := len(podCols)
	if i := colIndex(podCols, "OWNER"); i >= 0 {
		podOwner = i
	}
	podName := colIndex(podCols, "NAME")
	podStatus := colIndex(podCols, "STATUS")
	rsName, rsReady := colIndex(rsCols, "NAME"), colIndex(rsCols, "READY")
	depName, depReady := colIndex(depCols, "NAME"), colIndex(depCols, "READY")

	byOwner := map[string][][]string{}
	for _, r := range podRows {
		byOwner[cellAt(r, podOwner)] = append(byOwner[cellAt(r, podOwner)], r)
	}

	// File each ReplicaSet under the Deployment its name implies — the same
	// derivation the group headers use, and a derivation rather than a
	// lookup: a ReplicaSet whose name lacks the shape keeps its own name
	// instead of being filed under a Deployment it may not belong to.
	rsByDeploy := map[string][][]string{}
	for _, r := range rsRows {
		label, _ := domain.OwnerLabel(cellAt(r, rsName))
		rsByDeploy[label] = append(rsByDeploy[label], r)
	}

	out := make([]treeRow, 0, len(podRows)+len(rsRows)+len(depRows))
	seenPods := map[string]bool{}

	for di, d := range depRows {
		name := cellAt(d, depName)
		out = append(out, treeRow{
			ref:    domain.Ref{Kind: "deployments", Namespace: ns, Name: name, Loaded: true},
			name:   name,
			status: cellAt(d, depReady),
			last:   di == len(depRows)-1,
		})

		kids := rsByDeploy[name]
		for ri, rs := range kids {
			rsn := cellAt(rs, rsName)
			out = append(out, treeRow{
				ref:          domain.Ref{Kind: "replicasets", Namespace: ns, Name: rsn, Loaded: true},
				depth:        1,
				name:         rsn,
				status:       cellAt(rs, rsReady),
				last:         ri == len(kids)-1,
				ancestorLast: []bool{di == len(depRows)-1},
			})

			rsLast := ri == len(kids)-1
			pods := byOwner[rsn]
			for pi, p := range pods {
				pn := cellAt(p, podName)
				seenPods[pn] = true
				out = append(out, treeRow{
					ref:          domain.Ref{Kind: "pods", Namespace: ns, Name: pn, Loaded: true},
					depth:        2,
					name:         pn,
					status:       cellAt(p, podStatus),
					last:         pi == len(pods)-1,
					ancestorLast: []bool{di == len(depRows)-1, rsLast},
				})
			}
		}
	}

	// Pods with no Deployment above them — StatefulSet members, Job pods,
	// bare pods — are not orphans to hide. They are often exactly what an
	// operator came looking for.
	var loose [][]string
	for _, p := range podRows {
		if !seenPods[cellAt(p, podName)] {
			loose = append(loose, p)
		}
	}
	for i, p := range loose {
		pn := cellAt(p, podName)
		out = append(out, treeRow{
			ref:    domain.Ref{Kind: "pods", Namespace: ns, Name: pn, Loaded: true},
			name:   pn,
			status: cellAt(p, podStatus),
			last:   i == len(loose)-1,
		})
	}

	return out, nil
}

// toggleTree opens or closes the tree.
func (m *Model) toggleTree() {
	if m.treeOpen() {
		m.tree, m.treeIdx = nil, 0
		m.toast = "tree off"
		return
	}
	if m.curKind().Key != "pods" {
		m.toast = "the owner tree is a view of pods — open Pods first"
		return
	}

	// Opening the tree is what starts the ReplicaSet and Deployment watches.
	// Saying so matters: the point of the lazy-informer design is that
	// nothing watches behind your back, so the one thing that does announces
	// it.
	started := !m.kindLoaded("replicasets") || !m.kindLoaded("deployments")

	rows, err := m.buildOwnerTree()
	if err != nil {
		m.tree = nil
		m.toast = "✗ over " + strconv.Itoa(treeBudget) + " objects — the tree would not be readable; filter first"
		return
	}
	m.tree, m.treeIdx = rows, 0
	m.toast = "tree on · enter and actions follow the selected node's own kind"
	if started {
		m.toast += " · now watching replicasets and deployments"
	}
}

// treeFiltered applies the row search, keeping the ancestors of every match
// so the shape survives.
//
// Those ancestors are marked, dimmed and unselectable. A filtered tree that
// offered them as results would lie about what matched; one that dropped them
// would lie about the shape. This is the fork T42 avoids by staying flat, and
// the cost of asking for a tree.
func (m *Model) treeFiltered() []treeRow {
	if m.rowSearch == "" {
		return m.tree
	}
	q := strings.ToLower(m.rowSearch)

	matched := make([]bool, len(m.tree))
	keep := make([]bool, len(m.tree))
	for i, t := range m.tree {
		if !strings.Contains(strings.ToLower(t.name), q) && !strings.Contains(strings.ToLower(t.status), q) {
			continue
		}
		matched[i], keep[i] = true, true
		// Walk back to the root, marking each shallower node above it.
		want := t.depth
		for j := i - 1; j >= 0 && want > 0; j-- {
			if m.tree[j].depth < want {
				keep[j] = true
				want = m.tree[j].depth
			}
		}
	}

	out := make([]treeRow, 0, len(m.tree))
	for i, t := range m.tree {
		if !keep[i] {
			continue
		}
		t.ancestor = !matched[i]
		out = append(out, t)
	}
	return out
}

// treeSelected is the node under the cursor.
func (m *Model) treeSelected() (treeRow, bool) {
	rows := m.treeFiltered()
	if m.treeIdx < 0 || m.treeIdx >= len(rows) {
		return treeRow{}, false
	}
	return rows[m.treeIdx], true
}

// moveTree walks the cursor, skipping the ancestor rows a filter left behind.
func (m *Model) moveTree(delta int) {
	rows := m.treeFiltered()
	if len(rows) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for i := clamp(m.treeIdx+delta, 0, len(rows)-1); i >= 0 && i < len(rows); i += step {
		if rows[i].selectable() {
			m.treeIdx = i
			return
		}
	}
	// Nothing that way: stay put rather than land on an ancestor.
}

// treeGlyph is the branch drawing for one node.
//
// The tree replaces the row number rather than sitting beside it. A number
// exists to address a row — "the twelfth one" — and in a tree that changes
// meaning as soon as anything above it is filtered, so keeping it would be
// keeping a label that no longer refers to anything.
// treeBody renders the tree into the main panel.
//
// Each line is: branch drawing, a short kind tag, the name, and the node's own
// status graded the way every other cell in the app is graded. The kind tag is
// what makes a mixed list legible — three kinds share this column, so the row
// has to say which one it is rather than leaving it to indentation.
func (m *Model) treeBody(inner, rows int) []string {
	th := m.th()
	all := m.treeFiltered()

	visible := maxi(1, rows)
	m.treeScroll = clamp(m.treeScroll, 0, maxi(0, len(all)-visible))
	// Keep the cursor on screen without letting it drag the window further
	// than it has to — the same contract the table's syncScroll has.
	if m.treeIdx < m.treeScroll {
		m.treeScroll = m.treeIdx
	}
	if m.treeIdx >= m.treeScroll+visible {
		m.treeScroll = m.treeIdx - visible + 1
	}

	shorts := map[string]string{"deployments": "deploy", "replicasets": "rs", "pods": "po"}

	out := make([]string, 0, visible)
	end := clamp(m.treeScroll+visible, 0, len(all))
	for i := m.treeScroll; i < end; i++ {
		t := all[i]
		sel := i == m.treeIdx
		bg, base := th.Bg, th.Fg
		if sel {
			bg, base = th.SelBg, th.SelFg
		}

		var b strings.Builder
		if sel {
			b.WriteString(paint(bg, th.Accent, false, "▌"))
		} else {
			b.WriteString(paint(bg, bg, false, " "))
		}
		b.WriteString(paint(bg, th.Border, false, treeGlyph(t)))

		nameCol := base
		if t.ancestor {
			// Kept for the shape, not a result. Dimmed so a filtered tree
			// never reads as though it matched more than it did.
			nameCol = th.Subtle
		}
		b.WriteString(paint(bg, th.Accent2, false, shorts[t.ref.Kind]+"/"))
		b.WriteString(paint(bg, nameCol, sel && !t.ancestor, t.name))

		if t.status != "" && !t.ancestor {
			lvl := cellLevel("", t.status)
			b.WriteString(paint(bg, bg, false, "  "))
			b.WriteString(paint(bg, cellColor(th, lvl, t.status, base), false, severityGlyph(lvl)+t.status))
		}

		// Zone markers are private CSI sequences, so lipgloss.Width does not
		// count them and padBG pads to the visible width.
		out = append(out, m.mark("tree:"+strconv.Itoa(i), padBG(b.String(), inner, bg)))
	}

	if len(all) == 0 {
		out = append(out, paint(th.Bg, th.Subtle, false, " nothing matches "+m.rowSearch))
	}
	return out
}

func treeGlyph(t treeRow) string {
	if t.depth == 0 {
		return ""
	}
	var b strings.Builder
	// One column per level BETWEEN the root and this node — the root itself
	// draws no branch, so it gets no continuation column either. A line is
	// drawn only where that ancestor still has siblings coming.
	for i := 1; i < t.depth; i++ {
		if i < len(t.ancestorLast) && t.ancestorLast[i] {
			b.WriteString("   ")
		} else {
			b.WriteString("│  ")
		}
	}
	if t.last {
		b.WriteString("└─ ")
	} else {
		b.WriteString("├─ ")
	}
	return b.String()
}
