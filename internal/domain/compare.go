package domain

import (
	"strconv"
	"strings"
)

// Cell comparison for column sort (T07).
//
// Row cells are pre-rendered strings by the time anything can sort them —
// "5d2h", "900Mi", "250m", "1/3" — so every comparison here is a parse. Two
// rules follow from that, and both matter more than they look:
//
//   - the type is decided per COLUMN, not per cell. A column whose values all
//     parse as durations is a duration column; a mixed column falls back to
//     name order. Sniffing per cell makes "-" and "250m" incomparable and the
//     resulting order non-deterministic.
//   - sentinels sort last in BOTH directions. A descending CPU sort that
//     floats every unmetricked pod to the top has answered a different
//     question than the one that was asked.

// CellKind is what a column's values turned out to be.
type CellKind int

const (
	CellText CellKind = iota
	CellNumber
	CellDuration
	CellQuantity
	CellRatio
)

// sentinel reports whether a cell says "no value" rather than carrying one.
// These sink to the bottom whichever way the column is sorted.
func sentinel(s string) bool {
	switch strings.TrimSpace(s) {
	case "", "-", "n/a", "<none>", "<cluster>", "pending", "unknown":
		return true
	}
	return false
}

// SniffColumn decides how to compare a column, from its values.
//
// Every non-sentinel value must agree, because one unparseable value in a
// column of durations means the column is not durations — it is text that
// often looks like durations, and ordering it as durations would drop that
// value somewhere arbitrary.
// A single cell is often ambiguous — "10m" is ten minutes in AGE and ten
// milliCPU in CPU, and nothing in the string says which. So each value offers
// the set of kinds it *could* be, and the column is the intersection: "2h"
// can only be a duration, which settles "10m" beside it. When the whole
// column stays ambiguous (every value ends in "m") both readings are linear
// in the same number and therefore order identically, so the preference below
// picks one and the result is the same either way.
func candidateKinds(v string) []CellKind {
	var out []CellKind
	if isRatio(v) {
		out = append(out, CellRatio)
	}
	if isNumber(v) {
		out = append(out, CellNumber)
	}
	if isQuantity(v) {
		out = append(out, CellQuantity)
	}
	if isDuration(v) {
		out = append(out, CellDuration)
	}
	return out
}

// kindPreference breaks a tie left by the intersection. Ratio and number are
// unambiguous when they appear at all; quantity precedes duration because a
// column of bare "m" values is far more often milliCPU than minutes, and the
// two order identically anyway.
var kindPreference = []CellKind{CellRatio, CellNumber, CellQuantity, CellDuration}

func SniffColumn(values []string) CellKind {
	var live map[CellKind]bool
	seen := 0

	for _, v := range values {
		if sentinel(v) {
			continue
		}
		cands := candidateKinds(v)
		if len(cands) == 0 {
			return CellText // one value the column cannot mean settles it
		}
		seen++

		if live == nil {
			live = map[CellKind]bool{}
			for _, k := range cands {
				live[k] = true
			}
			continue
		}
		next := map[CellKind]bool{}
		for _, k := range cands {
			if live[k] {
				next[k] = true
			}
		}
		if len(next) == 0 {
			return CellText
		}
		live = next
	}

	if seen == 0 {
		return CellText
	}
	for _, k := range kindPreference {
		if live[k] {
			return k
		}
	}
	return CellText
}

// SortKey turns a cell into the value it is compared by — computed once per
// row rather than once per comparison, since parsing inside a less function
// is O(n log n) parses for no reason.
//
// The bool reports whether the cell carries a value at all; sentinels get
// false and are ordered last regardless of direction.
func SortKey(kind CellKind, v string) (float64, bool) {
	if sentinel(v) {
		return 0, false
	}
	switch kind {
	case CellText:
		// Text has no numeric key, but it does have a value: every cell ties
		// at zero and the comparison falls through to name order. Reporting
		// false here instead would mark every STATUS cell as "no value" and
		// a text column would refuse to sort at all.
		return 0, true
	case CellNumber:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	case CellQuantity:
		return parseQuantity(v)
	case CellDuration:
		return parseDuration(v)
	case CellRatio:
		// Order by how far short of its own target the row is, so 0/3 sorts
		// below 2/3 below 3/3 and "which of these is not ready" is one sort.
		a, b, ok := splitRatio(v)
		if !ok {
			return 0, false
		}
		if b == 0 {
			return 0, true
		}
		return a / b, true
	}
	return 0, false
}

// CompareCell orders two cells of a known column kind. It returns -1, 0 or 1,
// and never reports a sentinel as smaller than a real value.
func CompareCell(kind CellKind, a, b string) int {
	ka, oka := SortKey(kind, a)
	kb, okb := SortKey(kind, b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return 1 // a carries no value: after b, ascending or descending
	case !okb:
		return -1
	}
	switch {
	case ka < kb:
		return -1
	case ka > kb:
		return 1
	}
	// Equal keys still need a tiebreak — and for a text column every key is
	// equal, so this is the whole comparison rather than a fallback.
	switch {
	case NaturalLess(a, b):
		return -1
	case NaturalLess(b, a):
		return 1
	}
	return 0
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil
}

func isRatio(s string) bool {
	_, _, ok := splitRatio(s)
	return ok
}

func splitRatio(s string) (a, b float64, ok bool) {
	i := strings.IndexByte(s, '/')
	if i < 0 {
		return 0, 0, false
	}
	x, err1 := strconv.ParseFloat(s[:i], 64)
	y, err2 := strconv.ParseFloat(s[i+1:], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return x, y, true
}

// quantitySuffix is the Kubernetes resource-quantity ladder as it appears in
// rendered cells: binary for memory, milli for CPU, decimal for counts.
var quantitySuffix = []struct {
	suffix string
	mult   float64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"Pi", 1 << 50},
	{"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}, {"P", 1e15},
	{"m", 0.001},
}

func parseQuantity(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	for _, q := range quantitySuffix {
		if strings.HasSuffix(s, q.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, q.suffix), 64)
			if err != nil {
				return 0, false
			}
			return n * q.mult, true
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	return n, err == nil
}

func isQuantity(s string) bool {
	// A bare number is a number, not a quantity: the column sniff must not
	// call "0" a quantity and then disagree with itself on "17".
	for _, q := range quantitySuffix {
		if strings.HasSuffix(s, q.suffix) {
			_, ok := parseQuantity(s)
			return ok
		}
	}
	return false
}

// durationUnit is what ShortHumanDuration emits.
var durationUnit = map[byte]float64{
	's': 1, 'm': 60, 'h': 3600, 'd': 86400, 'y': 365 * 86400,
}

// parseDuration reads "5d2h", "10m", "62d" — the compact form
// duration.ShortHumanDuration produces, which time.ParseDuration cannot read
// because of the d and y units.
func parseDuration(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var total float64
	i := 0
	for i < len(s) {
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == i || j >= len(s) {
			return 0, false
		}
		n, err := strconv.ParseFloat(s[i:j], 64)
		if err != nil {
			return 0, false
		}
		mult, ok := durationUnit[s[j]]
		if !ok {
			return 0, false
		}
		total += n * mult
		i = j + 1
	}
	return total, true
}

func isDuration(s string) bool {
	_, ok := parseDuration(s)
	return ok
}
