package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestExtensionInputComponentRender(t *testing.T) {
	input := NewExtensionInputComponent("Rename Session", "session name")
	input.SetText("alpha")

	lines := input.Render(40)
	if len(lines) < 7 {
		t.Fatalf("Render returned %d lines, want >= 7", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Rename Session") {
		t.Fatalf("render missing title: %q", joined)
	}
	// Pi Input.setValue keeps the initial cursor before the first character.
	if !strings.Contains(joined, "> "+widthx.CursorMarker+"\x1b[7ma\x1b[27mlpha") {
		t.Fatalf("render missing bare input line: %q", joined)
	}
	if !strings.Contains(joined, "submit") || !strings.Contains(joined, "cancel") {
		t.Fatalf("render missing key hints: %q", joined)
	}
}

func TestExtensionInputComponentDelegatesDoneState(t *testing.T) {
	input := NewExtensionInputComponent("Prompt", "")
	input.HandleInput("h")
	input.HandleInput("i")
	input.HandleInput("\r")
	if !input.Done() {
		t.Fatal("expected done after submit")
	}
	if input.Cancelled() {
		t.Fatal("expected submitted input not to be cancelled")
	}
	if got := input.Text(); got != "hi" {
		t.Fatalf("Text() = %q want %q", got, "hi")
	}
}
