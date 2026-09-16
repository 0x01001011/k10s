package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0x01001011/k10s/internal/domain"
)

// createBackupSpec is the shape that broke the modal: a create verb whose
// kubectl equivalent is a heredoc, so the command is genuinely several lines.
func createBackupSpec() domain.LensActionSpec {
	return domain.LensActionSpec{
		ID:      "cnpg-backup",
		Label:   "Back up now",
		Confirm: "true",
		Kubectl: "kubectl create -n icc-system -f - <<'EOF'\n" +
			"{\n" +
			`  "apiVersion": "postgresql.cnpg.io/v1",` + "\n" +
			`  "kind": "Backup"` + "\n" +
			"}\n" +
			"EOF",
	}
}

// A body line the modal cannot split is a body line the modal cannot contain.
// overlayConfirm truncates each element to the inner width and sizes the box
// as len(body)+2, so an element holding five newlines paints five rows the box
// never reserved — straight over whatever the overlay was covering.
//
// The split belongs to the renderer rather than to lensConfirmBody because
// every confirm modal in the app funnels through overlayConfirm: plugins,
// deletes and the AI notice build their own message slices, and any of them
// may carry a newline. One guard here is smaller than a guard per caller, and
// it cannot be forgotten by the next one.
func TestConfirmBodyLinesSplitsEmbeddedNewlines(t *testing.T) {
	msg := lensConfirmBody(createBackupSpec(), "pgc", "icc-system", "postgresql")
	lines := confirmBodyLines(msg)

	for i, ln := range lines {
		if strings.Contains(ln, "\n") {
			t.Errorf("line %d carries an embedded newline: %q", i, ln)
		}
	}
	if len(lines) <= len(msg) {
		t.Errorf("splitting produced %d lines from %d elements — nothing was split", len(lines), len(msg))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "EOF") {
		t.Error("splitting dropped the command's last line")
	}
}

// The title already says what the action is. Repeating it as the first body
// line spends a row of a 58-column modal saying nothing new.
func TestLensConfirmBodyDoesNotRepeatTheTitle(t *testing.T) {
	sp := createBackupSpec()
	body := lensConfirmBody(sp, "pgc", "icc-system", "postgresql")

	for i, ln := range body {
		if strings.TrimSpace(ln) == sp.Label {
			t.Errorf("body[%d] repeats the title %q, which overlayConfirm already renders", i, sp.Label)
		}
	}
}

// The end-to-end guard, in the terms block_test.go already uses: every row of
// a rendered frame is exactly the terminal width. A row that is wider is the
// visible symptom — the unclosed box bleeding across the panes beside it.
func TestConfirmOverlayKeepsEveryRowAtTerminalWidth(t *testing.T) {
	m := demoProd(t)
	m.confirm = &confirmState{
		title:   "Back up now",
		message: lensConfirmBody(createBackupSpec(), "pgc", "icc-system", "postgresql"),
		onOK:    func(*Model) tea.Cmd { return nil },
	}

	for i, ln := range strings.Split(m.View(), "\n") {
		if got := lipgloss.Width(scanZones(ln)); got != m.w {
			t.Errorf("row %d is %d cells wide, want %d: %q", i, got, m.w, scanZones(ln))
		}
	}
}
