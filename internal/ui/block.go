package ui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/0x01001011/k10s/internal/theme"
)

// Block is a fixed-size rectangle of terminal cells. Every line is padded to
// exactly W visible cells, so blocks can be joined without re-measuring
// (important: measuring breaks once bubblezone markers are embedded).
type Block struct {
	W, H  int
	Lines []string
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// paintKey identifies one fg/bg/bold combination. The colour profile is part
// of the key so a profile switch (tests, or a terminal that reports no colour)
// can never serve stale escape sequences.
type paintKey struct {
	fg, bg  lipgloss.Color
	bold    bool
	profile termenv.Profile
}

var (
	paintMu    sync.RWMutex
	paintCache = map[paintKey][2]string{}
)

// paint writes text in fg on bg without going through lipgloss on every call.
//
// lipgloss.Style.Render re-resolves both colours from their hex strings and
// re-formats the ANSI sequence every single time it is called; at ~40 rows ×
// ~8 columns that dominated the frame. The escape prefix/suffix depends only
// on the colour pair, so it is rendered once per combination and reused.
//
// The pair is taken from lipgloss' own output, not hand-assembled, so the
// bytes stay identical to what Render would have produced.
//
// Only for single-line text with no border, margin or padding — that is every
// table cell, but not a Panel frame.
func paint(bg, fg lipgloss.Color, bold bool, text string) string {
	k := paintKey{fg: fg, bg: bg, bold: bold, profile: lipgloss.ColorProfile()}

	paintMu.RLock()
	wrap, ok := paintCache[k]
	paintMu.RUnlock()

	if !ok {
		style := lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(bold)
		// \x00 never appears in real cell text, so cutting on it splits
		// Render's output into exactly its prefix and suffix.
		pre, suf, found := strings.Cut(style.Render("\x00"), "\x00")
		if !found {
			// Render did something unexpected; fall back to it wholesale
			// rather than emit corrupt escapes.
			return style.Render(text)
		}
		wrap = [2]string{pre, suf}
		paintMu.Lock()
		paintCache[k] = wrap
		paintMu.Unlock()
	}
	return wrap[0] + text + wrap[1]
}

func pad(s string, w int) string {
	d := w - lipgloss.Width(s)
	switch {
	case d > 0:
		return s + spaces(d)
	case d < 0:
		return ansi.Truncate(s, w, "")
	}
	return s
}

// padBG pads to w using an independently-rendered background run, so we never
// nest lipgloss styles (a nested reset would drop the outer background).
func padBG(s string, w int, bg lipgloss.Color) string {
	return padBGOf(s, lipgloss.Width(s), w, bg)
}

// padBGOf is padBG for a caller that already knows how wide s is. Measuring a
// line means an ANSI-aware walk over every escape in it, and the table builds
// its rows to exact column widths, so it can say.
func padBGOf(s string, cur, w int, bg lipgloss.Color) string {
	switch d := w - cur; {
	case d > 0:
		return s + paint(bg, "", false, spaces(d))
	case d < 0:
		return ansi.Truncate(s, w, "")
	}
	return s
}

func trunc(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return ansi.Truncate(s, w, "")
	}
	return ansi.Truncate(s, w, "…")
}

func NewBlock(w, h int, bg lipgloss.Color) Block {
	fill := paint(bg, "", false, spaces(w))
	lines := make([]string, h)
	for i := range lines {
		lines[i] = fill
	}
	return Block{W: w, H: h, Lines: lines}
}

func BlockOf(w, h int, lines []string, bg lipgloss.Color) Block {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		s := ""
		if i < len(lines) {
			s = lines[i]
		}
		out[i] = padBG(s, w, bg)
	}
	return Block{W: w, H: h, Lines: out}
}

func HJoin(bs ...Block) Block {
	h, w := 0, 0
	for _, b := range bs {
		if b.H > h {
			h = b.H
		}
		w += b.W
	}
	lines := make([]string, h)
	for i := 0; i < h; i++ {
		var sb strings.Builder
		for _, b := range bs {
			if i < len(b.Lines) {
				sb.WriteString(b.Lines[i])
			} else {
				sb.WriteString(spaces(b.W))
			}
		}
		lines[i] = sb.String()
	}
	return Block{W: w, H: h, Lines: lines}
}

func VJoin(bs ...Block) Block {
	w, h := 0, 0
	var lines []string
	for _, b := range bs {
		if b.W > w {
			w = b.W
		}
		h += b.H
		lines = append(lines, b.Lines...)
	}
	return Block{W: w, H: h, Lines: lines}
}

// Overlay stamps o onto b at (x, y).
func (b Block) Overlay(o Block, x, y int) Block {
	lines := append([]string(nil), b.Lines...)
	for i := 0; i < o.H; i++ {
		ly := y + i
		if ly < 0 || ly >= len(lines) {
			continue
		}
		src := lines[ly]
		left := pad(ansi.Truncate(src, x, ""), x)
		right := ansi.TruncateLeft(src, x+o.W, "")
		lines[ly] = left + o.Lines[i] + right
	}
	return Block{W: b.W, H: b.H, Lines: lines}
}

func (b Block) String() string { return strings.Join(b.Lines, "\n") }

const (
	bTL, bTR, bBL, bBR = "╭", "╮", "╰", "╯"
	bH, bV             = "─", "│"
)

// Panel draws a bordered box with the title embedded in the top border and an
// optional right-aligned tag (used for the zoom button).
type PanelOpts struct {
	Title    string
	Tag      string // already-styled; TagPlain gives its visible text
	TagPlain string
	Focused  bool
	W, H     int
	// BorderCol overrides the border/title colour (used by the danger modal).
	BorderCol lipgloss.Color
}

func Panel(th theme.Theme, o PanelOpts, body []string) Block {
	borderCol := th.Border
	if o.Focused {
		borderCol = th.BorderOn
	}
	if o.BorderCol != "" {
		borderCol = o.BorderCol
	}
	bs := func(s string) string { return paint(th.Bg, borderCol, false, s) }

	titleCol, titleBold := th.Subtle, false
	if o.Focused {
		titleCol, titleBold = th.Accent, true
	}
	if o.BorderCol != "" {
		titleCol, titleBold = o.BorderCol, true
	}
	ts := func(s string) string { return paint(th.Bg, titleCol, titleBold, s) }

	inner := o.W - 2
	if inner < 1 {
		inner = 1
	}

	// Top border. Between the corners there are exactly `inner` cells:
	//
	//	bH + " " + title + " "   +   fill   +   " " + tag + " "
	//
	// so the title only gets what the tag leaves behind. Truncating it
	// against `inner` alone is what used to push long titles (`logs -f
	// <pod>`, `top <pod>`) past the block's own width, and a Block wider
	// than it claims drifts every join and overlay after it.
	rightPlain := ""
	if o.TagPlain != "" {
		rightPlain = " " + o.TagPlain + " "
	}
	// A tag with no room at all is dropped rather than cut: o.Tag carries
	// bubblezone markers, so truncating it would corrupt its click target.
	if lipgloss.Width(rightPlain) > inner-3 {
		rightPlain = ""
	}
	// -4: bH, the two spaces around the title, and one cell of fill kept so
	// a full-length title never butts up against the tag.
	avail := inner - 4 - lipgloss.Width(rightPlain)
	if avail < 0 {
		avail = 0
	}
	title := trunc(o.Title, avail)
	leftPlain := bH + " " + title + " "
	fill := inner - lipgloss.Width(leftPlain) - lipgloss.Width(rightPlain)
	if fill < 0 {
		fill = 0
	}
	top := bs(bTL+bH+" ") + ts(title) + bs(" "+strings.Repeat(bH, fill))
	if rightPlain != "" {
		top += bs(" ") + o.Tag + bs(" ")
	}
	top += bs(bTR)

	bodyH := o.H - 2
	lines := make([]string, 0, o.H)
	lines = append(lines, top)
	// Both border cells are identical on every body line, so they are built
	// once rather than per line.
	edge := bs(bV)
	for i := 0; i < bodyH; i++ {
		s := ""
		if i < len(body) {
			s = body[i]
		}
		lines = append(lines, edge+padBG(s, inner, th.Bg)+edge)
	}
	lines = append(lines, bs(bBL+strings.Repeat(bH, inner)+bBR))
	return Block{W: o.W, H: o.H, Lines: lines}
}
