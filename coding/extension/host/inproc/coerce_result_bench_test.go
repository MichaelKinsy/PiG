package inproc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// BenchmarkCoerceBeforeAgentStartResult decodes the result one subprocess before_agent_start handler returned: a system prompt with no lone surrogate.
func BenchmarkCoerceBeforeAgentStartResult(b *testing.B) {
	raw := json.RawMessage(`{"systemPrompt":"` + strings.Repeat("You are a careful assistant. ", 80) + `"}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := coerceResult[*extension.BeforeAgentStartEventResult](raw); !ok {
			b.Fatal("the result did not decode")
		}
	}
}
