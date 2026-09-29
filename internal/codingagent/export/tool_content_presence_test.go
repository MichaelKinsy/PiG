package export

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestToolResultContentPreservesEmptyText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   any
		text    string
		present bool
	}{
		{"empty array", []any{}, "", false},
		{"empty string", "", "", true},
		{"empty block", []any{map[string]any{"type": "text", "text": ""}}, "", true},
		{"leading empty block", []any{map[string]any{"type": "text", "text": ""}, map[string]any{"type": "text", "text": "next"}}, "\nnext", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := toolResultContent(tc.input)
			text := (agent.AgentToolResult{Content: content}).Text()
			present := false
			for _, block := range content {
				if _, ok := block.(ai.TextContent); ok {
					present = true
				}
			}
			if text != tc.text || present != tc.present {
				t.Fatalf("text=%q present=%t, want text=%q present=%t", text, present, tc.text, tc.present)
			}
		})
	}
}
