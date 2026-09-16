package ui

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/domain"
)

func fenceSpec() domain.LensActionSpec {
	return domain.LensActionSpec{
		ID:           "pg-fence",
		Label:        "Fence instance",
		Confirm:      "typed",
		ConfirmValue: "{{.Params.instance}}",
		Params: []domain.LensParamSpec{{
			Name:     "instance",
			Label:    "instance",
			Required: true,
			Options: []domain.LensOption{
				{Value: "postgresql-1", Note: "primary"},
				{Value: "postgresql-2", Note: "replica"},
				{Value: "reporting-3", Note: "replica"},
			},
		}},
	}
}

func backupSpec() domain.LensActionSpec {
	return domain.LensActionSpec{
		ID:    "pg-backup",
		Label: "Back up now",
		Params: []domain.LensParamSpec{
			{
				Name: "method", Label: "method", Default: "barmanObjectStore",
				Options: []domain.LensOption{
					{Value: "barmanObjectStore"}, {Value: "volumeSnapshot"}, {Value: "plugin"},
				},
			},
			{
				Name: "target", Label: "target",
				Options: []domain.LensOption{{Value: "primary"}, {Value: "prefer-standby"}},
			},
		},
	}
}

// previewSource is the demo backend with LensPreview overridden.
//
// The real one resolves the command through the shipped packs, so a synthetic
// action id renders nothing — and these tests are about the form's behaviour,
// not about any pack's contents. Rendering the parameters verbatim keeps the
// assertion on the one property that matters here: the preview moves when the
// fields do.
type previewSource struct {
	domain.Source
}

func (previewSource) LensActions(kind, ns, name, selected string) []domain.LensActionSpec {
	return nil
}

func (previewSource) LensAction(kind, ns, name, id, selected string, params map[string]string) (string, error) {
	return "", nil
}

func (previewSource) LensAck(kind, ns, name, id, want string) (bool, error) { return true, nil }

func (previewSource) LensPreview(kind, ns, name, id, selected string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "kubectl annotate " + kind + "/" + name + " -n " + ns
	for _, k := range keys {
		out += "\n  " + k + "=" + params[k]
	}
	return out
}

func openForm(t *testing.T, sp domain.LensActionSpec) *Model {
	t.Helper()
	m := demoProd(t)
	m.src = previewSource{Source: m.src}
	m.openLensForm("cnpg-clusters", "icc-system", "postgresql", "pgc", sp)
	if m.lensForm == nil {
		t.Fatal("openLensForm did not open a form")
	}
	return m
}

// A declared default is the field's starting value. Starting empty would make
// the form disagree with the preview, which renders defaults.
func TestLensFormStartsFromTheDeclaredDefaults(t *testing.T) {
	m := openForm(t, backupSpec())

	if got := m.lensForm.fields[0].buf; got != "barmanObjectStore" {
		t.Errorf("method starts as %q, want its default", got)
	}
	if got := m.lensForm.fields[1].buf; got != "" {
		t.Errorf("target starts as %q, want empty", got)
	}
	if got := m.lensForm.params()["method"]; got != "barmanObjectStore" {
		t.Errorf("params()[method] = %q", got)
	}
}

// Typing narrows the list. This is the whole difference from the free-text box
// it replaces: the operator sees which instances exist while they type.
func TestLensFormTypingFiltersTheOptions(t *testing.T) {
	m := openForm(t, fenceSpec())

	for _, r := range "postgres" {
		m.handleLensFormKey(key(string(r)))
	}
	f := m.lensForm.fields[0]
	if len(f.filtered) != 2 {
		t.Fatalf("filtered = %+v, want the two postgresql instances", f.filtered)
	}
	for _, o := range f.filtered {
		if !strings.HasPrefix(o.Value, "postgresql") {
			t.Errorf("filtered kept %q", o.Value)
		}
	}

	// Case-insensitively: nobody types a pod name in the right case under
	// pressure.
	m2 := openForm(t, fenceSpec())
	for _, r := range "REPORT" {
		m2.handleLensFormKey(key(string(r)))
	}
	if len(m2.lensForm.fields[0].filtered) != 1 {
		t.Errorf("uppercase filter = %+v, want reporting-3", m2.lensForm.fields[0].filtered)
	}
}

// Down moves through the suggestions, Enter takes the highlighted one.
func TestLensFormArrowsAndEnterPickAnOption(t *testing.T) {
	m := openForm(t, fenceSpec())

	m.handleLensFormKey(key("down"))
	m.handleLensFormKey(key("enter"))

	if got := m.lensForm.params()["instance"]; got != "postgresql-2" {
		t.Errorf("instance = %q, want the highlighted option", got)
	}
}

// A closed list refuses a value it does not contain. Submitting a typo writes
// it to the cluster, where it fences nothing and looks like success.
func TestLensFormRefusesAValueOutsideAClosedList(t *testing.T) {
	m := openForm(t, fenceSpec())
	for _, r := range "postgresql-9" {
		m.handleLensFormKey(key(string(r)))
	}
	if m.lensForm.ready() {
		t.Error("a value outside the list must not arm the form")
	}

	// allowFree reopens it for the genuinely open-ended fields.
	sp := fenceSpec()
	sp.Params[0].AllowFree = true
	m2 := openForm(t, sp)
	for _, r := range "postgresql-9" {
		m2.handleLensFormKey(key(string(r)))
	}
	if !m2.lensForm.ready() {
		t.Error("allowFree should accept a free value")
	}
}

// A required field left empty keeps the form inert. The button looking live
// while refusing the keypress is worse than having no button.
func TestLensFormRequiredFieldKeepsItInert(t *testing.T) {
	m := openForm(t, fenceSpec())
	if m.lensForm.ready() {
		t.Error("an empty required field must not arm the form")
	}
	m.handleLensFormKey(key("enter")) // accept the highlighted first option
	if !m.lensForm.ready() {
		t.Error("form should arm once the required field is filled")
	}
}

// Tab walks the fields. Two-field forms are the common case — method, target —
// and the operator has to be able to reach the second one.
func TestLensFormTabMovesBetweenFields(t *testing.T) {
	m := openForm(t, backupSpec())

	if m.lensForm.focus != 0 {
		t.Fatalf("focus starts at %d", m.lensForm.focus)
	}
	m.handleLensFormKey(key("tab"))
	if m.lensForm.focus != 1 {
		t.Errorf("tab left focus at %d, want 1", m.lensForm.focus)
	}
	m.handleLensFormKey(key("shift+tab"))
	if m.lensForm.focus != 0 {
		t.Errorf("shift+tab left focus at %d, want 0", m.lensForm.focus)
	}
}

// Esc cancels and writes nothing.
func TestLensFormEscCancels(t *testing.T) {
	m := openForm(t, fenceSpec())
	m.handleLensFormKey(key("esc"))

	if m.lensForm != nil {
		t.Error("esc should close the form")
	}
	if m.busy {
		t.Error("esc must not start a write")
	}
}

// The typed gate asks for the value at RISK, not the object's name. Fencing is
// about an instance; typing the cluster's name confirms something the operator
// was never shown.
func TestLensFormTypedGateAsksForTheParameterValue(t *testing.T) {
	m := openForm(t, fenceSpec())
	m.handleLensFormKey(key("down"))
	m.handleLensFormKey(key("enter")) // instance = postgresql-2

	if !m.lensForm.ready() {
		t.Fatal("form should be ready after the only field is filled")
	}
	if got := m.lensForm.confirmWant(); got != "postgresql-2" {
		t.Errorf("typed gate wants %q, want the chosen instance", got)
	}
}

// With no confirmValue the gate falls back to the object's own name, which is
// what every existing pack relies on.
func TestLensFormTypedGateFallsBackToTheObjectName(t *testing.T) {
	sp := fenceSpec()
	sp.ConfirmValue = ""
	m := openForm(t, sp)
	m.handleLensFormKey(key("enter"))

	if got := m.lensForm.confirmWant(); got != "postgresql" {
		t.Errorf("typed gate wants %q, want the object name", got)
	}
}

// The preview has to move with the form. A command showing one instance while
// the request carries another is the one thing it must never do.
func TestLensFormPreviewTracksTheFields(t *testing.T) {
	m := openForm(t, fenceSpec())
	first := strings.Join(m.lensForm.preview, "\n")

	m.handleLensFormKey(key("down"))
	m.handleLensFormKey(key("enter"))
	after := strings.Join(m.lensForm.preview, "\n")

	if first == after {
		t.Error("preview did not change when the parameter did")
	}
	if !strings.Contains(after, "postgresql-2") {
		t.Errorf("preview = %q, want the chosen instance", after)
	}
	// The T1 regression, now on the form's own preview: a multi-line command
	// must arrive already split, or it escapes the box.
	for i, ln := range m.lensForm.preview {
		if strings.Contains(ln, "\n") {
			t.Errorf("preview line %d carries an embedded newline: %q", i, ln)
		}
	}
}

// The invariant block_test.go holds every other panel to: a rendered frame has
// rows exactly as wide as the terminal. A form that overflows is the confirm
// modal's old bug in a new box.
func TestLensFormRendersWithinTheTerminalAtEveryWidth(t *testing.T) {
	for _, size := range []struct{ w, h int }{{140, 44}, {100, 30}, {80, 24}} {
		m := demoProd(t)
		m.src = previewSource{Source: m.src}
		m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m.openLensForm("cnpg-clusters", "icc-system", "postgresql", "pgc", fenceSpec())

		// Part-way through: a filter typed, a suggestion highlighted.
		for _, r := range "postgres" {
			m.handleLensFormKey(key(string(r)))
		}
		m.handleLensFormKey(key("down"))

		for i, ln := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(scanZones(ln)); got != m.w {
				t.Errorf("%dx%d row %d is %d cells, want %d: %q",
					size.w, size.h, i, got, m.w, scanZones(ln))
			}
		}
	}
}

// The suggestion list is capped rather than scrolled. A cluster with two
// hundred Backups would otherwise push the preview and the buttons off screen,
// and the typeahead is the way through a long list.
func TestLensFormCapsALongSuggestionList(t *testing.T) {
	sp := fenceSpec()
	sp.Params[0].Options = nil
	for i := 0; i < 40; i++ {
		sp.Params[0].Options = append(sp.Params[0].Options,
			domain.LensOption{Value: "backup-" + strconv.Itoa(i)})
	}
	m := openForm(t, sp)

	rows := m.lensFormField(0, m.lensForm.fields[0], 50)
	if len(rows) > lensFormMaxOptions+3 {
		t.Errorf("focused field rendered %d rows for 40 options", len(rows))
	}
	if !strings.Contains(scanZones(strings.Join(rows, "\n")), "more") {
		t.Error("a capped list must say how many it is hiding")
	}
}

// A create action's preview is an indented JSON manifest. Collapsing the
// indentation flattens every nested key to the same level, and a reader
// checking whether targetTime sits UNDER recoveryTarget or beside it then
// cannot tell — which is the one question the preview is there to answer.
func TestWrapPreviewKeepsIndentation(t *testing.T) {
	line := `      "name": "a-very-long-cluster-name-that-will-not-fit-on-one-line-at-all",`
	got := wrapPreview(line, 40)

	if len(got) < 2 {
		t.Fatalf("line was not wrapped: %q", got)
	}
	for i, seg := range got {
		if !strings.HasPrefix(seg, "      ") {
			t.Errorf("segment %d lost its indent: %q", i, seg)
		}
		if w := lipgloss.Width(seg); w > 40 {
			t.Errorf("segment %d is %d wide, want <= 40: %q", i, w, seg)
		}
	}
	// A short line is returned untouched, indent and all.
	short := `    "kind": "Cluster",`
	if got := wrapPreview(short, 40); len(got) != 1 || got[0] != short {
		t.Errorf("short line was altered: %q", got)
	}
}

// An action with no parameters must not open a form. The confirm modal it has
// always used is still the right surface for a yes/no.
func TestLensFormIsNotOpenedForAParameterlessAction(t *testing.T) {
	m := demoProd(t)
	m.fireLensAction(domain.LensActionSpec{ID: "pg-wake", Label: "Wake cluster", Confirm: "true"})

	if m.lensForm != nil {
		t.Error("a parameterless action opened a form")
	}
	if m.confirm == nil {
		t.Error("a parameterless action should still open the confirm modal")
	}
}
