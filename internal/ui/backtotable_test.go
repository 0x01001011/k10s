package ui

import (
	"fmt"
	"testing"

	"github.com/0x01001011/k10s/internal/mock"
)

// clickKind clicks the sidebar row of the kind with the given key. The view
// is rendered first so the row's zone exists to be hit.
func clickKind(t *testing.T, m *Model, key string) {
	t.Helper()
	m.View()
	for i, k := range m.kinds() {
		if k.Key != key {
			continue
		}
		if !clickZone(m, fmt.Sprintf("res:%d", i)) {
			t.Fatalf("no sidebar zone for %s", key)
		}
		return
	}
	t.Fatalf("kind %q not in sidebar", key)
}

// Reading a pod's describe, clicking "Pods" in the sidebar is how you get
// back to the list — it must not be a silent no-op because the kind is
// already selected.
func TestClickingCurrentKindLeavesDescribe(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	kind := m.curKind().Key

	m.showText("describe web-1", "Name: web-1")
	if m.mode != modeText {
		t.Fatal("expected the describe view")
	}

	clickKind(t, m, kind)
	if m.mode != modeTable {
		t.Errorf("mode = %v, want the table", m.mode)
	}
	if m.curKind().Key != kind {
		t.Errorf("kind = %q, want %q kept", m.curKind().Key, kind)
	}
}

func TestClickingCurrentKindLeavesLogsAndStopsTheStream(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	stopped := false
	m.logStop = func() { stopped = true }

	clickKind(t, m, m.curKind().Key)
	if m.mode != modeTable {
		t.Errorf("mode = %v, want the table", m.mode)
	}
	if !stopped {
		t.Error("leaving the log view must stop the follow, not leave it streaming behind the table")
	}
	if m.logStop != nil {
		t.Error("the stop hook should be cleared once used")
	}
}

func TestClickingCurrentKindExitsTheShell(t *testing.T) {
	m, sh := newShellModel(t)
	openShell(t, m, sh)

	clickKind(t, m, m.curKind().Key)
	if m.mode != modeTable {
		t.Errorf("mode = %v, want the table", m.mode)
	}
	if m.shellSess != nil || !sh.isClosed() {
		t.Error("leaving a shell via the sidebar must close the session, not leak it")
	}
}

// Switching to another kind while in a shell used to flip the view but keep
// the pod exec open underneath.
func TestClickingAnotherKindExitsTheShell(t *testing.T) {
	m, sh := newShellModel(t)
	openShell(t, m, sh)

	var other string
	for _, k := range m.kinds() {
		if k.Key != m.curKind().Key {
			other = k.Key
			break
		}
	}
	clickKind(t, m, other)
	if m.mode != modeTable || m.curKind().Key != other {
		t.Errorf("mode=%v kind=%q, want the %s table", m.mode, m.curKind().Key, other)
	}
	if !sh.isClosed() {
		t.Error("the shell session should be closed when the view moves on")
	}
}

// The [ detach ] tag on the shell panel and the [ close ] tag on a log view
// do the same teardown as the sidebar.
func TestCloseTagTearsDownTheShell(t *testing.T) {
	m, sh := newShellModel(t)
	openShell(t, m, sh)

	m.View()
	if !clickZone(m, "close") {
		t.Fatal("no [ detach ] zone on the shell panel")
	}
	if m.mode != modeTable {
		t.Errorf("mode = %v, want the table", m.mode)
	}
	if !sh.isClosed() {
		t.Error("[ detach ] must close the session")
	}
}

// A sidebar step that moves nowhere (↑ on the first visible kind) re-selects
// the current kind, which must not close the view you were reading.
func TestNoMoveStepKeepsTheLogView(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	dismissOnboarding(m)
	openLogs(t, m)

	m.focus = focusList
	m.move(-1 << 20) // to the first kind, closing logs is expected here
	openLogs(t, m)
	m.move(-1) // nowhere to go
	if m.mode != modeLogs {
		t.Errorf("mode = %v, want the log view kept open by a step that moved nothing", m.mode)
	}
}
