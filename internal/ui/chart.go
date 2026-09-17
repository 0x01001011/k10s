package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/theme"
)

// The chart panel (T44): one object's CPU across the window history.go keeps.
//
// Braille, because a braille cell is a 2×4 dot matrix and therefore buys eight
// times the resolution of a block glyph out of the same terminal cell — a 60×8
// panel plots 120 points over 32 vertical steps. Nothing else in a terminal
// gets close without bringing a second rendering model along with it.
//
// The ASCII fallback is the sparkline ladder at one column per sample. Half
// the resolution, and it looks like what it is rather than approximating
// braille badly.

// brailleBase is the start of the Unicode braille block. The low byte is a
// bitmask of the eight dots, which is why a plot can be accumulated by OR-ing
// into a grid instead of drawn stroke by stroke.
const brailleBase = 0x2800

// brailleDot maps (column 0-1, row 0-3) to its bit. This is the braille
// standard's numbering, not raster order — dots 1-3 then 7 down the left,
// 4-6 then 8 down the right.
var brailleDot = [2][4]byte{
	{0x01, 0x02, 0x04, 0x40},
	{0x08, 0x10, 0x20, 0x80},
}

// chartGutter is the width reserved for axis labels.
const chartGutter = 6

// chartPanel plots samples into w×h cells, newest at the right.
//
// Returns nil when there is nothing worth drawing: fewer than two samples is
// not a shape, and a panel under 24×3 is decoration rather than information —
// the bar and the number already state the current value, which is all a
// cramped chart could manage to repeat.
func chartPanel(th theme.Theme, samples []int32, w, h int, grade string) []string {
	if len(samples) < 2 || w < 24 || h < 3 {
		return nil
	}

	var max int32
	for _, s := range samples {
		if s > max {
			max = s
		}
	}
	if max <= 0 {
		max = 1
	}

	plotW := w - chartGutter
	col := gradeColor(th, grade)

	if glyphs.full == asciiGlyphs.full {
		return asciiChart(th, samples, plotW, h, max, col)
	}

	// Accumulate into a dot grid: plotW×2 horizontal subcells, h×4 vertical.
	dotsX, dotsY := plotW*2, h*4
	grid := make([][]byte, h)
	for i := range grid {
		grid[i] = make([]byte, plotW)
	}

	for x := 0; x < dotsX; x++ {
		// Map each subcolumn back to a sample, so a short window stretches
		// across the panel rather than huddling against one edge.
		si := x * (len(samples) - 1) / (dotsX - 1)
		v := samples[clamp(si, 0, len(samples)-1)]

		y := clamp(int(int64(v)*int64(dotsY-1)/int64(max)), 0, dotsY-1)
		// Screen rows run downwards; the value axis runs up.
		row := (dotsY - 1 - y) / 4
		sub := (dotsY - 1 - y) % 4
		grid[row][x/2] |= brailleDot[x%2][sub]
	}

	out := make([]string, 0, h)
	for r := 0; r < h; r++ {
		var line strings.Builder
		for c := 0; c < plotW; c++ {
			line.WriteRune(rune(brailleBase + int(grid[r][c])))
		}
		out = append(out,
			paint(th.Bg, th.Subtle, false, axisLabel(r, h, max))+
				paint(th.Bg, col, false, line.String()))
	}
	return out
}

// chartLines is the chart as it appears under the table: a rule, a caption
// naming what is being plotted, and the plot.
//
// It says what it is waiting for rather than drawing an empty box. A window
// fills at the backend's metrics cadence, so "nothing here yet" is the
// expected state for the first minute and has to be distinguishable from
// "this object uses no CPU".
func (m *Model) chartLines(inner, h int) []string {
	th := m.th()
	rule := paint(th.Bg, th.Border, false, strings.Repeat("╌", inner))

	name := m.curName()
	samples := m.rowSamples(m.curKind().Key, m.curNamespace(), name)

	caption := func(s string) []string {
		return []string{rule, paint(th.Bg, th.Subtle, false, " "+s)}
	}
	if len(samples) < 2 {
		return caption("CPU · " + name + " — sampling, one point per refresh")
	}

	// Grade the plot by the value the table is already showing, so the chart
	// and the cell above it never disagree about how this object is doing.
	grade := "ok"
	cols, _ := m.tableData()
	if ci := colIndex(cols, "CPU"); ci >= 0 {
		if row := m.curRow(); row != nil {
			grade = cellLevel("", cellAt(row, ci))
		}
	}

	plot := chartPanel(th, samples, inner, h-2, grade)
	if plot == nil {
		return caption("CPU · " + name + " — not enough room to plot")
	}

	out := caption(fmt.Sprintf("CPU · %s · last %d samples", name, len(samples)))
	for _, l := range plot {
		out = append(out, padBG(l, inner, th.Bg))
	}
	return out
}

// axisLabel prints the scale on the top, middle and bottom rows and nothing
// elsewhere — three numbers is enough to read a shape, and more would crowd a
// panel this size.
func axisLabel(row, h int, max int32) string {
	switch row {
	case 0:
		return fmt.Sprintf("%5s ", shortNum(max))
	case h - 1:
		return fmt.Sprintf("%5s ", "0")
	case h / 2:
		return fmt.Sprintf("%5s ", shortNum(max/2))
	}
	return strings.Repeat(" ", chartGutter)
}

// shortNum keeps an axis label inside five cells.
func shortNum(v int32) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%dM", v/1_000_000)
	case v >= 1000:
		return fmt.Sprintf("%dk", v/1000)
	}
	return fmt.Sprintf("%d", v)
}

// asciiChart is the fallback: one column per sample, filled to the band the
// value reaches, so it still reads as a shape.
func asciiChart(th theme.Theme, samples []int32, w, h int, max int32, col lipgloss.Color) []string {
	g := glyphs
	out := make([]string, 0, h)
	for r := 0; r < h; r++ {
		// Each row covers one band of the range.
		hi := int32(int64(max) * int64(h-r) / int64(h))
		lo := int32(int64(max) * int64(h-r-1) / int64(h))

		var line strings.Builder
		for c := 0; c < w; c++ {
			si := clamp(c*(len(samples)-1)/maxi(1, w-1), 0, len(samples)-1)
			switch v := samples[si]; {
			case v >= hi:
				line.WriteString(g.full)
			case v > lo:
				line.WriteString(g.ladder[len(g.ladder)/2])
			default:
				line.WriteString(" ")
			}
		}
		out = append(out,
			paint(th.Bg, th.Subtle, false, axisLabel(r, h, max))+
				paint(th.Bg, col, false, line.String()))
	}
	return out
}
