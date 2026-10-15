package tui

import (
	"strings"
	"testing"
)

// assistant-message.ts:39,74 stores the output padding as given (constructor and setOutputPad); only the settings layer limits it to 0 or 1. A pad of 3 puts three
// columns before the text, so the component must not clamp it to 1.
func TestAssistantMessageOutputPadIsNotClamped(t *testing.T) {
	pad := 3
	assistant := NewAssistantMessageComponent(nil, false, nil, "", &pad, nil)
	assistant.SetTextDelta("hello")
	if got := stripANSI(assistant.Render(20)[1]); !strings.HasPrefix(got, "   hello") {
		t.Fatalf("constructor pad 3: %q", got)
	}
	assistant.SetOutputPad(2)
	if got := stripANSI(assistant.Render(20)[1]); !strings.HasPrefix(got, "  hello") || strings.HasPrefix(got, "   ") {
		t.Fatalf("SetOutputPad(2): %q", got)
	}
}
