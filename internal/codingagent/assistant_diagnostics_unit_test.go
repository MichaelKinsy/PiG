package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func diagnosticAssistant() agent.AgentMessage {
	message := assistantMsg("", ai.TextContent{Text: "survived"}).Assistant
	message.API, message.Provider, message.ModelID = "anthropic-messages", "anthropic", "claude-fable-5-1"
	message.Timestamp = 1
	message.Usage = &ai.Usage{Input: 1, Output: 1, TotalTokens: 2}
	var transformations []any
	for _, index := range []int{2, 5, 8} {
		transformations = append(transformations, map[string]any{"type": "thinking_dropped", "path": fmt.Sprintf("messages.%d.content.0", index), "reason": "prefix_binding_mismatch"})
	}
	message.Diagnostics = []ai.AssistantMessageDiagnostic{{Type: "anthropic_input_transformations", Timestamp: 1, Details: map[string]any{"transformations": transformations}}}
	return agent.AgentMessage{Assistant: message}
}

func TestAssistantDiagnosticsUpstreamCases(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-assistant-diagnostics.test.ts:63
	t.Run("shows Anthropic thinking drops when cache miss notices are enabled", func(t *testing.T) {
		for _, enabled := range []bool{true, false} {
			m := &InteractiveMode{chatContainer: tui.NewContainer()}
			m.opts.Settings.ShowCacheMissNotices = enabled
			m.maybeShowThinkingDropNotice(diagnosticAssistant().Assistant)
			if enabled {
				if got := stripANSITest(strings.Join(m.chatContainer.Render(120), "\n")); !strings.Contains(got, "Anthropic dropped 3 thinking blocks (details in session)") {
					t.Fatalf("notice = %q", got)
				}
			} else if m.chatContainer.ChildCount() != 0 {
				t.Fatal("disabled notice added chat children")
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-assistant-diagnostics.test.ts:83
	t.Run("does not repeat unchanged Anthropic thinking drops", func(t *testing.T) {
		m := resumeThinkingMode(t, false, diagnosticAssistant())
		m.opts.Settings.ShowCacheMissNotices = true
		m.renderSessionEntries()
		m.chatContainer.Clear()
		current := diagnosticAssistant()
		current.Assistant.Timestamp = 2
		m.maybeShowThinkingDropNotice(current.Assistant)
		if m.chatContainer.ChildCount() != 0 {
			t.Fatal("unchanged diagnostic count added chat children")
		}
	})
}

func TestCountDroppedThinkingBlocksIgnoresOtherDiagnosticShapes(t *testing.T) {
	message := diagnosticAssistant().Assistant
	for _, value := range []any{nil, "thinking_dropped", map[string]any{"type": "thinking_dropped"}, []any{nil, 3, "thinking_dropped", map[string]any{"type": "other"}}} {
		message.Diagnostics[0].Details["transformations"] = value
		if count := countDroppedThinkingBlocks(message); count != 0 {
			t.Fatalf("malformed transformations %#v count = %d", value, count)
		}
	}
	message.Diagnostics[0].Type = "other"
	message.Diagnostics[0].Details["transformations"] = []any{map[string]any{"type": "thinking_dropped"}}
	if count := countDroppedThinkingBlocks(message); count != 0 {
		t.Fatalf("unrelated diagnostic count = %d", count)
	}
}

func BenchmarkThinkingDropNoticeUnchanged(b *testing.B) {
	message := diagnosticAssistant().Assistant
	m := &InteractiveMode{opts: InteractiveOptions{Settings: Settings{ShowCacheMissNotices: true}}, previousThinkingDroppedCount: 3}
	b.ReportAllocs()
	for b.Loop() {
		m.maybeShowThinkingDropNotice(message)
	}
}

func TestThinkingDropNoticeTracksTheSelectedBranch(t *testing.T) {
	m := resumeThinkingMode(t, false, diagnosticAssistant(), userMsg("next"), assistantMsg("no drops"))
	m.opts.Settings.ShowCacheMissNotices = true
	m.renderSessionEntries()
	m.chatContainer.Clear()
	m.maybeShowThinkingDropNotice(diagnosticAssistant().Assistant)
	if m.chatContainer.ChildCount() == 0 {
		t.Fatal("zero-drop latest response failed to reset the comparison")
	}
	branch := m.currentSession().GetBranch()
	if err := m.currentSession().Fork(branch[0].Base.ID); err != nil {
		t.Fatal(err)
	}
	m.renderSessionEntries()
	m.chatContainer.Clear()
	m.maybeShowThinkingDropNotice(diagnosticAssistant().Assistant)
	if m.chatContainer.ChildCount() != 0 {
		t.Fatal("branch replacement retained the other branch's comparison")
	}
}
