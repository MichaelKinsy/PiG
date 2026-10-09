package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// The same binding runs at startup and after model changes; it reads no transcript history and starts no background work.
func BenchmarkInteractiveThinkingStateBinding(b *testing.B) {
	model := &ai.Model{ID: "sparse", Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevel(ai.ThinkingHigh)}}
	m := &InteractiveMode{agent: mustNewAgent(agent.AgentOptions{Model: model, ThinkingLevel: ai.ThinkingHigh}), editor: tui.NewEditor(), statusLine: NewFooterComponent(model, "", nil)}
	b.ReportAllocs()
	for b.Loop() {
		m.initThinkingLevel()
	}
}

// Pi sdk.ts:231-255 selects/clamps once. footer.ts:186-190 reads that Session state, not settings.
func TestInteractiveStartupPreservesSessionThinking(t *testing.T) {
	for _, tc := range []struct {
		spec  string
		level ai.ModelThinkingLevel
	}{
		{"deepseek/deepseek-flash", ai.ThinkingHigh},
		{"anthropic/claude-sonnet-4-6", ai.ThinkingLow},
		{"openai/gpt-5.4", ai.ThinkingHigh},
		{"github-copilot/claude-sonnet-4.6", ai.ThinkingOff},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			generated, ok := ai.LookupModelExact(tc.spec)
			if !ok {
				t.Fatal("missing pinned model", tc.spec)
			}
			model := generated.ToModel()
			model.Capabilities = generated.ToCapabilities()
			a := mustNewAgent(agent.AgentOptions{Model: model, ThinkingLevel: tc.level})
			m := &InteractiveMode{opts: InteractiveModeOptions{Model: model, Settings: Settings{DefaultThinkingLevel: "medium"}}, agent: a, editor: tui.NewEditor(), statusLine: NewFooterComponent(model, "", nil)}
			m.initThinkingLevel()
			if got := a.ThinkingLevel(); got != tc.level {
				t.Fatalf("startup changed Session level: got %q, want %q", got, tc.level)
			}
			if m.thinkingLevel != string(tc.level) || m.editor.ThinkingLevel != string(tc.level) {
				t.Fatalf("UI level=%s editor=%s want=%s", m.thinkingLevel, m.editor.ThinkingLevel, tc.level)
			}
			suffix := " • " + string(tc.level)
			if tc.level == ai.ThinkingOff {
				suffix = " • thinking off"
			}
			if line := stripANSI(m.statusLine.Render(160)[1]); !strings.Contains(line, model.ID+suffix) {
				t.Fatalf("footer=%q want suffix=%q", line, suffix)
			}
		})
	}
}
