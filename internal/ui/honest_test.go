package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/mock"
)

// The sentinel vocabulary (T41). A bare "-" used to mean unknown, unset,
// defaulted, not-applicable and pending all at once — five states, one glyph,
// all dimmed, so a cell that was waiting on something read exactly like a
// cell that had settled.
func TestSentinelVocabularyIsGraded(t *testing.T) {
	for _, tc := range []struct {
		cell, want, why string
	}{
		{"pending", "warn", "something is expected and has not arrived — the one sentinel worth a glyph"},
		{"lost", "error", "a lost PVC is a failure, not a gap"},
		// "subtle" is a colour, not a severity: cellLevel's contract is that
		// these are dimmed and never glyphed.
		{"n/a", "subtle", "not applicable to this object: nothing is wrong, nothing is coming"},
		{"<cluster>", "subtle", "cluster-scoped is a fact about the resource"},
		{"<none>", "subtle", "deliberately absent"},
	} {
		if got := cellLevel("", tc.cell); got != tc.want {
			t.Errorf("cellLevel(%q) = %q, want %q — %s", tc.cell, got, tc.want, tc.why)
		}
	}
}

// A lens pack's declared severity still wins outright over the vocabulary.
func TestLensSeverityStillOverridesSentinels(t *testing.T) {
	if got := cellLevel("ok", "pending"); got != "ok" {
		t.Errorf("cellLevel(ok, pending) = %q, want ok", got)
	}
}

// TestPendingCellsAreGlyphedInTheTable: grading is only useful if the glyph
// reaches the frame, since colour alone is unreadable for a colour-blind
// operator and on a low-contrast terminal.
func TestPendingCellsAreGlyphedInTheTable(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})
	frame := stripSGR(m.View())

	if !strings.Contains(frame, "pending") {
		t.Fatal("the demo pod with no metrics should read 'pending'")
	}
	for _, line := range strings.Split(frame, "\n") {
		if !strings.Contains(line, "pending") {
			continue
		}
		// severityGlyph marks warn cells with "! ".
		if !strings.Contains(line, "! pending") {
			t.Errorf("a pending cell carries colour but no glyph: %q", strings.TrimRight(line, " "))
		}
	}
}

// TestNoBareDashesInTheDemoTables is the regression guard for the vocabulary
// itself: every sentinel on screen has to say which of the five states it is.
func TestNoBareDashesInTheDemoTables(t *testing.T) {
	m := newTestModel(t, mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 44})

	for _, k := range m.kinds() {
		m.jumpToResource(k.Key)
		_, rows := m.tableData()
		for _, row := range rows {
			for ci, cell := range row {
				// The synthetic "all namespaces" row is a control, not data.
				if cell == "—" {
					continue
				}
				if cell == "-" {
					t.Errorf("kind %s column %d still renders a bare dash: %v", k.Key, ci, row)
				}
			}
		}
	}
}
