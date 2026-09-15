package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/mock"
)

// The zone scanner replaced bubblezone because bubblezone's was quadratic.
// These tests pin the behaviour the rest of the UI relies on: markers never
// reach the terminal, they never count towards a line's width, and the
// coordinates handed to click handling are the ones the text actually landed
// on.

func TestScanZonesStripsEveryMarker(t *testing.T) {
	frame := markZone("a", "hello") + " " + markZone("b", "world")

	got := scanZones(frame)

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("a marker survived into the output: %q", got)
	}
	if got != "hello world" {
		t.Errorf("scanZones = %q, want %q", got, "hello world")
	}
}

func TestScanZonesRecordsCellPositions(t *testing.T) {
	// "..." then a 5-cell zone starting at cell 3.
	frame := "..." + markZone("mid", "abcde") + "!!"

	if got := scanZones(frame); got != "...abcde!!" {
		t.Fatalf("text = %q, want %q", got, "...abcde!!")
	}

	z := getZone("mid")
	if !z.ok {
		t.Fatal("zone mid was not recorded")
	}
	if z.x != 3 || z.y != 0 || z.w != 5 {
		t.Errorf("zone mid = {x:%d y:%d w:%d}, want {x:3 y:0 w:5}", z.x, z.y, z.w)
	}
}

func TestScanZonesCountsLines(t *testing.T) {
	frame := "first\nsecond\n" + markZone("third", "xy")

	scanZones(frame)

	z := getZone("third")
	if !z.ok {
		t.Fatal("zone third was not recorded")
	}
	if z.y != 2 || z.x != 0 || z.w != 2 {
		t.Errorf("zone third = {x:%d y:%d w:%d}, want {x:0 y:2 w:2}", z.x, z.y, z.w)
	}
}

// The scanner must tell its own markers apart from the colour escapes that
// surround them, and must not count either as visible width. This is the case
// that breaks if zoneAt ever stops checking the terminator.
func TestScanZonesIgnoresColourEscapes(t *testing.T) {
	coloured := lipgloss.NewStyle().
		Background(lipgloss.Color("#1a1b26")).
		Foreground(lipgloss.Color("#c0caf5")).
		Render("ab")

	frame := coloured + markZone("after", "cd")

	got := scanZones(frame)
	if !strings.Contains(got, "\x1b[") {
		t.Error("colour escapes must survive the scan, only markers are stripped")
	}
	if w := lipgloss.Width(got); w != 4 {
		t.Errorf("visible width = %d, want 4 (%q)", w, got)
	}

	z := getZone("after")
	if !z.ok || z.x != 2 || z.w != 2 {
		t.Errorf("zone after = {x:%d w:%d ok:%v}, want {x:2 w:2 ok:true}", z.x, z.w, z.ok)
	}
}

// Markers must be invisible to width measurement even before a scan, because
// the layout code measures lines it has already marked.
func TestMarkedTextKeepsItsWidth(t *testing.T) {
	plain := "resources"
	if got, want := lipgloss.Width(markZone("res:0", plain)), lipgloss.Width(plain); got != want {
		t.Errorf("marked width = %d, want %d — a marker is being counted as visible text", got, want)
	}
}

// Wide runes are why width is measured with ansi.StringWidth rather than by
// counting bytes or runes.
func TestScanZonesMeasuresWideRunes(t *testing.T) {
	frame := "日本" + markZone("wide", "x")

	scanZones(frame)

	z := getZone("wide")
	if !z.ok {
		t.Fatal("zone wide was not recorded")
	}
	if z.x != 4 {
		t.Errorf("zone wide starts at cell %d, want 4 (two double-width runes)", z.x)
	}
}

// A zone left open (its closing marker sliced off by an overlay, say) must not
// be reported at all rather than reported with junk bounds.
func TestUnclosedZoneIsNotReported(t *testing.T) {
	scanZones("text" + zoneMarker("dangling") + "more")

	if z := getZone("dangling"); z.ok {
		t.Errorf("an unclosed marker was reported as a zone: %+v", z)
	}
}

// Every frame replaces the zone table wholesale. A target that is no longer
// drawn must stop being clickable — including on a frame that carries no
// markers at all, which is what happens while a modal is open.
func TestZonesFromThePreviousFrameAreDropped(t *testing.T) {
	scanZones(markZone("gone", "x"))
	if !getZone("gone").ok {
		t.Fatal("setup: zone gone should be recorded by the first scan")
	}

	scanZones("a frame with no markers at all")

	if z := getZone("gone"); z.ok {
		t.Error("a zone from the previous frame is still clickable after a frame that had none")
	}
}

func TestInBounds(t *testing.T) {
	scanZones("..." + markZone("btn", "abcde"))
	z := getZone("btn") // x=3, y=0, w=5 → cells 3..7

	at := func(x, y int) tea.MouseMsg {
		return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	}

	for _, c := range []struct {
		name string
		msg  tea.MouseMsg
		want bool
	}{
		{"first cell", at(3, 0), true},
		{"last cell", at(7, 0), true},
		{"one cell left", at(2, 0), false},
		{"one cell past the end", at(8, 0), false},
		{"wrong row", at(5, 1), false},
	} {
		if got := z.inBounds(c.msg); got != c.want {
			t.Errorf("%s: inBounds = %v, want %v", c.name, got, c.want)
		}
	}

	if (zoneBounds{}).inBounds(at(0, 0)) {
		t.Error("an unset zone must never be in bounds")
	}
}

// Cell text comes from the cluster: pod names, event messages and lens
// JSONPath columns are arbitrary strings, so a cell can carry CJK or an emoji.
// Those are wider than their rune count, so padding a cell with %-*s (which
// counts runes) overshoots and every column after it in that row shifts.
//
// This is checked on tableBody's own output rather than on a whole frame,
// because Panel pads its body lines with padBG, which measures and would
// truncate an over-wide row back — hiding the misalignment behind a frame that
// still looks the right size.
//
// It matters more since tableBody started telling padBGOf the row width it
// computed from the layout instead of measuring: that arithmetic is only
// correct if every cell really is as wide as its column.
func TestTableRowWithWideRunesKeepsColumnWidth(t *testing.T) {
	for _, name := range []string{"日本語テストのポッド", "🚀 rocket-pod", "café-ingress"} {
		t.Run(name, func(t *testing.T) {
			src := &wideSource{Source: mock.New(""), inject: name}
			m := newTestModel(t, src)
			m.jumpToResource("pods")

			const inner = 100
			for i, ln := range m.tableBody(inner, 20) {
				if got := lipgloss.Width(ln); got != inner {
					t.Fatalf("table line %d is %d cells wide, want %d — a cell is not its column's width: %q",
						i, got, inner, ln)
				}
			}
		})
	}
}

// wideSource replaces the first cell of the first row with text whose display
// width differs from its rune count.
type wideSource struct {
	domain.Source
	inject string
}

func (w *wideSource) Rows(kind, ns string) ([]string, [][]string) {
	cols, rows := w.Source.Rows(kind, ns)
	if len(rows) > 0 && len(rows[0]) > 0 {
		out := make([][]string, len(rows))
		copy(out, rows)
		first := append([]string(nil), rows[0]...)
		first[0] = w.inject
		out[0] = first
		return cols, out
	}
	return cols, rows
}

// paint exists to avoid lipgloss.Style.Render per call. It is only a win if it
// is a drop-in: the bytes must match what Render would have produced, or
// frames change.
func TestPaintMatchesLipglossRender(t *testing.T) {
	bg := lipgloss.Color("#1a1b26")
	for _, c := range []struct {
		name string
		fg   lipgloss.Color
		bold bool
	}{
		{"plain", lipgloss.Color("#c0caf5"), false},
		{"bold", lipgloss.Color("#c0caf5"), true},
		{"accent", lipgloss.Color("#7aa2f7"), false},
		{"background only", "", false},
	} {
		want := lipgloss.NewStyle().Background(bg).Foreground(c.fg).Bold(c.bold).Render("cell")
		if got := paint(bg, c.fg, c.bold, "cell"); got != want {
			t.Errorf("%s: paint = %q, want %q", c.name, got, want)
		}
	}
}

// The second call is the one that reads the cache, so it is the one that could
// return the wrong wrapper.
func TestPaintIsStableAcrossCalls(t *testing.T) {
	bg, fg := lipgloss.Color("#1a1b26"), lipgloss.Color("#f7768e")

	first := paint(bg, fg, false, "x")
	second := paint(bg, fg, false, "x")
	if first != second {
		t.Errorf("cached paint returned %q then %q", first, second)
	}

	// A different combination must not pick up the first one's escapes.
	if other := paint(bg, fg, true, "x"); other == first {
		t.Error("bold and non-bold share a cache entry")
	}
}
