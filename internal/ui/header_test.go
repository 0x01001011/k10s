package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/mock"
)

// stripSGR removes colour sequences so a test can assert on the characters a
// reader sees rather than on the escape codes around them.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// headerAt renders a frame at the given size and returns the banner rows with
// styling stripped, plus the model that drew them.
func headerAt(t *testing.T, w, h int) (*Model, []string) {
	t.Helper()
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	out := m.View()
	if out == "" {
		t.Fatalf("empty frame at %dx%d", w, h)
	}
	lines := strings.Split(out, "\n")
	n := m.layout().headerH
	if len(lines) < n {
		t.Fatalf("frame has %d lines, header wants %d", len(lines), n)
	}
	got := make([]string, 0, n)
	for _, l := range lines[:n] {
		got = append(got, stripSGR(l))
	}
	return m, got
}

// TestHeaderNeverClipsMidToken is the T39 guard. The banner used to be
// assembled at full length and then cut by the block, so at 80 columns the
// first row ended on "nodes" with no count after it, and the gauge row ended
// "42%    81" — a memory total cut inside the number. Segments are dropped
// whole now, so anything still on screen is complete.
func TestHeaderNeverClipsMidToken(t *testing.T) {
	// Tokens that never legitimately end a line. If one is last, the line was
	// cut mid-token.
	danglers := []string{"nodes", "ver", "ns", "theme", "CPU", "MEM", "│", "·", "/"}

	for _, size := range []struct{ w, h int }{
		{80, 24}, {88, 30}, {96, 30}, {110, 30}, {120, 40}, {140, 44}, {160, 48},
	} {
		_, rows := headerAt(t, size.w, size.h)
		for i, line := range rows {
			trimmed := strings.TrimRight(line, " ")
			if trimmed == "" {
				continue // the deliberate blank row in the four-row banner
			}
			for _, d := range danglers {
				if strings.HasSuffix(trimmed, d) {
					t.Errorf("%dx%d header row %d ends on %q — cut mid-token: %q",
						size.w, size.h, i, d, trimmed)
				}
			}
			if strings.Contains(trimmed, "…") {
				t.Errorf("%dx%d header row %d is truncated: %q", size.w, size.h, i, trimmed)
			}
		}
	}
}

// TestHeaderFitsItsWidth: every banner row must be exactly the terminal width,
// or the joins below it drift (the Block invariant).
func TestHeaderFitsItsWidth(t *testing.T) {
	for _, w := range []int{80, 88, 96, 110, 120, 140, 160} {
		_, rows := headerAt(t, w, 40)
		for i, line := range rows {
			if got := lipgloss.Width(line); got != w {
				t.Errorf("at width %d, header row %d is %d cells wide", w, i, got)
			}
		}
	}
}

// TestHeaderShrinksWithTheTerminal pins the row budget: four rows is a third
// of an 80x24 screen spent before the first pod, and two of those four carry
// nothing.
func TestHeaderShrinksWithTheTerminal(t *testing.T) {
	for _, tc := range []struct{ w, want int }{
		{80, 1}, {95, 1}, {96, 2}, {119, 2}, {120, 4}, {160, 4},
	} {
		if got := headerRows(tc.w); got != tc.want {
			t.Errorf("headerRows(%d) = %d, want %d", tc.w, got, tc.want)
		}
	}
}

// TestNarrowTerminalGivesRowsToTheTable is the point of the card: 80x24 showed
// twelve of fourteen pods, and 47% of its columns went to a sidebar and a
// static verb list.
func TestNarrowTerminalGivesRowsToTheTable(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if got := m.visibleRows(); got < 15 {
		t.Errorf("80x24 shows %d table rows, want at least 15", got)
	}
	l := m.layout()
	if l.rightW != 0 {
		t.Errorf("at 80 columns the Actions pane still takes %d columns", l.rightW)
	}
	if l.leftW == 0 {
		t.Error("the sidebar was dropped too — it is the only thing saying where you are")
	}
	if l.mainW < 55 {
		t.Errorf("the table got %d of 80 columns", l.mainW)
	}
}

// TestDemoBannerSurvivesEveryWidth: every figure in this header is sample
// data while the demo backend is up, so the tag has to be on the frame at any
// size. It is allowed to shorten, not to disappear.
func TestDemoBannerSurvivesEveryWidth(t *testing.T) {
	for _, w := range []int{80, 96, 120, 160} {
		_, rows := headerAt(t, w, 40)
		if !strings.Contains(strings.Join(rows, "\n"), "DEMO") {
			t.Errorf("at width %d the demo banner is gone: %q", w, rows)
		}
	}
}

// TestHeaderZoneIsMarkedOnlyWhenDrawn: the ns and theme buttons are the
// header's only mouse affordance. When they did not fit they were still
// marked as click targets past the right-hand edge, so the affordance died
// silently. A zone must exist if and only if its segment is on screen.
func TestHeaderZoneIsMarkedOnlyWhenDrawn(t *testing.T) {
	for _, w := range []int{80, 96, 120, 160} {
		m, rows := headerAt(t, w, 40)
		header := strings.Join(rows, "\n")

		for _, tc := range []struct{ id, text string }{
			{"theme", "theme " + m.th().Name},
			{"nsbtn", "ns " + m.namespace},
		} {
			drawn := strings.Contains(header, tc.text)
			marked := getZone(tc.id).ok
			if drawn != marked {
				t.Errorf("at width %d: %s drawn=%v but zone marked=%v", w, tc.id, drawn, marked)
			}
		}
	}
}
