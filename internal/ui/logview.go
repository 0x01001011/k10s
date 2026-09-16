package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/theme"
)

// The log viewer.
//
// Three behaviours make it feel like `tail -f` rather than a text dump:
//
//   - newest is at the bottom and the view opens pinned there;
//   - line numbers count *up from the bottom*, so the newest line is always
//     1 and a number keeps meaning the same thing as new lines arrive;
//   - scrolling up unpins ("follow off"), scrolling back to the bottom pins
//     again — the behaviour every log tool has.
//
// Long lines wrap rather than being cut off with an ellipsis: a truncated
// log line is often exactly the part you needed.

// logInitial is what the viewer opens with: the newest 200 lines. Enough to
// see what a pod is doing right now, and small enough that the view is up
// and pinned to the bottom immediately rather than after a long fetch.
const logInitial = 200

// logChunk is the paging size: each scroll back past the oldest loaded line
// asks for this many more, indefinitely.
const logChunk = 500

// wrapLine breaks s into segments of at most w cells, splitting on spaces
// where possible so words survive. Returns at least one segment.
func wrapLine(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	if lipgloss.Width(s) <= w {
		return []string{s}
	}

	var out []string
	for lipgloss.Width(s) > w {
		cut := breakPoint(s, w)
		out = append(out, strings.TrimRight(s[:cut], " "))
		s = strings.TrimLeft(s[cut:], " ")
		if s == "" {
			break
		}
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// breakPoint finds where to split a line: the last space inside the width
// if there is a reasonable one, otherwise a hard cut at the width.
func breakPoint(s string, w int) int {
	// Byte index of the w'th cell. Log lines are overwhelmingly ASCII, and
	// a hard cut on a multi-byte boundary is corrected by the rune scan.
	limit := w
	if limit > len(s) {
		limit = len(s)
	}
	for limit > 0 && limit < len(s) && !isRuneStart(s[limit]) {
		limit--
	}
	if sp := strings.LastIndexByte(s[:limit], ' '); sp > w/2 {
		return sp
	}
	return limit
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// logLevelStyle colours just the level token, not the whole line — the
// message is what you read; the level is what you scan for.
func logLevelStyle(th theme.Theme, tok string) (lipgloss.Color, bool) {
	l, ok := levelOf(tok)
	switch {
	case !ok:
		return "", false
	case l == lvlErr:
		return th.Err, true
	case l == lvlWarn:
		return th.Warn, true
	case l == lvlInfo:
		return th.Ok, true
	default:
		return th.Subtle, true
	}
}

// renderLogLine colours the level token and dims the leading timestamp,
// leaving the message itself in the normal foreground. The key=value context
// a parsed record trails behind logSep is dimmed too: it is there when you
// need it, and out of the way when you are scanning messages.
func renderLogLine(th theme.Theme, s string) string {
	bg := th.Bg
	st := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Background(bg).Foreground(c)
	}

	if head, ctx, found := strings.Cut(s, logSep); found {
		return renderLogLine(th, head) + st(th.Border).Render(logSep) + st(th.Subtle).Render(ctx)
	}

	fields := strings.Fields(s)
	if len(fields) == 0 {
		return st(th.Fg).Render(s)
	}

	var b strings.Builder
	rest := s
	for _, f := range fields[:min(len(fields), 3)] {
		idx := strings.Index(rest, f)
		if idx < 0 {
			break
		}
		b.WriteString(st(th.Fg).Render(rest[:idx]))
		rest = rest[idx+len(f):]

		switch {
		case looksLikeTimestamp(f):
			b.WriteString(st(th.Subtle).Render(f))
		default:
			if col, ok := logLevelStyle(th, f); ok {
				b.WriteString(lipgloss.NewStyle().Background(bg).Foreground(col).Bold(true).Render(f))
			} else {
				b.WriteString(st(th.Fg).Render(f))
			}
		}
	}
	b.WriteString(st(th.Fg).Render(rest))
	return b.String()
}

// looksLikeTimestamp is a cheap shape test — enough to dim the leading
// RFC3339-ish stamp kubectl prepends, without parsing dates on every line.
func looksLikeTimestamp(f string) bool {
	if len(f) < 8 {
		return false
	}
	digits := 0
	for i := 0; i < len(f); i++ {
		if f[i] >= '0' && f[i] <= '9' {
			digits++
		}
	}
	return digits >= 6 && (strings.Count(f, ":") >= 2 || strings.Count(f, "-") >= 2)
}

// logDisp is one loaded line as the viewer deals with it: the text to draw
// (parsed, or the raw line in raw mode) and the severity it was logged at.
// Parsing every line on every frame would put a JSON decode on the render
// path, so it happens once, when the line arrives.
type logDisp struct {
	text string
	lvl  logLevel
}

func (m *Model) logDispOf(raw string) logDisp {
	text, lvl := prettyLog(raw)
	if m.logRaw {
		text = raw
	}
	return logDisp{text: text, lvl: lvl}
}

// rebuildLog re-derives the whole display cache from the raw lines. Called
// when the lines change wholesale (a new log, an older page) or when the way
// they are rendered changes (raw toggle).
func (m *Model) rebuildLog() {
	m.logDisps = make([]logDisp, 0, len(m.textLines))
	for _, raw := range m.textLines {
		m.logDisps = append(m.logDisps, m.logDispOf(raw))
	}
	m.refilterLog()
}

// refilterLog recomputes which lines are on screen. Filtering is on the
// rendered text, so what you type matches what you see.
func (m *Model) refilterLog() {
	m.logShown = m.logShown[:0]
	for _, d := range m.logDisps {
		if m.logPasses(d) {
			m.logShown = append(m.logShown, d.text)
		}
	}
}

// logPasses applies both filters: the level floor and the text box. A line
// with no level at all (a stack trace, a shell banner) is never hidden by
// the level floor — dropping it would hide the panic under the ERROR.
func (m *Model) logPasses(d logDisp) bool {
	if m.logMin != lvlNone && d.lvl != lvlNone && d.lvl < m.logMin {
		return false
	}
	return matchLogFilter(d.text, m.logFilter)
}

// appendLogLine caches one streamed line and reports whether it is visible
// under the current filters.
func (m *Model) appendLogLine(raw string) bool {
	d := m.logDispOf(raw)
	m.logDisps = append(m.logDisps, d)
	if !m.logPasses(d) {
		return false
	}
	m.logShown = append(m.logShown, d.text)
	return true
}

// setLogFilter / cycleLogLevel / toggleLogRaw are the three controls. Each
// one re-anchors the view at the newest line: after changing what you are
// looking at, the useful place to be is the bottom.
func (m *Model) setLogFilter(q string) {
	m.logFilter = q
	m.refilterLog()
	m.logScroll, m.logFollow = 0, true
}

// cycleLogLevel steps the severity floor: all → INFO → WARN → ERROR → all.
func (m *Model) cycleLogLevel() {
	if m.logMin >= lvlErr {
		m.logMin = lvlNone
	} else if m.logMin == lvlNone {
		m.logMin = lvlInfo
	} else {
		m.logMin++
	}
	m.refilterLog()
	m.logScroll, m.logFollow = 0, true
}

func (m *Model) toggleLogRaw() {
	m.logRaw = !m.logRaw
	m.rebuildLog()
	m.logScroll, m.logFollow = 0, true
}

// logShownLines is what the viewer draws and scrolls over.
func (m *Model) logShownLines() []string { return m.logShown }

// logBody renders the visible window of the log, bottom-anchored, with
// wrapped lines and bottom-relative numbering.
func (m *Model) logBody(inner, rows int) []string {
	th := m.th()
	s := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Background(th.Bg).Foreground(c)
	}

	// One row is spent on the status line at the bottom.
	viewRows := maxi(1, rows-1)

	// Numbers count up from the newest line, so the gutter width follows
	// the oldest number on screen rather than the total.
	numW := m.logGutterWidth()
	textW := m.logTextWidth(inner)

	// Build display rows newest-first, then reverse: wrapping means one log
	// line can occupy several rows, so the window has to be filled from the
	// bottom to keep the newest line pinned to the last row.
	type drow struct {
		num  int    // bottom-relative log-line number, 0 for continuations
		text string // one wrapped segment
	}
	var stack []drow

	lines := m.logShownLines()
	skip := m.logScroll // how many display rows are hidden below the view
	for i := len(lines) - 1; i >= 0 && len(stack) < viewRows+skip; i-- {
		segs := wrapLine(lines[i], textW)
		num := len(lines) - i
		// Segments belong to one line; emit them bottom-up so the first
		// segment (carrying the number) ends up on top.
		for j := len(segs) - 1; j >= 0; j-- {
			n := 0
			if j == 0 {
				n = num
			}
			stack = append(stack, drow{num: n, text: segs[j]})
		}
	}
	if skip > len(stack) {
		skip = len(stack)
	}
	stack = stack[skip:]
	if len(stack) > viewRows {
		stack = stack[:viewRows]
	}

	out := make([]string, 0, rows)
	for i := len(stack) - 1; i >= 0; i-- {
		d := stack[i]
		gutter := strings.Repeat(" ", numW)
		if d.num > 0 {
			gutter = fmt.Sprintf("%*d", numW, d.num)
		}
		out = append(out, s(th.Border).Render(" "+gutter+" ")+renderLogLine(th, d.text))
	}
	for len(out) < viewRows {
		out = append([]string{""}, out...)
	}

	out = append(out, m.logStatusLine(inner))
	return out
}

// logGutterWidth is the width of the line-number column, which grows with
// the number of loaded lines.
func (m *Model) logGutterWidth() int {
	return clamp(len(fmt.Sprint(len(m.logShown))), 2, 6)
}

// logTextWidth is the column the log text itself is rendered into: the
// panel's inner width less the gutter. Passing inner explicitly lets the
// renderer use the width it was handed; logCurrentWidth derives it for the
// scroll bookkeeping, which runs outside a render.
func (m *Model) logTextWidth(inner int) int {
	return maxi(8, inner-m.logGutterWidth()-3)
}

// logCurrentWidth is the text width the log panel is being drawn at right
// now, matching what viewMain hands logBody.
func (m *Model) logCurrentWidth() int {
	return m.logTextWidth(m.layout().mainW - 2)
}

// logRows is how many screen rows a log line occupies once wrapped. Scroll
// offsets are counted in display rows, not log lines, so a wrapped line has
// to count for every row it takes up.
func (m *Model) logRows(s string) int {
	return len(wrapLine(s, m.logCurrentWidth()))
}

// logTotalRows is the height of the whole loaded log in display rows.
func (m *Model) logTotalRows() int {
	w := m.logCurrentWidth()
	n := 0
	for _, ln := range m.logShownLines() {
		n += len(wrapLine(ln, w))
	}
	return n
}

// logScrollBy moves the log view, managing follow state. delta > 0 moves
// toward older entries (up the screen). Reaching the top of what is loaded
// asks for more, which is what makes scrolling up feel endless.
func (m *Model) logScrollBy(delta int) {
	maxScroll := maxi(0, m.logTotalRows()-1)
	m.logScroll = clamp(m.logScroll+delta, 0, maxScroll)
	// Pinned to the bottom means following; anywhere else means paused.
	m.logFollow = m.logScroll == 0
}

// logNeedsOlder reports whether the view has reached the oldest loaded line
// and should pull the next page.
//
// The check fires slightly before the very top (prefetch) so the next 500
// are usually already there by the time you get there — scrolling back
// stays continuous instead of stalling at each page boundary.
func (m *Model) logNeedsOlder() bool {
	const prefetch = 40
	return m.logMore && !m.logLoading &&
		m.logScroll+m.visibleRows()+prefetch >= m.logTotalRows()
}

// logStatusLine reports follow state, how much is loaded, and whether older
// entries are still available.
func (m *Model) logStatusLine(inner int) string {
	th := m.th()
	s := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Background(th.Bg).Foreground(c)
	}

	var left string
	if m.logFollow {
		left = s(th.Ok).Bold(true).Render(" ● following") + s(th.Subtle).Render("  newest at bottom")
	} else {
		left = s(th.Warn).Bold(true).Render(" ⏸ paused") +
			s(th.Subtle).Render(fmt.Sprintf("  %d row(s) below · end resumes", m.logScroll))
	}
	// The three log controls are only discoverable if they are written down,
	// and only when there is room to write them down.
	if inner >= 96 {
		left += s(th.Subtle).Render("  ·  f filter · w level · t raw")
	}

	right := fmt.Sprintf("%d loaded", len(m.textLines))
	if len(m.logShown) != len(m.textLines) {
		right = fmt.Sprintf("%d/%d shown", len(m.logShown), len(m.textLines))
	}
	if m.logMin != lvlNone {
		right += " · ≥" + m.logMin.String()
	}
	if m.logRaw {
		right += " · raw"
	}
	switch {
	case m.logLoading:
		right = "loading older…"
	case m.logMore:
		right += " · ↑ for older"
	default:
		right += " · start of log"
	}

	gap := inner - lipgloss.Width(left) - len(right) - 1
	if gap < 1 {
		gap = 1
	}
	return left + s(th.Bg).Render(spaces(gap)) + s(th.Subtle).Render(right) + s(th.Bg).Render(" ")
}

// logSearchBox is the log viewer's filter box. It mirrors the table's own
// search box — same key, same place, same look — because "f then type" is
// the one search gesture in k10s.
func (m *Model) logSearchBox(inner int) string {
	th := m.th()
	focused := m.focus == focusMainSearch
	qCol, curCol := th.Subtle, th.Border
	if focused {
		qCol, curCol = th.Fg, th.Accent
	}
	cnt := fmt.Sprintf("%d/%d", len(m.logShown), len(m.textLines))
	row := paint(th.Bg, th.Accent, false, " / ") + paint(th.Bg, qCol, false, trunc(m.logFilter, inner-6-len(cnt)))
	if focused {
		row += paint(th.Bg, curCol, false, "█")
	}
	if m.logFilter == "" {
		row += paint(th.Bg, th.Subtle, false, trunc(" terms match, -term excludes", inner-6-len(cnt)))
	}
	gap := inner - lipgloss.Width(row) - len(cnt) - 1
	if gap < 1 {
		gap = 1
	}
	return m.mark("logsearch", padBG(row+paint(th.Bg, th.Bg, false, spaces(gap))+paint(th.Bg, th.Subtle, false, cnt), inner, th.Bg))
}
