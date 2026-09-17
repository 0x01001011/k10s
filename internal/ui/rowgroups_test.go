package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/mock"
)

func groupedModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	return m
}

// Pods group by owner on open: an operator thinks "web-frontend", not "these
// three of the fourteen".
func TestPodsGroupByOwnerByDefault(t *testing.T) {
	m := groupedModel(t)
	if got := m.groupFor("pods"); got != groupOwner {
		t.Fatalf("pods group key = %q, want owner", got)
	}
	cols, rows := m.tableData()
	if spans := m.groupSpans(cols, rows); len(spans) < 2 {
		t.Fatalf("expected several owner groups, got %d", len(spans))
	}

	frame := stripSGR(m.View())
	if !strings.Contains(frame, "web-frontend · rs 6b8c7d9f5") {
		t.Error("the header should name the Deployment and say which ReplicaSet it came from")
	}
	// cache-redis-0/1 are StatefulSet members: no pod-template-hash, so
	// nothing is stripped and nothing is invented.
	if !strings.Contains(frame, "cache-redis") {
		t.Error("a pod whose owner name has no template hash should keep that name")
	}
	if strings.Contains(frame, "cache-redis · rs") {
		t.Error("a ReplicaSet was invented for a name with no template hash")
	}
}

// Row numbers count objects, continuously, across every header.
func TestRowNumbersAreContinuousAcrossGroups(t *testing.T) {
	m := groupedModel(t)
	nums := gutterNumbers(m, 150, 30)
	if len(nums) < 10 {
		t.Fatalf("only %d rows rendered", len(nums))
	}
	for i, n := range nums {
		if want := strconv.Itoa(i + 1); n != want {
			t.Fatalf("row %d numbered %q, want %q (gutter: %v)", i, n, want, nums)
		}
	}
}

// Folding hides a group's rows and leaves every other row's number alone:
// numbering comes from position in the row slice, not the render index.
func TestCollapsingAGroupDoesNotRenumberRowsBelowIt(t *testing.T) {
	m := groupedModel(t)
	before := gutterNumbers(m, 150, 30)

	cols, rows := m.tableData()
	spans := m.groupSpans(cols, rows)
	m.rowIdx = spans[0].first
	m.toggleRowGroup(spans, spans[0].first)

	after := gutterNumbers(m, 150, 30)
	if len(after) >= len(before) {
		t.Fatalf("folding hid nothing: %d rows before, %d after", len(before), len(after))
	}

	hidden := map[string]bool{}
	for i := 0; i < spans[0].count; i++ {
		hidden[strconv.Itoa(spans[0].first+i+1)] = true
	}
	var want []string
	for _, n := range before {
		if !hidden[n] {
			want = append(want, n)
		}
	}
	if strings.Join(after, ",") != strings.Join(want, ",") {
		t.Errorf("after folding, numbers are %v, want %v", after, want)
	}
}

// The sidebar's rule, for rows: a match hidden behind a fold would make the
// filter look broken.
func TestSearchShowsMatchesInCollapsedRowGroups(t *testing.T) {
	m := groupedModel(t)
	cols, rows := m.tableData()
	spans := m.groupSpans(cols, rows)

	target := cellAt(rows[spans[0].first], 0)
	m.rowIdx = spans[0].first
	m.toggleRowGroup(spans, spans[0].first)
	if !m.rowCollapsed(spans, spans[0].first) {
		t.Fatal("the group did not fold")
	}

	m.rowSearch = target
	if m.rowCollapsed(spans, spans[0].first) {
		t.Error("a folded group still hid a search match")
	}
	if frame := stripSGR(m.View()); !strings.Contains(frame, target) {
		t.Errorf("the match %q is not on the frame", target)
	}
}

// Folding the group holding the cursor keeps the marker on its header, so
// "where am I" never becomes a guess.
func TestCollapsedGroupHoldingTheCursorIsMarked(t *testing.T) {
	m := groupedModel(t)
	cols, rows := m.tableData()
	spans := m.groupSpans(cols, rows)

	m.rowIdx = spans[0].first
	m.toggleRowGroup(spans, spans[0].first)

	label, _ := groupLabel(groupOwner, spans[0].value)
	for _, line := range strings.Split(stripSGR(m.View()), "\n") {
		if strings.Contains(line, label) && strings.Contains(line, "▸") {
			return
		}
	}
	t.Error("the folded group holding the cursor is not marked")
}

// Arrow keys walk only what is on screen and never open a group you folded.
func TestArrowsSkipCollapsedRows(t *testing.T) {
	m := groupedModel(t)
	cols, rows := m.tableData()
	spans := m.groupSpans(cols, rows)

	m.rowIdx = spans[0].first
	m.toggleRowGroup(spans, spans[0].first)
	m.rowIdx = 0
	m.move(1)

	if m.rowCollapsed(spans, m.rowIdx) {
		t.Errorf("the cursor landed on a hidden row (%d)", m.rowIdx)
	}
}

// space folds, but stays a search character while searching.
func TestSpaceIsTypeableIntoTheRowSearchBox(t *testing.T) {
	m := groupedModel(t)
	cols, rows := m.tableData()
	spans := m.groupSpans(cols, rows)

	m.rowSearch = "web "
	m.handleKey(key(" "))
	if m.rowCollapsed(spans, spans[0].first) {
		t.Error("space folded a group while the row search was active")
	}
}

// Sorting states the whole table's order; grouping states its shape.
// Honouring both would sort within groups, which answers neither question.
func TestSortingDropsToFlat(t *testing.T) {
	m := groupedModel(t)
	cols, rows := m.tableData()
	if len(m.groupSpans(cols, rows)) == 0 {
		t.Fatal("pods should start grouped")
	}

	m.cycleSort("pods", colIndex(cols, "RESTARTS"))
	_, sorted := m.tableData()
	if got := m.groupSpans(cols, sorted); len(got) != 0 {
		t.Errorf("sorting left %d groups in place", len(got))
	}
}

// Grouping reads a cell already in the row: it must not cost another row
// build, let alone another watch.
func TestGroupingBuildsNoExtraRows(t *testing.T) {
	src := &countingSource{Source: mock.New("")}
	m := newTestModel(t, src)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})

	src.rows.Store(0)
	_ = m.View()
	if got := src.rows.Load(); got != 1 {
		t.Errorf("a grouped frame called Rows() %d times, want 1", got)
	}
}

// A kind that cannot answer the key falls back to flat, silently.
func TestGroupFallsBackToFlatWhenTheColumnIsMissing(t *testing.T) {
	m := groupedModel(t)
	m.jumpToResource("secrets")
	m.setGroup("secrets", groupNode)

	cols, rows := m.tableData()
	if got := m.groupSpans(cols, rows); len(got) != 0 {
		t.Errorf("secrets grouped by node produced %d groups", len(got))
	}
}

// Grouping by namespace inside one namespace is a header with every row under
// it.
func TestGroupByNamespaceIsInertInASingleNamespace(t *testing.T) {
	m := groupedModel(t)
	cols, _ := m.tableData()
	if got := groupColumn(cols, m.curKind(), groupNamespace, "default"); got >= 0 {
		t.Error("namespace grouping should be inert inside a single namespace")
	}

	// Under :ns all the backend prepends the NAMESPACE column, so the real
	// column set is what has to be asked.
	m.applyNamespace(domain.AllNamespaces)
	allCols, _ := m.tableData()
	if got := groupColumn(allCols, m.curKind(), groupNamespace, domain.AllNamespaces); got < 0 {
		t.Errorf("namespace grouping should work under :ns all (cols: %v)", allCols)
	}
}

// The choice round-trips through the flat config line; collapse state does
// not, because a folded row-group costs nothing to reopen.
func TestGroupChoiceRoundTripsThroughConfig(t *testing.T) {
	in := map[string]groupKey{"pods": groupNode, "services": groupNone}
	out := parseGroupConfig(renderGroupConfig(in))

	if out["pods"] != groupNode {
		t.Errorf("pods=%q survived as %q", groupNode, out["pods"])
	}
	if out["services"] != groupNone {
		t.Errorf("services=%q survived as %q", groupNone, out["services"])
	}
	// A kind left on its default is not written at all.
	if got := renderGroupConfig(map[string]groupKey{"pods": groupOwner}); got != "" {
		t.Errorf("a default was written to config: %q", got)
	}
}

// Grouping off renders the flat table this feature replaced.
func TestGroupNoneRendersAFlatTable(t *testing.T) {
	m := groupedModel(t)
	m.setGroup("pods", groupNone)

	if frame := stripSGR(m.View()); strings.Contains(frame, "▾ api-gateway") {
		t.Error("grouping off still drew a group header")
	}
	nums := gutterNumbers(m, 150, 30)
	for i, n := range nums {
		if n != strconv.Itoa(i+1) {
			t.Fatalf("flat numbering broke at %d: %v", i, nums)
		}
	}
}
