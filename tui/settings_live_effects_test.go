package tui

import (
	"strings"
	"testing"
)

func TestEditorRuntimeSettingsApplyImmediately(t *testing.T) {
	editor := NewEditor()
	editor.SetText("hello")
	before := editor.Render(20)

	editor.SetPaddingX(2)
	if editor.PaddingX() != 2 {
		t.Fatalf("padding = %d, want 2", editor.PaddingX())
	}
	after := editor.Render(20)
	if len(after) < 3 || !strings.HasPrefix(after[2], "  hello") {
		t.Fatalf("padded editor line = %q", after)
	}
	if strings.Join(before, "\n") == strings.Join(after, "\n") {
		t.Fatal("padding change did not alter rendered editor")
	}

	editor.SetAutocompleteMaxVisible(30)
	if got := editor.AutocompleteMaxVisible(); got != 20 {
		t.Fatalf("autocomplete max = %d, want clamped 20", got)
	}
	editor.SetAutocompleteMaxVisible(1)
	if got := editor.AutocompleteMaxVisible(); got != 3 {
		t.Fatalf("autocomplete max = %d, want clamped 3", got)
	}
}

func TestMessageOutputPaddingAppliesImmediately(t *testing.T) {
	user := NewUserMessageBlock("hello")
	assistant := NewAssistantMessageBlock(false)
	assistant.SetTextDelta("hello")
	custom := NewCustomMessageComponent("note", "hello")

	user.SetOutputPad(0)
	assistant.SetOutputPad(0)
	custom.SetOutputPad(0)

	if got := stripANSI(user.Render(20)[1]); !strings.HasPrefix(got, "hello") {
		t.Fatalf("user line retained padding: %q", got)
	}
	if got := stripANSI(assistant.Render(20)[1]); !strings.HasPrefix(got, "hello") {
		t.Fatalf("assistant line retained padding: %q", got)
	}
	// The component spacer and Box top padding precede the label. The custom
	// box keeps its fixed inset independently of transcript output padding.
	if got := stripANSI(custom.Render(20)[2]); !strings.HasPrefix(got, " [note]") {
		t.Fatalf("default custom box lost its fixed inset: %q", got)
	}
}
