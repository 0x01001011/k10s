package ui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/domain"
)

// Row grouping (T42).
//
// Pods arrive under their owner: `web-frontend` with its three pods beneath
// it, rather than fourteen rows in alphabetical order. This is one level of
// collapsible headers over a row slice that stays FLAT, globally ordered and
// flatly indexed — group headers are a rendering concern and nothing else.
//
// That is deliberately not a tree, and the reasons are structural rather than
// aesthetic. A real tree in the main list breaks five things that work today:
// sort becomes undefined across it (a tree orders siblings, so "by RESTARTS
// descending" means nothing); filtering forks into showing orphan matches (a
// lie about the shape) or ancestors of matches (rows that do not match); row
// numbers stop being addressable, because row 12 changes identity across a
// fold; a 500-pod namespace becomes 630 lines you can only navigate if you
// already know which Deployment you want; and curRow() gains a "this row is
// not an object" branch in five call sites, each a new way to fire an action
// against the wrong thing. The nested tree is opt-in — card T46 — and the
// multi-hop one already exists in the X panel.
//
// Everything below mirrors the Resources sidebar, whose folding rules are
// already tested and already written down in docs/ui.md.

// groupKey names what rows are grouped by. Each reads a value that already
// sits in the row — none costs a request, a watch, or a walk.
type groupKey string

const (
	groupNone      groupKey = ""
	groupOwner     groupKey = "owner"
	groupNode      groupKey = "node"
	groupNamespace groupKey = "namespace"
	groupStatus    groupKey = "status"
	groupObject    groupKey = "object"
)

// groupKeys is what :group accepts.
var groupKeys = []groupKey{groupNone, groupOwner, groupNode, groupNamespace, groupStatus, groupObject}

// Deliberately absent: label:<k>. Labels are not in the row set, so the key
// would return one group called "<none>" for every object — worse than not
// offering it. It arrives when a label column does.

// groupSpan is one run of consecutive rows sharing a group value.
type groupSpan struct {
	value string
	first int // index into the flat row slice
	count int
}

// maxGroups and maxGroupedRows are where grouping stops helping.
//
// Forty headers is already more than fits a screen, and past two thousand
// rows nobody reads a grouped list — they filter it. Both fall back to flat
// silently, because a grouping that quietly half-applied would be harder to
// understand than one that did not apply.
const (
	maxGroups      = 40
	maxGroupedRows = 2000
)

// groupColumn resolves a group key to the cell index it reads, or -1 when
// this kind cannot answer it.
//
// cols is the column set the backend actually returned, NOT Kind.Cols. Under
// :ns all the backend prepends a NAMESPACE column, which both adds a column
// to group by and shifts every meta cell one to the right — reading Kind.Cols
// here would miss the first and mis-address the second.
func groupColumn(cols []string, k domain.Kind, key groupKey, ns string) int {
	switch key {
	case groupOwner:
		for i, name := range k.Meta {
			if name == "OWNER" {
				return len(cols) + i
			}
		}
		return -1
	case groupNode:
		return colIndex(cols, "NODE")
	case groupStatus:
		if i := colIndex(cols, "STATUS"); i >= 0 {
			return i
		}
		return colIndex(cols, "PHASE")
	case groupObject:
		return colIndex(cols, "OBJECT")
	case groupNamespace:
		// Grouping by namespace inside one namespace is a header with every
		// row under it.
		if ns != domain.AllNamespaces {
			return -1
		}
		return colIndex(cols, "NAMESPACE")
	}
	return -1
}

// defaultGroup is what a kind groups by before anyone says otherwise.
//
// Pods group by owner because that is the shape an operator already holds in
// their head: they think "web-frontend", not "these three of the fourteen".
// Events group by object for the same reason. Everything else stays flat — a
// grouping nobody asked for is an extra line per group and nothing more.
func defaultGroup(kind string) groupKey {
	switch kind {
	case "pods":
		return groupOwner
	case "events":
		return groupObject
	}
	return groupNone
}

// groupFor is the key in force for a kind.
func (m *Model) groupFor(kind string) groupKey {
	if g, ok := m.groups[kind]; ok {
		return g
	}
	return defaultGroup(kind)
}

func (m *Model) setGroup(kind string, g groupKey) {
	if m.groups == nil {
		m.groups = map[string]groupKey{}
	}
	m.groups[kind] = g
	m.saveConfig()
	m.reanchorRow()
}

// groupSpans splits rows into runs sharing a group value, or returns nil when
// this table is not grouped.
//
// Sorting and grouping are exclusive. A sort is a statement about the whole
// table's order; honouring both would mean sorting within groups, which
// answers neither question. The sort wins, because it is what the user just
// asked for.
func (m *Model) groupSpans(cols []string, rows [][]string) []groupSpan {
	k := m.curKind()
	key := m.groupFor(k.Key)
	if key == groupNone || len(rows) == 0 || len(rows) > maxGroupedRows {
		return nil
	}
	if m.sortFor(k.Key).col >= 0 {
		return nil
	}
	ci := groupColumn(cols, k, key, m.namespace)
	if ci < 0 {
		return nil
	}

	// One pass over rows already in hand, one string compare each. The rows
	// arrive in the backend's natural order, which for every key here puts
	// equal values next to each other; a value that reappears later gets its
	// own span rather than being merged, because merging would mean moving
	// rows and the row order is not ours to change.
	var spans []groupSpan
	for i, r := range rows {
		v := cellAt(r, ci)
		if n := len(spans); n > 0 && spans[n-1].value == v {
			spans[n-1].count++
			continue
		}
		spans = append(spans, groupSpan{value: v, first: i, count: 1})
	}

	// One group holding everything is noise; forty headers is past helping.
	if len(spans) < 2 || len(spans) > maxGroups {
		return nil
	}
	return spans
}

// spanAt finds the span holding row i.
func spanAt(spans []groupSpan, i int) (groupSpan, bool) {
	for _, s := range spans {
		if i >= s.first && i < s.first+s.count {
			return s, true
		}
	}
	return groupSpan{}, false
}

// groupStateKey scopes collapse state to the kind AND the key it was folded
// under, so switching from owner to node does not arrive with half the node
// groups already closed.
func (m *Model) groupStateKey() string {
	kind := m.curKind().Key
	return kind + "\x00" + string(m.groupFor(kind))
}

// rowCollapsed reports whether row i is inside a folded group.
//
// A row search ignores folding entirely, exactly as the sidebar does: a match
// hidden behind a fold would make the filter look broken.
func (m *Model) rowCollapsed(spans []groupSpan, i int) bool {
	if len(spans) == 0 || m.rowSearch != "" {
		return false
	}
	s, ok := spanAt(spans, i)
	if !ok {
		return false
	}
	return m.collapsedRows[m.groupStateKey()][s.value]
}

// toggleRowGroup folds or unfolds the group holding row i.
func (m *Model) toggleRowGroup(spans []groupSpan, i int) bool {
	s, ok := spanAt(spans, i)
	if !ok {
		return false
	}
	if m.collapsedRows == nil {
		m.collapsedRows = map[string]map[string]bool{}
	}
	sk := m.groupStateKey()
	if m.collapsedRows[sk] == nil {
		m.collapsedRows[sk] = map[string]bool{}
	}
	m.collapsedRows[sk][s.value] = !m.collapsedRows[sk][s.value]

	// The cursor stays on its object; folding the group it sits in moves it
	// to that group's first row, which is the row the header marks — so
	// "where am I" never becomes a guess.
	if m.collapsedRows[sk][s.value] {
		m.rowIdx = s.first
	}
	return true
}

// groupLabel is what a header prints for one span.
//
// For owners it names the Deployment where the ReplicaSet name allows that to
// be derived, and says which ReplicaSet it was: an operator reading
// `web-frontend · rs 6b8c7d9f5` learns both. Where the shape does not match,
// the owner name is printed as-is rather than guessed at.
func groupLabel(key groupKey, value string) (main, sub string) {
	if value == "" {
		return "<none>", ""
	}
	if key == groupOwner {
		label, hash := domain.OwnerLabel(value)
		if hash != "" {
			return label, "rs " + hash
		}
		return label, ""
	}
	return value, ""
}

// skipCollapsed moves an index off a hidden row, continuing in the direction
// the cursor was already travelling. A folded group is skipped whole; running
// off the end comes back to the last row that is actually visible, so the
// cursor can never rest on something the screen does not show.
func (m *Model) skipCollapsed(cols []string, rows [][]string, i, delta int) int {
	spans := m.groupSpans(cols, rows)
	if len(spans) == 0 || !m.rowCollapsed(spans, i) {
		return i
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for j := i; j >= 0 && j < len(rows); j += step {
		if !m.rowCollapsed(spans, j) {
			return j
		}
	}
	// Nothing that way: turn round rather than sit on a hidden row.
	for j := i; j >= 0 && j < len(rows); j -= step {
		if !m.rowCollapsed(spans, j) {
			return j
		}
	}
	return i
}

// rowGroupHeader draws one group header line.
//
// It is the sidebar's groupHeader for rows: the same chevron, the same count
// of what is hidden, and the same rule that a folded group holding the cursor
// keeps its marker — without it, folding would turn "where am I" into a
// guess.
func (m *Model) rowGroupHeader(key groupKey, s groupSpan, folded, holdsCursor bool, inner int) string {
	th := m.th()

	chevron, chevCol := "▾", th.Border
	if folded {
		chevron = "▸"
		if holdsCursor {
			chevCol = th.Accent
		}
	}

	main, sub := groupLabel(key, s.value)
	labelCol := th.Fg
	if folded && holdsCursor {
		labelCol = th.Accent2
	}

	row := paint(th.Bg, chevCol, false, " "+chevron+" ") + paint(th.Bg, labelCol, true, main)
	if sub != "" {
		row += paint(th.Bg, th.Subtle, false, " · "+sub)
	}

	// The count is what the header is for when folded — "three pods are under
	// here" — and is still worth stating when open.
	tag := strconv.Itoa(s.count)
	gap := inner - lipgloss.Width(row) - len(tag) - 1
	if gap < 1 {
		gap = 1
	}
	return row + paint(th.Bg, th.Bg, false, spaces(gap)) + paint(th.Bg, th.Subtle, false, tag)
}

// parseGroupConfig reads `pods=owner,events=object`.
func parseGroupConfig(s string) map[string]groupKey {
	out := map[string]groupKey{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kind, key, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(kind)] = groupKey(strings.TrimSpace(key))
	}
	return out
}

// renderGroupConfig writes the form parseGroupConfig reads, skipping kinds
// still on their default so the file stays about what was changed.
func renderGroupConfig(groups map[string]groupKey) string {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		if groups[k] != defaultGroup(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+string(groups[k]))
	}
	return strings.Join(parts, ",")
}
