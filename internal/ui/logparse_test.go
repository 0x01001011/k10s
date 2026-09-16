package ui

import (
	"strings"
	"testing"
)

func TestPrettyLogJSONRecord(t *testing.T) {
	raw := `{"level":"info","ts":"2026-08-25T08:12:46.111Z","logger":"controller","msg":"reconciling","namespace":"default","retries":3}`
	got, lvl := prettyLog(raw)

	if lvl != lvlInfo {
		t.Errorf("level = %v, want INFO", lvl)
	}
	head, ctx, found := strings.Cut(got, logSep)
	if !found {
		t.Fatalf("expected context after the message, got %q", got)
	}
	if !strings.HasSuffix(head, "INFO reconciling") {
		t.Errorf("head = %q, want it to end with the level and message", head)
	}
	for _, want := range []string{"logger=controller", "namespace=default", "retries=3"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context %q is missing %q", ctx, want)
		}
	}
	if strings.Contains(got, `"msg"`) {
		t.Errorf("parsed line still shows raw JSON keys: %q", got)
	}
}

// The container timestamp kubectl prepends and the record's own stamp are the
// same moment; showing both is what made these lines unreadable.
func TestPrettyLogDropsDuplicateTimestamp(t *testing.T) {
	raw := `2026-08-25T15:12:46.111+00:00 {"level":"warn","ts":"2026-08-25T15:12:46.100Z","msg":"slow"}`
	got, lvl := prettyLog(raw)

	if lvl != lvlWarn {
		t.Errorf("level = %v, want WARN", lvl)
	}
	if strings.Contains(got, "2026-08-25") {
		t.Errorf("expected no full date in %q", got)
	}
	if !strings.HasPrefix(got, "15:12:46 WARN slow") {
		t.Errorf("got %q, want it to start with the time, level and message", got)
	}
}

// A JSON record whose first space falls inside a string must still parse.
func TestPrettyLogJSONWithSpacedMessage(t *testing.T) {
	raw := `{"level":"error","msg":"reconcile failed","error":"connection refused"}`
	got, lvl := prettyLog(raw)

	if lvl != lvlErr {
		t.Fatalf("level = %v, want ERROR", lvl)
	}
	if !strings.HasPrefix(got, "ERROR reconcile failed") {
		t.Errorf("got %q, want the message promoted to the front", got)
	}
	if !strings.Contains(got, `error="connection refused"`) {
		t.Errorf("got %q, want the error field quoted in the context", got)
	}
}

func TestPrettyLogLogfmt(t *testing.T) {
	raw := `level=warn msg="bucket near capacity" key=tenant:4417 used=94%`
	got, lvl := prettyLog(raw)

	if lvl != lvlWarn {
		t.Errorf("level = %v, want WARN", lvl)
	}
	if !strings.HasPrefix(got, "WARN bucket near capacity") {
		t.Errorf("got %q, want the message first", got)
	}
	if !strings.Contains(got, "key=tenant:4417") {
		t.Errorf("got %q, want the remaining pairs kept", got)
	}
}

// Prose that happens to contain "=" is not a structured record, and mangling
// it would lose the line you were reading.
func TestPrettyLogLeavesProseAlone(t *testing.T) {
	for _, raw := range []string{
		"2026-08-25T08:12:12.775Z ERROR upstream dial tcp 10.96.14.77:8080: i/o timeout attempt=1/3",
		"panic: runtime error: index out of range [3] with length 2",
		"",
	} {
		got, _ := prettyLog(raw)
		if got != raw {
			t.Errorf("prettyLog(%q) = %q, want it unchanged", raw, got)
		}
	}
}

func TestPrettyLogFindsLevelInPlainText(t *testing.T) {
	if _, lvl := prettyLog("2026-08-25T08:12:12.775Z ERROR upstream i/o timeout"); lvl != lvlErr {
		t.Errorf("level = %v, want ERROR", lvl)
	}
	if _, lvl := prettyLog("just some output"); lvl != lvlNone {
		t.Errorf("level = %v, want none", lvl)
	}
}

func TestMatchLogFilter(t *testing.T) {
	line := "08:12:47 ERROR reconcile failed │ namespace=default"
	cases := []struct {
		filter string
		want   bool
	}{
		{"", true},
		{"reconcile", true},
		{"RECONCILE", true},          // case-insensitive
		{"reconcile default", true},  // every term must match
		{"reconcile missing", false}, // one term that does not
		{"-healthz", true},           // exclusion that does not appear
		{"-reconcile", false},        // exclusion that does
		{"reconcile -default", false},
	}
	for _, c := range cases {
		if got := matchLogFilter(line, c.filter); got != c.want {
			t.Errorf("matchLogFilter(_, %q) = %v, want %v", c.filter, got, c.want)
		}
	}
}
