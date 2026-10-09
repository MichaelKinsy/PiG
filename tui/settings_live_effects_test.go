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
	if len(after) < 3 || !strings.HasPrefix(after[1], "  hello") {
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
	user := NewUserMessageComponent("hello", nil, 1, nil)
	assistant := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	assistant.SetTextDelta("hello")
	custom := NewCustomMessageComponent(&CustomMessage{CustomType: "note", Content: "hello"}, nil, nil, 1)

	user.SetOutputPad(0)
	assistant.SetOutputPad(0)
	custom.SetOutputPad(0)

	if got := stripANSI(user.Render(20)[1]); !strings.HasPrefix(got, "hello") {
		t.Fatalf("user line retained padding: %q", got)
	}
	if got := stripANSI(assistant.Render(20)[1]); !strings.HasPrefix(got, "hello") {
		t.Fatalf("assistant line retained padding: %q", got)
	}
	// custom-message.ts:90: the default box takes its horizontal padding from outputPad, so pad 0 leaves no inset and pad 1 keeps Pi's one cell.
	// The component spacer and Box top padding precede the label.
	if got := stripANSI(custom.Render(20)[2]); !strings.HasPrefix(got, "[note]") {
		t.Fatalf("custom box kept an inset at output pad 0: %q", got)
	}
	custom.SetOutputPad(1)
	if got := stripANSI(custom.Render(20)[2]); !strings.HasPrefix(got, " [note]") {
		t.Fatalf("custom box lost its inset at output pad 1: %q", got)
	}
}
