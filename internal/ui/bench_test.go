package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0x01001011/k10s/internal/mock"
)

// benchModel is newTestModel for a *testing.B (Setenv lives on testing.TB but
// newTestModel's signature is pinned to *testing.T by the rest of the suite).
func benchModel(b *testing.B) *Model {
	b.Helper()
	b.Setenv("K10S_CONFIG", b.TempDir()+"/config.yaml")
	m := New(mock.New(""))
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 44})
	return m
}

// BenchmarkView measures one full frame: the render hot path that decides
// whether the TUI feels smooth. It is the optimisation target — do not change
// what it measures, or the numbers stop comparing across commits.
func BenchmarkView(b *testing.B) {
	m := benchModel(b)
	_ = m.View() // warm caches/styles so the first frame is not in the average

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sink = m.View()
	}
}

// BenchmarkKeypressFrame measures the full interactive round trip: a
// navigation keypress plus the frame it produces.
func BenchmarkKeypressFrame(b *testing.B) {
	m := benchModel(b)
	m.Update(key("j"))
	_ = m.View()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		m.Update(key("j"))
		sink = m.View()
	}
}

// sink keeps the compiler from eliding the rendered frame.
var sink string
