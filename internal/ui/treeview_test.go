package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/mock"
)

func treeModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	m.toggleTree()
	if !m.treeOpen() {
		t.Fatalf("the tree did not open: %q", m.toast)
	}
	return m
}

func TestTreeNestsPodsUnderReplicaSetsUnderDeployments(t *testing.T) {
	m := treeModel(t)

	var depths []int
	var kinds []string
	for _, r := range m.tree {
		if strings.HasPrefix(r.name, "api-gateway") {
			depths = append(depths, r.depth)
			kinds = append(kinds, r.ref.Kind)
		}
	}
	want := []string{"deployments", "replicasets", "pods", "pods"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("api-gateway chain is %v, want %v", kinds, want)
	}
	for i, d := range depths {
		if wantD := []int{0, 1, 2, 2}[i]; d != wantD {
			t.Errorf("node %d (%s) is at depth %d, want %d", i, kinds[i], d, wantD)
		}
	}
}

// Actions must follow the node under the cursor, not the sidebar: a
// Deployment, a ReplicaSet and a Pod share one list here.
func TestTreeActionsFollowTheSelectedNodesKind(t *testing.T) {
	m := treeModel(t)

	if got := m.targetKind().Key; got != "deployments" {
		t.Fatalf("the first node is a %s, want deployments", got)
	}
	if got := m.curName(); got != m.tree[0].name {
		t.Errorf("curName = %q, want %q", got, m.tree[0].name)
	}

	for i, r := range m.tree {
		if r.ref.Kind == "pods" {
			m.treeIdx = i
			break
		}
	}
	if got := m.targetKind().Key; got != "pods" {
		t.Errorf("on a pod node, targetKind = %s", got)
	}
}

// targetKind must not leak into the table: tableData keys on curKind, so
// overriding that would repoint the whole table at the cursor.
func TestTreeDoesNotRepointTheUnderlyingTable(t *testing.T) {
	m := treeModel(t)
	for i, r := range m.tree {
		if r.ref.Kind == "deployments" {
			m.treeIdx = i
			break
		}
	}
	if got := m.curKind().Key; got != "pods" {
		t.Errorf("curKind = %q while the tree cursor is on a Deployment — the table moved", got)
	}
	cols, _ := m.tableData()
	if colIndex(cols, "RESTARTS") < 0 {
		t.Errorf("the underlying table is no longer pods: %v", cols)
	}
}

// A filter keeps the ancestors of a match so the shape survives, but marks
// them: offering them as results would lie about what matched.
func TestTreeFilterKeepsAncestorsUnselectable(t *testing.T) {
	m := treeModel(t)
	m.rowSearch = "billing-worker-6f8d9c5b7-qq91x"

	rows := m.treeFiltered()
	if len(rows) == 0 {
		t.Fatal("the filter matched nothing")
	}

	var matched, ancestors int
	for _, r := range rows {
		if r.ancestor {
			ancestors++
			if r.selectable() {
				t.Errorf("ancestor %q is selectable", r.name)
			}
			continue
		}
		matched++
	}
	if matched != 1 {
		t.Errorf("%d rows matched, want 1", matched)
	}
	if ancestors != 2 {
		t.Errorf("%d ancestors kept, want 2 (the ReplicaSet and the Deployment)", ancestors)
	}
}

// The cursor never rests on a row that is only there for the shape.
func TestTreeCursorSkipsAncestors(t *testing.T) {
	m := treeModel(t)
	m.rowSearch = "billing-worker-6f8d9c5b7-qq91x"
	m.treeIdx = 0

	m.moveTree(1)
	rows := m.treeFiltered()
	if m.treeIdx < len(rows) && rows[m.treeIdx].ancestor {
		t.Errorf("the cursor landed on an ancestor (%q)", rows[m.treeIdx].name)
	}
}

// A continuation line must not run past a branch that already ended.
func TestTreeGlyphsCloseTheirBranches(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  treeRow
		want string
	}{
		{"root", treeRow{depth: 0}, ""},
		{"middle child", treeRow{depth: 1}, "├─ "},
		{"last child", treeRow{depth: 1, last: true}, "└─ "},
		{
			"grandchild under a last parent",
			treeRow{depth: 2, last: true, ancestorLast: []bool{false, true}},
			"   └─ ",
		},
		{
			"grandchild under a parent with siblings to come",
			treeRow{depth: 2, ancestorLast: []bool{false, false}},
			"│  ├─ ",
		},
	} {
		if got := treeGlyph(tc.row); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Pods with no Deployment above them — StatefulSet members, Job pods — are
// often exactly what an operator came looking for.
func TestTreeKeepsPodsWithNoDeployment(t *testing.T) {
	m := treeModel(t)
	frame := stripSGR(m.View())
	for _, want := range []string{"cache-redis-0", "migrate-db-29341-8kdlp"} {
		if !strings.Contains(frame, want) {
			t.Errorf("%q is missing from the tree", want)
		}
	}
}

// The demo namespace has to fit, or every other test here is vacuous.
func TestTreeBudgetAdmitsARealNamespace(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	if _, err := m.buildOwnerTree(); err != nil {
		t.Fatalf("the demo namespace should fit inside the budget: %v", err)
	}
}

// Closing the tree puts the table back exactly as it was.
func TestTreeTogglesBackToTheTable(t *testing.T) {
	m := treeModel(t)
	m.toggleTree()

	if m.treeOpen() {
		t.Fatal("the tree did not close")
	}
	if got := m.targetKind().Key; got != "pods" {
		t.Errorf("after closing, targetKind = %q", got)
	}
	if frame := stripSGR(m.View()); !strings.Contains(frame, "Pods · default") {
		t.Error("the pods table did not come back")
	}
}

// Opening Pods must still watch exactly one kind. The tree is what starts the
// other two, on the keypress, and it says so.
func TestOpeningPodsStillWatchesOneKind(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	_ = m.View()

	if m.treeOpen() {
		t.Fatal("the tree opened without being asked")
	}
	m.toggleTree()
	if !strings.Contains(m.toast, "tree on") {
		t.Fatalf("toast = %q", m.toast)
	}
}
