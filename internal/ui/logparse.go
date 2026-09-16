package ui

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Most things running in a cluster log structured records, not prose: a JSON
// object per line from zap/logr, or logfmt from anything older. Printed raw,
// one record is three screen rows of punctuation with the message buried in
// the middle — see any controller's log.
//
// So a line is parsed before it is drawn: the timestamp, the level and the
// message come first, and everything else follows as key=value context. The
// raw text is kept as it arrived (the viewer can show it again with "t") —
// parsing only changes what is rendered, never what was received.

// logLevel ranks severity so the viewer can hide everything below a floor.
type logLevel int

const (
	lvlNone logLevel = iota // no level found on the line
	lvlDebug
	lvlInfo
	lvlWarn
	lvlErr
)

// String is the token shown in the status line and used in filters.
func (l logLevel) String() string {
	switch l {
	case lvlDebug:
		return "DEBUG"
	case lvlInfo:
		return "INFO"
	case lvlWarn:
		return "WARN"
	case lvlErr:
		return "ERROR"
	}
	return "ALL"
}

// levelOf maps one token to a severity. It is the single place level names
// are known: both the colouring and the level filter go through it.
func levelOf(tok string) (logLevel, bool) {
	switch strings.ToUpper(strings.Trim(tok, "[](){}\"',:=")) {
	case "ERROR", "ERR", "FATAL", "PANIC", "CRITICAL", "CRIT", "SEVERE":
		return lvlErr, true
	case "WARN", "WARNING":
		return lvlWarn, true
	case "INFO", "NOTICE":
		return lvlInfo, true
	case "DEBUG", "TRACE":
		return lvlDebug, true
	}
	return lvlNone, false
}

// Field names that carry the three parts worth promoting to the front of the
// line. Everything else keeps its key and trails behind the message.
var (
	msgKeys   = []string{"msg", "message", "MESSAGE", "log"}
	lvlKeys   = []string{"level", "lvl", "severity", "loglevel", "log.level"}
	tsKeys    = []string{"ts", "time", "timestamp", "@timestamp", "datetime"}
	leadExtra = []string{"logger", "caller", "component", "error", "err"}
)

// logSep separates the message from the trailing key=value context. The
// renderer dims everything after it, which is what makes the message the
// part your eye lands on.
const logSep = " │ "

// prettyLog turns one raw log line into the text the viewer draws, plus the
// severity it was written at. A line that is not structured comes back
// unchanged — an unparseable line is still a line you need to read.
func prettyLog(raw string) (string, logLevel) {
	ts, body := splitTimestamp(raw)
	fields, ok := parseStructured(body)
	if !ok {
		return raw, scanLevel(body)
	}
	return renderFields(ts, fields)
}

// splitTimestamp peels off the RFC3339 stamp kubectl prepends, so a record
// that also carries its own "ts" field is not stamped twice.
func splitTimestamp(raw string) (string, string) {
	f, rest, found := strings.Cut(raw, " ")
	// A stamp always starts with its year. Without that test, a JSON record
	// whose first space falls inside a string ("msg":"reconcile failed") has
	// half of itself eaten as a timestamp and never parses.
	if !found || f == "" || f[0] < '0' || f[0] > '9' {
		return "", raw
	}
	if !strings.Contains(f, "T") || !looksLikeTimestamp(f) {
		return "", raw
	}
	return shortTime(f), strings.TrimLeft(rest, " ")
}

type kv struct{ k, v string }

func parseStructured(body string) ([]kv, bool) {
	if strings.HasPrefix(body, "{") {
		return parseJSONFields(body)
	}
	return parseLogfmt(body)
}

// parseJSONFields reads a JSON object into key/value pairs. Key order in the
// object is not preserved (Go maps do not keep it), so keys are sorted —
// which also means the same producer renders the same way every time.
func parseJSONFields(body string) ([]kv, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil || len(raw) == 0 {
		return nil, false
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kv, 0, len(keys))
	for _, k := range keys {
		out = append(out, kv{k, jsonValue(raw[k])})
	}
	return out, true
}

// jsonValue unquotes strings and leaves everything else (numbers, objects,
// arrays) in its compact JSON form.
func jsonValue(r json.RawMessage) string {
	var s string
	if err := json.Unmarshal(r, &s); err == nil {
		return s
	}
	return string(r)
}

// parseLogfmt reads `k=v k="v with spaces"` records. It is deliberately
// strict — every token must be a pair and there must be at least two — so a
// sentence containing "x=1" is not mistaken for a structured record.
func parseLogfmt(body string) ([]kv, bool) {
	var out []kv
	for i := 0; i < len(body); {
		for i < len(body) && body[i] == ' ' {
			i++
		}
		if i >= len(body) {
			break
		}
		start := i
		for i < len(body) && body[i] != '=' && body[i] != ' ' {
			i++
		}
		if i >= len(body) || body[i] != '=' || i == start {
			return nil, false
		}
		key := body[start:i]
		i++
		var val string
		if i < len(body) && body[i] == '"' {
			j := i + 1
			for j < len(body) && body[j] != '"' {
				if body[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(body) {
				return nil, false // unterminated quote: not a record we understand
			}
			var err error
			if val, err = strconv.Unquote(body[i : j+1]); err != nil {
				val = body[i+1 : j]
			}
			i = j + 1
		} else {
			j := i
			for j < len(body) && body[j] != ' ' {
				j++
			}
			val, i = body[i:j], j
		}
		out = append(out, kv{key, val})
	}
	if len(out) < 2 {
		return nil, false
	}
	return out, true
}

// renderFields lays the parsed record out as `time LEVEL message │ k=v …`.
func renderFields(ts string, fields []kv) (string, logLevel) {
	taken := map[string]bool{}
	pick := func(names []string) string {
		for _, n := range names {
			for _, f := range fields {
				if f.k == n && !taken[f.k] {
					taken[f.k] = true
					return f.v
				}
			}
		}
		return ""
	}

	msg := pick(msgKeys)
	lvlTok := pick(lvlKeys)
	if ts == "" {
		ts = shortTime(pick(tsKeys))
	} else {
		pick(tsKeys) // drop the record's own stamp; the container one is shown
	}

	lvl, ok := levelOf(lvlTok)
	level := ""
	if ok {
		level = lvl.String()
	} else if lvlTok != "" {
		level = strings.ToUpper(lvlTok)
	}

	// Context fields: the ones that identify who logged and what failed come
	// first, the rest keep their sorted order.
	var extras []string
	add := func(f kv) { extras = append(extras, f.k+"="+quoteIfSpaced(f.v)) }
	for _, n := range leadExtra {
		for _, f := range fields {
			if f.k == n && !taken[f.k] {
				taken[f.k] = true
				add(f)
			}
		}
	}
	for _, f := range fields {
		if !taken[f.k] {
			taken[f.k] = true
			add(f)
		}
	}

	head := strings.Join(nonEmpty(ts, level, msg), " ")
	switch {
	case len(extras) == 0:
		return head, lvl
	case head == "":
		return strings.Join(extras, " "), lvl
	}
	return head + logSep + strings.Join(extras, " "), lvl
}

func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func quoteIfSpaced(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, " \t") {
		return strconv.Quote(v)
	}
	return v
}

// shortTime reduces a timestamp to the time of day — the date is the same
// for nearly every line on screen, and the clock is what you correlate with.
// Anything it cannot read is passed through untouched.
func shortTime(v string) string {
	if v == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z0700", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Format("15:04:05")
		}
	}
	// Epoch seconds, the other thing zap emits by default.
	if f, err := strconv.ParseFloat(v, 64); err == nil && f > 1e8 && f < 1e11 {
		return time.Unix(int64(f), 0).Format("15:04:05")
	}
	return v
}

// scanLevel finds the severity of an unstructured line by looking at its
// first few tokens, which is where every text logger puts it.
func scanLevel(s string) logLevel {
	for i, f := range strings.Fields(s) {
		if i >= 6 {
			break
		}
		if l, ok := levelOf(f); ok {
			return l
		}
	}
	return lvlNone
}

// matchLogFilter reports whether text passes the filter box. Terms are
// space-separated and all must match (case-insensitively); a term prefixed
// with "-" must not appear. That covers the two things you actually do while
// reading a log — narrow to one thing, and drop the noise.
func matchLogFilter(text, filter string) bool {
	if strings.TrimSpace(filter) == "" {
		return true
	}
	low := strings.ToLower(text)
	for _, term := range strings.Fields(strings.ToLower(filter)) {
		neg := strings.HasPrefix(term, "-") && len(term) > 1
		if neg {
			term = term[1:]
		}
		if strings.Contains(low, term) == neg {
			return false
		}
	}
	return true
}
