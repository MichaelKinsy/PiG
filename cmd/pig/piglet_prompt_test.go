package main

import (
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
)

// TestApplyPigletPreStart_SystemPrompt locks piglet system-prompt application:
// an active piglet's prompt becomes the base prompt unless the user passed
// --system-prompt, and a nil / prompt-less piglet changes nothing.
func TestApplyPigletPreStart_SystemPrompt(t *testing.T) {
	withPrompt := &piglet.Piglet{Name: "p", SystemPrompt: &piglet.PromptRef{Text: "BAKED ROLE"}}

	t.Run("applies piglet prompt", func(t *testing.T) {
		var f CLIFlags
		applyPigletPreStart(withPrompt, &f)
		if f.SystemPrompt != "BAKED ROLE" {
			t.Fatalf("SystemPrompt = %q, want BAKED ROLE", f.SystemPrompt)
		}
	})

	t.Run("explicit --system-prompt wins", func(t *testing.T) {
		f := CLIFlags{SystemPrompt: "USER SET"}
		applyPigletPreStart(withPrompt, &f)
		if f.SystemPrompt != "USER SET" {
			t.Fatalf("SystemPrompt = %q, want the user's value", f.SystemPrompt)
		}
	})

	t.Run("nil piglet is a no-op", func(t *testing.T) {
		var f CLIFlags
		applyPigletPreStart(nil, &f)
		if f.SystemPrompt != "" {
			t.Fatalf("SystemPrompt = %q, want empty", f.SystemPrompt)
		}
	})

	t.Run("prompt-less piglet leaves it empty", func(t *testing.T) {
		var f CLIFlags
		applyPigletPreStart(&piglet.Piglet{Name: "x"}, &f)
		if f.SystemPrompt != "" {
			t.Fatalf("SystemPrompt = %q, want empty", f.SystemPrompt)
		}
	})
}
