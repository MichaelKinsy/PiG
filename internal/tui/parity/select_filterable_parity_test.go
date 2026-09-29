package parity

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func renderFilterableListToTUI(t *testing.T, rows, cols int, list *tui.FilterableList) func(*bytes.Buffer) {
	t.Helper()
	return func(w *bytes.Buffer) {
		ti := tui.NewWithOutput(w, cols, rows)
		ti.Add(list)
		ti.Render()
	}
}

func TestParityFilterableList_GoldenVisibleState(t *testing.T) {
	labels := make([]string, 24)
	for i := range labels {
		labels[i] = fmt.Sprintf("option-%02d", i)
	}
	list := tui.NewFilterableList("Pick", labels)
	for _, ch := range "option-1" {
		list.HandleInput(string(ch))
	}
	list.HandleInput("\x1b[B")

	AssertGolden(t, "select-filterable-visible", 12, 36, renderFilterableListToTUI(t, 12, 36, list))
}

func TestParityFilterableList_NoMatchGolden(t *testing.T) {
	list := tui.NewFilterableList("Pick", []string{"alpha", "beta", "gamma"})
	for _, ch := range "zzz" {
		list.HandleInput(string(ch))
	}
	AssertGolden(t, "select-filterable-no-match", 6, 32, renderFilterableListToTUI(t, 6, 32, list))
}

func TestParityFilterableList_DescriptionColumnGolden(t *testing.T) {
	labels := []string{"alpha", "beta-long", "gamma"}
	descs := []string{"the first option", "second\noption with newline", "third option"}
	list := tui.NewFilterableList("Pick", labels)
	list.Descriptions = descs

	AssertGolden(t, "select-filterable-descriptions", 8, 60, renderFilterableListToTUI(t, 8, 60, list))
}
