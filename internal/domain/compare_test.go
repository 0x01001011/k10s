package domain

import (
	"sort"
	"testing"
)

func TestSniffColumnDecidesPerColumn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   CellKind
	}{
		{"restarts", []string{"0", "17", "9"}, CellNumber},
		{"age", []string{"10m", "2h", "3d", "62d"}, CellDuration},
		{"memory", []string{"900Mi", "2Gi", "24Mi"}, CellQuantity},
		{"cpu millis", []string{"142m", "0m", "310m"}, CellQuantity},
		{"ready", []string{"1/1", "0/1", "2/2"}, CellRatio},
		{"names", []string{"pod-2", "pod-10"}, CellText},
		// One unparseable value means the column is not that type: ordering
		// it as one would drop "whenever" somewhere arbitrary.
		{"mixed", []string{"10m", "2h", "whenever"}, CellText},
		// Sentinels are skipped, not counted against the type.
		{"sparse durations", []string{"-", "10m", "pending", "3d"}, CellDuration},
		{"all sentinels", []string{"-", "n/a", "<none>"}, CellText},
	} {
		if got := SniffColumn(tc.values); got != tc.want {
			t.Errorf("%s: SniffColumn(%v) = %v, want %v", tc.name, tc.values, got, tc.want)
		}
	}
}

func TestCompareCellOrdersByValueNotByString(t *testing.T) {
	for _, tc := range []struct {
		kind CellKind
		a, b string
		want int
	}{
		// The bug the card names: "10m" < "2h" is false as a string.
		{CellDuration, "10m", "2h", -1},
		{CellDuration, "2h", "3d", -1},
		{CellDuration, "3d", "62d", -1},
		{CellDuration, "5d", "5d2h", -1},
		// "2Gi" > "900Mi" is false as a string too.
		{CellQuantity, "900Mi", "2Gi", -1},
		{CellQuantity, "250m", "1", -1},
		{CellNumber, "9", "17", -1},
		{CellNumber, "0", "0", 0},
		// A ratio short of its target sorts below a satisfied one.
		{CellRatio, "0/1", "1/1", -1},
		{CellRatio, "1/2", "2/2", -1},
	} {
		if got := CompareCell(tc.kind, tc.a, tc.b); got != tc.want {
			t.Errorf("CompareCell(%v, %q, %q) = %d, want %d", tc.kind, tc.a, tc.b, got, tc.want)
		}
		// Antisymmetry: a mis-signed comparator gives a different order
		// depending on which way sort.Slice happens to walk the slice.
		if got := CompareCell(tc.kind, tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareCell(%v, %q, %q) = %d, want %d (not antisymmetric)",
				tc.kind, tc.b, tc.a, got, -tc.want)
		}
	}
}

// TestSentinelsSortLastBothDirections is the one that matters in use: a
// descending CPU sort whose top ten rows are all unmetricked pods has
// answered a different question than the one that was asked.
func TestSentinelsSortLastBothDirections(t *testing.T) {
	for _, s := range []string{"-", "", "n/a", "<none>", "pending"} {
		if got := CompareCell(CellNumber, s, "0"); got != 1 {
			t.Errorf("%q vs a real value = %d, want 1 (sentinel last)", s, got)
		}
		if got := CompareCell(CellNumber, "0", s); got != -1 {
			t.Errorf("a real value vs %q = %d, want -1 (sentinel last)", s, got)
		}
	}
	if got := CompareCell(CellNumber, "-", "n/a"); got != 0 {
		t.Errorf("two sentinels compared %d, want 0", got)
	}
}

// Descending is the caller negating the comparator, so this checks sentinels
// survive that negation and stay at the bottom.
func TestDescendingKeepsSentinelsAtTheBottom(t *testing.T) {
	rows := []string{"5", "-", "17", "pending", "0"}
	kind := SniffColumn(rows)

	sort.SliceStable(rows, func(i, j int) bool {
		_, oki := SortKey(kind, rows[i])
		_, okj := SortKey(kind, rows[j])
		if !oki {
			return false // a sentinel is never "before"
		}
		if !okj {
			return true
		}
		return CompareCell(kind, rows[i], rows[j]) > 0 // descending
	})

	if rows[0] != "17" {
		t.Errorf("descending sort starts with %q, want 17: %v", rows[0], rows)
	}
	for _, s := range rows[len(rows)-2:] {
		if s != "-" && s != "pending" {
			t.Errorf("a real value sank below a sentinel: %v", rows)
		}
	}
}

func TestEqualValuesCompareEqual(t *testing.T) {
	// Two pods with the same CPU must not swap places between frames.
	if got := CompareCell(CellQuantity, "310Mi", "310Mi"); got != 0 {
		t.Errorf("identical quantities compared %d, want 0", got)
	}
	// Same size, different spelling: the keys tie, so the tiebreak is name
	// order, and "1Gi" sorts before "1024Mi" because 1 < 1024.
	if got := CompareCell(CellQuantity, "1Gi", "1024Mi"); got != -1 {
		t.Errorf("1Gi vs 1024Mi = %d, want -1 (equal keys fall back to name order)", got)
	}
}

func TestParseDurationRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "d", "5", "5x", "abc", "5d2"} {
		if _, ok := parseDuration(s); ok {
			t.Errorf("parseDuration(%q) accepted a value it cannot mean", s)
		}
	}
}
