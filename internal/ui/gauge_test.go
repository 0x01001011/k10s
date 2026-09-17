package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/mock"
)

// TestGaugeShowsAnyUsageAtAll is the bug the card names. The old fill was
// `pct * width / 100`, truncated, so at width 16 every percentage from 1 to 6
// drew zero filled cells and a node at 6% was pixel-identical to one at 0%.
func TestGaugeShowsAnyUsageAtAll(t *testing.T) {
	th := themeFor(t)
	empty := stripSGR(bar(th, 0, 16))

	for _, pct := range []int{1, 2, 3, 4, 5, 6} {
		if got := stripSGR(bar(th, pct, 16)); got == empty {
			t.Errorf("%d%% renders identically to 0%%: %q", pct, got)
		}
	}
}

// 99% and 100% must be distinguishable, or a node one pod short of full looks
// full.
func TestGaugeDoesNotRoundUpToFull(t *testing.T) {
	th := themeFor(t)
	for _, w := range []int{6, 10, 16} {
		full := stripSGR(bar(th, 100, w))
		near := stripSGR(bar(th, 99, w))
		if near == full {
			t.Errorf("width %d: 99%% and 100%% render identically (%q)", w, full)
		}
		if !strings.Contains(full, strings.Repeat(glyphs.full, w)) {
			t.Errorf("width %d: 100%% is not a full bar: %q", w, full)
		}
	}
}

// A negative or over-100 reading must not panic: the old code passed a
// negative count straight to strings.Repeat.
func TestGaugeSurvivesImpossibleInput(t *testing.T) {
	th := themeFor(t)
	for _, tc := range []struct{ pct, width int }{
		{-1, 16}, {-100, 16}, {101, 16}, {1000, 16}, {50, 0}, {50, -1}, {0, 1},
	} {
		got := bar(th, tc.pct, tc.width)
		if tc.width <= 0 && got != "" {
			t.Errorf("bar(%d, %d) = %q, want empty", tc.pct, tc.width, got)
		}
	}
}

// The header is laid out arithmetically, so a mis-measured bar shifts
// everything beside it.
func TestGaugeWidthIsExact(t *testing.T) {
	th := themeFor(t)
	for _, w := range []int{6, 10, 16} {
		for pct := 0; pct <= 100; pct++ {
			if got, want := lipgloss.Width(bar(th, pct, w)), barWidth(w); got != want {
				t.Fatalf("bar(%d%%, width %d) is %d cells, want %d", pct, w, got, want)
			}
		}
	}
}

// Severity must never be carried by colour alone: red against green is the
// worst possible pair for deuteranopia, and it was the gauge's only signal.
func TestGaugeGradesAreDistinctWithoutColour(t *testing.T) {
	th := themeFor(t)
	seen := map[string]int{}
	for _, pct := range []int{10, 70, 95} {
		mark := string([]rune(stripSGR(bar(th, pct, 16)))[0])
		if prev, dup := seen[mark]; dup {
			t.Errorf("%d%% and %d%% share the mark %q — the grade is colour-only", prev, pct, mark)
		}
		seen[mark] = pct
	}
}

func TestGradeThresholds(t *testing.T) {
	for _, tc := range []struct {
		pct  int
		want string
	}{
		{0, "ok"}, {59, "ok"}, {60, "warn"}, {84, "warn"}, {85, "error"}, {100, "error"},
	} {
		if got := gradeOf(tc.pct); got != tc.want {
			t.Errorf("gradeOf(%d) = %q, want %q", tc.pct, got, tc.want)
		}
	}
}

// Every rune in either set must measure one cell: the row is padded
// arithmetically, so a width-2 glyph shifts every column to its right.
func TestGlyphSetsAreSingleWidth(t *testing.T) {
	sets := map[string]glyphSet{"unicode": unicodeGlyphs, "ascii": asciiGlyphs}

	for name, g := range sets {
		all := append([]string{g.full, g.trough, g.tick, g.ok, g.warn, g.err}, g.partials...)
		all = append(all, g.ladder...)
		for _, r := range all {
			if got := lipgloss.Width(r); got != 1 {
				t.Errorf("%s: %q measures %d cells", name, r, got)
			}
		}
		// Two different grades must never produce the same mark.
		if g.ok == g.warn || g.warn == g.err || g.ok == g.err {
			t.Errorf("%s: grade marks are not distinct (%q %q %q)", name, g.ok, g.warn, g.err)
		}
	}

	// The ASCII set has to be ASCII, or it is not a fallback.
	ascii := append([]string{asciiGlyphs.full, asciiGlyphs.trough, asciiGlyphs.tick}, asciiGlyphs.ladder...)
	ascii = append(ascii, asciiGlyphs.partials...)
	for _, r := range ascii {
		for _, c := range r {
			if c > 0x7f {
				t.Errorf("ascii set contains a non-ASCII rune: %q", r)
			}
		}
	}
}

// A sparkline is scaled to its own row: a 5m sidecar and a 4-core gateway each
// have to show their own shape, and a shared scale flattens one of them.
func TestSparkScalesToItsOwnWindow(t *testing.T) {
	th := themeFor(t)

	flat := []rune(stripSGR(spark(th, []int32{5, 5, 5, 5}, "ok")))
	if len(flat) != 4 {
		t.Fatalf("a 4-sample window rendered %d glyphs", len(flat))
	}
	for _, r := range flat {
		if r != flat[0] {
			t.Errorf("a flat series is not flat: %q", string(flat))
			break
		}
	}

	// The same shape at a different magnitude renders identically.
	small := stripSGR(spark(th, []int32{1, 2, 3, 4, 8}, "ok"))
	big := stripSGR(spark(th, []int32{100, 200, 300, 400, 800}, "ok"))
	if small != big {
		t.Errorf("the same shape at different magnitudes differed: %q vs %q", small, big)
	}
	top := glyphs.ladder[len(glyphs.ladder)-1]
	if !strings.HasSuffix(small, top) {
		t.Errorf("the window maximum is not the top rung: %q", small)
	}
}

func TestSparkHandlesAnEmptyOrZeroWindow(t *testing.T) {
	th := themeFor(t)
	if got := spark(th, nil, "ok"); got != "" {
		t.Errorf("an empty window rendered %q", got)
	}
	// All-zero renders the floor rung rather than dividing by its own maximum.
	if got := stripSGR(spark(th, []int32{0, 0, 0}, "ok")); len([]rune(got)) != 3 {
		t.Errorf("an all-zero window rendered %q", got)
	}
}

// The ring is the sparkline's memory: it has to wrap oldest-first, or the
// shape is drawn backwards.
func TestRingWrapsOldestFirst(t *testing.T) {
	var r ring
	for i := int32(1); i <= histLen+3; i++ {
		r.push(i)
	}
	got := r.samples()
	if len(got) != histLen {
		t.Fatalf("ring holds %d samples, want %d", len(got), histLen)
	}
	want := int32(4) // the last histLen values pushed, in order
	for i, v := range got {
		if v != want {
			t.Fatalf("sample %d = %d, want %d (window: %v)", i, v, want, got)
		}
		want++
	}
}

func TestRingIsShortBeforeItIsFull(t *testing.T) {
	var r ring
	r.push(7)
	r.push(9)
	if got := r.samples(); len(got) != 2 || got[0] != 7 || got[1] != 9 {
		t.Errorf("partial window = %v, want [7 9]", got)
	}
}

// TestHistoryIsNotFedByView is the invariant the whole file rests on. The
// existing trend map is sampled inside tableBody's row loop, which makes it
// useless as a time series: View runs on every keystroke rather than once per
// tick, so typing appends duplicates and idling appends nothing, and it walks
// only visible rows, so scrolling past a pod leaves a hole. Rendering must
// never move a sample.
func TestHistoryIsNotFedByView(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	m.spark = true
	m.observeMetrics()

	before := map[string][]int32{}
	for k, r := range m.hist {
		before[k] = r.samples()
	}
	if len(before) == 0 {
		t.Fatal("no series were recorded")
	}

	for i := 0; i < 20; i++ {
		_ = m.View()
	}

	for k, r := range m.hist {
		if got, want := len(r.samples()), len(before[k]); got != want {
			t.Fatalf("%s grew from %d samples to %d across 20 frames", k, want, got)
		}
	}
}

// A series whose row has gone is dropped. The existing trend map has no sweep
// at all and grows with every pod name ever scrolled past.
func TestHistorySweepDropsVanishedRows(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	m.spark = true
	m.observeMetrics()

	m.hist[histKey("pods", "default", "a-pod-that-left")] = &ring{}
	m.observeMetrics()

	if _, still := m.hist[histKey("pods", "default", "a-pod-that-left")]; still {
		t.Error("a series outlived its row")
	}
}

// A sentinel is not a zero: recording one would draw a dip the cluster never
// had.
func TestHistorySkipsSentinelReadings(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	m.spark = true
	m.observeMetrics()

	// payment-api-9d7c8f6b5-wr3nc is the demo's unmetricked pod.
	if r := m.hist[histKey("pods", "default", "payment-api-9d7c8f6b5-wr3nc")]; r != nil {
		if s := r.samples(); len(s) > 0 {
			t.Errorf("a pending CPU reading was recorded as %v", s)
		}
	}
}
