package mock

import (
	"testing"

	"github.com/0x01001011/k10s/internal/lens"
)

// The vm-agents fixture is only correct relative to the shipped pack: a column
// added to victoriametrics.yaml makes every row here one cell short, and a
// severity value renamed makes the demo's colours a lie. Check both against the
// real pack, and check the rows are worst-first — the demo does not sort.
func TestDemoVMAgentsMatchesShippedPack(t *testing.T) {
	_, k, ok := lensKindOf("vm-agents")
	if !ok {
		t.Fatal("shipped packs have no \"vm-agents\" kind")
	}
	rows := demoLensRows["vm-agents"]
	if len(rows) == 0 {
		t.Fatal("no demo rows for vm-agents")
	}

	s := New(lensDemoContext)
	worst := 0
	for _, r := range rows {
		if len(r) != len(k.Columns) {
			t.Fatalf("row %v has %d cells, pack declares %d columns", r, len(r), len(k.Columns))
		}
		rank := 3 // lens.LevelOK
		for i, c := range k.Columns {
			if c.Severity == "" {
				continue
			}
			lvl := s.CellLevel("vm-agents", c.Header, r[i])
			if lvl == "" {
				t.Errorf("row %v: %s=%q grades to nothing — value not in the pack's %q table", r, c.Header, r[i], c.Severity)
				continue
			}
			if got := rankOf(lvl); got < rank {
				rank = got
			}
		}
		if rank < worst {
			t.Errorf("row %v sits below a healthier row — fixture must be worst-first", r)
		}
		worst = rank
	}
}

func rankOf(level string) int {
	for _, l := range []lens.Level{lens.LevelOK, lens.LevelWarn, lens.LevelError, lens.LevelUnknown} {
		if l.String() == level {
			return l.Rank()
		}
	}
	return 3
}
