package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/mock"
)

// TestSortDoesNotMutateBackendRows is the bug the copy prevents: on the
// unfiltered path tableData returns the backend's own slice, and sorting in
// place would reorder informer-derived state that Rows() hands out again.
func TestSortDoesNotMutateBackendRows(t *testing.T) {
	src := mock.New("")
	cols, rows := src.Rows("pods", "default")

	before := make([]string, len(rows))
	for i, r := range rows {
		before[i] = strings.Join(r, "|")
	}

	_ = sortRows(rows, sortState{col: colIndex(cols, "RESTARTS"), desc: true}, len(cols))

	after := make([]string, len(rows))
	for i, r := range rows {
		after[i] = strings.Join(r, "|")
	}
	if !reflect.DeepEqual(before, after) {
		t.Error("sortRows reordered the backend's own slice")
	}
}

// TestSortIsPerKind: sorting Pods by RESTARTS must not follow you into
// Services, and coming back to Pods must find it as you left it.
func TestSortIsPerKind(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	cols, _ := m.tableData()
	ri := colIndex(cols, "RESTARTS")
	if ri < 0 {
		t.Fatal("pods have no RESTARTS column")
	}

	m.cycleSort("pods", ri)
	if got := m.sortFor("pods").col; got != ri {
		t.Fatalf("pods sort col = %d, want %d", got, ri)
	}

	m.jumpToResource("services")
	if got := m.sortFor("services").col; got != -1 {
		t.Errorf("services inherited a sort column (%d)", got)
	}

	m.jumpToResource("pods")
	if got := m.sortFor("pods").col; got != ri {
		t.Errorf("pods lost its sort on the way back: col = %d, want %d", got, ri)
	}
}

// TestClickCyclesSortThreeWays: ascending, descending, then back to the
// backend's own order — so there is always a way out without a fourth key.
func TestClickCyclesSortThreeWays(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	cols, _ := m.tableData()
	ri := colIndex(cols, "RESTARTS")

	m.cycleSort("pods", ri)
	if s := m.sortFor("pods"); s.col != ri || s.desc {
		t.Fatalf("first click: %+v, want ascending on %d", s, ri)
	}
	m.cycleSort("pods", ri)
	if s := m.sortFor("pods"); s.col != ri || !s.desc {
		t.Fatalf("second click: %+v, want descending on %d", s, ri)
	}
	m.cycleSort("pods", ri)
	if s := m.sortFor("pods"); s.col != -1 {
		t.Fatalf("third click: %+v, want no sort", s)
	}
}

// TestSortByRestartsPutsTheWorstFirst is the question the card exists for:
// "which pod is restarting most" should be one action, not a read of all
// fourteen rows.
func TestSortByRestartsPutsTheWorstFirst(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	cols, _ := m.tableData()
	ri := colIndex(cols, "RESTARTS")

	m.cycleSort("pods", ri) // ascending
	m.cycleSort("pods", ri) // descending
	_, rows := m.tableData()

	if len(rows) < 3 {
		t.Fatal("not enough demo rows")
	}
	if rows[0][ri] != "17" {
		t.Errorf("descending RESTARTS starts with %q, want 17 (next two: %q %q)",
			rows[0][ri], rows[1][ri], rows[2][ri])
	}
}

// TestSortKeepsSentinelsAtTheBottom: a descending CPU sort whose top rows are
// unmetricked pods has answered a different question than the one asked.
func TestSortKeepsSentinelsAtTheBottom(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	cols, _ := m.tableData()
	ci := colIndex(cols, "CPU")

	m.cycleSort("pods", ci)
	m.cycleSort("pods", ci) // descending
	_, rows := m.tableData()

	last := rows[len(rows)-1][ci]
	if _, ok := domain.SortKey(domain.CellQuantity, last); ok {
		t.Errorf("last row has a real CPU value (%q) — a sentinel belongs there", last)
	}
	if _, ok := domain.SortKey(domain.CellQuantity, rows[0][ci]); !ok {
		t.Errorf("first row of a descending sort is a sentinel (%q)", rows[0][ci])
	}
}

// TestSortedHeaderKeepsItsRowWidth: the indicator is drawn inside the
// column's own width. tryFit floors a column at its header width, so widening
// a header for an arrow would let the arrow push a column off the screen.
func TestSortedHeaderKeepsItsRowWidth(t *testing.T) {
	for _, w := range []int{80, 100, 140, 160} {
		m := newTestModel(t, mock.New(""))
		m.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		cols, _ := m.tableData()

		for ci := range cols {
			m.sorts = map[string]sortState{"pods": {col: ci, desc: true}}
			for _, line := range strings.Split(m.View(), "\n") {
				if got := lipgloss.Width(line); got != w && got != 0 {
					t.Fatalf("width %d, sorted on %s: a line is %d cells", w, cols[ci], got)
				}
			}
		}
	}
}

// TestSortSurvivesAFrame: the memo keys on the sort, so a keypress reorders
// the table on the next frame rather than on the next message.
func TestSortSurvivesAFrame(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	cols, _ := m.tableData()
	ri := colIndex(cols, "RESTARTS")

	_, before := m.tableData()
	first := before[0][ri]

	m.cycleSort("pods", ri)
	m.cycleSort("pods", ri)
	_, after := m.tableData()

	if after[0][ri] == first && first != "17" {
		t.Error("the row order did not change after sorting — the memo is keyed wrong")
	}
}

// TestFlipDoesNothingWithoutASortColumn: reversing the backend's own order is
// not a thing anyone asked for.
func TestFlipDoesNothingWithoutASortColumn(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	if m.flipSortDirection("pods") {
		t.Error("S flipped a sort that does not exist")
	}
}
