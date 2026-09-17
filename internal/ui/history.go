package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/domain"
)

// Metric history for inline sparklines (T44).
//
// This must NOT be fed from View, and the existing trend map is the cautionary
// example: arrowFor observes its value inside tableBody's row loop, which has
// two consequences that make it useless as a time series. View runs on every
// keystroke rather than once per tick, so a burst of typing appends duplicate
// samples while an idle model appends none — a time axis that is not time. And
// View walks only the visible rows, so scrolling past a pod stops sampling it,
// and scrolling back leaves a hole the sparkline cannot know about.
//
// So: fed from the repaint tick in Update, over the whole current row set, and
// swept on the same tick.

// histLen is how many samples one row keeps; sparkLen is how many of them the
// inline sparkline draws.
//
// The window is sized by the chart, which wants a shape rather than a glance:
// 64 samples against the live backend's 15s metrics refresh is sixteen
// minutes. The sparkline takes the most recent eight of that same window,
// because eight is what a table cell can spare — one store rather than two,
// and no second sampling path to keep in step with the first.
//
// Cost at 5000 pods: 64 × 4 B plus the map bucket and key, roughly 350 B a
// row, so about 1.75 MB. More would manufacture resolution the sampler does
// not have.
const (
	histLen  = 64
	sparkLen = 8
)

// ring is a fixed-size sample window.
//
// int32 rather than uint16: a 64-core pod is 64000 milliCPU, and memory in MiB
// overflows 16 bits at 64 GiB.
type ring struct {
	buf  [histLen]int32
	head uint8
	n    uint8
}

func (r *ring) push(v int32) {
	r.buf[r.head] = v
	r.head = (r.head + 1) % histLen
	if int(r.n) < histLen {
		r.n++
	}
}

// samples returns the window oldest-first, the order spark draws.
func (r *ring) samples() []int32 {
	if r.n == 0 {
		return nil
	}
	out := make([]int32, 0, r.n)
	start := (int(r.head) - int(r.n) + histLen) % histLen
	for i := 0; i < int(r.n); i++ {
		out = append(out, r.buf[(start+i)%histLen])
	}
	return out
}

// histKey identifies one row's series — the same shape as the trend map's key,
// for the same reason: kind, namespace and name together are the only stable
// identity a row has.
func histKey(kind, ns, name string) string {
	return kind + "\x00" + ns + "\x00" + name
}

// observeMetrics appends one sample per row from the current kind's CPU
// column, and drops every series whose row has gone.
//
// The sweep is the part the existing trend map lacks: m.trends is keyed per
// object and cleared only by resetTrends on a backend switch, so it grows with
// every pod name ever scrolled past. One pass here covers both.
func (m *Model) observeMetrics() {
	kind := m.curKind().Key
	cols, rows := m.tableData()
	if len(rows) == 0 {
		return
	}

	ci := colIndex(cols, "CPU")
	if ci < 0 {
		ci = colIndex(cols, "CPU%")
	}
	if ci < 0 {
		return
	}
	nameIdx := colIndex(cols, "NAME")
	if nameIdx < 0 {
		nameIdx = 0
	}
	nsIdx := colIndex(cols, "NAMESPACE")

	if m.hist == nil {
		m.hist = map[string]*ring{}
	}

	live := make(map[string]bool, len(rows))
	for _, r := range rows {
		ns := m.namespace
		if nsIdx >= 0 {
			ns = cellAt(r, nsIdx)
		}
		k := histKey(kind, ns, cellAt(r, nameIdx))
		live[k] = true

		v, ok := domain.SortKey(domain.CellQuantity, cellAt(r, ci))
		if !ok {
			// A sentinel is not a zero. Skip, rather than record a dip the
			// cluster never had.
			continue
		}
		if m.hist[k] == nil {
			m.hist[k] = &ring{}
		}
		m.hist[k].push(int32(v))
	}

	// Sweep: anything not in the current row set is gone.
	for k := range m.hist {
		if !live[k] {
			delete(m.hist, k)
		}
	}
}

// rowNamespace is the namespace a row belongs to: the active filter, or —
// under :ns all — whatever that row's own NAMESPACE cell says.
func (m *Model) rowNamespace(row, cols []string) string {
	if i := colIndex(cols, "NAMESPACE"); i >= 0 {
		return cellAt(row, i)
	}
	return m.namespace
}

// lipglossWidthOf is the display width of an already-styled run.
func lipglossWidthOf(s string) int {
	if s == "" {
		return 0
	}
	return lipgloss.Width(s)
}

// rowSamples is the whole window for one row, or nil.
func (m *Model) rowSamples(kind, ns, name string) []int32 {
	r := m.hist[histKey(kind, ns, name)]
	if r == nil {
		return nil
	}
	return r.samples()
}

// rowSpark is the tail of that window — what fits in a table cell.
func (m *Model) rowSpark(kind, ns, name string) []int32 {
	s := m.rowSamples(kind, ns, name)
	if len(s) > sparkLen {
		return s[len(s)-sparkLen:]
	}
	return s
}
