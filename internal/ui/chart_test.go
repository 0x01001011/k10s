package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/mock"
)

func ramp(n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(i * 10)
	}
	return out
}

// Every plotted line must be exactly the width it was given, or the panel it
// sits in drifts.
func TestChartLinesAreExactlyTheRequestedWidth(t *testing.T) {
	th := themeFor(t)
	for _, w := range []int{24, 40, 60, 100} {
		for _, h := range []int{3, 5, 8} {
			lines := chartPanel(th, ramp(40), w, h, "ok")
			if lines == nil {
				t.Fatalf("%dx%d produced no plot", w, h)
			}
			if len(lines) != h {
				t.Errorf("%dx%d produced %d lines", w, h, len(lines))
			}
			for i, l := range lines {
				if got := lipgloss.Width(l); got != w {
					t.Errorf("%dx%d line %d is %d cells", w, h, i, got)
				}
			}
		}
	}
}

// A panel too small to carry a shape draws nothing: the bar and the number
// already state the current value, which is all a cramped chart could repeat.
func TestChartRefusesWhenThereIsNoRoom(t *testing.T) {
	th := themeFor(t)
	for _, tc := range []struct{ w, h int }{{10, 8}, {23, 8}, {60, 2}, {60, 0}} {
		if got := chartPanel(th, ramp(40), tc.w, tc.h, "ok"); got != nil {
			t.Errorf("%dx%d drew a plot anyway", tc.w, tc.h)
		}
	}
}

// One point is not a shape.
func TestChartNeedsTwoSamples(t *testing.T) {
	th := themeFor(t)
	if got := chartPanel(th, nil, 60, 8, "ok"); got != nil {
		t.Error("an empty window drew a plot")
	}
	if got := chartPanel(th, []int32{5}, 60, 8, "ok"); got != nil {
		t.Error("a single sample drew a plot")
	}
}

// A rising series has to rise: the top row carries ink at the right-hand end
// and the bottom row at the left.
func TestChartPlotsTheShape(t *testing.T) {
	th := themeFor(t)
	lines := chartPanel(th, ramp(40), 60, 6, "ok")
	if lines == nil {
		t.Fatal("no plot")
	}

	blank := string(rune(brailleBase))
	top := []rune(stripSGR(lines[0]))[chartGutter:]
	bottom := []rune(stripSGR(lines[len(lines)-1]))[chartGutter:]

	if string(top[0]) != blank {
		t.Errorf("the top row has ink at the left of a rising series: %q", string(top))
	}
	if string(top[len(top)-1]) == blank {
		t.Errorf("the top row has no ink at the right of a rising series: %q", string(top))
	}
	if string(bottom[0]) == blank {
		t.Errorf("the bottom row has no ink at the left of a rising series: %q", string(bottom))
	}
}

// An all-zero window must not divide by its own maximum.
func TestChartSurvivesAFlatZeroWindow(t *testing.T) {
	th := themeFor(t)
	lines := chartPanel(th, []int32{0, 0, 0, 0}, 40, 5, "ok")
	if lines == nil {
		t.Fatal("a flat window drew nothing")
	}
	for i, l := range lines {
		if got := lipgloss.Width(l); got != 40 {
			t.Errorf("line %d is %d cells", i, got)
		}
	}
}

// The axis is readable: the top says the maximum, the bottom says zero.
func TestChartAxisLabelsTheRange(t *testing.T) {
	th := themeFor(t)
	lines := chartPanel(th, []int32{0, 2500}, 40, 5, "ok")
	if !strings.Contains(stripSGR(lines[0]), "2k") {
		t.Errorf("the top of the axis does not name the maximum: %q", stripSGR(lines[0]))
	}
	if !strings.Contains(stripSGR(lines[len(lines)-1]), "0") {
		t.Errorf("the bottom of the axis does not name zero: %q", stripSGR(lines[len(lines)-1]))
	}
}

// An empty window says it is sampling rather than drawing an empty box: "not
// here yet" has to be distinguishable from "this object uses no CPU".
func TestChartSaysWhenItIsStillSampling(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	m.chart, m.spark = true, true

	got := stripSGR(strings.Join(m.chartLines(100, 8), "\n"))
	if !strings.Contains(got, "sampling") {
		t.Errorf("an empty window did not say it is sampling: %q", got)
	}
}

// With samples in hand the panel plots, and every line still fits.
func TestChartRendersInTheFrame(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	m.chart, m.spark = true, true

	// Two ticks is two samples, the minimum a shape needs.
	m.observeMetrics()
	m.observeMetrics()

	frame := m.View()
	for _, line := range strings.Split(frame, "\n") {
		if got := lipgloss.Width(line); got != 160 && got != 0 {
			t.Fatalf("a frame line is %d cells with the chart open: %q", got, stripSGR(line))
		}
	}
	if !strings.Contains(stripSGR(frame), "CPU · ") {
		t.Error("the chart caption is not on the frame")
	}
}
