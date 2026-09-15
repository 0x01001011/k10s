package k8s

import (
	"testing"
	"time"

	"github.com/0x01001011/k10s/internal/lens"
)

func colsFor(t *testing.T, packYAML, kindKey string) []lensCol {
	t.Helper()
	p := mustPack(t, packYAML)
	for _, k := range p.Kinds {
		if k.Key == kindKey {
			cols, err := compileColumns(p, k)
			if err != nil {
				t.Fatalf("compileColumns: %v", err)
			}
			return cols
		}
	}
	t.Fatalf("kind %q not in pack", kindKey)
	return nil
}

const formatPack = `
name: fmt
kinds:
  - key: fmt-things
    name: Things
    short: fmtt
    group: Fmt
    gvr: f.example.com/v1/things
    columns:
      - {header: NAME, path: .metadata.name}
      - {header: AGE, path: .metadata.creationTimestamp, format: age}
      - {header: SIZE, path: .spec.size, format: bytes}
      - {header: COUNT, path: .status.count, format: int}
      - {header: READY, path: .status.ready, format: bool}
      - {header: REV, path: .status.revision, truncate: 7}
`

func TestLensColumnFormats(t *testing.T) {
	cols := colsFor(t, formatPack, "fmt-things")
	sixDaysAgo := time.Now().Add(-6 * 24 * time.Hour).UTC().Format(time.RFC3339)

	obj := map[string]any{
		"metadata": map[string]any{
			"name":              "thing-1",
			"creationTimestamp": sixDaysAgo,
		},
		"spec": map[string]any{"size": float64(1073741824)},
		// JSON numbers decode to float64 — "3" must not render as "3e+00".
		"status": map[string]any{
			"count":    float64(3),
			"ready":    true,
			"revision": "6f4c1b2a9e8d7c6b5a4938271605f4e3d2c1b0a9",
		},
	}

	want := map[string]string{
		"NAME":  "thing-1",
		"AGE":   age(time.Now().Add(-6 * 24 * time.Hour)),
		"COUNT": "3",
		"READY": "true",
		"REV":   "6f4c1b2",
	}
	got := map[string]string{}
	for _, c := range cols {
		got[c.header] = lensCell(c, obj)
	}
	for h, w := range want {
		if got[h] != w {
			t.Errorf("%s = %q, want %q", h, got[h], w)
		}
	}
	if got["SIZE"] != "1Gi" {
		t.Errorf("SIZE = %q, want %q", got["SIZE"], "1Gi")
	}
	if len([]rune(got["REV"])) != 7 {
		t.Errorf("truncate did not cut to 7 runes: %q", got["REV"])
	}
}

// An exact table, because a substring check for "G" passes even if
// humanBytesLens returns the constant "G" — and an off-by-one in the
// "KMGTPE"[exp] index would ship green.
func TestHumanBytesLens(t *testing.T) {
	cases := map[int64]string{
		0:                "0",
		512:              "512",
		1023:             "1023",
		1024:             "1Ki",
		1048576:          "1Mi",
		1073741824:       "1Gi",
		1099511627776:    "1Ti",
		1125899906842624: "1Pi",
	}
	for in, want := range cases {
		if got := humanBytesLens(in); got != want {
			t.Errorf("humanBytesLens(%d) = %q, want %q", in, got, want)
		}
	}
	// A value that is not a number passes through untouched — Kubernetes
	// quantities arrive as "10Gi" strings on some fields.
	if got := formatLensCell("bytes", 0, "10Gi"); got != "10Gi" {
		t.Errorf("an unparseable bytes value = %q, want it verbatim", got)
	}
}

func TestLensAgeOfAnUnparseableValueIsADash(t *testing.T) {
	cols := colsFor(t, formatPack, "fmt-things")
	obj := map[string]any{"metadata": map[string]any{"creationTimestamp": "not a time"}}
	for _, c := range cols {
		if c.header == "AGE" {
			if got := lensCell(c, obj); got != "-" {
				t.Errorf("unparseable age = %q, want %q", got, "-")
			}
		}
	}
}

func TestLensTruncateDoesNotSplitARune(t *testing.T) {
	// Seven runes of a multi-byte string must stay seven valid runes, not
	// fourteen bytes cut through the middle of one.
	if got := truncRunes("日本語のテキストです", 7); len([]rune(got)) != 7 {
		t.Errorf("truncRunes cut to %d runes: %q", len([]rune(got)), got)
	}
	if got := truncRunes("short", 40); got != "short" {
		t.Errorf("truncRunes shortened a short string: %q", got)
	}
}

const missingPack = `
name: missing
kinds:
  - key: missing-things
    name: Things
    short: mst
    group: Missing
    gvr: m.example.com/v1/things
    columns:
      - {header: NAME, path: .metadata.name}
      - {header: PHASE, path: .status.phase}
      - {header: REASON, path: ".status.conditions[0].reason"}
`

// Half the status fields across the five packs are optional. A path into a
// field that is not there must be an empty cell, never an error and never a
// panic.
//
// Case (b) is the one AllowMissingKeys does NOT cover: the jsonpath evaluator
// returns "array index out of bounds" from evalArray unconditionally, without
// consulting the flag that evalField checks. An implementation that only sets
// AllowMissingKeys(true) passes (a) and fails (b).
func TestLensMissingPathIsEmptyCell(t *testing.T) {
	cols := colsFor(t, missingPack, "missing-things")

	cases := map[string]map[string]any{
		"no .status at all": {
			"metadata": map[string]any{"name": "a"},
		},
		"empty conditions list": {
			"metadata": map[string]any{"name": "b"},
			"status":   map[string]any{"phase": "Running", "conditions": []any{}},
		},
	}

	for name, obj := range cases {
		t.Run(name, func(t *testing.T) {
			row := make([]string, 0, len(cols))
			for _, c := range cols {
				row = append(row, lensCell(c, obj))
			}
			if len(row) != len(cols) {
				t.Fatalf("row is %d wide, want %d — a missing cell must not shorten the row", len(row), len(cols))
			}
			if row[len(row)-1] != "" {
				t.Errorf("REASON = %q, want an empty cell", row[len(row)-1])
			}
		})
	}
}

// T27 already validated every shipped path. This pins that compilation agrees
// with validation, so the two cannot drift.
func TestCompileColumnsAcceptsEveryShippedPack(t *testing.T) {
	packs, errs := lens.Builtins()
	for _, err := range errs {
		t.Fatalf("builtin pack failed to parse: %v", err)
	}
	for _, p := range packs {
		for _, k := range p.Kinds {
			if _, err := compileColumns(p, k); err != nil {
				t.Errorf("lens %q kind %q: %v", p.Name, k.Key, err)
			}
		}
	}
}

func TestCompileColumnsAttachesSeverityTables(t *testing.T) {
	cols := colsFor(t, argoLikePack, "al-apps")
	var health lensCol
	for _, c := range cols {
		if c.header == "HEALTH" {
			health = c
		}
	}
	if !health.hasSev {
		t.Fatal("HEALTH declares severity: health but carries no table")
	}
	if got := health.sev.Level("Degraded"); got != lens.LevelError {
		t.Errorf("Degraded = %v, want error", got)
	}
	// The unlisted-value case, which is how CNPG phases work.
	if got := health.sev.Level("Something else entirely"); got != lens.LevelWarn {
		t.Errorf("unlisted value = %v, want the table's default (warn)", got)
	}
}
