package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/theme"
)

// Bars, sparklines and the glyph set behind them (T44).
//
// One rule across all of them: LENGTH OR HEIGHT carries the magnitude, colour
// only grades it, and a glyph always repeats the grade. Red against green is
// the worst possible pair for deuteranopia and it was the gauge's only signal
// — the trend arrow is rescued by its shape, the bar had nothing.
//
// Built rather than pulled in, and the reasons are specific rather than
// principled. bubbles/progress is already in go.mod and is still wrong: it
// renders a gradient through a lipgloss.Style per segment, which is exactly
// the per-cell Style.Render cost paint() exists to avoid and which measured
// 43% of the frame. ntcharts is good and Bubble Tea native, but brings its own
// canvas, viewport and zone handling — a second rendering model beside
// block.go and zones.go, the latter of which exists *because* bubblezone was
// removed on measurement. asciigraph emits a pre-escaped string lipgloss.Width
// would have to re-walk. The whole vocabulary is a few ladders of runes.

// gaugeWarnPct and gaugeErrPct are the thresholds every meter grades by.
// Hoisted out of the old inline switch because several call sites want them,
// and a threshold written down twice eventually disagrees with itself.
const (
	gaugeWarnPct = 60
	gaugeErrPct  = 85
)

// glyphSet is the rune vocabulary, chosen once at startup rather than per
// call — a frame that mixes the two sets is worse than a plain one.
type glyphSet struct {
	full     string
	partials []string // 1/8 .. 7/8, ascending
	trough   string
	tick     string
	ladder   []string // 8 rungs, ascending, for sparklines
	ok       string
	warn     string
	err      string
}

var unicodeGlyphs = glyphSet{
	full:     "█",
	partials: []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉"},
	trough:   "·",
	tick:     "┊",
	ladder:   []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"},
	ok:       "·", warn: "!", err: "×",
}

// asciiGlyphs is not a downgrade of the above so much as the same encodings
// spelled in characters every terminal agrees the width of. The rungs are
// still visually ordered, which is all the sparkline needs.
var asciiGlyphs = glyphSet{
	full:     "#",
	partials: []string{"-", "-", "-", "=", "=", "=", "="},
	trough:   ".",
	tick:     ":",
	ladder:   []string{"_", ".", "-", "~", "=", "+", "*", "#"},
	ok:       ".", warn: "!", err: "x",
}

// glyphs is resolved once. A per-call check would let one frame mix the sets,
// and the answer cannot change while the process runs.
var glyphs = resolveGlyphs()

func resolveGlyphs() glyphSet {
	if os.Getenv("K10S_ASCII") == "1" {
		return asciiGlyphs
	}
	// A terminal that has not said it speaks UTF-8 renders the block elements
	// as mojibake, which is worse than plain ASCII.
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		u := strings.ToUpper(v)
		if strings.Contains(u, "UTF-8") || strings.Contains(u, "UTF8") {
			return unicodeGlyphs
		}
		return asciiGlyphs
	}
	return unicodeGlyphs
}

// gradeOf grades a percentage against the meter thresholds.
func gradeOf(pct int) string {
	switch {
	case pct >= gaugeErrPct:
		return "error"
	case pct >= gaugeWarnPct:
		return "warn"
	}
	return "ok"
}

// gradeMark is the shape that repeats the grade, so a monochrome screenshot
// still ranks three nodes.
func (g glyphSet) gradeMark(grade string) string {
	switch grade {
	case "error":
		return g.err
	case "warn":
		return g.warn
	}
	return g.ok
}

func gradeColor(th theme.Theme, grade string) lipgloss.Color {
	switch grade {
	case "error":
		return th.Err
	case "warn":
		return th.Warn
	}
	return th.Ok
}

// bar renders a ratio meter width cells wide, followed by the percentage.
//
// The old implementation computed `filled := pct * width / 100` and truncated,
// so at width 16 every percentage from 1 to 6 drew zero filled cells: a node
// at 6% was pixel-identical to a node at 0%. It also never guarded a negative
// pct, which reached strings.Repeat and panicked. Both are fixed here, and the
// eighths buy the sub-cell resolution that keeps 99% and 100% apart.
func bar(th theme.Theme, pct, width int) string {
	if width <= 0 {
		return ""
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}

	g := glyphs
	grade := gradeOf(pct)
	col := gradeColor(th, grade)

	eighths := pct * width * 8 / 100
	// Any usage at all shows as something: a bar reading empty for a pod
	// doing real work is the bug this function exists to fix.
	if eighths == 0 && pct > 0 {
		eighths = 1
	}
	// And it only reads full at 100 — 99% must not round up to it.
	if full := width * 8; eighths >= full && pct < 100 {
		eighths = full - 1
	}

	filled, rem := eighths/8, eighths%8

	var b strings.Builder
	b.WriteString(paint(th.Bg, col, false, g.gradeMark(grade)))
	b.WriteString(paint(th.Bg, col, false, strings.Repeat(g.full, filled)))

	used := filled
	if rem > 0 && used < width {
		b.WriteString(paint(th.Bg, col, false, g.partials[rem-1]))
		used++
	}
	// The trough carries threshold ticks, so "how close to warn" is readable
	// without knowing the palette.
	for i := used; i < width; i++ {
		ch, colr := g.trough, th.Border
		if i == width*gaugeWarnPct/100 || i == width*gaugeErrPct/100 {
			ch, colr = g.tick, th.Subtle
		}
		b.WriteString(paint(th.Bg, colr, false, ch))
	}
	b.WriteString(paint(th.Bg, col, false, fmt.Sprintf(" %3d%%", pct)))
	return b.String()
}

// barWidth is what bar() occupies: the grade mark, the meter, and " 100%".
func barWidth(width int) int {
	if width <= 0 {
		return 0
	}
	return 1 + width + 5
}

// spark draws one glyph per sample, oldest to newest, so the newest sits
// beside the number it explains.
//
// Scaled against the row's OWN window maximum rather than a cluster-wide one:
// a 5m sidecar and a 4-core gateway each need to show their own shape, and a
// shared scale flattens one of them to a flat line.
func spark(th theme.Theme, samples []int32, grade string) string {
	if len(samples) == 0 {
		return ""
	}
	g := glyphs
	col := gradeColor(th, grade)

	var max int32
	for _, s := range samples {
		if s > max {
			max = s
		}
	}

	var b strings.Builder
	for _, s := range samples {
		idx := 0
		if max > 0 {
			idx = int(int64(s) * int64(len(g.ladder)-1) / int64(max))
		}
		idx = clamp(idx, 0, len(g.ladder)-1)
		b.WriteString(paint(th.Bg, col, false, g.ladder[idx]))
	}
	return b.String()
}
