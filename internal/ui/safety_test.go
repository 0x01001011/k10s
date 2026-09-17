package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/mock"
)

func actionByID(t *testing.T, id string) Action {
	t.Helper()
	for _, a := range Actions {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no action %q", id)
	return Action{}
}

// TestDeleteRequiresTypedName: enter is the universal "open" key, so a plain
// confirm put deletion one keystroke from every table — D, enter.
func TestDeleteRequiresTypedName(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	name := m.curName()

	m.fireAction(actionByID(t, domain.ADelete))
	if m.confirm == nil {
		t.Fatal("delete did not open a confirm modal")
	}
	if m.confirm.typed != name {
		t.Fatalf("delete confirm asks for %q, want the object name %q", m.confirm.typed, name)
	}
	if m.confirm.armed() {
		t.Error("the delete confirm is armed before anything was typed")
	}

	// Enter with an empty field must not fire.
	m.handleKey(key("enter"))
	if m.confirm == nil {
		t.Fatal("enter dismissed an unarmed confirm")
	}

	m.confirm.buf = name
	if !m.confirm.armed() {
		t.Error("typing the exact name did not arm the confirm")
	}
}

// Evicting every pod off a node is the most consequential key in the app.
func TestDrainRequiresTypedName(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.jumpToResource("nodes")
	name := m.curName()

	m.fireAction(actionByID(t, domain.ADrain))
	if m.confirm == nil {
		t.Fatal("drain did not open a confirm modal")
	}
	if m.confirm.typed != name {
		t.Errorf("drain confirm asks for %q, want %q", m.confirm.typed, name)
	}
}

// TestEditWithNoChangesDoesNotApply: quitting vi with :q used to write the
// object straight back to the cluster.
func TestEditWithNoChangesDoesNotApply(t *testing.T) {
	src := &applyCountingSource{Source: mock.New("")}
	m := newTestModel(t, src)

	path := filepath.Join(t.TempDir(), "obj.yaml")
	const body = "apiVersion: v1\nkind: Pod\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	m.Update(editExitMsg{kind: "pods", ns: "default", name: "api-gateway", path: path, before: body})

	if src.applies != 0 {
		t.Errorf("an unchanged file was applied (%d times)", src.applies)
	}
	if !strings.Contains(m.toast, "unchanged") {
		t.Errorf("toast = %q, want it to say nothing was applied", m.toast)
	}
}

// An editor that crashed and left an empty file is a mistake, not a manifest.
func TestEditWithAnEmptyFileDoesNotApply(t *testing.T) {
	src := &applyCountingSource{Source: mock.New("")}
	m := newTestModel(t, src)

	path := filepath.Join(t.TempDir(), "obj.yaml")
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m.Update(editExitMsg{kind: "pods", ns: "default", name: "api-gateway", path: path, before: "apiVersion: v1\n"})

	if src.applies != 0 {
		t.Errorf("an empty file was applied (%d times)", src.applies)
	}
	if !strings.Contains(m.toast, "empty") {
		t.Errorf("toast = %q, want it to say the file came back empty", m.toast)
	}
}

// A real edit still applies — the guard must not become a way of never
// writing.
func TestEditWithChangesStillApplies(t *testing.T) {
	src := &applyCountingSource{Source: mock.New("")}
	m := newTestModel(t, src)

	path := filepath.Join(t.TempDir(), "obj.yaml")
	if err := os.WriteFile(path, []byte("apiVersion: v1\nkind: Pod\nedited: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, cmd := m.Update(editExitMsg{kind: "pods", ns: "default", name: "api-gateway", path: path, before: "apiVersion: v1\n"})
	runCmdTree(m, cmd, 0)

	if src.applies == 0 {
		t.Error("a genuine edit was not applied")
	}
}

// runCmdTree runs a command and everything it batches. drainCmd stops at a
// tea.BatchMsg, and the editor path returns exactly that — the mouse-restore
// command alongside the apply.
func runCmdTree(m *Model, cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 4 {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runCmdTree(m, c, depth+1)
		}
	case nil:
	default:
		if IsAsyncMsg(msg) {
			_, next := m.Update(msg)
			runCmdTree(m, next, depth+1)
		}
	}
}

// TestPaletteFindsActionsByName: the palette found kinds and objects but never
// verbs, so R, X and ctrl+y appeared in no pane and no hint string.
func TestPaletteFindsActionsByName(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.openPalette()
	m.input.SetValue("describe")

	hits := m.paletteHits()
	if len(hits) == 0 {
		t.Fatal("no hits for 'describe'")
	}
	if hits[0].action == nil {
		t.Fatalf("the first hit for a verb is not an action: %+v", hits[0])
	}
	if !strings.Contains(hits[0].sub, "action") {
		t.Errorf("an action hit does not say it is one: %q", hits[0].sub)
	}
}

// An action the current kind cannot run is still findable — the point is to
// learn it exists — but it says so.
func TestPaletteSaysWhenAnActionDoesNotApply(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.jumpToResource("configmaps")
	m.openPalette()
	m.input.SetValue("logs")

	for _, h := range m.paletteHits() {
		if h.action == nil {
			continue
		}
		if !strings.Contains(h.sub, "not available") {
			t.Errorf("Logs on ConfigMaps does not say it is unavailable: %q", h.sub)
		}
		return
	}
	t.Error("Logs was not findable at all")
}

// applyCountingSource counts writes, so a test can assert that none happened.
type applyCountingSource struct {
	domain.Source
	applies int
}

func (s *applyCountingSource) Apply(kind, ns, name, body string) error {
	s.applies++
	return s.Source.Apply(kind, ns, name, body)
}
