package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/mock"
)

// The literal a leaking export would contain. Every redaction test asserts on
// this one string so a new leak path fails loudly rather than silently.
const leak = "hunter2-SUPER-SECRET"

// A Secret whose data survives ONLY if redaction fails: no other field of this
// object carries the marker, so a single Contains check is a complete test of
// the Secret path.
const secretYAML = `apiVersion: v1
kind: Secret
type: Opaque
metadata:
  name: db-credentials
  namespace: default
data:
  password: ` + leak + `
  username: ` + leak + `
stringData:
  bootstrap.env: ` + leak + `
`

func TestRedactSecretDataAndStringData(t *testing.T) {
	got := redactYAML(secretYAML)
	if strings.Contains(got, leak) {
		t.Fatalf("Secret data survived redaction:\n%s", got)
	}
	// Redaction must not eat the shape — an export with no keys left is
	// useless for the ticket it was pasted into.
	for _, want := range []string{"kind: Secret", "password", "stringData", "bootstrap.env"} {
		if !strings.Contains(got, want) {
			t.Errorf("redacted Secret lost %q:\n%s", want, got)
		}
	}
}

// The annotation embeds a copy of the whole object, secret data included, so
// redacting .data while leaving this behind redacts nothing.
func TestRedactLastAppliedConfiguration(t *testing.T) {
	in := `apiVersion: v1
kind: Secret
metadata:
  name: db-credentials
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"kind":"Secret","data":{"password":"` + leak + `"}}
    app.kubernetes.io/name: db
data:
  password: ` + leak + `
`
	got := redactYAML(in)
	if strings.Contains(got, leak) {
		t.Fatalf("last-applied-configuration leaked the object:\n%s", got)
	}
	if strings.Contains(got, "last-applied-configuration") {
		t.Errorf("annotation should be dropped entirely, not blanked:\n%s", got)
	}
	if !strings.Contains(got, "app.kubernetes.io/name") {
		t.Errorf("unrelated annotations must survive:\n%s", got)
	}
}

// Credentials do not only live in Secrets: a plain Pod carries them in env
// vars and in ad-hoc credential fields on CRs.
func TestRedactCredentialFields(t *testing.T) {
	in := `apiVersion: v1
kind: Pod
metadata:
  name: api
spec:
  token: ` + leak + `
  clientSecret: ` + leak + `
  containers:
    - name: app
      image: ghcr.io/p10/api:1.0
      env:
        - name: DB_PASSWORD
          value: ` + leak + `
        - name: LOG_LEVEL
          value: info
`
	got := redactYAML(in)
	if strings.Contains(got, leak) {
		t.Fatalf("credential field leaked:\n%s", got)
	}
	if !strings.Contains(got, "info") || !strings.Contains(got, "ghcr.io/p10/api:1.0") {
		t.Errorf("harmless values must survive:\n%s", got)
	}
}

// kubectl's generic describer — the one every CR and every lens-pack kind goes
// through, since only the kinds in describe.go's kindToGK map get a typed
// describer — renders field names through smartLabelFor, which camelCase-splits
// them into Title Words. So a CR's clientSecret prints as "Client Secret:", and
// a scrubber matching raw spellings leaks exactly the objects the lens packs
// exist to show, in a file whose YAML section redacted the same value correctly.
func TestRedactTextHandlesTheDescriberSpacedKeys(t *testing.T) {
	in := "Name:           oidc-client\n" +
		"Client Secret:  " + leak + "\n" +
		"Private Key:    " + leak + "\n" +
		"API Key:        " + leak + "\n" +
		"Access Key:     " + leak + "\n" +
		"Secret Key:     " + leak + "\n" +
		"Password:       " + leak + "\n" +
		"Token:          " + leak + "\n" +
		"Secret Name:    db-credentials\n" +
		"Image:          ghcr.io/p10/api:1.0\n"
	got := redactText(in)
	if strings.Contains(got, leak) {
		t.Fatalf("describer-spaced credential key leaked:\n%s", got)
	}
	// A reference names a Secret, it does not contain one — losing it makes
	// the export useless for the debugging it exists for.
	if !strings.Contains(got, "db-credentials") || !strings.Contains(got, "ghcr.io/p10/api:1.0") {
		t.Errorf("a reference or a harmless value was eaten:\n%s", got)
	}
}

// A credential passed as a container flag has no key to match on: in YAML it is
// an element of .spec.containers[].args (a bare string in a list), and in
// describe output it prints as a line with no "key: value" shape. Exporters,
// grafana and oidc proxies all take credentials this way.
func TestRedactFlagValues(t *testing.T) {
	yamlIn := `apiVersion: v1
kind: Pod
metadata:
  name: exporter
spec:
  containers:
    - name: app
      args:
        - --client-secret=` + leak + `
        - --password=` + leak + `
        - --log-level=info
`
	if got := redactYAML(yamlIn); strings.Contains(got, leak) {
		t.Fatalf("flag credential leaked through the tree walker:\n%s", got)
	} else if !strings.Contains(got, "info") {
		t.Errorf("harmless flag was eaten:\n%s", got)
	}

	textIn := "    Args:\n" +
		"      --basic-auth-password=" + leak + "\n" +
		"      --token=" + leak + "\n" +
		"      --listen-address=:9090\n"
	got := redactText(textIn)
	if strings.Contains(got, leak) {
		t.Fatalf("flag credential leaked through the line scrubber:\n%s", got)
	}
	if !strings.Contains(got, ":9090") {
		t.Errorf("harmless flag was eaten:\n%s", got)
	}
}

// The toast is the ONLY surface the export path has — nothing else in the UI
// shows it — so what survives truncation decides whether the feature works at
// all. The filename is the part the reader has to retype; a tail cut leaves a
// directory that names no file.
func TestExportToastKeepsTheFilenameWhenItCannotFit(t *testing.T) {
	const name = "secrets-db-credentials-094946.txt"
	toast := "✓ saved /home/somebody/.k10s/exports/" + name
	for _, w := range []int{80, 60, 46} {
		got := fitToast(toast, w)
		if len([]rune(got)) > w {
			t.Errorf("width %d: got %d runes: %q", w, len([]rune(got)), got)
		}
		if !strings.HasSuffix(got, name) {
			t.Errorf("width %d dropped the filename: %q", w, got)
		}
	}
	// A toast that already fits is left exactly alone.
	if got := fitToast(toast, 200); got != toast {
		t.Errorf("a fitting toast was altered: %q", got)
	}
}

// Unparseable input must not fall through raw: describe output and a YAML the
// backend garbled both go through the line scrubber instead.
func TestRedactTextIsTheFallbackForNonYAML(t *testing.T) {
	in := "Name:         db-credentials\n" +
		"Annotations:  kubectl.kubernetes.io/last-applied-configuration:\n" +
		"                {\"data\":{\"password\":\"" + leak + "\"}}\n" +
		"password: " + leak + "\n" +
		"Image:    ghcr.io/p10/api:1.0\n"
	for _, got := range []string{redactText(in), redactYAML(in + "\t: [bad")} {
		if strings.Contains(got, leak) {
			t.Fatalf("scrubber leaked:\n%s", got)
		}
	}
	if !strings.Contains(redactText(in), "ghcr.io/p10/api:1.0") {
		t.Error("scrubber ate a harmless line")
	}
}

// End to end: the file on disk is what leaks, so assert on the file.
func TestExportWritesRedactedFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("K10S_EXPORT_DIR", dir)
	m := newTestModel(t, mock.New(""))
	m.jumpToResource("secrets")

	cmd := m.handleKey(key("ctrl+y"))
	if cmd == nil {
		t.Fatal("ctrl+y produced no command")
	}
	msg, ok := cmd().(exportDoneMsg)
	if !ok {
		t.Fatalf("export returned %T, want exportDoneMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("export failed: %v", msg.err)
	}
	if !filepath.IsAbs(msg.path) {
		t.Errorf("toast path %q is not absolute", msg.path)
	}
	b, err := os.ReadFile(msg.path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	body := string(b)
	if strings.Contains(body, "last-applied-configuration") {
		t.Errorf("export kept last-applied-configuration:\n%s", body)
	}
	for _, want := range []string{"## TABLE", "## YAML", "## DESCRIBE"} {
		if !strings.Contains(body, want) {
			t.Errorf("export is missing the %s section", want)
		}
	}

	// A second export must not clobber the first.
	m.Update(exportDoneMsg{path: msg.path})
	msg2 := m.handleKey(key("ctrl+y"))().(exportDoneMsg)
	if msg2.err != nil {
		t.Fatalf("second export failed: %v", msg2.err)
	}
	if msg2.path == msg.path {
		t.Errorf("second export reused %q — the first was clobbered", msg.path)
	}
}

func TestExportErrorIsAToastNotAPanic(t *testing.T) {
	t.Setenv("K10S_EXPORT_DIR", filepath.Join(t.TempDir(), "file"))
	if err := os.WriteFile(filepath.Join(t.TempDir(), "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A regular file where the directory should be: MkdirAll fails.
	if err := os.WriteFile(os.Getenv("K10S_EXPORT_DIR"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t, mock.New(""))
	msg := m.handleKey(key("ctrl+y"))().(exportDoneMsg)
	if msg.err == nil {
		t.Fatal("writing into a file-as-directory should have failed")
	}
	m.Update(msg)
	if !strings.HasPrefix(m.toast, "✗") {
		t.Errorf("toast = %q, want an error toast", m.toast)
	}
}

func TestExportToastCarriesTheAbsolutePath(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(exportDoneMsg{path: "/tmp/k10s/export.txt"})
	if !strings.Contains(m.toast, "/tmp/k10s/export.txt") {
		t.Errorf("toast = %q, want the absolute path", m.toast)
	}
}

var _ tea.Msg = exportDoneMsg{}
