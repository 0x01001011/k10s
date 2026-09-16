package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/mock"
	"github.com/0x01001011/k10s/internal/theme"
)

// openLogs drives the real log-open path and resolves its async command.
func openLogs(t *testing.T, m *Model) {
	t.Helper()
	cmd := m.startLogs(m.curKind().Key, m.curNamespace(), m.curName())
	if cmd == nil {
		t.Fatal("startLogs returned no command")
	}
	drainCmd(m, cmd)
	if m.mode != modeLogs {
		t.Fatalf("expected the log viewer, mode = %v", m.mode)
	}
}

// ---- wrapping ------------------------------------------------------------

func TestWrapLineKeepsEverything(t *testing.T) {
	line := "2026-08-25T08:12:12.775Z ERROR upstream dial tcp 10.96.14.77:8080: i/o timeout attempt=1/3"
	segs := wrapLine(line, 40)

	if len(segs) < 2 {
		t.Fatalf("expected the line to wrap, got %d segment(s)", len(segs))
	}
	for i, s := range segs {
		if len(s) > 40 {
			t.Errorf("segment %d is %d cells, over the 40 limit: %q", i, len(s), s)
		}
	}
	// Nothing may be lost — that is the whole point of wrapping over
	// truncating.
	joined := strings.Join(segs, " ")
	for _, word := range strings.Fields(line) {
		if !strings.Contains(joined, word) {
			t.Errorf("wrapping dropped %q", word)
		}
	}
	if strings.Contains(joined, "…") {
		t.Error("wrapped output should never contain an ellipsis")
	}
}

func TestWrapLineShortLineUntouched(t *testing.T) {
	if got := wrapLine("short", 40); len(got) != 1 || got[0] != "short" {
		t.Errorf("wrapLine(short) = %v, want it unchanged", got)
	}
}

func TestWrapLineHandlesUnbreakableText(t *testing.T) {
	long := strings.Repeat("x", 100)
	segs := wrapLine(long, 20)
	if len(segs) < 5 {
		t.Fatalf("expected several segments, got %d", len(segs))
	}
	if strings.Join(segs, "") != long {
		t.Error("a word with no spaces must still be split without loss")
	}
}

// ---- level colouring -----------------------------------------------------

func TestLogLevelTokensRecognised(t *testing.T) {
	th := themeFor(t)
	for _, tok := range []string{"ERROR", "error", "WARN", "warning", "INFO", "DEBUG"} {
		if _, ok := logLevelStyle(th, tok); !ok {
			t.Errorf("%q should be recognised as a level", tok)
		}
	}
	for _, tok := range []string{"upstream", "http", "2026-08-25T08:12:01.442Z"} {
		if _, ok := logLevelStyle(th, tok); ok {
			t.Errorf("%q should not be treated as a level", tok)
		}
	}
}

func TestRenderLogLineColoursOnlyTheLevel(t *testing.T) {
	th := themeFor(t)
	out := renderLogLine(th, "2026-08-25T08:12:01.442Z ERROR upstream dial failed")

	// The message text survives intact...
	plain := stripANSI(out)
	if !strings.Contains(plain, "upstream dial failed") {
		t.Errorf("message text lost: %q", plain)
	}

	// ...and the level is styled differently from a non-level word, which is
	// the actual requirement. Comparing two renders avoids asserting on
	// whichever escape encoding lipgloss picks.
	warn := renderLogLine(th, "2026-08-25T08:12:01.442Z WARN  upstream dial failed")
	if out == warn {
		t.Error("ERROR and WARN should not render identically")
	}
	plainWord := renderLogLine(th, "2026-08-25T08:12:01.442Z plain upstream dial failed")
	if styledPrefix(out) == styledPrefix(plainWord) {
		t.Error("a level token should be styled differently from an ordinary word")
	}
}

// ---- bottom-relative numbering & follow ----------------------------------

func TestLogNumbersCountFromBottom(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	body := strings.Join(m.logBody(120, 12), "\n")
	plain := stripANSI(body)

	// The newest line sits last and carries number 1.
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	last := lines[len(lines)-2] // -1 is the status line
	if !strings.Contains(last, " 1 ") {
		t.Errorf("bottom log row should be numbered 1, got %q", last)
	}
}

func TestLogOpensFollowingAtBottom(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	if !m.logFollow {
		t.Error("the log view should open following")
	}
	if m.logScroll != 0 {
		t.Errorf("logScroll = %d, want 0 (pinned to newest)", m.logScroll)
	}
}

func TestScrollUpPausesAndBottomResumes(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	m.move(-5) // up the screen
	if m.logFollow {
		t.Error("scrolling up should pause following")
	}
	if m.logScroll == 0 {
		t.Error("scrolling up should move away from the bottom")
	}

	m.handleKey(key("G"))
	if !m.logFollow {
		t.Error("End should resume following")
	}
	if m.logScroll != 0 {
		t.Errorf("logScroll = %d, want 0 after End", m.logScroll)
	}
}

func TestPausedViewHoldsStillAsLinesArrive(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	m.move(-5)
	before := m.logScroll
	m.Update(logLineMsg{gen: m.logGen, line: "new entry", ok: true})

	if m.logScroll != before+1 {
		t.Errorf("logScroll = %d, want %d — a new line must not shift a paused view",
			m.logScroll, before+1)
	}
	if m.logFollow {
		t.Error("a new line should not silently resume following")
	}
}

// A wrapped line takes several display rows, and the paused offset counts
// display rows — so it has to grow by all of them or the view creeps.
func TestPausedViewHoldsStillForWrappedLines(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	m.move(-5)
	before := m.logScroll

	long := strings.Repeat("wrapped line content ", 40)
	want := len(wrapLine(long, m.logCurrentWidth()))
	if want < 2 {
		t.Fatalf("test line only occupies %d row(s) — it must wrap", want)
	}
	m.Update(logLineMsg{gen: m.logGen, line: long, ok: true})

	if m.logScroll != before+want {
		t.Errorf("logScroll = %d, want %d — a wrapped line must push by every row it takes",
			m.logScroll, before+want)
	}
}

func TestFollowingAppendsAtBottom(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	m.Update(logLineMsg{gen: m.logGen, line: "brand new", ok: true})
	if m.logScroll != 0 {
		t.Errorf("logScroll = %d, want 0 while following", m.logScroll)
	}
	if m.textLines[len(m.textLines)-1] != "brand new" {
		t.Error("a new line should land at the bottom")
	}
}

// ---- infinite scroll-back ------------------------------------------------

func TestScrollingUpLoadsOlderLines(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	if !m.logMore {
		t.Skip("demo log is not deep enough to page")
	}
	before := len(m.textLines)

	// Scroll near the top of what is loaded, then let the fetch run.
	m.logScroll = len(m.textLines) - 10
	cmd := m.maybeLoadOlder()
	if cmd == nil {
		t.Fatal("reaching the top should request older lines")
	}
	drainCmd(m, cmd)

	if len(m.textLines) <= before {
		t.Errorf("older lines were not prepended: %d -> %d", before, len(m.textLines))
	}
}

func TestOlderLinesArePrependedNotAppended(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)
	if !m.logMore {
		t.Skip("demo log is not deep enough to page")
	}

	newest := m.textLines[len(m.textLines)-1]

	m.logScroll = len(m.textLines) - 10
	drainCmd(m, m.maybeLoadOlder())

	if got := m.textLines[len(m.textLines)-1]; got != newest {
		t.Errorf("newest line changed to %q — older entries must go on top", got)
	}
}

func TestPagingStopsAtStartOfLog(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	// Page until the backend says there is nothing older.
	for i := 0; i < 10 && m.logMore; i++ {
		m.logScroll = len(m.textLines) - 10
		cmd := m.maybeLoadOlder()
		if cmd == nil {
			break
		}
		drainCmd(m, cmd)
	}
	if m.logMore {
		t.Skip("backend still reports more history after 10 pages")
	}

	// With nothing older left, no further request is made.
	m.logScroll = len(m.textLines) - 1
	if cmd := m.maybeLoadOlder(); cmd != nil {
		t.Error("no more requests should be made once the log start is reached")
	}
	if !strings.Contains(stripANSI(m.logStatusLine(120)), "start of log") {
		t.Error("the status line should say the start of the log has been reached")
	}
}

func TestLogStatusLineShowsFollowState(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	if got := stripANSI(m.logStatusLine(120)); !strings.Contains(got, "following") {
		t.Errorf("status = %q, want it to say following", got)
	}
	m.move(-3)
	if got := stripANSI(m.logStatusLine(120)); !strings.Contains(got, "paused") {
		t.Errorf("status = %q, want it to say paused", got)
	}
}

// ---- describe fallback ---------------------------------------------------

func TestLogsOnKindWithoutLogsFallsBackToDescribe(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)

	for i, k := range m.kinds() {
		if k.Key == "secrets" {
			m.selectResource(i)
		}
	}
	if m.curKind().Can(domain.ALogs) {
		t.Skip("secrets unexpectedly advertise logs")
	}

	drainCmd(m, m.startLogs("secrets", m.curNamespace(), m.curName()))

	if strings.Contains(m.toast, "✗") {
		t.Errorf("toast = %q — a kind without logs is not an error", m.toast)
	}
	if !strings.Contains(m.textTitle, "describe") {
		t.Errorf("title = %q, want it to have fallen back to describe", m.textTitle)
	}
}

func TestNoLogsIsNotReportedAsAnError(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)

	m.Update(logStartMsg{kind: "secrets", ns: "default", name: "db-credentials", err: domain.ErrNoLogs})
	if strings.Contains(m.toast, "logs are only available") {
		t.Errorf("toast = %q — that message should be gone", m.toast)
	}
	if strings.HasPrefix(m.toast, "✗") {
		t.Errorf("toast = %q, want an informational message not an error", m.toast)
	}
}

// ---- namespace chooser returns to where you were -------------------------

func TestNamespaceChooserReturnsToPreviousKind(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)

	for i, k := range m.kinds() {
		if k.Key == "services" {
			m.selectResource(i)
		}
	}

	m.showNamespaceChooser()
	if m.curKind().Key != "namespaces" {
		t.Fatal("chooser did not open")
	}
	m.rowIdx = 0 // "all"
	m.openSelected()

	if got := m.curKind().Key; got != "services" {
		t.Errorf("returned to %q, want services — the view you came from", got)
	}
}

func TestNamespaceChooserFromPodsStillReturnsToPods(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)

	m.showNamespaceChooser()
	m.rowIdx = 1
	m.openSelected()

	if got := m.curKind().Key; got != "pods" {
		t.Errorf("returned to %q, want pods", got)
	}
}

// ---- "all" is row 0 ------------------------------------------------------

func TestAllNamespaceRowIsNumberedZero(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	m.showNamespaceChooser()

	nums := gutterNumbers(m, 80, 12)
	if len(nums) < 2 {
		t.Fatalf("expected several namespace rows, got %v", nums)
	}
	if nums[0] != "0" {
		t.Errorf("the all row should be numbered 0, got %q", nums[0])
	}
	if nums[1] != "1" {
		t.Errorf("the first real namespace should be 1, got %q", nums[1])
	}
}

func TestOtherKindsStillStartAtOne(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)

	// Check the gutter specifically: cell values elsewhere in the row can
	// legitimately be "0" (RESTARTS, for one).
	nums := gutterNumbers(m, 100, 8)
	if len(nums) == 0 {
		t.Fatal("no rows rendered")
	}
	if nums[0] != "1" {
		t.Errorf("first gutter number = %q, want 1", nums[0])
	}
	for _, n := range nums {
		if n == "0" {
			t.Error("only the namespaces table has a row 0")
		}
	}
}

// styledPrefix returns the escape sequence introducing the third field, so
// two renders can be compared on styling rather than on text.
func styledPrefix(s string) string {
	i := strings.Index(s, "upstream")
	if i < 0 {
		return s
	}
	return s[:i]
}

// gutterNumbers returns the row number printed in each table row's gutter.
func gutterNumbers(m *Model, w, rows int) []string {
	var out []string
	// The first two lines are the header and its rule.
	for _, ln := range m.tableBody(w, rows)[2:] {
		f := strings.Fields(stripANSI(ln))
		if len(f) == 0 {
			continue
		}
		// A selected row leads with the ▌ marker.
		if f[0] == "▌" {
			f = f[1:]
		}
		if len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out
}

// helpers

func themeFor(t *testing.T) theme.Theme {
	t.Helper()
	m := newTestModel(t, mock.New(""))
	return m.th()
}

// stripANSI removes CSI sequences: both SGR colour codes (…m) and
// our own zone markers (…z). Scanning only for 'm' would run past a
// zone marker and swallow real text up to the next literal "m".
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7E) {
				i++ // parameter bytes
			}
			i++ // the final byte
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// ---- busy indicator ------------------------------------------------------

func TestActionShowsLoadingWhileInFlight(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)

	cmd := m.fireAction(Actions[0]) // describe
	if !m.busy {
		t.Fatal("firing an action should mark the panel busy")
	}
	view := stripANSI(m.viewMain(84, 14).String())
	if !strings.Contains(view, "describe") {
		t.Errorf("the busy panel should name the action, got:\n%s", view)
	}

	drainCmd(m, cmd)
	if m.busy {
		t.Error("the result should clear the busy state")
	}
}

func TestEnterAndDoubleClickShowLoading(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)

	// Use a kind whose enter falls back to describe — logs deliberately skip
	// the spinner (see TestLogsOpenWithoutALoadingFlash).
	for i, k := range m.kinds() {
		if k.Key == "configmaps" {
			m.selectResource(i)
		}
	}

	m.handleKey(key("enter"))
	if !m.busy {
		t.Error("enter should show a loading state while the fetch runs")
	}

	m.busy = false
	m.View() // register zones
	l := m.layout()
	x, y := l.leftW+5, l.midY+3
	m.handleMouse(clickAt(x, y))
	m.handleMouse(clickAt(x, y)) // double click
	if !m.busy {
		t.Error("a double click should show a loading state too")
	}
}

// Logs open already scrolled to the newest line, so flashing a spinner
// first just made entering them look like the view jumped.
func TestLogsOpenWithoutALoadingFlash(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)

	cmd := m.startLogs("pods", "default", "web-1")
	if m.busy {
		t.Error("opening logs should not show the busy spinner")
	}
	drainCmd(m, cmd)

	if m.mode != modeLogs {
		t.Fatalf("mode = %v, want the log viewer", m.mode)
	}
	if m.logScroll != 0 || !m.logFollow {
		t.Error("logs should open pinned to the newest line, already at the bottom")
	}
}

// Each scroll back past the top pulls exactly one more page.
func TestEachPageBackLoadsAnotherChunk(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	if len(m.textLines) != logInitial {
		t.Fatalf("opened with %d lines, want the newest %d", len(m.textLines), logInitial)
	}
	if !m.logMore {
		t.Skip("demo log is not deep enough to page")
	}

	m.logScroll = len(m.textLines) - 5
	drainCmd(m, m.maybeLoadOlder())
	if got := len(m.textLines); got != logInitial+logChunk {
		t.Errorf("after one page back: %d lines, want %d", got, logInitial+logChunk)
	}
}

func TestToastOnlyActionsAlsoShowLoading(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)

	// Port-forward's only visible result is a toast, which is exactly the
	// case where silence looked like nothing had happened.
	for _, a := range Actions {
		if a.ID == domain.APortFwd {
			m.fireAction(a)
		}
	}
	if !m.busy {
		t.Error("an action whose result is only a toast should still show loading")
	}
}

// ---- filtering -----------------------------------------------------------

// press sends one key through the real key handler and resolves whatever it
// returns, so a test exercises the same path a keystroke does.
func press(m *Model, k string) {
	drainCmd(m, func() tea.Cmd { _, cmd := m.Update(key(k)); return cmd }())
}

func TestLogFilterNarrowsToMatchingLines(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	total := len(m.logShown)
	if total != len(m.textLines) {
		t.Fatalf("an unfiltered log shows %d of %d lines", total, len(m.textLines))
	}

	press(m, "f")
	if m.focus != focusMainSearch {
		t.Fatal("f should open the filter box in the log viewer")
	}
	for _, r := range "healthz" {
		press(m, string(r))
	}
	if m.logFilter != "healthz" {
		t.Fatalf("filter = %q, want it to collect what was typed", m.logFilter)
	}
	if len(m.logShown) == 0 || len(m.logShown) >= total {
		t.Fatalf("%d line(s) shown of %d, want a strict subset", len(m.logShown), total)
	}
	for _, ln := range m.logShown {
		if !strings.Contains(strings.ToLower(ln), "healthz") {
			t.Errorf("filtered view still shows %q", ln)
		}
	}
	// Nothing is thrown away: the raw lines are all still loaded.
	if len(m.textLines) != total {
		t.Errorf("filtering dropped raw lines: %d, want %d", len(m.textLines), total)
	}

	press(m, "esc")
	if m.logFilter != "" || len(m.logShown) != total {
		t.Errorf("esc should clear the filter, got %q with %d shown", m.logFilter, len(m.logShown))
	}
}

func TestLogLevelFloorCycles(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	all := len(m.logShown)
	for _, want := range []logLevel{lvlInfo, lvlWarn, lvlErr, lvlNone} {
		press(m, "w")
		if m.logMin != want {
			t.Fatalf("level floor = %v, want %v", m.logMin, want)
		}
	}
	if len(m.logShown) != all {
		t.Errorf("a full cycle should end back at %d lines, got %d", all, len(m.logShown))
	}

	press(m, "w") // INFO and above
	press(m, "w") // WARN and above
	for _, ln := range m.logShown {
		if strings.Contains(ln, "INFO") {
			t.Errorf("WARN floor still shows %q", ln)
		}
	}
	if len(m.logShown) == 0 {
		t.Error("the demo log has warnings; the WARN floor should keep them")
	}
}

func TestLogRawToggleRestoresTheOriginalLine(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	parsed := strings.Join(m.logShown, "\n")
	if !strings.Contains(parsed, logSep) {
		t.Fatal("the demo log has JSON records; expected at least one parsed line")
	}

	press(m, "t")
	if !m.logRaw {
		t.Fatal("t should switch to raw lines")
	}
	if got := strings.Join(m.logShown, "\n"); got != strings.Join(m.textLines, "\n") {
		t.Error("raw mode should show the lines exactly as received")
	}

	press(m, "t")
	if m.logRaw || strings.Join(m.logShown, "\n") != parsed {
		t.Error("t again should go back to parsed lines")
	}
}

// A hidden line must not move a paused view: the offset counts rows on
// screen, and a filtered-out line never reaches the screen.
func TestFilteredStreamLineDoesNotScrollAPausedView(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	m.setLogFilter("healthz")
	m.logScrollBy(3)
	if m.logFollow {
		t.Fatal("scrolling up should pause following")
	}
	at := m.logScroll

	m.Update(logLineMsg{gen: m.logGen, ok: true, line: "2026-08-25T08:13:00.000Z INFO worker flushed batch size=1"})
	if m.logScroll != at {
		t.Errorf("a hidden line moved the view: %d, want %d", m.logScroll, at)
	}
	m.Update(logLineMsg{gen: m.logGen, ok: true, line: "2026-08-25T08:13:01.000Z INFO http GET /healthz 200 0.3ms"})
	if m.logScroll <= at {
		t.Errorf("a visible line should push a paused view: %d, want > %d", m.logScroll, at)
	}
}
