package tui

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestInputSetValueRetainsUTF16Cursor(t *testing.T) {
	// input.ts setValue clamps the existing UTF-16 cursor; it neither moves to the end nor rounds a surviving surrogate-half position.
	input := NewInput(InputOptions{})
	input.SetText("abc")
	input.HandleInput("X")
	if got := input.Text(); got != "Xabc" {
		t.Errorf("initial setValue moved cursor: %q", got)
	}
	input.HandleInput("\x01")
	input.HandleInput("\x1b[C")
	input.SetText("😀")
	input.HandleInput("X")
	if got := jsstring.ToUTF16(input.Text()); !slices.Equal(got, []uint16{0xd83d, 'X', 0xde00}) {
		t.Errorf("split surrogate cursor=%x", got)
	}
}

func TestInputRendererEncodesSplitUTF16Cursor(t *testing.T) {
	input := NewInput(InputOptions{})
	input.HandleInput("x")
	input.SetValue("😀")
	input.Focused = true
	var output bytes.Buffer
	renderer := NewWithOutput(&output, 10, 4)
	renderer.Add(input)
	renderer.SetFocus(input)
	renderer.Render()
	if !utf8.Valid(output.Bytes()) {
		t.Fatalf("terminal received WTF-8: %x", output.Bytes())
	}
	if !strings.Contains(output.String(), "> �\x1b[7m�\x1b[27m      ") {
		t.Fatalf("split cursor output=%q", output.String())
	}
}

func TestInputStartsUnfocused(t *testing.T) {
	if NewInput(InputOptions{}).Focused {
		t.Fatal("Input starts unfocused until its TUI assigns focus")
	}
}

func TestInputEmptyAndBoundaryActionsBreakKillAccumulation(t *testing.T) {
	for _, action := range []string{"", "\x1b[3~"} {
		t.Run(action, func(t *testing.T) {
			input := NewInput(InputOptions{})
			input.SetText("first second")
			input.HandleInput("\x05")
			input.HandleInput("\x17")
			input.HandleInput(action)
			input.HandleInput("\x17")
			input.HandleInput("\x19")
			if got := input.Text(); got != "first " {
				t.Fatalf("empty/boundary action did not break kill accumulation: %q", got)
			}
		})
	}
}

func TestInputPasteRemovesOnlyFirstStartMarker(t *testing.T) {
	input := NewInput(InputOptions{})
	input.HandleInput("\x1b[200~outer\x1b[200~inner\x1b[201~")
	if got := input.Text(); got != "outer\x1b[200~inner" {
		t.Fatalf("nested start marker lost: %q", got)
	}
}
