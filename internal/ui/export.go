package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/0x01001011/k10s/internal/config"
)

// Context export: ctrl+y writes what is on screen — the table, the selected
// object's YAML and its describe output — to one file and toasts the absolute
// path. A path is the thing you paste into a ticket anyway, and it is the only
// mechanism that works everywhere: the clipboard route was cut because
// shelling out to xclip/pbcopy needs binaries a headless box does not have
// (and a new Go module, which is forbidden), while OSC 52 truncates well below
// a realistic pod YAML.
//
// Everything here runs in a tea.Cmd, off the render goroutine: the export
// reads the backend and writes a file, and View may do neither.

// redacted is what replaces a value rather than what removes it: a key that
// silently vanished reads as "this object has no password", which is a
// different and more dangerous claim than "this export will not show it".
const redacted = "***REDACTED***"

// lastApplied embeds a JSON copy of the entire object — including the very
// Secret data redacted everywhere else — so it is dropped whole rather than
// blanked. kubectl's own describer skips it for the same reason
// (kubectl@v0.36.4 pkg/describe/describe.go:100 skipAnnotations), but the
// export cannot depend on the backend having done it: a generic describer, a
// CR, or the demo backend all hand it straight through.
const lastApplied = "kubectl.kubernetes.io/last-applied-configuration"

// sensitiveKey reports whether a field name names a credential. There was no
// prior art to reuse — nothing in the tree redacted anything before this — so
// the list is substrings of the lowercased key, chosen to catch the shapes
// credentials actually appear in (DB_PASSWORD, clientSecret, .dockerconfigjson)
// while leaving references readable. "secret" alone is deliberately NOT here:
// it would blank secretName/secretRef/secretKeyRef, which point at a Secret
// rather than containing one, and losing them makes an export useless for
// exactly the debugging it exists for. Secret payloads are covered by the
// kind: Secret rule instead.
// The key is normalised to letters and digits first. Separators are not
// cosmetic here: kubectl's generic describer renders field names through
// smartLabelFor, which camelCase-splits them into Title Words, so a CR's
// clientSecret prints as "Client Secret:". Every kind NOT in describe.go's
// kindToGK map — which is every CR and every lens-pack kind — goes through that
// describer, so matching raw spellings would leak exactly the objects the lens
// packs exist to show. Normalising also covers client-secret and API_KEY free.
func sensitiveKey(k string) bool {
	k = compactKey(k)
	// A reference is a pointer to a credential, not the credential itself:
	// keeping secretName / secretRef / secretKeyRef readable is what makes an
	// export useful for the debugging it exists for.
	if strings.HasSuffix(k, "ref") || strings.HasSuffix(k, "secretname") {
		return false
	}
	for _, s := range []string{
		"password", "passwd", "passphrase",
		"token", "credential",
		"apikey",
		"accesskey",
		"privatekey",
		"secretkey",
		"clientsecret",
		"dockerconfigjson", "dockercfg",
	} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// compactKey lowercases and drops every separator, so "Client Secret",
// "client-secret", "client_secret", "clientSecret" and ".dockerconfigjson" all
// collapse to one spelling to match against.
func compactKey(k string) string {
	var b strings.Builder
	b.Grow(len(k))
	for _, r := range strings.ToLower(k) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// redactFlag blanks the value of a credential passed as a command-line flag —
// --password=hunter2, --client-secret hunter2 — wherever that string appears.
// Neither key-based walker catches these: in YAML the flag is an element of
// .spec.containers[].args (a list of plain strings, not a key/value pair), and
// in describe output it prints as a bare line with no "key: value" shape at
// all. Exporters, grafana and oidc proxies all routinely take credentials this
// way, so this is a common shape rather than an exotic one.
func redactFlag(s string) string {
	for {
		i := strings.Index(s, "--")
		if i < 0 {
			return s
		}
		rest := s[i+2:]
		// The flag name ends at '=', whitespace, or the end of the token.
		end := strings.IndexAny(rest, "= \t\"',")
		if end <= 0 {
			return s[:i+2] + redactFlagTail(rest)
		}
		if !sensitiveKey(rest[:end]) {
			return s[:i+2+end] + redactFlagTail(rest[end:])
		}
		// Blank from the separator to the end of the value token.
		sep := rest[end]
		val := rest[end+1:]
		stop := len(val)
		if sep == '=' || sep == ' ' || sep == '\t' {
			if j := strings.IndexAny(val, " \t\"',"); j >= 0 {
				stop = j
			}
		} else {
			// A quote or comma directly after the name is not a value.
			return s[:i+2+end] + redactFlagTail(rest[end:])
		}
		if stop == 0 {
			return s[:i+2+end] + redactFlagTail(rest[end:])
		}
		return s[:i+2+end+1] + redacted + redactFlagTail(val[stop:])
	}
}

// redactFlagTail keeps scanning the remainder for further flags.
func redactFlagTail(s string) string {
	if !strings.Contains(s, "--") {
		return s
	}
	return redactFlag(s)
}

// redactYAML strips credentials structurally: it parses, walks and re-marshals,
// so a value is matched by the field that holds it rather than by how it
// happens to be laid out. Input it cannot parse still does not pass through
// raw — it falls back to the line scrubber, because "could not parse" is not a
// reason to print a Secret.
func redactYAML(s string) string {
	var tree any
	if err := sigsyaml.Unmarshal([]byte(s), &tree); err != nil {
		return redactText(s)
	}
	out, err := sigsyaml.Marshal(redactTree(tree))
	if err != nil {
		return redactText(s)
	}
	return string(out)
}

func redactTree(v any) any {
	switch t := v.(type) {
	case string:
		// Reached for every scalar, including the elements of an args list,
		// where a credential has no key to match on — only a --flag=value.
		return redactFlag(t)
	case []any:
		for i, e := range t {
			t[i] = redactTree(e)
		}
		return t
	case map[string]any:
		delete(t, lastApplied)

		// A Secret's whole payload is credential, whatever the keys are
		// called. Checking kind on the map that holds data (rather than only
		// at the document root) also covers a Secret nested in a List.
		if kind, _ := t["kind"].(string); kind == "Secret" {
			for _, f := range []string{"data", "stringData"} {
				if d, ok := t[f].(map[string]any); ok {
					for k := range d {
						d[k] = redacted
					}
				}
			}
		}

		// The {name, value} shape: an env var's key is "value", so key-based
		// matching alone reads DB_PASSWORD's contents straight out.
		if n, ok := t["name"].(string); ok && sensitiveKey(n) {
			if _, has := t["value"]; has {
				t["value"] = redacted
			}
		}

		for k, val := range t {
			if sensitiveKey(k) {
				// Replaced whole, not walked: a credential held as a map or a
				// list is still a credential.
				t[k] = redacted
				continue
			}
			t[k] = redactTree(val)
		}
		return t
	default:
		return v
	}
}

// redactText is the scrubber for everything that is not a parseable object:
// describe output, shelled-out command output, and YAML too broken to parse.
// It works line by line on "key: value", which is the shape both describe and
// YAML print, and drops an indented block whose header was sensitive.
func redactText(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	skipOver := -1 // drop lines indented deeper than this; -1 = not skipping

	for _, ln := range lines {
		ind := len(ln) - len(strings.TrimLeft(ln, " \t"))
		if skipOver >= 0 {
			if strings.TrimSpace(ln) == "" || ind > skipOver {
				continue
			}
			skipOver = -1
		}

		trimmed := strings.TrimSpace(ln)
		key, rest, hasColon := strings.Cut(trimmed, ":")
		if !hasColon {
			// No key to match on — but a bare "--password=hunter2" line from
			// a describer's Args section is still a credential.
			out = append(out, redactFlag(ln))
			continue
		}
		key = strings.Trim(strings.TrimPrefix(key, "- "), `"'`)

		if strings.Contains(trimmed, lastApplied) {
			skipOver = ind
			continue
		}
		if !sensitiveKey(key) {
			// The key is innocent; the value may still carry a flag, as
			// "Args: --token=hunter2" does.
			out = append(out, redactFlag(ln))
			continue
		}
		// A block scalar ("password: |") keeps its payload on the following,
		// deeper-indented lines — blanking only this line would leak all of it.
		if v := strings.TrimSpace(rest); v == "" || v == "|" || v == ">" ||
			strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">") {
			skipOver = ind
		}
		out = append(out, ln[:ind]+key+": "+redacted)
	}
	return strings.Join(out, "\n")
}

// tildePath is a display shortening only — exportDoneMsg still carries the
// absolute path, and the toast is the one place the user reads it.
func tildePath(p string) string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" || !strings.HasPrefix(p, h+string(os.PathSeparator)) {
		return p
	}
	return "~" + p[len(h):]
}

// exportDoneMsg lands after the export file has been written (or failed to be).
type exportDoneMsg struct {
	path string
	err  error
}

// exportDir is where exports land: next to the config file, so one directory
// holds everything k10s owns. K10S_EXPORT_DIR overrides it (tests, and a box
// with no writable home); os.TempDir is the last resort so the key never does
// nothing.
func exportDir() string {
	if d := os.Getenv("K10S_EXPORT_DIR"); d != "" {
		return d
	}
	if p := config.Path(); p != "" {
		return filepath.Join(filepath.Dir(p), "exports")
	}
	return filepath.Join(os.TempDir(), "k10s-exports")
}

// safeSeg keeps a path segment to what a filename may hold. Namespaces and
// object names are DNS labels, but a lens kind key or a CR name reaches here
// too, and none of them get to write outside exportDir.
func safeSeg(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		}
		return '-'
	}, s)
	return strings.Trim(s, ".-")
}

// exportCmd snapshots the current view on the event loop — table rows are
// built by the row builder, which owns the packs' compiled JSONPaths and is
// not safe to touch from anywhere else — and does the reads and the write in
// the returned Cmd.
func (m *Model) exportCmd() tea.Cmd {
	r := m.curKind()
	kind, ns, name := r.Key, m.curNamespace(), m.curName()
	cols, rows := m.tableData()
	info := m.src.ClusterInfo()
	src := m.src

	label := r.Short
	if name != "" {
		label += "/" + name
	}
	m.toast = "… exporting " + label
	m.startBusy("export " + label)

	var b strings.Builder
	fmt.Fprintf(&b, "# k10s export — %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "# context: %s   cluster: %s\n", info.Context, info.Cluster)
	fmt.Fprintf(&b, "# kind: %s   namespace: %s   name: %s\n", kind, ns, name)
	// The banner names the annotation obliquely on purpose: spelling the key
	// out here would put the string it promises to remove back in the file.
	b.WriteString("# Secret data, the last-applied annotation and credential-like\n")
	b.WriteString("# fields are redacted. Check before sharing anyway.\n\n")
	fmt.Fprintf(&b, "## TABLE — %s (namespace %s, %d rows)\n\n", kind, ns, len(rows))
	b.WriteString(redactText(tableText(cols, rows)))
	header := b.String()

	return func() tea.Msg {
		var b strings.Builder
		b.WriteString(header)

		if name != "" {
			y, yerr := src.YAML(kind, ns, name)
			b.WriteString("\n\n## YAML\n\n")
			b.WriteString(section(y, yerr, redactYAML))

			d, derr := src.Describe(kind, ns, name)
			b.WriteString("\n\n## DESCRIBE\n\n")
			b.WriteString(section(d, derr, redactText))
		}
		b.WriteString("\n")

		path, err := writeExport(kind, ns, name, b.String())
		return exportDoneMsg{path: path, err: err}
	}
}

// section renders one backend read. A failure is recorded in the file rather
// than aborting the export: a describe that 403s should not cost you the YAML
// you could read.
func section(body string, err error, scrub func(string) string) string {
	if err != nil {
		return "(unavailable: " + err.Error() + ")"
	}
	return scrub(body)
}

// tableText renders the visible table as plain columns. It deliberately does
// not go near view.go's renderer: that one emits ANSI and zone markers, which
// are noise in a file, and it is on the render path.
func tableText(cols []string, rows [][]string) string {
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = len(c)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(w) && len(c) > w[i] {
				w[i] = len(c)
			}
		}
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		for i, c := range cells {
			if i >= len(w) {
				break
			}
			b.WriteString(c)
			if i < len(w)-1 {
				b.WriteString(strings.Repeat(" ", w[i]-len(c)+2))
			}
		}
		b.WriteString("\n")
	}
	writeRow(cols)
	for _, r := range rows {
		writeRow(r)
	}
	return b.String()
}

// writeExport creates the file without ever overwriting one: O_EXCL, then a
// counter. Two exports of the same object in the same second are the common
// case (press the key twice), and silently replacing the first would lose the
// snapshot someone was about to paste.
func writeExport(kind, ns, name, body string) (string, error) {
	dir := exportDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Short on purpose: the toast is clipped at half the terminal width
	// (view.go:1129), and a path that does not fit is a path nobody can paste.
	// The directory already says "k10s export" and the file's own header
	// carries the full RFC3339 timestamp, so the name only needs enough to
	// tell two exports apart at a glance. The namespace is left out for the
	// same reason — it is in the file's header, and two same-named objects in
	// different namespaces still get separate files via the counter below.
	stem := strings.Join([]string{safeSeg(kind), safeSeg(name)}, "-")
	stem = strings.Trim(stem, "-") + "-" + time.Now().Format("150405")

	for n := 0; n < 100; n++ {
		p := filepath.Join(dir, stem+".txt")
		if n > 0 {
			p = filepath.Join(dir, fmt.Sprintf("%s-%d.txt", stem, n+1))
		}
		// 0600: the export is redacted, not sanitised — it still holds every
		// non-credential detail of a production object.
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.WriteString(body)
		cerr := f.Close()
		if werr != nil {
			return "", werr
		}
		if cerr != nil {
			return "", cerr
		}
		return filepath.Abs(p)
	}
	return "", fmt.Errorf("too many exports of %s/%s this second", ns, name)
}
