package codingagent

import (
	"bytes"
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ported from tui-cell-size-input.test.ts "consumes cell size responses and
// still forwards later user input" through Pig's driver-owned input path.
func TestDispatchKeyConsumesCellSizeResponseBeforeEditor(t *testing.T) {
	previousDimensions := tui.GetCellDimensions()
	t.Cleanup(func() { tui.SetCellDimensions(previousDimensions) })
	tui.SetCellDimensions(tui.CellDimensions{WidthPx: 9, HeightPx: 18})

	renderer := tui.NewWithOutput(&bytes.Buffer{}, 80, 24)
	t.Cleanup(renderer.CancelPendingRender)
	editor := tui.NewEditor()
	mode := &InteractiveMode{tuiInst: renderer, editor: editor, isIdle: true}

	if err := mode.dispatchKey(context.Background(), "\x1b[6;20;10t"); err != nil {
		t.Fatalf("dispatch cell-size response: %v", err)
	}
	if got := editor.Text(); got != "" {
		t.Fatalf("cell-size response reached editor: %q", got)
	}
	if got, want := tui.GetCellDimensions(), (tui.CellDimensions{WidthPx: 10, HeightPx: 20}); got != want {
		t.Fatalf("cell dimensions = %+v, want %+v", got, want)
	}

	if err := mode.dispatchKey(context.Background(), "q"); err != nil {
		t.Fatalf("dispatch later user input: %v", err)
	}
	if got := editor.Text(); got != "q" {
		t.Fatalf("later user input = %q, want q", got)
	}
}
