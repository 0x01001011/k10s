package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/mock"
)

// podCols is the pod table's real column set, so these tests fail for the
// same reason the product would.
var podCols = []string{"NAME", "READY", "STATUS", "RESTARTS", "CPU", "MEM", "NODE", "AGE"}

func podTestRows() [][]string {
	return [][]string{
		{"api-gateway-7d9f4c8b6d-2xk4p", "1/1", "Running", "0", "142m", "310Mi", "ip-10-0-2-88", "6d"},
		{"billing-worker-6f8d9c5b7-qq91x", "0/1", "CrashLoopBackOff", "17", "0m", "24Mi", "ip-10-0-2-88", "3h"},
		{"web-frontend-6b8c7d9f5-zp7gx", "1/1", "Terminating", "0", "18m", "92Mi", "ip-10-0-2-88", "1d"},
	}
}

// keptHeaders names the columns fitCols decided to show.
func keptHeaders(cols []string, keep []int) []string {
	out := make([]string, 0, len(keep))
	for _, ci := range keep {
		out = append(out, cols[ci])
	}
	return out
}

func hasCol(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestLowestPriorityColumnDropsFirst is the T40 guard for drop order. Columns
// used to be dropped right to left, which is an accident of declaration order
// rather than a statement about importance: AGE sits last in the pod columns
// and died at every width, while NODE and CPU — which nobody reads first
// during an incident — outlived it.
func TestLowestPriorityColumnDropsFirst(t *testing.T) {
	extra := make([]int, len(podCols))

	for _, avail := range []int{40, 50, 60, 70} {
		_, keep := fitCols(podCols, podTestRows(), extra, avail, 2)
		got := keptHeaders(podCols, keep)

		if !hasCol(got, "NAME") {
			t.Errorf("avail=%d dropped the identity column: %v", avail, got)
		}
		// NODE, CPU and MEM have no priority of their own (50); AGE has 80.
		// A low-priority column outliving AGE means the order is wrong.
		if !hasCol(got, "AGE") && (hasCol(got, "NODE") || hasCol(got, "CPU") || hasCol(got, "MEM")) {
			t.Errorf("avail=%d dropped AGE while keeping a lower-priority column: %v", avail, got)
		}
		if !hasCol(got, "STATUS") && hasCol(got, "NODE") {
			t.Errorf("avail=%d dropped STATUS while keeping NODE: %v", avail, got)
		}
	}
}

// TestNameSurvivesUntilColumnsAreExhausted: the shrink loop used to take cells
// from the widest column over its minimum, which is always NAME, so at 100
// columns NAME was already `api-gateway-7d9f4…` while four columns were still
// on screen — two pods differing only in their hash suffix rendered the same.
func TestNameSurvivesUntilColumnsAreExhausted(t *testing.T) {
	extra := make([]int, len(podCols))
	rows := podTestRows()
	longest := 0
	for _, r := range rows {
		if w := lipgloss.Width(r[0]); w > longest {
			longest = w
		}
	}

	// Room for NAME in full plus three narrow columns and their gaps.
	avail := longest + 3*8 + 4*2
	w, keep := fitCols(podCols, rows, extra, avail, 2)
	if w[0] < longest {
		t.Errorf("NAME got %d cells for a %d-cell name, with %d columns shown (%v)",
			w[0], longest, len(keep), keptHeaders(podCols, keep))
	}
}

// TestColumnNeverNarrowerThanItsHeader: a column cut below its own header
// renders `RESTAR…`, which names nothing. A column that cannot afford its
// header should leave the screen instead.
func TestColumnNeverNarrowerThanItsHeader(t *testing.T) {
	extra := make([]int, len(podCols))
	for _, avail := range []int{45, 60, 80, 120, 200} {
		w, keep := fitCols(podCols, podTestRows(), extra, avail, 2)
		if len(keep) <= 2 {
			continue // the last-resort floor keeps two columns whatever happens
		}
		for k, ci := range keep {
			if h := lipgloss.Width(podCols[ci]); w[k] < h {
				t.Errorf("avail=%d: %s got %d cells for a %d-cell header",
					avail, podCols[ci], w[k], h)
			}
		}
	}
}

// TestWidthIsMeasuredInCellsNotBytes: tryFit measured with len(), which counts
// bytes, while cells are padded and cut by display width. Any non-ASCII value
// — an event message, an i18n namespace — over-reserved and pushed real
// columns off the right-hand edge.
func TestWidthIsMeasuredInCellsNotBytes(t *testing.T) {
	cols := []string{"NAME", "MESSAGE"}
	// 7 runes, 21 bytes: measured as bytes this column claims three times the
	// room it needs.
	const msg = "日本語テキスト"
	rows := [][]string{{"pod-a", msg}}
	extra := make([]int, len(cols))

	w, keep := fitCols(cols, rows, extra, 60, 2)
	if len(keep) != 2 {
		t.Fatalf("both columns should fit in 60 cells, got %v", keptHeaders(cols, keep))
	}
	if got, want := w[1], lipgloss.Width(msg); got != want {
		t.Errorf("MESSAGE reserved %d cells for a %d-cell value", got, want)
	}
}

func TestColumnPolicyRanksIdentityHighest(t *testing.T) {
	for _, c := range []string{"READY", "STATUS", "AGE", "NODE", "RESTARTS", "NAMESPACE"} {
		if colPriority("NAME") <= colPriority(c) {
			t.Errorf("NAME must outrank %s for drop order", c)
		}
		if colWeight("NAME") < colWeight(c) {
			t.Errorf("NAME must outweigh %s for shrink order", c)
		}
	}
}

func TestDropIndexNeverTakesTheIdentityColumn(t *testing.T) {
	// Every column has the same priority here, so only the explicit guard
	// stops the identity column being taken.
	cols := []string{"NAME", "NAME", "NAME", "NAME"}
	if got := dropIndex(cols, []int{0, 1, 2, 3}); got == 0 {
		t.Error("dropIndex returned the identity column")
	}
}

// TestTableRowsFitTheTerminal is the invariant every column change has to
// keep: a row wider than its pane breaks every join and overlay below it.
func TestTableRowsFitTheTerminal(t *testing.T) {
	for _, w := range []int{80, 100, 140, 160} {
		m := newTestModel(t, mock.New(""))
		m.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		for _, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got != w && got != 0 {
				t.Fatalf("at width %d a frame line is %d cells: %q", w, got, stripSGR(line))
			}
		}
	}
}
