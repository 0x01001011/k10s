package k8s

import (
	"strings"
	"testing"
)

// The values an operator has to pick from are rarely a bare list on the
// object. ArgoCD's rollback revisions are a field of each entry in
// .status.history, and Kargo's verification ids are two arrays deep — so a
// walk that only follows map keys can offer suggestions for neither.
func TestLensPathListWalksIndexesAndWildcards(t *testing.T) {
	obj := map[string]any{
		"status": map[string]any{
			"instanceNames": []any{"db-1", "db-2"},
			"history": []any{
				map[string]any{"revision": "aaa111", "id": int64(1)},
				map[string]any{"revision": "bbb222", "id": int64(2)},
			},
			"freightHistory": []any{
				map[string]any{"verificationHistory": []any{
					map[string]any{"id": "ver-1"},
					map[string]any{"id": "ver-2"},
				}},
				map[string]any{"verificationHistory": []any{
					map[string]any{"id": "ver-0"},
				}},
			},
		},
	}

	for _, c := range []struct {
		path string
		want string
	}{
		// The plain walk still works — this is the CNPG case.
		{".status.instanceNames", "db-1,db-2"},
		{".status.history[*].revision", "aaa111,bbb222"},
		{".status.history[0].revision", "aaa111"},
		{".status.history[1].revision", "bbb222"},
		// Two wildcards, flattened in order: the current freight's
		// verifications come before the previous freight's.
		{".status.freightHistory[*].verificationHistory[*].id", "ver-1,ver-2,ver-0"},
		{".status.freightHistory[0].verificationHistory[*].id", "ver-1,ver-2"},
		// Out of range is empty, not a panic: a Stage that has never been
		// promoted has no freight history at all.
		{".status.history[9].revision", ""},
		{".status.nope[*].revision", ""},
		// A non-string leaf is skipped rather than rendered as Go syntax.
		{".status.history[*].id", ""},
	} {
		got := strings.Join(lensPathList(obj, c.path), ",")
		if got != c.want {
			t.Errorf("lensPathList(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
