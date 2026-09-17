package ui

import (
	"sort"

	"github.com/0x01001011/k10s/internal/domain"
)

// Column sort (T07).
//
// Sorting happens in the UI, over the already-filtered rows, and never in the
// backend: Rows() and RowCount() share cached state, so pushing a view
// preference down there would put it behind the informer cache.
//
// Two things make it cheap enough to sit on the frame path. tableData is
// memoised per frame (see Model.rowsMemo), so this runs once rather than
// three to five times; and the sort key is computed once per row instead of
// inside the less function, which would otherwise be O(n log n) parses of
// strings like "5d2h".

// sortState is one kind's column sort. col indexes the kind's own column
// slice; -1 is the backend's natural order, which is what a kind starts with
// and what a third click returns to.
type sortState struct {
	col  int
	desc bool
}

// sortFor returns the sort for one kind.
func (m *Model) sortFor(kind string) sortState {
	if s, ok := m.sorts[kind]; ok {
		return s
	}
	return sortState{col: -1}
}

func (m *Model) setSort(kind string, s sortState) {
	if m.sorts == nil {
		m.sorts = map[string]sortState{}
	}
	m.sorts[kind] = s
	// The row under the cursor is about to move; keep the cursor on the
	// object rather than on the position it happened to occupy.
	m.reanchorRow()
}

// cycleSort advances one column through ascending, descending, and back to
// the backend's own order — the three states a header click walks.
func (m *Model) cycleSort(kind string, col int) {
	s := m.sortFor(kind)
	switch {
	case s.col != col:
		s = sortState{col: col}
	case !s.desc:
		s.desc = true
	default:
		s = sortState{col: -1}
	}
	m.setSort(kind, s)
}

// moveSortColumn walks the sort one column left or right, for keyboards. It
// skips nothing: a column that is off screen can still be sorted by, which is
// the point — "which pod restarted most" is answerable at 80 columns even
// when RESTARTS did not fit.
func (m *Model) moveSortColumn(kind string, ncols, delta int) {
	if ncols == 0 {
		return
	}
	s := m.sortFor(kind)
	switch {
	case s.col < 0 && delta > 0:
		s.col = 0
	case s.col < 0:
		s.col = ncols - 1
	default:
		s.col += delta
	}
	// Past either end is "no sort", so there is always a way back to the
	// backend's order without a fourth key to remember.
	if s.col < 0 || s.col >= ncols {
		s = sortState{col: -1}
	}
	m.setSort(kind, s)
}

// flipSortDirection reverses the current sort, and does nothing when there
// isn't one — reversing the backend's order is not a thing anyone asked for.
func (m *Model) flipSortDirection(kind string) bool {
	s := m.sortFor(kind)
	if s.col < 0 {
		return false
	}
	s.desc = !s.desc
	m.setSort(kind, s)
	return true
}

// sortRows orders rows by the given sort column, returning them untouched
// when there is no sort.
//
// The input slice is never sorted in place: on the unfiltered path it is the
// backend's own slice, and reordering it would mutate informer-derived state
// that Rows() may hand out again.
func sortRows(rows [][]string, s sortState, ncols int) [][]string {
	if s.col < 0 || s.col >= ncols || len(rows) < 2 {
		return rows
	}

	values := make([]string, 0, len(rows))
	for _, r := range rows {
		values = append(values, cellAt(r, s.col))
	}
	kind := domain.SniffColumn(values)

	// One parse per row, then compare parsed keys.
	type keyed struct {
		key float64
		ok  bool
		row []string
	}
	ks := make([]keyed, len(rows))
	for i, r := range rows {
		k, ok := domain.SortKey(kind, cellAt(r, s.col))
		ks[i] = keyed{key: k, ok: ok, row: r}
	}

	sort.SliceStable(ks, func(i, j int) bool {
		a, b := ks[i], ks[j]
		// A cell with no value is last whichever way the column is sorted.
		// A descending CPU sort whose top rows are all unmetricked pods has
		// answered a different question than the one that was asked.
		if !a.ok || !b.ok {
			return a.ok && !b.ok
		}
		switch {
		case a.key < b.key:
			return !s.desc
		case a.key > b.key:
			return s.desc
		}
		// Equal keys still need a stable tiebreak, or two pods with the same
		// CPU swap places between frames.
		ai, bi := cellAt(a.row, s.col), cellAt(b.row, s.col)
		if ai != bi {
			if s.desc {
				return domain.NaturalLess(bi, ai)
			}
			return domain.NaturalLess(ai, bi)
		}
		return false
	})

	out := make([][]string, len(ks))
	for i, k := range ks {
		out[i] = k.row
	}
	return out
}

// sortToast says what the sort now is, because the header arrow can be on a
// column that did not fit on screen — which is deliberate (you can sort by
// RESTARTS at 80 columns) but silent without this.
func (m *Model) sortToast(cols []string) string {
	s := m.sortFor(m.curKind().Key)
	if s.col < 0 || s.col >= len(cols) {
		return "sort → default order"
	}
	dir := "ascending"
	if s.desc {
		dir = "descending"
	}
	return "sort → " + cols[s.col] + " " + dir
}

func cellAt(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}

// sortArrow is the mark drawn after a sorted column's header. It is reserved
// through the same extra[] mechanism the severity glyphs use, never by
// widening the header — tryFit floors a column at its header width, so a
// two-cell indicator would promote RESTARTS from 8 cells to 10 and the sort
// indicator would itself push a column off the screen.
func sortArrow(desc bool) string {
	if desc {
		return "▼"
	}
	return "▲"
}
