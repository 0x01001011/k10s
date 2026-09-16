package k8s

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/lens"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/util/jsonpath"
)

// lensCol is one compiled table column.
//
// jp is parsed ONCE, when the registry is built, because parsing a JSONPath
// per cell per frame would put real work on the render path. The consequence
// is that *jsonpath.JSONPath is NOT safe for concurrent use — FindResults
// mutates parser state — so a compiled path must stay confined to the row
// builder, which runs only on bubbletea's single event-loop goroutine.
//
// Nothing else may reach these. The background count sweep DOES handle lens
// kinds (wantedKinds in counts.go ranges lensOrder as well as builtinKinds, so
// lens kinds get sidebar badges) — but it reaches them only through gvrFor and
// a Limit:1 dynamic LIST, and it must never touch lensKind.cols. Calling
// lensCell from a sweep worker would run FindResults concurrently with the row
// builder, and countKind's recover() would swallow the resulting panic into a
// silent wrong count. Edge resolution and the ack read deliberately parse a
// fresh JSONPath per call for the same reason — they run from tea.Cmd
// goroutines.
type lensCol struct {
	header   string
	jp       *jsonpath.JSONPath
	format   string
	truncate int
	sev      lens.Severity
	hasSev   bool
}

func compileColumns(p lens.Pack, k lens.Kind) ([]lensCol, error) {
	out := make([]lensCol, 0, len(k.Columns))
	for _, c := range k.Columns {
		jp := jsonpath.New(c.Header)
		// AllowMissingKeys covers a missing FIELD. It does not cover an
		// out-of-range array index — see lensCell.
		jp.AllowMissingKeys(true)
		if err := jp.Parse(lens.Braced(c.Path)); err != nil {
			return nil, fmt.Errorf("column %q: %w", c.Header, err)
		}
		col := lensCol{
			header:   c.Header,
			jp:       jp,
			format:   c.Format,
			truncate: c.Truncate,
		}
		if c.Severity != "" {
			sev, ok := p.Severities[c.Severity]
			if !ok {
				return nil, fmt.Errorf("column %q: undeclared severity table %q", c.Header, c.Severity)
			}
			col.sev, col.hasSev = sev, true
		}
		out = append(out, col)
	}
	return out, nil
}

// lensCell evaluates one compiled path against one object.
//
// ANY failure is an empty cell, never an error: a missing field, an
// out-of-bounds array index, a type that will not render. Half the status
// fields across the five shipped packs are optional, and a row that is one
// cell short would break the table's width invariant.
//
// AllowMissingKeys(true) is not sufficient on its own. jsonpath's evalArray
// returns "array index out of bounds" unconditionally without consulting that
// flag — only evalField honours it — so `.status.conditions[0].reason` against
// an empty conditions list errors even with the flag set. Swallowing the error
// here is what makes both cases behave the same.
func lensCell(c lensCol, obj map[string]any) string {
	if c.jp == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := c.jp.Execute(&buf, obj); err != nil {
		return ""
	}
	return formatLensCell(c.format, c.truncate, buf.String())
}

func formatLensCell(format string, truncate int, raw string) string {
	// jsonpath renders a missing key as the empty string, and a wildcard
	// path as space-separated matches. Both are fine verbatim.
	v := strings.TrimSpace(raw)
	switch format {
	case "age":
		v = lensAge(v)
	case "until":
		v = lensUntil(v)
	case "bytes":
		if n, ok := parseLensNumber(v); ok {
			v = humanBytesLens(int64(n))
		}
	case "int":
		// JSON numbers decode to float64, so an unformatted render can come
		// back as "3e+00" rather than "3".
		if n, ok := parseLensNumber(v); ok {
			v = strconv.FormatInt(int64(n), 10)
		}
	case "bool":
		switch v {
		case "true", "false":
		default:
			if b, err := strconv.ParseBool(v); err == nil {
				v = strconv.FormatBool(b)
			}
		}
	}
	if truncate > 0 {
		v = truncRunes(v, truncate)
	}
	return v
}

// lensAge reuses rows.go's age() so a lens row and a builtin row describe the
// same instant the same way, including "-" for something unparseable.
func lensAge(v string) string {
	if v == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return "-"
	}
	return age(t)
}

// lensUntil renders time REMAINING for a future instant (cert-manager's
// .status.notAfter), because duration.ShortHumanDuration returns the literal
// "<invalid>" for anything below -1s — so format: age would print "<invalid>"
// on every healthy certificate. Once the instant is past it falls back to
// lensAge, so an expired cert reads as an age like every other time column.
func lensUntil(v string) string {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return lensAge(v) // "" for empty, "-" for unparseable
	}
	if d := time.Until(t); d > 0 {
		return duration.ShortHumanDuration(d)
	}
	// A leading "-" is the only thing separating "expires in 3d" from "expired
	// 3 days ago": both render as "3d" otherwise, in the same colour, and an
	// EXPIRES column that cannot say which side of now it is on is worse than
	// no column.
	return "-" + age(t)
}

func parseLensNumber(v string) (float64, bool) {
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// truncRunes cuts to n runes, never through the middle of one.
func truncRunes(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// lensRow carries the severity rank alongside the formatted cells so the
// comparator never has to re-derive it, and never has to know which column
// index the severity landed in.
type lensRow struct {
	ns   string
	row  []string
	rank int
}

// lensTable is the row path for a lens kind.
//
// Severity-first ordering is reached through applyNamespaceOpt's existing
// sorted=false escape hatch — the one events already use — so neither
// applyNamespace nor sortRows is touched, and no column-index arithmetic is
// needed anywhere.
func (s *Store) lensTable(lk *lensKind, ns string) ([]string, [][]string) {
	rows := s.lensRows(lk, ns)

	// A cluster-scoped kind has no namespace to filter on. Its rows carry
	// ns "-", which applyNamespace would compare against "default" and
	// discard — emptying the table in every namespace view while the sidebar
	// badge still counted them. Builtin cluster-scoped kinds (pvs, nodes,
	// clusterroles) return their rows directly for the same reason.
	if !lk.kind.Namespaced {
		if lensHasSeverity(lk) {
			sortLensRows(rows)
		} else {
			sort.SliceStable(rows, func(i, j int) bool { return lensName(rows[i]) < lensName(rows[j]) })
		}
		out := make([][]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.row)
		}
		return lk.kind.Cols, out
	}

	if !lensHasSeverity(lk) {
		// Nothing earned the escape hatch: plain A→Z, exactly like a
		// builtin kind.
		plain := make([]nsRow, 0, len(rows))
		for _, r := range rows {
			plain = append(plain, nsRow{ns: r.ns, row: r.row})
		}
		return applyNamespace(lk.kind.Cols, plain, ns)
	}
	sortLensRows(rows)
	out := make([]nsRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, nsRow{ns: r.ns, row: r.row})
	}
	return applyNamespaceOpt(lk.kind.Cols, out, ns, false)
}

func lensHasSeverity(lk *lensKind) bool {
	for _, c := range lk.cols {
		if c.hasSev {
			return true
		}
	}
	return false
}

func (s *Store) lensRows(lk *lensKind, ns string) []lensRow {
	lister := s.lensLister(lk, ns)
	if lister == nil {
		return nil
	}
	objs, err := lister.List(labels.Everything())
	if err != nil {
		return nil
	}
	out := make([]lensRow, 0, len(objs))
	for _, o := range objs {
		u, ok := o.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		row := make([]string, 0, len(lk.cols))
		rank := lens.LevelOK.Rank()
		for _, c := range lk.cols {
			cell := lensCell(c, u.Object)
			row = append(row, cell)
			if !c.hasSev {
				continue
			}
			// An EMPTY cell is skipped, not graded. The field is absent,
			// which is not a status — and grading it would rank every row by
			// its most optional column. ArgoCD's OPERATION is empty on any
			// app that is not mid-sync, and its table defaults to unknown,
			// so grading empties would float every healthy row.
			if cell == "" {
				continue
			}
			// The WORST severity across the row wins. Taking the first
			// column instead would sort a Synced-but-Degraded Application as
			// healthy, because SYNC is declared before HEALTH — exactly the
			// row an operator most needs to see.
			if r := c.sev.Level(cell).Rank(); r < rank {
				rank = r
			}
		}
		nsName := u.GetNamespace()
		if nsName == "" {
			nsName = "-"
		}
		out = append(out, lensRow{ns: nsName, row: row, rank: rank})
	}
	return out
}

// sortLensRows is worst-first, then namespace, then name. Stable, so equal
// ranks keep their A→Z order.
//
// This is the whole point of severity: k9s sorts STATUS alphabetically, which
// scatters CrashLoopBackOff among Running on a large cluster (#3589, closed as
// not planned). Broken things belong at the top.
func sortLensRows(rows []lensRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.ns != b.ns {
			return a.ns < b.ns
		}
		return lensName(a) < lensName(b)
	})
}

func lensName(r lensRow) string {
	if len(r.row) == 0 {
		return ""
	}
	return r.row[0]
}

// lensCount counts cached objects. It is reached from RowCount only after the
// isStarted && SyncedFor guard, so it never opens a watch — and it formats not
// one cell, which is what keeps the sidebar cheap.
func (s *Store) lensCount(lk *lensKind, ns string) int {
	s.infMu.Lock()
	desired := s.desiredInformerScope(lk.kind.Key, []string{ns})
	scope := s.accessScopeLocked(lk.kind.Key, desired)
	lister := s.dynFactoryLocked(scope).ForResource(lk.gvr).Lister()
	s.infMu.Unlock()
	if lister == nil {
		return domain.CountUnknown
	}
	objs, err := lister.List(labels.Everything())
	if err != nil {
		return domain.CountUnknown
	}
	eff := ns
	if eff == "" {
		eff = "default"
	}
	if eff == domain.AllNamespaces || !lk.kind.Namespaced {
		return len(objs)
	}
	n := 0
	for _, o := range objs {
		if u, ok := o.(*unstructured.Unstructured); ok && u.GetNamespace() == eff {
			n++
		}
	}
	return n
}

// humanBytesLens formats a byte count the way Kubernetes quantities read.
// internal/ui has a similar helper but it is unexported, and exporting one
// across packages to save eight lines is the worse trade.
func humanBytesLens(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 5; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f%ci", float64(n)/float64(div), "KMGTPE"[exp])
}
