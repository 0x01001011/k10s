package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/mock"
	"github.com/0x01001011/k10s/internal/theme"
)

// Colour alone cannot carry severity: a colour-blind operator and a
// low-contrast terminal both need the mark. Every cell the table COLOURS as a
// severity must therefore also be MARKED — the two must not drift apart, which
// is why both read cellLevel.
func TestEveryColouredSeverityIsAlsoGlyphed(t *testing.T) {
	th := theme.Themes[0]
	def := lipgloss.Color("#ffffff")
	cases := []struct {
		level, value string
		glyph        string
	}{
		{"error", "whatever", "x "}, // lens level wins over the value
		{"warn", "whatever", "! "},
		{"ok", "whatever", "+ "},
		{"unknown", "whatever", "? "},
		{"", "CrashLoopBackOff", "x "}, // statusColors: the builtin path
		{"", "Pending", "! "},
		{"", "Running", "+ "},
		{"", "0/1", "! "}, // the ready-ratio heuristic
		{"", "Ready,SchedulingDisabled", "! "},
	}
	for _, c := range cases {
		lvl := cellLevel(c.level, c.value)
		if got := severityGlyph(lvl); got != c.glyph {
			t.Errorf("severityGlyph(%q/%q) = %q, want %q", c.level, c.value, got, c.glyph)
		}
		if cellColor(th, c.level, c.value, def) == def {
			t.Errorf("%q/%q is glyphed but painted the default colour", c.level, c.value)
		}
	}
}

// "Completed" and "-" are dimmed, not graded: a glyph there would claim a
// severity the table does not have, and would cost every such column two cells.
func TestUngradedValuesAreNotGlyphed(t *testing.T) {
	for _, v := range []string{"Completed", "-", "<none>", "3/3", "80/TCP", "ip-10-0-2-88", ""} {
		if g := severityGlyph(cellLevel("", v)); g != "" {
			t.Errorf("%q glyphed %q, want none", v, g)
		}
	}
}

// The glyph is reserved in the column width, not added on top of it: an
// unreserved leading rune shifts every column to its right, and padBGOf trusts
// the arithmetic rowW rather than measuring the row.
func TestGlyphIsReservedInColumnWidth(t *testing.T) {
	cols := []string{"NAME", "STATUS"}
	rows := [][]string{{"api", "Running"}, {"db", "Failed"}}
	plain, _ := fitCols(cols, rows, []int{0, 0}, 60, 2)
	glyphed, _ := fitCols(cols, rows, []int{0, 2}, 60, 2)
	if glyphed[1] != plain[1]+2 {
		t.Errorf("STATUS sized %d with a glyph and %d without, want +2", glyphed[1], plain[1])
	}
	if glyphed[0] != plain[0] {
		t.Errorf("NAME resized (%d → %d) by a glyph in another column", plain[0], glyphed[0])
	}
}

// Reservation widens the VALUES, never the header: "READY" is already wider
// than "! 0/1", so it must not grow to 7 and steal cells from NAME.
func TestReservationDoesNotWidenAHeaderThatAlreadyFits(t *testing.T) {
	cols := []string{"NAME", "READY"}
	rows := [][]string{{"api", "0/1"}}
	w, _ := fitCols(cols, rows, []int{0, 2}, 60, 2)
	if w[1] != len("READY") {
		t.Errorf("READY sized %d, want %d", w[1], len("READY"))
	}
}

// A graded table must still end every line at exactly the panel width, at the
// narrow width where truncation actually bites. The lens path is the one
// TestTableRowWithWideRunesKeepsColumnWidth does not reach.
func TestGlyphedLensRowsKeepTheirWidth(t *testing.T) {
	m := newTestModel(t, mock.New("k10s-demo-prod"))
	m.jumpToResource("vm-agents")
	for _, inner := range []int{100, 46} {
		for i, ln := range m.tableBody(inner, 20) {
			if got := lipgloss.Width(ln); got > inner {
				t.Fatalf("inner %d: line %d is %d cells wide, want ≤ %d: %q", inner, i, got, inner, ln)
			}
		}
	}
}

// The mark must be one cell wide and printable on a dumb terminal: an emoji is
// width-2 in some terminals and width-1 in others, which would corrupt the row.
func TestGlyphsAreSingleWidthASCII(t *testing.T) {
	for _, lvl := range []string{"ok", "warn", "error", "unknown"} {
		g := severityGlyph(lvl)
		if len(g) != 2 || g[0] < 0x21 || g[0] > 0x7e || g[1] != ' ' {
			t.Errorf("%s glyph %q is not a printable ASCII mark plus its separator", lvl, g)
		}
		if lipgloss.Width(g) != 2 {
			t.Errorf("%s glyph %q measures %d cells, want the 2 the column reserved", lvl, g, lipgloss.Width(g))
		}
	}
	seen := map[string]bool{}
	for _, lvl := range []string{"ok", "warn", "error", "unknown"} {
		g := severityGlyph(lvl)
		if seen[g] {
			t.Errorf("glyph %q is used for two levels — the mark must tell them apart", g)
		}
		seen[g] = true
	}
	if strings.TrimSpace(severityGlyph("error")) == "" {
		t.Error("the error glyph is blank")
	}
}
