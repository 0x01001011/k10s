package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/plugin"
	"github.com/0x01001011/k10s/internal/theme"
)

func (m *Model) View() string {
	// One kind list per frame; see Model.kindsMemo.
	m.kindsMemo = nil
	if m.w < 10 || m.h < 8 {
		return ""
	}
	th := m.th()
	if m.w < 72 || m.h < 22 {
		msg := fmt.Sprintf("k10s needs a terminal ≥ 72x22 (currently %dx%d)", m.w, m.h)
		b := NewBlock(m.w, m.h, th.Bg)
		return scanZones(b.Overlay(BlockOf(len(msg), 1, []string{
			lipgloss.NewStyle().Background(th.Bg).Foreground(th.Warn).Render(msg),
		}, th.Bg), (m.w-len(msg))/2, m.h/2).String())
	}

	l := m.layout()

	header := m.viewHeader(l)
	var mid Block
	if m.zoomed {
		mid = m.viewMain(l.mainW, l.midH)
	} else {
		mid = HJoin(
			m.viewList(l.leftW, l.midH),
			m.viewMain(l.mainW, l.midH),
			m.viewActions(l.rightW, l.midH),
		)
	}
	root := VJoin(header, mid, m.viewPrompt(l), m.viewStatus())

	if sug := m.suggestions(); len(sug) > 0 && !m.modalOpen() {
		root = m.overlaySuggestions(root, l, sug)
	}
	if m.themeOpen {
		root = m.overlayThemePicker(root)
	}
	if m.setOpen {
		root = m.overlaySettings(root)
	}
	if m.palOpen {
		root = m.overlayPalette(root)
	}
	if m.confirm != nil {
		root = m.overlayConfirm(root)
	}
	return scanZones(root.String())
}

// ---- header (borderless): identity + cluster totals -----------------------

func gauge(th theme.Theme, pct, width int) string {
	col := th.Ok
	switch {
	case pct >= 85:
		col = th.Err
	case pct >= 60:
		col = th.Warn
	}
	filled := pct * width / 100
	if filled > width {
		filled = width
	}
	on := paint(th.Bg, col, false, strings.Repeat("▰", filled))
	off := paint(th.Bg, th.Border, false, strings.Repeat("▱", width-filled))
	num := paint(th.Bg, col, false, fmt.Sprintf("%3d%%", pct))
	return on + off + " " + num
}

func (m *Model) viewHeader(l layout) Block {
	th := m.th()
	inner := m.w - 2

	nodes := m.src.Nodes()
	ci := m.src.ClusterInfo()

	ready := 0
	cpuSum, memSum := 0, 0
	var usedMilli, allocMilli, usedBytes, allocBytes int64
	for _, n := range nodes {
		if n.Status == "Ready" {
			ready++
		}
		cpuSum += n.CPU
		memSum += n.Mem
		usedMilli += n.CPUMilli
		allocMilli += n.CPUAllocMilli
		usedBytes += n.MemBytes
		allocBytes += n.MemAllocBytes
	}
	nn := maxi(1, len(nodes))
	cpuPct, memPct := cpuSum/nn, memSum/nn
	// Totals come from each node's own allocatable, so mixed-size clusters
	// add up to what `kubectl top node` shows rather than a per-node guess.
	usedCores, totalCores := float64(usedMilli)/1000, float64(allocMilli)/1000
	usedGiB, totalGiB := float64(usedBytes)/(1<<30), float64(allocBytes)/(1<<30)

	brand := paint(th.Bg, th.Accent, true, " ⎈ k10s")
	sep := paint(th.Bg, th.Border, false, "  │  ")
	nodeCol := th.Ok
	if ready < nn {
		nodeCol = th.Warn
	}
	// "0/0 ready" would be a claim about a cluster we have not counted.
	// With no nodes in hand — still connecting, or nothing to connect to —
	// the header says nothing instead.
	nodeTxt := fmt.Sprintf("%d/%d ready", ready, len(nodes))
	if len(nodes) == 0 {
		nodeTxt, nodeCol = "—", th.Subtle
	}
	ctxTxt := ci.Context
	if ctxTxt == "" {
		ctxTxt = "no context"
	}
	ctxCol := th.Accent2
	// The demo says so on every frame, not once in a toast that scrolls
	// away. Everything else in this header — the version, the node count,
	// the gauges below — is sample data while this is showing.
	demoTag := ""
	if m.demoMode() {
		ctxCol = th.Warn
		demoTag = paint(th.Bg, th.Warn, true, " DEMO") +
			paint(th.Bg, th.Subtle, false, " sample data · :ctx to leave")
	}
	line0 := brand + sep + paint(th.Bg, ctxCol, false, ctxTxt) + demoTag +
		sep + paint(th.Bg, th.Subtle, false, "ver ") + paint(th.Bg, th.Fg, false, ci.Version) +
		sep + paint(th.Bg, th.Subtle, false, "nodes ") + paint(th.Bg, nodeCol, false, nodeTxt)

	// Right-hand buttons: namespace, then theme. Both are clickable and
	// both say what they currently are, so the header doubles as status.
	nsPlain := "ns " + m.namespace + " ▾"
	nsBtn := m.mark("nsbtn", paint(th.Bg, th.Subtle, false, "ns ")+paint(th.Bg, th.Accent2, false, m.namespace)+paint(th.Bg, th.Subtle, false, " ▾"))

	themePlain := "theme " + m.th().Name + " ⟳"
	themeTag := m.mark("theme", paint(th.Bg, th.Subtle, false, "theme ")+paint(th.Bg, th.Accent, false, m.th().Name)+paint(th.Bg, th.Subtle, false, " ⟳"))

	right := nsBtn + paint(th.Bg, th.Border, false, "  │  ") + themeTag
	rightPlain := nsPlain + "  │  " + themePlain

	gapw := inner - lipgloss.Width(line0) - lipgloss.Width(rightPlain)
	if gapw < 1 {
		gapw = 1
	}
	line0 += paint(th.Bg, th.Bg, false, spaces(gapw)) + right

	// Both gauges carry a direction arrow after the percentage. Only real
	// readings are tracked: with no nodes the totals are placeholders, and
	// the first reading after a reconnect must not register as a jump.
	if len(nodes) > 0 {
		m.cpuTrend.observe(cpuPct, m.anim)
		m.memTrend.observe(memPct, m.anim)
	}
	totals := paint(th.Bg, th.Subtle, true, " CPU  ") + gauge(th, cpuPct, 16) +
		paint(th.Bg, th.Bg, false, " ") + trendGlyph(th, th.Bg, m.cpuTrend.arrow(m.anim)) +
		paint(th.Bg, th.Subtle, false, fmt.Sprintf("  %.1f/%.0f cores", usedCores, totalCores)) +
		paint(th.Bg, th.Bg, false, "    ") +
		paint(th.Bg, th.Subtle, true, "MEM  ") + gauge(th, memPct, 16) +
		paint(th.Bg, th.Bg, false, " ") + trendGlyph(th, th.Bg, m.memTrend.arrow(m.anim)) +
		paint(th.Bg, th.Subtle, false, fmt.Sprintf("  %.1f/%.1f GiB", usedGiB, totalGiB))
	// nn is clamped to 1 so the averages above cannot divide by zero, which
	// with no nodes at all would print "0.0/16 cores" — a capacity figure for
	// a cluster that isn't there. Say nothing instead.
	if len(nodes) == 0 {
		totals = paint(th.Bg, th.Subtle, true, " CPU  ") + paint(th.Bg, th.Subtle, false, "—") +
			paint(th.Bg, th.Bg, false, "    ") +
			paint(th.Bg, th.Subtle, true, "MEM  ") + paint(th.Bg, th.Subtle, false, "—")
	}
	// paint(th.Bg, th.Bg, false, "    ") +
	// paint(th.Bg, th.Subtle, false, "per-node view → Resources ▸ Nodes")

	lines := []string{
		line0,
		"",
		totals,
		paint(th.Bg, th.Border, false, spaces(1)+strings.Repeat("╌", maxi(1, inner))),
	}
	return BlockOf(m.w, l.headerH, lines, th.Bg)
}

// ---- left: resource list + search box --------------------------------------

func (m *Model) viewList(w, h int) Block {
	th := m.th()
	if w == 0 {
		return Block{W: 0, H: h, Lines: make([]string, h)}
	}
	inner := w - 2
	focused := m.focus == focusList
	f := m.filtered()
	ks := m.kinds()

	groups := m.groupOrder()
	groupIdx := func(name string) int {
		for gi, g := range groups {
			if g == name {
				return gi
			}
		}
		return 0
	}

	// One skeleton, shared with the scrolling code (listEntries), so what is
	// drawn and what the scroll offset counts are the same lines.
	entries := m.listEntries()
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		switch {
		case e.kind < 0 && e.group == "":
			lines = append(lines, "")
		case e.group != "":
			lines = append(lines, m.mark(fmt.Sprintf("grp:%d", groupIdx(e.group)),
				padBG(m.groupHeader(e.group, e.folded, inner), inner, th.Bg)))
		default:
			lines = append(lines, m.kindRow(ks[e.kind], e.kind, inner))
		}
	}
	if len(f) == 0 {
		lines = append(lines, paint(th.Bg, th.Subtle, false, " no match"))
	}

	// No search box here: the list is type-to-filter, so a permanent box was
	// two wasted rows. The active filter shows in the panel title instead.
	//
	// The window comes from the pane's own scroll offset: the wheel moves it
	// freely, and the selection only drags it along far enough to stay
	// visible (syncListScroll). Re-centring on the selection every frame
	// would fight the wheel for control of the pane.
	avail := h - 2
	total := len(lines)
	top := m.listTop(total)
	if total > avail {
		lines = lines[top:clamp(top+avail, top, total)]
	} else {
		top = 0
	}
	for len(lines) < avail {
		lines = append(lines, "")
	}

	title := "Resources"
	if m.search != "" {
		title += " · " + m.search
	}
	tag, tagPlain := "", ""
	// A pane that scrolls has to say so, or the wheel is a feature nobody
	// finds. The arrows show which way there is more.
	more := ""
	if top > 0 {
		more += "↑"
	}
	if top+avail < total {
		more += "↓"
	}
	if focused || m.search != "" {
		tagPlain = fmt.Sprintf("%d/%d", len(f), len(ks))
	}
	if more != "" {
		if tagPlain != "" {
			tagPlain += " "
		}
		tagPlain += more
	}
	if tagPlain != "" {
		tag = paint(th.Bg, th.Subtle, false, tagPlain)
	}

	return Panel(th, PanelOpts{Title: title, Tag: tag, TagPlain: tagPlain, Focused: focused, W: w, H: h}, lines)
}

// kindRow renders one kind in the Resources pane: its name, the badge count
// when the backend knows it, and the selection highlight.
func (m *Model) kindRow(r domain.Kind, idx, inner int) string {
	th := m.th()

	// A lazily-watching backend only knows counts for kinds already opened;
	// show nothing rather than a misleading 0.
	count := ""
	if n := m.src.RowCount(r.Key, m.namespace); n != domain.CountUnknown {
		count = strconv.Itoa(n)
	}
	label := trunc(r.Name, inner-4-len(count))
	gap := inner - 3 - lipgloss.Width(label) - len(count)
	if gap < 1 {
		gap = 1
	}

	var row string
	if idx == m.resIdx {
		row = paint(th.SelBg, th.Accent, false, " ▸ ") +
			paint(th.SelBg, th.SelFg, true, label) +
			paint(th.SelBg, "", false, spaces(gap)) +
			paint(th.SelBg, th.Accent2, false, count)
	} else {
		row = paint(th.Bg, th.Bg, false, "   ") + paint(th.Bg, th.Fg, false, label) +
			paint(th.Bg, th.Bg, false, spaces(gap)) + paint(th.Bg, th.Subtle, false, count)
	}
	return m.mark(fmt.Sprintf("res:%d", idx), padBG(row, inner, colorOf(idx == m.resIdx, th.SelBg, th.Bg)))
}

// groupHeader renders one Resources-pane group line: a chevron saying which
// way it folds, and — when it is folded — how many kinds are hidden inside,
// plus the selection marker if the kind you are looking at is one of them.
// Without that marker a folded group would silently swallow "where am I".
func (m *Model) groupHeader(group string, folded bool, inner int) string {
	th := m.th()

	holdsCursor := false
	for _, i := range m.groupKinds(group) {
		if i == m.resIdx {
			holdsCursor = true
			break
		}
	}

	chevron, label := "▾", th.Subtle
	if folded {
		chevron = "▸"
		if holdsCursor {
			label = th.Accent2
		}
	}
	chevCol := th.Border
	if folded && holdsCursor {
		chevCol = th.Accent
	}

	row := paint(th.Bg, chevCol, false, " "+chevron+" ") + paint(th.Bg, label, true, strings.ToUpper(group))
	if !folded {
		return row
	}
	tag := strconv.Itoa(len(m.groupKinds(group)))
	gap := inner - lipgloss.Width(row) - len(tag) - 1
	if gap < 1 {
		gap = 1
	}
	// Subtle, not Border: this is a count you are meant to read, and the
	// border colour is for the lines around the panel.
	return row + paint(th.Bg, th.Bg, false, spaces(gap)) + paint(th.Bg, th.Subtle, false, tag)
}

func colorOf(cond bool, a, b lipgloss.Color) lipgloss.Color {
	if cond {
		return a
	}
	return b
}

// ---- center: table / text -------------------------------------------------

// fitCols sizes the visible columns for avail cells. Columns are dropped from
// the right (never the first one) before the name column gets crushed.
//
// extra[ci] is the number of cells a column spends on decoration inside its
// own width — a trend arrow, a severity glyph. It is reserved here, once, from
// every row's worth of the column, so the column does not jitter by two cells
// as a decoration comes and goes between frames.
func fitCols(cols []string, rows [][]string, extra []int, avail, gap int) ([]int, []int) {
	keep := make([]int, len(cols))
	for i := range keep {
		keep[i] = i
	}
	for {
		w, ok := tryFit(cols, rows, extra, keep, avail, gap)
		if ok || len(keep) <= 2 {
			return w, keep
		}
		keep = keep[:len(keep)-1]
	}
}

func tryFit(cols []string, rows [][]string, extra, keep []int, avail, gap int) ([]int, bool) {
	n := len(keep)
	nat := make([]int, n)
	min := make([]int, n)
	for k, ci := range keep {
		nat[k] = 0
		for _, r := range rows {
			if ci < len(r) && len(r[ci]) > nat[k] {
				nat[k] = len(r[ci])
			}
		}
		// Decoration widens the values, never the header: a header already
		// wider than "value + glyph" needs no further room.
		if ci < len(extra) {
			nat[k] += extra[ci]
		}
		if h := len(cols[ci]); h > nat[k] {
			nat[k] = h
		}
		m := 7
		if ci == 0 {
			m = 18
		}
		if cols[ci] == "NAMESPACE" {
			m = 9 // short values (kube-system, cert-manager…); leave room for NAME
		}
		if m > nat[k] {
			m = nat[k]
		}
		min[k] = m
	}
	total := gap * (n - 1)
	for _, x := range nat {
		total += x
	}
	for total > avail {
		bi, bv := -1, 0
		for i := range nat {
			if nat[i] > min[i] && nat[i] > bv {
				bi, bv = i, nat[i]
			}
		}
		if bi < 0 {
			return nat, false
		}
		nat[bi]--
		total--
	}
	return nat, true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var statusColors = map[string]string{
	"Running": "ok", "Ready": "ok", "Active": "ok", "Bound": "ok", "True": "ok", "Normal": "ok",
	"Completed": "subtle", "False": "subtle", "<none>": "subtle", "-": "subtle",
	"Pending": "warn", "Terminating": "warn", "ContainerCreating": "warn", "Warning": "warn", "NotReady": "err",
	"CrashLoopBackOff": "err", "Error": "err", "ImagePullBackOff": "err", "Failed": "err", "Evicted": "err",
}

// cellLevel grades one cell, in the lens vocabulary. level, when non-empty, is
// a lens pack's declared severity and wins outright: a pack that says Degraded
// is an error has said so about that exact column, which beats every guess
// below it. "" means the cell carries no severity at all.
//
// This is the ONE grading decision in the table: colour and glyph both read it,
// so a cell can never be painted red without also being marked. "subtle" is a
// colour, not a severity — Completed and <none> are dimmed, never glyphed.
//
// The lens vocabulary is "error"; statusColors says "err". They are kept
// separate rather than merged, because one table maps VALUES and the other
// maps LEVELS — collapsing them would silently drop lens errors to default.
func cellLevel(level, v string) string {
	switch level {
	case "ok", "warn", "error", "unknown":
		return level
	}
	if strings.Contains(v, "SchedulingDisabled") {
		return "warn"
	}
	switch statusColors[v] {
	case "ok":
		return "ok"
	case "warn":
		return "warn"
	case "err":
		return "error"
	case "subtle":
		return "subtle"
	}
	// "1/3" is a ready ratio and deserves a warning. "80/TCP" is a port and
	// does not — both halves must be numbers before this means anything.
	if strings.Contains(v, "/") && len(v) <= 7 {
		parts := strings.SplitN(v, "/", 2)
		if len(parts) == 2 && allDigits(parts[0]) && allDigits(parts[1]) && parts[0] != parts[1] {
			return "warn"
		}
	}
	return ""
}

// severityGlyph is the leading mark for a graded cell. Severity is otherwise
// carried by colour alone, which is unreadable for a colour-blind operator and
// on a low-contrast terminal: the worst-first sort is still right, but "which
// of these is the failure" is not answerable without it.
//
// Plain ASCII, one cell wide each, on purpose — an emoji is width-2 in some
// terminals and width-1 in others, and the row is padded arithmetically, so a
// mis-measured glyph shifts every column to its right.
//
// The trailing space is part of the returned constant so the mark is ONE
// painted run per cell rather than two: at 40 rows × several graded columns a
// second run per cell is a few KB of escape sequences on every frame.
func severityGlyph(level string) string {
	switch level {
	case "ok":
		return "+ "
	case "warn":
		return "! "
	case "error":
		return "x "
	case "unknown":
		return "? "
	}
	return ""
}

func cellColor(th theme.Theme, level, v string, def lipgloss.Color) lipgloss.Color {
	switch cellLevel(level, v) {
	case "ok":
		return th.Ok
	case "warn":
		return th.Warn
	case "error":
		return th.Err
	case "unknown", "subtle":
		return th.Subtle
	}
	return def
}

func (m *Model) viewMain(w, h int) Block {
	th := m.th()
	focused := m.focus == focusMain || m.focus == focusMainSearch
	inner := w - 2

	zoomPlain := "[ zoom ]"
	zoomLbl := "zoom"
	if m.zoomed {
		zoomPlain, zoomLbl = "[ restore ]", "restore"
	}
	tagStyle := lipgloss.NewStyle().Background(th.Bg).Foreground(th.Accent2)
	brk := lipgloss.NewStyle().Background(th.Bg).Foreground(th.Border)
	zoomTag := m.mark("zoom", brk.Render("[ ")+tagStyle.Render(zoomLbl)+brk.Render(" ]"))

	// The first connection takes over the panel: k10s is on screen from the
	// first frame, so this is where "we are still reaching the cluster"
	// gets said — never a blank terminal before the program starts.
	if m.connecting {
		body := make([]string, 0, h-2)
		body = append(body, "")
		body = append(body, m.connectingLines(inner)...)
		return Panel(th, PanelOpts{
			Title: "Connecting", Tag: zoomTag, TagPlain: zoomPlain,
			Focused: focused, W: w, H: h,
		}, body)
	}

	// An action in flight takes over the panel: pressing a key must visibly
	// do something, even for actions whose only result is a toast.
	if m.busy {
		body := make([]string, 0, h-2)
		body = append(body, "")
		body = append(body, m.busyLines(inner)...)
		return Panel(th, PanelOpts{
			Title: m.busyLabel, Tag: zoomTag, TagPlain: zoomPlain,
			Focused: focused, W: w, H: h,
		}, body)
	}

	if m.mode == modeShell {
		detach := m.mark("close", brk.Render("[ ")+
			lipgloss.NewStyle().Background(th.Bg).Foreground(th.Err).Render("detach")+brk.Render(" ]"))
		return Panel(th, PanelOpts{
			Title: "shell · " + m.shellName,
			Tag:   detach + brk.Render(" ") + zoomTag, TagPlain: "[ detach ] " + zoomPlain,
			Focused: focused, W: w, H: h,
		}, m.shellBody(inner, h-2))
	}

	if m.mode == modeContexts {
		return Panel(th, PanelOpts{
			Title: "Kube context · enter reconnects",
			Tag:   zoomTag, TagPlain: zoomPlain, Focused: focused, W: w, H: h,
		}, m.contextBody(inner, h-2))
	}

	if m.mode == modeTree {
		closeTag := m.mark("close", brk.Render("[ ")+lipgloss.NewStyle().Background(th.Bg).Foreground(th.Err).Render("close")+brk.Render(" ]"))
		return Panel(th, PanelOpts{
			Title: m.treeTitle, Tag: closeTag + brk.Render(" ") + zoomTag,
			TagPlain: "[ close ] " + zoomPlain, Focused: focused, W: w, H: h,
		}, m.treeBody(inner, h-2))
	}

	if m.mode == modeText || m.mode == modeLogs {
		closeTag := m.mark("close", brk.Render("[ ")+lipgloss.NewStyle().Background(th.Bg).Foreground(th.Err).Render("close")+brk.Render(" ]"))
		body := m.textBody(inner, h-2)
		if m.mode == modeLogs {
			body = m.logBody(inner, h-2)
		}
		return Panel(th, PanelOpts{
			Title: m.textTitle, Tag: closeTag + brk.Render(" ") + zoomTag,
			TagPlain: "[ close ] " + zoomPlain, Focused: focused, W: w, H: h,
		}, body)
	}

	// No cluster: the panel says so and lists the way in. This comes after
	// the modes above on purpose — :ctx and /setup are exactly what you
	// reach for from here, so they must still be able to take the panel.
	if m.offline {
		return Panel(th, PanelOpts{
			Title: "No cluster", Tag: zoomTag, TagPlain: zoomPlain,
			BorderCol: th.Warn, Focused: focused, W: w, H: h,
		}, m.noClusterLines(inner))
	}

	nsLabel := m.namespace
	if nsLabel == domain.AllNamespaces {
		nsLabel = "all namespaces"
	}

	// The search box only takes space while it's actually in use. Reserving
	// two rows permanently cost two rows of data on every screen for a box
	// that is empty most of the time.
	searching := m.focus == focusMainSearch || m.rowSearch != ""

	bodyH := h - 2
	if searching {
		bodyH -= 2
	}
	if bodyH < 1 {
		bodyH = 1
	}
	body := m.tableBody(inner, bodyH)
	for len(body) < bodyH {
		body = append(body, "")
	}
	if searching {
		body = append(body, lipgloss.NewStyle().Background(th.Bg).Foreground(th.Border).Render(strings.Repeat("╌", inner)))
		body = append(body, m.tableSearchBox(inner))
	}

	// A cluster-scoped kind ignores the namespace entirely, so naming one in
	// its title only suggests a filter that isn't there — "ClusterRoles ·
	// default" reads as though switching namespace would change the rows.
	title := m.res().Name
	if m.res().Namespaced {
		title += " · " + nsLabel
	}
	if m.rowSearch != "" {
		title += " · find: " + m.rowSearch
	}

	// Advertise the find key next to zoom, since there is no visible search
	// box to hint at it any more.
	tag, tagPlain := zoomTag, zoomPlain
	if !searching {
		findHint := m.mark("tablesearch", brk.Render("[ ")+tagStyle.Render("f")+
			lipgloss.NewStyle().Background(th.Bg).Foreground(th.Subtle).Render(" to search")+brk.Render(" ]"))
		tag = findHint + brk.Render(" ") + zoomTag
		tagPlain = "[ f to search ] " + zoomPlain
	}

	return Panel(th, PanelOpts{
		Title: title,
		Tag:   tag, TagPlain: tagPlain, Focused: focused, W: w, H: h,
	}, body)
}

// tableSearchBox renders the main panel's own row-search box, mirroring the
// Resources pane's search box but scoped to the table currently on screen.
func (m *Model) tableSearchBox(inner int) string {
	th := m.th()
	focused := m.focus == focusMainSearch
	qCol, curCol := th.Subtle, th.Border
	if focused {
		qCol, curCol = th.Fg, th.Accent
	}
	total := m.tableTotal()
	cnt := ""
	if focused || m.rowSearch != "" {
		_, rows := m.tableData()
		cnt = fmt.Sprintf("%d/%d", len(rows), total)
	}
	row := paint(th.Bg, th.Accent, false, " / ") + paint(th.Bg, qCol, false, trunc(m.rowSearch, inner-6-len(cnt)))
	if focused {
		row += paint(th.Bg, curCol, false, "█")
	} else if m.rowSearch == "" {
		row += paint(th.Bg, th.Subtle, false, trunc("press f to search rows…", inner-5))
	}
	if cnt != "" {
		gap := inner - lipgloss.Width(row) - len(cnt) - 1
		if gap < 1 {
			gap = 1
		}
		row += paint(th.Bg, th.Bg, false, spaces(gap)) + paint(th.Bg, th.Subtle, false, cnt)
	}
	return m.mark("tablesearch", padBG(row, inner, th.Bg))
}

func (m *Model) textBody(inner, rows int) []string {
	th := m.th()
	out := make([]string, 0, rows)
	end := clamp(m.textTop+rows, 0, len(m.textLines))
	for i := m.textTop; i < end; i++ {
		ln := m.textLines[i]
		col := th.Fg
		switch {
		case strings.Contains(ln, "ERROR"), strings.Contains(ln, "Warning"):
			col = th.Err
		case strings.Contains(ln, "WARN"):
			col = th.Warn
		}
		if strings.HasPrefix(ln, "  ") && strings.Contains(ln, ":") {
			col = th.Subtle
		}
		out = append(out, lipgloss.NewStyle().Background(th.Bg).Foreground(col).Render(" "+trunc(ln, inner-1)))
	}
	if len(m.textLines) > rows {
		pctScrolled := (m.textTop + rows) * 100 / len(m.textLines)
		bar := lipgloss.NewStyle().Background(th.Bg).Foreground(th.Subtle).
			Render(fmt.Sprintf(" %d/%d  %d%%  ↑↓ scroll · esc close", end, len(m.textLines), pctScrolled))
		if len(out) == rows {
			out[rows-1] = bar
		}
	}
	return out
}

// treeBody paints the relationship tree.
//
// Unlike textBody, which colours a whole line by what it can guess from the
// text, every run here is painted from severity the WALK already resolved.
// That is the point of the mode existing: a line reading
// "po/web-0  x CrashLoopBackOff" should have the failure in red and the spine
// in grey, not the whole line in one colour chosen by a substring match.
func (m *Model) treeBody(inner, rows int) []string {
	th := m.th()
	out := make([]string, 0, rows)

	if m.treeNote != "" && rows > 2 {
		out = append(out,
			padBG(paint(th.Bg, m.treeNoteColor(), false, " "+trunc(m.treeNote, inner-1)), inner, th.Bg),
			padBG("", inner, th.Bg),
		)
		rows -= 2
	}

	if len(m.treeRows) <= 1 {
		for _, ln := range treeEmptyHelp {
			if len(out) >= rows {
				break
			}
			out = append(out, padBG(paint(th.Bg, th.Subtle, false, " "+trunc(ln, inner-1)), inner, th.Bg))
		}
		return out
	}

	end := clamp(m.treeTop+rows, 0, len(m.treeRows))
	for i := m.treeTop; i < end; i++ {
		out = append(out, m.treeLine(m.treeRows[i], i == m.treeIdx, inner))
	}
	return out
}

// treeNoteColor grades the header the same way the rows are graded, so the
// one line an operator reads first is the one that changes colour when
// something is wrong.
func (m *Model) treeNoteColor() lipgloss.Color {
	th := m.th()
	if strings.Contains(m.treeNote, "need attention") {
		return th.Warn
	}
	return th.Subtle
}

// treeLine paints one row: spine, identity, status, edge.
//
// The name carries the row's worst severity. Scanning names down the left
// edge is how you find the failure in a tree of forty objects; making that
// work means the colour has to be on the name, not only on the status cell
// several columns to its right.
func (m *Model) treeLine(r treeRow, cursor bool, inner int) string {
	th := m.th()
	bg, nameCol := th.Bg, th.Fg
	switch r.worst {
	case "error":
		nameCol = th.Err
	case "warn":
		nameCol = th.Warn
	case "unknown":
		nameCol = th.Subtle
	}
	if cursor {
		// The selected row takes the selection colours outright. A cursor
		// that kept per-run colouring would be unreadable against SelBg on
		// half the themes, and "which row am I on" beats "how bad is this
		// row" for the one row you are already looking at.
		bg, nameCol = th.SelBg, th.SelFg
	}

	var b strings.Builder
	b.WriteString(paint(bg, bg, false, " "))
	spine := th.Border
	if cursor {
		spine = th.SelFg
	}
	b.WriteString(paint(bg, spine, false, r.prefix))

	if r.ref.Name == "" {
		b.WriteString(paint(bg, th.Subtle, false, r.kind+"   (not loaded — enter opens this kind)"))
		return padBG(trunc(b.String(), inner), inner, bg)
	}

	b.WriteString(paint(bg, nameCol, cursor, r.kind+"/"+r.ref.Name))
	if r.ns != "" {
		b.WriteString(paint(bg, th.Subtle, false, "  "+r.ns))
	}
	for _, c := range r.cells {
		col := th.Fg
		if !cursor {
			col = cellColor(th, c.level, c.text, th.Fg)
		} else {
			col = th.SelFg
		}
		b.WriteString(paint(bg, th.Subtle, false, "   "))
		b.WriteString(paint(bg, col, false, c.text))
	}
	if r.ref.Rel != "" {
		b.WriteString(paint(bg, th.Subtle, false, "   via "+r.ref.Rel))
	}
	return padBG(trunc(b.String(), inner), inner, bg)
}

func (m *Model) tableBody(inner, rows int) []string {
	th := m.th()
	nameCol := "NAME"
	if m.res().Key == "events" {
		nameCol = "OBJECT"
	}
	const gap = 2
	cols, allRows := m.tableData()

	// Left gutter: selection marker + a dim 1-based row number, so rows can
	// be referred to ("the 12th one") and position is obvious while
	// scrolling. Sized to the widest number actually present.
	numW := len(strconv.Itoa(maxi(1, len(allRows))))
	numW = clamp(numW, 2, 5)
	gutter := numW + 3 // "▌" + space + digits + space

	// Usage columns get a trailing " ▲"/" ▼" (see trend.go); graded cells get
	// a leading "x "/"! ". Both are laid out inside the cell, so the column
	// reserves the two cells up front and never jitters as the decoration
	// comes and goes.
	nameIdx, nsIdx := -1, -1
	metric := make([]bool, len(cols))
	for ci, c := range cols {
		switch {
		case c == nameCol:
			nameIdx = ci
		case c == "NAMESPACE":
			nsIdx = ci
		case metricColumn(c):
			metric[ci] = true
		}
	}

	// Resolved ONCE per frame, not once per cell: the assertion is cheap but
	// the row loop runs width × height times.
	level := m.levelFor(m.res().Key)
	glyphFor := func(ci int, v string) string {
		lvl := ""
		if level != nil && ci < len(cols) {
			lvl = level(cols[ci], v)
		}
		return severityGlyph(cellLevel(lvl, v))
	}

	// One pass, no copy of the rows: a column reserves the glyph as soon as a
	// single row of it grades, and is then skipped for the rest of the scan.
	extra := make([]int, len(cols))
	for ci := range cols {
		if metric[ci] {
			extra[ci] = 2
		}
	}
	for _, row := range allRows {
		for ci := range row {
			if ci >= len(extra) || extra[ci] > 0 {
				continue
			}
			if glyphFor(ci, row[ci]) != "" {
				extra[ci] = 2
			}
		}
	}
	widths, keep := fitCols(cols, allRows, extra, inner-gutter, gap)
	arrowFor := func(row []string, ci int) int {
		if nameIdx < 0 || nameIdx >= len(row) || ci >= len(row) || !metric[ci] {
			return 0
		}
		v, ok := metricValue(row[ci])
		if !ok {
			return 0
		}
		ns := ""
		if nsIdx >= 0 && nsIdx < len(row) {
			ns = row[nsIdx]
		} else {
			ns = m.namespace
		}
		t := m.rowTrend(m.res().Key, ns, row[nameIdx], cols[ci])
		t.observe(v, m.anim)
		return t.arrow(m.anim)
	}

	var hdr strings.Builder
	hdr.WriteString(paint(th.Bg, th.Bg, false, spaces(gutter)))
	for k, ci := range keep {
		hdr.WriteString(paint(th.Bg, th.Subtle, true, pad(trunc(cols[ci], widths[k]), widths[k])))
		if k < len(keep)-1 {
			hdr.WriteString(paint(th.Bg, th.Bg, false, spaces(gap)))
		}
	}
	out := []string{hdr.String(), paint(th.Bg, th.Border, false, strings.Repeat("╌", inner))}

	// Every cell is padded or truncated to its column width, and the metric
	// columns spend their last two cells on the arrow, so a row's visible
	// width is known without walking it for escape sequences.
	rowW := gutter + gap*maxi(0, len(keep)-1)
	for k := range keep {
		rowW += widths[k]
	}

	visible := rows - 2
	if visible < 1 {
		visible = 1
	}
	m.rowScroll = clamp(m.rowScroll, 0, maxi(0, len(allRows)-visible))
	end := clamp(m.rowScroll+visible, 0, len(allRows))

	for i := m.rowScroll; i < end; i++ {
		row := allRows[i]
		sel := i == m.rowIdx
		bg := th.Bg
		base := th.Fg
		if sel {
			bg, base = th.SelBg, th.SelFg
		}
		var b strings.Builder
		// Gutter: marker, then the dim row number.
		if sel {
			b.WriteString(paint(bg, th.Accent, false, "▌"))
		} else {
			b.WriteString(paint(bg, bg, false, " "))
		}
		numCol := th.Border
		if sel {
			numCol = th.Accent2
		}
		b.WriteString(paint(bg, bg, false, " "))
		b.WriteString(paint(bg, numCol, false, fmt.Sprintf("%*d", numW, i+rowNumBase(m.res().Key))))
		b.WriteString(paint(bg, bg, false, " "))

		for k, ci := range keep {
			v := ""
			if ci < len(row) {
				v = row[ci]
			}
			lvl := ""
			if level != nil && ci < len(cols) {
				lvl = level(cols[ci], v)
			}
			lvl = cellLevel(lvl, v)
			col := cellColor(th, lvl, v, base)
			if ci < len(cols) && cols[ci] == "NAMESPACE" {
				col = th.Accent2
			}
			// The glyph is drawn in the two cells the column reserved for
			// it, so the value keeps its own width and the row still adds
			// up to rowW.
			w := widths[k]
			if ci < len(extra) && extra[ci] > 0 && !metric[ci] {
				// The whole column spends the two cells, glyph or not, so
				// an ungraded value still lines up under a graded one.
				g := severityGlyph(lvl)
				if g == "" {
					g = "  "
				}
				b.WriteString(paint(bg, col, false, g))
				w -= 2
			}
			// Padded by display width, not rune count: a CJK or emoji cell
			// is wider than its runes, and a cell wider than its column
			// pushes the row past the panel it is drawn in.
			cell := pad(trunc(v, w), w)
			switch {
			case ci < len(cols) && cols[ci] == nameCol && sel:
				b.WriteString(paint(bg, col, true, cell))
			case ci < len(metric) && metric[ci] && w > 2:
				// Value, then the arrow in the two reserved cells.
				cell = pad(trunc(v, w-2), w-2)
				b.WriteString(paint(bg, col, false, cell))
				b.WriteString(paint(bg, bg, false, " "))
				b.WriteString(trendGlyph(th, bg, arrowFor(row, ci)))
			default:
				b.WriteString(paint(bg, col, false, cell))
			}
			if k < len(keep)-1 {
				b.WriteString(paint(bg, bg, false, spaces(gap)))
			}
		}
		out = append(out, m.mark(fmt.Sprintf("row:%d", i), padBGOf(b.String(), rowW, inner, bg)))
	}
	if len(allRows) == 0 {
		loadErr := m.kindLoadError()
		switch {
		case loadErr != nil:
			out = append(out, m.loadErrorLines(inner, loadErr)...)
		case m.kindLoading():
			// Distinguish "still fetching" from "genuinely empty" — showing
			// "no resources found" during the first list is a lie.
			out = append(out, "")
			out = append(out, m.loadingLines(inner)...)
		case m.rowSearch != "":
			out = append(out, paint(th.Bg, th.Subtle, false, fmt.Sprintf("   no rows match %q", m.rowSearch)))
		default:
			out = append(out, paint(th.Bg, th.Subtle, false, "   no resources found"))
		}
	}
	return out
}

// ---- right: quick actions -------------------------------------------------

func (m *Model) viewActions(w, h int) Block {
	th := m.th()
	if w == 0 {
		return Block{W: 0, H: h, Lines: make([]string, h)}
	}
	inner := w - 2
	if m.mode == modeContexts {
		lines := []string{
			paint(th.Bg, th.Subtle, false, " context picker"),
			paint(th.Bg, th.Border, false, strings.Repeat("╌", inner)),
			paint(th.Bg, th.Subtle, false, " actions paused"),
			paint(th.Bg, th.Accent, false, " [enter] reconnect"),
		}
		return Panel(th, PanelOpts{Title: "Actions", Focused: false, W: w, H: h}, lines)
	}
	r := m.res()

	// With no cluster there is no object under the cursor, so every action
	// in this pane would be a button that only produces an error. The pane
	// says what is missing instead.
	if m.offline {
		return Panel(th, PanelOpts{Title: "Actions", Focused: false, W: w, H: h}, []string{
			paint(th.Bg, th.Subtle, false, " "+trunc("no cluster", inner-1)),
			paint(th.Bg, th.Border, false, strings.Repeat("╌", inner)),
			paint(th.Bg, th.Subtle, false, " "+trunc("nothing to act on", inner-1)),
			"",
			paint(th.Bg, th.Accent2, false, " r") + paint(th.Bg, th.Subtle, false, trunc("  retry", inner-3)),
			paint(th.Bg, th.Accent2, false, " /setup") + paint(th.Bg, th.Subtle, false, trunc("  guide", inner-8)),
		})
	}

	lines := []string{
		paint(th.Bg, th.Subtle, false, " "+trunc(r.Short+"/"+m.curName(), inner-1)),
		paint(th.Bg, th.Border, false, strings.Repeat("╌", inner)),
	}
	// Only actions that actually apply to the selected kind are listed —
	// a pane of greyed-out rows is noise, and the list is short enough that
	// its contents change legibly as you move between kinds.
	shown := make([]Action, 0, len(Actions))
	for _, a := range Actions {
		if r.Can(a.ID) {
			shown = append(shown, a)
		}
	}

	sepDone := false
	for _, a := range shown {
		if a.Risky && !sepDone {
			lines = append(lines, paint(th.Bg, th.Border, false, strings.Repeat("╌", inner)))
			sepDone = true
		}

		// Three visual states so the pane responds to the pointer: normal,
		// hovered (pointer is over it), and flashed (just clicked — lit
		// briefly so the click is acknowledged even when the action only
		// produces a toast).
		flashed := m.flashAct == a.ID
		hovered := m.hoverAct == a.ID && !flashed

		bg := th.Bg
		keyCol, labCol := th.Accent, th.Fg
		if a.Risky {
			keyCol, labCol = th.Err, th.Err
		}
		switch {
		case flashed:
			bg = th.Accent
			keyCol, labCol = th.Bg, th.Bg
			if a.Risky {
				bg = th.Err
			}
		case hovered:
			bg = th.SelBg
			labCol = th.SelFg
			if a.Risky {
				labCol = th.Err
			}
		}
		label := a.Label
		if a.ID == domain.ACordon && strings.Contains(rowStatus(m), "SchedulingDisabled") {
			label = "Uncordon"
		}
		marker := " "
		if hovered || flashed {
			marker = "▌"
		}
		row := paint(bg, keyCol, false, marker) + paint(bg, th.Border, false, "[") +
			paint(bg, keyCol, false, a.Key) + paint(bg, th.Border, false, "] ") +
			paint(bg, labCol, flashed, trunc(label, inner-6))
		lines = append(lines, m.mark("act:"+a.ID, padBG(row, inner, bg)))
	}
	// Lens verbs sit below the builtin actions, under their own rule: they
	// are the pack's vocabulary, not k10s's, and mixing them into the same
	// list would make "Sync" look as universal as "Describe".
	if specs := m.lensActions(); len(specs) > 0 {
		lines = append(lines, paint(th.Bg, th.Border, false, strings.Repeat("╌", inner)))
		for i, sp := range specs {
			k := lensKeyFor(i)
			if k == "" {
				break
			}
			keyCol, labCol := th.Accent2, th.Fg
			if sp.Disabled {
				// Disabled but still listed, and still keyed: pressing it
				// says why. A vanished button is a mystery.
				keyCol, labCol = th.Border, th.Subtle
			}
			if sp.Confirm == "typed" {
				keyCol = th.Err
			}
			glyph := " "
			if m.lensAck != nil && m.lensAck.id == sp.ID {
				glyph = m.lensAckGlyph()
			}
			row := paint(th.Bg, th.Border, false, glyph+"[") + paint(th.Bg, keyCol, false, k) + paint(th.Bg, th.Border, false, "] ") +
				paint(th.Bg, labCol, false, trunc(sp.Label, inner-6))
			lines = append(lines, m.mark("lens:"+sp.ID, padBG(row, inner, th.Bg)))
		}
	}

	plugins := m.availablePlugins()
	if len(plugins) > 0 {
		lines = append(lines, paint(th.Bg, th.Border, false, strings.Repeat("╌", inner)))
	}
	for _, item := range plugins {
		shortcut := plugin.NormalizeShortcut(item.ShortCut)
		keyCol, labelCol := th.Accent2, th.Fg
		if item.Dangerous {
			keyCol, labelCol = th.Err, th.Err
		}
		row := paint(th.Bg, th.Border, false, " [") + paint(th.Bg, keyCol, false, shortcut) + paint(th.Bg, th.Border, false, "] ") +
			paint(th.Bg, labelCol, false, trunc(pluginLabel(item), inner-len(shortcut)-5))
		lines = append(lines, m.mark("plugin:"+item.Name, padBG(row, inner, th.Bg)))
	}
	return Panel(th, PanelOpts{Title: "Actions", Focused: false, W: w, H: h}, lines)
}

// ---- bottom: prompt + status ---------------------------------------------

func (m *Model) viewPrompt(l layout) Block {
	th := m.th()
	focused := m.focus == focusPrompt
	inner := m.w - 2

	var caret, modeTag, modePlain, title, placeholder string
	if m.pmode == promptAI && !aiDisabled {
		caret = paint(th.Bg, th.Accent2, true, " ✦ ")
		modePlain = "[ AI · " + m.cfg.model + " ]"
		modeTag = m.mark("aimode", paint(th.Bg, th.Border, false, "[ ")+paint(th.Bg, th.Accent2, false, "AI · "+m.cfg.model)+paint(th.Bg, th.Border, false, " ]"))
		title = "Prompt"
		if focused {
			title = "Prompt · plain text → AI · /commands still work · esc close"
		}
		placeholder = "ask about your cluster…   ·   /settings to change provider/model"
	} else {
		caret = paint(th.Bg, th.Accent, true, " ❯ ")
		modePlain = "[ CMD ]"
		modeTag = m.mark("aimode", paint(th.Bg, th.Border, false, "[ ")+paint(th.Bg, th.Accent, false, "CMD")+paint(th.Bg, th.Border, false, " ]"))
		title = "Command"
		if focused {
			title = "Command · enter run · esc close"
		}
		placeholder = "kubectl get pods -A · :po · :ns · /help"
	}

	m.input.Placeholder = placeholder
	m.input.Width = inner - 5
	m.input.TextStyle = lipgloss.NewStyle().Background(th.Bg).Foreground(th.Fg)
	m.input.PlaceholderStyle = lipgloss.NewStyle().Background(th.Bg).Foreground(th.Subtle)
	m.input.Cursor.Style = lipgloss.NewStyle().Background(th.Accent).Foreground(th.Bg)

	zoomLbl, zoomPlainTag := "grow", "[ grow ]"
	if m.promptZoom {
		zoomLbl, zoomPlainTag = "shrink", "[ shrink ]"
	}
	zoomTag := m.mark("promptzoom",
		paint(th.Bg, th.Border, false, "[ ")+paint(th.Bg, th.Accent2, false, zoomLbl)+paint(th.Bg, th.Border, false, " ]"))

	var body []string
	if m.promptZoom {
		// A tall box is only worth the space if it actually shows the whole
		// command, so the value wraps across the rows rather than scrolling
		// sideways in a one-line field.
		body = m.zoomedPromptBody(caret, inner, l.promptH-2)
	} else {
		body = []string{m.mark("prompt", padBG(caret+m.input.View(), inner, th.Bg))}
	}

	return Panel(th, PanelOpts{
		Title: title, Tag: zoomTag + paint(th.Bg, th.Border, false, " ") + modeTag,
		TagPlain: zoomPlainTag + " " + modePlain,
		Focused:  focused, W: m.w, H: l.promptH,
	}, body)
}

// fitToast shortens a toast from the MIDDLE, not the tail. A toast that is
// too long is nearly always a path, and the tail of a path is its filename —
// the one part the reader has to retype. Cutting the end leaves
// "/home/k/.k10s/exports/pods-api-gate…", which names no file at all.
func fitToast(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 3 {
		return string(r[:w])
	}
	// A quarter to the head, the rest to the tail: the head only has to carry
	// enough to recognise ("✓ saved /home/s…"), while the tail has to carry a
	// whole filename.
	head := w / 4
	return string(r[:head]) + "…" + string(r[len(r)-(w-head-1):])
}

func (m *Model) viewStatus() Block {
	th := m.th()
	dot, dotCol := " ● ", th.Accent2
	if m.mouseOff {
		// Make copy-mode unmistakable: clicking is dead while it's on.
		dot, dotCol = " ✂ ", th.Warn
	}
	// The hints are the same every frame and the reader has read them; a toast
	// is the only surface some results have at all — ctrl+y's export path is
	// nowhere else — so while one is showing the hints give up width to it
	// rather than clipping it at half the screen.
	hints := "tab panes · enter open · ctrl+p search · f find · z zoom · ctrl+s copy · q quit"
	hintw := m.w/2 - 2
	if m.toast != "" {
		hintw = m.w / 3
	}
	right := paint(th.Bg, th.Subtle, false, trunc(hints, hintw)) + paint(th.Bg, th.Bg, false, " ")
	rightPlain := trunc(hints, hintw)

	toastw := m.w - lipgloss.Width(dot) - lipgloss.Width(rightPlain) - 2
	if toastw < 20 {
		toastw = 20
	}
	left := paint(th.Bg, dotCol, false, dot) + paint(th.Bg, th.Fg, false, fitToast(m.toast, toastw))

	// A waiting release earns one clickable badge and nothing more: the
	// toast that announced it scrolls away, and there is no other place that
	// keeps saying so.
	if badge := m.updateBadge(); badge != "" {
		right = markZone("updbtn", paint(th.Bg, th.Accent2, true, " "+badge+" ")) +
			paint(th.Bg, th.Border, false, "│ ") + right
		rightPlain = " " + badge + " │ " + rightPlain
	}

	gapw := m.w - lipgloss.Width(dot) - lipgloss.Width(fitToast(m.toast, toastw)) - lipgloss.Width(rightPlain) - 1
	if gapw < 1 {
		gapw = 1
	}
	return BlockOf(m.w, 1, []string{left + paint(th.Bg, th.Bg, false, spaces(gapw)) + right}, th.Bg)
}

// ---- overlays --------------------------------------------------------------

func (m *Model) overlaySuggestions(root Block, l layout, sug []SlashCommand) Block {
	th := m.th()
	w := 62
	if w > m.w-6 {
		w = m.w - 6
	}
	inner := w - 2

	// Only a screenful is drawn, but every match stays reachable: the window
	// follows the highlight, and the tag says how far down the list it is.
	cur := clamp(m.sugIdx, 0, len(sug)-1)
	top := m.sugTop(len(sug))
	shown := sug[top:clamp(top+m.sugRows(), top, len(sug))]

	var body []string
	for off, c := range shown {
		i := top + off
		selected := i == cur
		bg := th.Bg
		if selected {
			bg = th.SelBg
		}
		lead := "  "
		if selected {
			lead = "▸ "
		}
		row := paint(bg, th.Accent, false, lead) + paint(bg, th.Accent, true, c.Name)
		// The spelled-out name next to the short one, so ":po" and ":pods"
		// are visibly the same command rather than two things to remember.
		// Same accent as the name it belongs to, just not bold — th.Border
		// is the colour of box lines and left it barely legible.
		if c.Full != "" && c.Full != c.Name {
			row += paint(bg, bg, false, " ") + paint(bg, th.Accent, false, c.Full)
		}
		if c.Args != "" {
			row += paint(bg, bg, false, " ") + paint(bg, th.Accent2, false, c.Args)
		}
		desc := trunc(c.Desc, inner-lipgloss.Width(row)-3)
		gap := inner - lipgloss.Width(row) - lipgloss.Width(desc) - 1
		if gap < 1 {
			gap = 1
		}
		row += paint(bg, bg, false, spaces(gap)) + paint(bg, th.Subtle, false, desc) + paint(bg, bg, false, " ")
		body = append(body, markZone(fmt.Sprintf("sug:%d", i), padBG(row, inner, bg)))
	}

	h := len(body) + 2
	title := "cluster commands  /"
	if v := m.input.Value(); v != "" && v[0] == ':' {
		title = "k10s commands  :"
	}
	tag, tagPlain := "", ""
	if len(shown) < len(sug) {
		tagPlain = fmt.Sprintf("%d/%d", cur+1, len(sug))
		if top > 0 {
			tagPlain = "↑ " + tagPlain
		}
		if top+len(shown) < len(sug) {
			tagPlain += " ↓"
		}
		tag = lipgloss.NewStyle().Background(th.Bg).Foreground(th.Subtle).Render(tagPlain)
	}
	box := Panel(th, PanelOpts{Title: title, Tag: tag, TagPlain: tagPlain, W: w, H: h, Focused: true}, body)
	y := l.promptY - h + 1
	if y < 0 {
		y = 0
	}
	return root.Overlay(box, 1, y)
}

func (m *Model) overlayConfirm(root Block) Block {
	th := m.th()
	c := m.confirm
	accent := th.Accent
	if c.danger {
		accent = th.Err
	}
	w := 58
	if w > m.w-8 {
		w = m.w - 8
	}
	inner := w - 2

	body := []string{""}
	for _, ln := range c.message {
		body = append(body, paint(th.Bg, th.Fg, false, "  "+trunc(ln, inner-3)))
	}
	body = append(body, "")

	if c.typed != "" {
		body = append(body,
			paint(th.Bg, th.Subtle, false, "  type "),
			paint(th.Bg, accent, true, "  "+trunc(c.typed, inner-3)),
			"",
		)
		field := c.buf + "▏"
		col := th.Fg
		if c.armed() {
			col = th.Ok
		}
		body = append(body,
			paint(th.Bg, th.Border, false, "  ▸ ")+paint(th.Bg, col, false, trunc(field, inner-5)),
			"",
		)
	}

	// A notice has nothing to decline, so it gets one button — offering
	// "Cancel" against a statement of fact only invites the question of
	// what cancelling it would do.
	okPlain, noPlain := "  Enter · Confirm  ", "  Esc · Cancel  "
	if c.notice {
		okPlain, noPlain = "  Enter · OK  ", ""
	}
	okBG := accent
	if !c.armed() {
		// Not a decoration: the button is genuinely inert until the word
		// matches, and looking live while refusing clicks is worse than
		// having no button.
		okBG = th.Border
	}
	ok := markZone("cf:ok", lipgloss.NewStyle().Background(okBG).Foreground(th.Bg).Bold(true).Render(okPlain))
	btnGap := 2
	row := ok
	if noPlain != "" {
		no := markZone("cf:no", lipgloss.NewStyle().Background(th.Border).Foreground(th.Fg).Render(noPlain))
		row = ok + paint(th.Bg, th.Bg, false, spaces(btnGap)) + no
	} else {
		btnGap = 0
	}
	pre := (inner - len(okPlain) - len(noPlain) - btnGap) / 2
	if pre < 1 {
		pre = 1
	}
	body = append(body, paint(th.Bg, th.Bg, false, spaces(pre))+row)
	body = append(body, "")

	h := len(body) + 2
	border := th.Accent
	if c.danger {
		border = th.Err
	}
	mark := "⚠  "
	if c.notice {
		mark = "ⓘ  "
	}
	box := Panel(th, PanelOpts{
		Title: mark + c.title, Focused: true, W: w, H: h, BorderCol: border,
	}, body)

	return root.Overlay(box, (m.w-w)/2, (m.h-h)/2)
}

// rowNumBase is the number given to the first row of a table. Normally 1,
// but the Namespaces table leads with the synthetic "all" entry, which is
// numbered 0 so the real namespaces below it are 1..N — matching the count
// the sidebar shows for the kind.
func rowNumBase(kindKey string) int {
	if kindKey == "namespaces" {
		return 0
	}
	return 1
}

// zoomedPromptBody lays the command out over the tall box: the live input on
// the first row (so the caret still blinks where you type) and the rest of
// the value wrapped underneath, which is the point of growing the box.
func (m *Model) zoomedPromptBody(caret string, inner, rows int) []string {
	th := m.th()

	textW := maxi(8, inner-4)
	segs := wrapLine(m.input.Value(), textW)

	out := make([]string, 0, rows)
	// Row 1 stays the real textinput so editing and the caret behave
	// normally; the wrap below is a read-only continuation.
	out = append(out, m.mark("prompt", padBG(caret+m.input.View(), inner, th.Bg)))
	if len(segs) > 1 {
		for _, seg := range segs[1:] {
			if len(out) >= rows-1 {
				break
			}
			out = append(out, padBG(paint(th.Bg, th.Bg, false, "   ")+paint(th.Bg, th.Fg, false, seg), inner, th.Bg))
		}
	}
	for len(out) < rows-1 {
		out = append(out, "")
	}

	hint := " ctrl+z shrink · esc back · enter run"
	if m.pmode == promptAI && !aiDisabled {
		hint = " ctrl+z shrink · esc back · enter ask " + m.cfg.model
	}
	out = append(out, padBG(paint(th.Bg, th.Subtle, false, trunc(hint, inner)), inner, th.Bg))
	return out
}
