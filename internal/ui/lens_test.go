package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/domain"
	"github.com/0x01001011/k10s/internal/mock"
	"github.com/0x01001011/k10s/internal/theme"
)

// A lens pack's declared severity must beat every guess below it, and the
// lens word for a failure is "error" while statusColors says "err". An
// implementation that reuses statusColors' vocabulary compiles, renders, and
// silently paints every lens error in the default foreground.
func TestCellColorUsesTheLensLevelVocabulary(t *testing.T) {
	th := theme.Themes[0]
	def := th.Fg

	cases := map[string]struct {
		level, value string
		want         string
	}{
		"error paints Err":              {"error", "Degraded", "Err"},
		"warn paints Warn":              {"warn", "Progressing", "Warn"},
		"ok paints Ok":                  {"ok", "Synced", "Ok"},
		"unknown paints Subtle":         {"unknown", "Missing", "Subtle"},
		"no level falls back to guess":  {"", "Running", "Ok"},
		"level beats the value's guess": {"error", "Running", "Err"},
	}
	named := map[string]any{"Err": th.Err, "Warn": th.Warn, "Ok": th.Ok, "Subtle": th.Subtle}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := cellColor(th, c.level, c.value, def)
			if got != named[c.want] {
				t.Errorf("cellColor(%q, %q) = %v, want %s (%v)", c.level, c.value, got, c.want, named[c.want])
			}
		})
	}
}

// "1/3" is a ready ratio and earns a warning. "80/TCP" is a port and does
// not — the old heuristic coloured every service port amber.
func TestCellColorRatioHeuristicIgnoresNonNumericPairs(t *testing.T) {
	th := theme.Themes[0]
	def := th.Fg

	if got := cellColor(th, "", "1/3", def); got != th.Warn {
		t.Errorf("1/3 = %v, want Warn", got)
	}
	if got := cellColor(th, "", "3/3", def); got != def {
		t.Errorf("3/3 = %v, want the default", got)
	}
	for _, port := range []string{"80/TCP", "443/TCP", "53/UDP"} {
		if got := cellColor(th, "", port, def); got != def {
			t.Errorf("%s = %v, want the default — a port is not a ratio", port, got)
		}
	}
}

func demoProd(t *testing.T) *Model {
	t.Helper()
	m := New(mock.New("k10s-demo-prod"))
	m.Update(tea.WindowSizeMsg{Width: 150, Height: 44})
	return m
}

func selectLensKind(t *testing.T, m *Model, key string) {
	t.Helper()
	for i, k := range m.kinds() {
		if k.Key == key {
			m.resIdx = i
			return
		}
	}
	t.Fatalf("kind %q is not in the demo", key)
}

// The demo gates lens kinds the way discovery gates them for real: they are
// absent on the plain demo context and present on the prod one. Without the
// gate every screenshot would claim ArgoCD is always installed.
func TestDemoGatesLensKindsOnOneContext(t *testing.T) {
	has := func(src domain.Source, key string) bool {
		for _, k := range src.Kinds() {
			if k.Key == key {
				return true
			}
		}
		return false
	}
	if has(mock.New(""), "argocd-apps") {
		t.Error("the default demo context serves argocd-apps; it must not")
	}
	if !has(mock.New("k10s-demo-prod"), "argocd-apps") {
		t.Error("the prod demo context does not serve argocd-apps")
	}
}

// Switching with an empty name cycles contexts. The Source is rebuilt from
// the resolved name, so the lens kinds must follow the context — an
// implementation that patches the index after building keeps the old kinds.
func TestDemoContextCycleCarriesItsLensKinds(t *testing.T) {
	src := domain.Source(mock.New(""))
	seen := false
	for i := 0; i < len(src.Contexts()); i++ {
		next, err := src.SwitchContext("")
		if err != nil {
			t.Fatalf("SwitchContext: %v", err)
		}
		src = next
		for _, k := range src.Kinds() {
			if k.Key == "argocd-apps" {
				seen = true
			}
		}
	}
	if !seen {
		t.Error("cycling through every demo context never reached the lens kinds")
	}
}

// A digit fires the lens verb it is numbered with. This is the whole
// keyboard path for the feature: without it the verbs are mouse-only.
func TestDigitFiresTheNumberedLensAction(t *testing.T) {
	m := demoProd(t)
	selectLensKind(t, m, "argocd-apps")

	specs := m.lensActions()
	if len(specs) < 2 {
		t.Fatalf("argocd-apps offers %d lens actions, want at least 2", len(specs))
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if m.confirm == nil {
		t.Fatal("pressing 1 did not open the confirmation for the first lens action")
	}
	if m.confirm.title != specs[0].Label {
		t.Errorf("confirm title = %q, want %q", m.confirm.title, specs[0].Label)
	}
}

// An action whose kubectl equivalent is shown is one an operator can check
// before running and reproduce afterwards.
func TestLensConfirmShowsTheEquivalentKubectl(t *testing.T) {
	m := demoProd(t)
	selectLensKind(t, m, "argocd-apps")
	specs := m.lensActions()
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if m.confirm == nil {
		t.Fatal("no confirmation opened")
	}
	body := strings.Join(m.confirm.message, "\n")
	if specs[0].Kubectl == "" {
		t.Fatal("the sync action carries no kubectl line")
	}
	if !strings.Contains(body, specs[0].Kubectl) {
		t.Errorf("confirmation body does not contain the kubectl line.\nbody:\n%s\nwant substring:\n%s", body, specs[0].Kubectl)
	}
}

// The typed confirmation is the guard on writes nothing can undo. Enter must
// do nothing until the word matches, and "y" must be a letter of that word
// rather than a shortcut that skips the whole gate.
func TestTypedConfirmDoesNotFireUntilTheWordMatches(t *testing.T) {
	fired := false
	m := demoProd(t)
	m.confirm = &confirmState{
		title: "Fence",
		typed: "pay",
		onOK:  func(*Model) tea.Cmd { fired = true; return nil },
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if fired {
		t.Fatal("Enter fired a typed confirmation with an empty field")
	}
	// "y" is the y of "pay" — treating it as the yes-shortcut would bypass
	// the gate entirely.
	for _, r := range "pay" {
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if fired {
			t.Fatalf("a keystroke (%q) fired the action before the word was complete", string(r))
		}
	}
	if m.confirm == nil {
		t.Fatal("typing dismissed the modal")
	}
	if m.confirm.buf != "pay" {
		t.Fatalf("typed buffer = %q, want %q", m.confirm.buf, "pay")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !fired {
		t.Error("Enter did not fire once the word matched")
	}
}

func TestTypedConfirmBackspaceDisarms(t *testing.T) {
	m := demoProd(t)
	m.confirm = &confirmState{typed: "db", onOK: func(*Model) tea.Cmd { return nil }}
	for _, r := range "db" {
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !m.confirm.armed() {
		t.Fatal("the full word did not arm the confirmation")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.confirm.armed() {
		t.Error("backspacing a character left the confirmation armed")
	}
}

// An action that needs an instance asks for one instead of being unreachable.
// Seven of the shipped verbs are in this shape — a CNPG instance is
// "my-db-2", never the cluster's own name, so there is nothing to default to.
func TestActionNeedingAnInstanceAsksForOne(t *testing.T) {
	m := demoProd(t)
	selectLensKind(t, m, "cnpg-clusters")

	var need domain.LensActionSpec
	for _, sp := range m.lensActions() {
		if sp.Disabled && strings.Contains(sp.DisabledWhy, "selected") {
			need = sp
			break
		}
	}
	if need.ID == "" {
		t.Fatal("no shipped CNPG action requires a selection; the gate is untested")
	}
	m.fireLensAction(need)
	if m.confirm == nil || m.confirm.ask == "" {
		t.Fatalf("firing %q did not ask which instance to act on", need.ID)
	}
	if m.confirm.armed() {
		t.Error("an unanswered question is armed; Enter would run the action with no instance")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !m.confirm.armed() {
		t.Error("an answered question is still not armed")
	}
}

// "not loaded" and "does not exist" are different answers and only one of
// them is fixed by opening that kind, so the panel must say which it means.
func TestRenderRelatedMarksUnloadedNeighbours(t *testing.T) {
	out := renderRelated([]domain.Ref{
		{Kind: "pods", Namespace: "prod", Name: "api-0", Rel: "label", Loaded: true},
		{Kind: "lh-volumes", Rel: "ownerRef", Loaded: false},
	})
	if !strings.Contains(out, "via label") || !strings.Contains(out, "via ownerRef") {
		t.Errorf("relationships are not grouped by edge:\n%s", out)
	}
	if !strings.Contains(out, "prod/api-0") {
		t.Errorf("a loaded neighbour lost its namespace:\n%s", out)
	}
	if !strings.Contains(out, "not loaded") {
		t.Errorf("an unloaded neighbour is not marked, so it reads as absent:\n%s", out)
	}
	if strings.Contains(strings.SplitN(out, "via ownerRef", 2)[0], "not loaded") {
		t.Errorf("the loaded neighbour was marked not loaded:\n%s", out)
	}
}

func TestRenderRelatedExplainsAnEmptyResult(t *testing.T) {
	if out := renderRelated(nil); !strings.Contains(out, "lens pack") {
		t.Errorf("an empty result does not explain where relationships come from:\n%s", out)
	}
}

// The severity a pack declares must reach the table. This runs the whole
// path — Source, interface assertion, lookup — rather than the map alone.
func TestDemoGradesCellsFromTheShippedPacks(t *testing.T) {
	m := demoProd(t)
	level := m.levelFor("argocd-apps")
	if level == nil {
		t.Fatal("the demo backend does not implement domain.CellLevels")
	}
	cases := map[[2]string]string{
		{"HEALTH", "Degraded"}:    "error",
		{"HEALTH", "Healthy"}:     "ok",
		{"HEALTH", "Progressing"}: "warn",
		{"SYNC", "Synced"}:        "ok",
		// An absent optional status is not a status. Grading it would paint
		// the OPERATION column of every idle Application.
		{"OPERATION", ""}: "",
		// A column with no severity table declared is never graded.
		{"PROJECT", "platform"}: "",
	}
	for in, want := range cases {
		if got := level(in[0], in[1]); got != want {
			t.Errorf("CellLevel(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// The instance a prompt collected belongs to the row it was collected on.
//
// Carrying it further is how you fence the wrong cluster with no prompt:
// having answered "which instance?" with db-a-2 on cluster db-a, moving to
// db-b and pressing the same key would pass Check (Selected is non-empty),
// skip the question, and write db-a's instance onto db-b.
func TestSelectedInstanceDoesNotLeakToTheNextRow(t *testing.T) {
	m := demoProd(t)
	selectLensKind(t, m, "cnpg-clusters")

	rowKey := func() string {
		return m.curKind().Key + "\x00" + m.curNamespace() + "\x00" + m.curName()
	}
	first := rowKey()
	m.lensSel, m.lensSelKey = "reporting-db-2", first
	if got := m.selectedFor(first); got != "reporting-db-2" {
		t.Fatalf("the answering row lost its own selection: %q", got)
	}

	m.move(1)
	if second := rowKey(); second == first {
		t.Fatal("the demo has only one cnpg row; the leak cannot be tested")
	}
	if got := m.selectedFor(rowKey()); got != "" {
		t.Errorf("the next row inherited %q as its instance", got)
	}
	// And the action is therefore offered as a question again, not fired.
	for _, sp := range m.lensActions() {
		if sp.NeedsSelection && !sp.Disabled {
			t.Errorf("action %q is enabled on a row that never named an instance", sp.ID)
		}
	}
}

// Every lens action carries the same id on every row, so a wait cannot be
// keyed on it: refreshing A then B would let A's acknowledgement clear B's
// spinner and report B as done when only A was.
func TestAStaleAckDoesNotClearTheCurrentWait(t *testing.T) {
	m := demoProd(t)
	m.lensSeq = 2
	m.lensAck = &lensAckState{id: "kargo-refresh", label: "Refresh stage/B", seq: 2}

	m.handleLensAck(lensAckMsg{seq: 1, ok: true})
	if m.lensAck == nil {
		t.Fatal("an older request's acknowledgement cleared the current wait")
	}
	m.handleLensAck(lensAckMsg{seq: 2, ok: true})
	if m.lensAck != nil {
		t.Error("the current request's acknowledgement did not end the wait")
	}
	if !strings.Contains(m.toast, "Refresh stage/B") {
		t.Errorf("toast = %q, want it to name the completed action", m.toast)
	}
}

// Timing out is not a failure: the write went through and the controller has
// not answered. Reporting it as an error sends the operator to retry a
// request that is already queued.
func TestAckTimeoutReportsSentNotFailed(t *testing.T) {
	m := demoProd(t)
	m.lensSeq = 1
	m.lensAck = &lensAckState{id: "kargo-refresh", label: "Refresh stage/prod", seq: 1}
	m.handleLensAck(lensAckMsg{seq: 1, done: true})
	if strings.HasPrefix(m.toast, "✗") {
		t.Errorf("a timeout is reported as a failure: %q", m.toast)
	}
	if !strings.Contains(m.toast, "sent") {
		t.Errorf("toast = %q, want it to say the write was sent", m.toast)
	}
}

// Lens tables sort worst-first, so an unrelated status change reorders them
// under a purely positional rowIdx and the next keystroke lands elsewhere.
func TestCursorFollowsItsObjectWhenTheTableReorders(t *testing.T) {
	m := demoProd(t)
	selectLensKind(t, m, "argocd-apps")
	m.move(2)
	want := m.curName()
	m.anchorRow()
	if want == "" || want == "-" {
		t.Fatal("no row selected")
	}

	// Shift every row up by one, the way a re-sort does: remove the row
	// above the cursor. rowIdx now points one row past the anchored object.
	_, rows := m.tableData()
	if len(rows) < 3 {
		t.Fatalf("need at least 3 demo rows, got %d", len(rows))
	}
	m.rowIdx = 0
	above := m.curName()
	m.rowIdx = 2
	if err := m.src.Delete("argocd-apps", m.curNamespace(), above); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := m.curName(); got == want {
		t.Fatal("removing a row above the cursor did not shift it; the test proves nothing")
	}

	m.reanchorRow()
	if got := m.curName(); got != want {
		t.Errorf("after a reorder the cursor is on %q, want %q — the next keystroke would act on the wrong object", got, want)
	}
}

// A disabled action that is merely refused must NOT open an input box, and
// the routing must not depend on words in pack-authored free text.
func TestRefusedActionIsReportedNotPrompted(t *testing.T) {
	m := demoProd(t)
	selectLensKind(t, m, "argocd-apps")
	m.fireLensAction(domain.LensActionSpec{
		ID: "x", Label: "Sync",
		Disabled:    true,
		DisabledWhy: "another operation is already in progress on the selected revision",
	})
	if m.confirm != nil {
		t.Fatalf("a refusal opened a modal: %+v", m.confirm)
	}
	if !strings.Contains(m.toast, "already in progress") {
		t.Errorf("toast = %q, want the refusal reason", m.toast)
	}
}
