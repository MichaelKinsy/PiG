// SPDX-License-Identifier: MIT

package coding

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestRestoreSessionRuntimeStateDoesNotProjectMessages(t *testing.T) {
	services := newTestServices(t)
	session := icodingagent.NewSession("settings-only", services.CWD())
	for i := range 1000 {
		if _, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("message %d", i)}}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "last"}}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendThinkingLevelChange("high"); err != nil {
		t.Fatal(err)
	}
	projection := testing.AllocsPerRun(10, func() { _ = icodingagent.BuildSessionProjection(session.GetBranch(), icodingagent.LastLeaf(), nil) })
	restored := testing.AllocsPerRun(10, func() { restoreSessionRuntimeState(session, services, nil, ai.ThinkingLow, true) })
	if restored >= projection {
		t.Fatalf("settings restoration allocates %.0f, full message projection %.0f", restored, projection)
	}
}

// upstream: packages/coding-agent/src/core/sdk.ts:198-203 — supplied models bypass saved-model selection.
func TestRestoreSessionRuntimeStateExplicitModelSelection(t *testing.T) {
	services := newTestServices(t)
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModelChange("openai", "gpt-4o"); err != nil {
		t.Fatal(err)
	}
	fallback := fakeModel()
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprint(restore), func(t *testing.T) {
			model, thinking := restoreSessionRuntimeState(manager, services, fallback, ai.ThinkingLow, restore)
			if thinking != ai.ThinkingLow {
				t.Fatalf("thinking=%s want low", thinking)
			}
			if !restore && model != fallback {
				t.Fatalf("explicit fallback displaced by saved model: %v", model)
			}
			if restore && (model == nil || model.ID != "gpt-4o") {
				t.Fatalf("saved model=%v want gpt-4o", model)
			}
		})
	}
}

// Pi session-manager.ts:417-431 resolves settings on the selected branch and lets
// an assistant's model replace a preceding model_change; sdk.ts:194-249 consumes it.
func TestRestoreSessionRuntimeStateUsesActiveBranchAssistantModel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test")
	services := newTestServices(t)
	fallback, err := BuildModel("openai/gpt-5", services)
	if err != nil {
		t.Fatal(err)
	}
	s := icodingagent.NewSession("restored", "/project")
	if _, err := s.AppendModelChange("openai", "gpt-5"); err != nil {
		t.Fatal(err)
	}
	assistant, err := s.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "selected answer"}}, Provider: "openai", ModelID: "gpt-4o", API: "openai-completions", StopReason: ai.StopReasonStop}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendModelChange("openai", "gpt-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendThinkingLevelChange("high"); err != nil {
		t.Fatal(err)
	}
	if err := s.Branch(assistant); err != nil {
		t.Fatal(err)
	}
	model, thinking := restoreSessionRuntimeState(s, services, fallback, ai.ThinkingLow, true)
	if model.ID != "gpt-4o" || thinking != ai.ThinkingLow {
		t.Fatalf("restored=%s/%s want gpt-4o/low", model.ID, thinking)
	}
	state := icodingagent.BuildSessionContext(s.GetBranch(), icodingagent.LastLeaf(), nil)
	fmt.Printf("SESSION_CONTEXT model=%s thinking=%s\n", state.Model.ModelID, state.ThinkingLevel)
	if _, err := s.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "untagged historical reply"}}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	model, thinking = restoreSessionRuntimeState(s, services, fallback, ai.ThinkingLow, true)
	if model.ID != fallback.ID || thinking != ai.ThinkingLow {
		t.Fatalf("missing assistant metadata did not use the fallback: %s/%s", model.ID, thinking)
	}
}

// virtual-models.ts:134-136 returns the last model_change even when its provider is not a string, and sdk.ts:213-220
// then restores nothing (the lookup compares strings), so the explicit fallback stays; the earlier model_change is not
// restored.
func TestRestoreSessionRuntimeStateStopsAtAModelChangeWithANonStringProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test")
	services := newTestServices(t)
	fallback, err := BuildModel("openai/gpt-5", services)
	if err != nil {
		t.Fatal(err)
	}
	s := icodingagent.NewSession("non-string-provider", "/project")
	if _, err := s.AppendModelChange("openai", "gpt-4o"); err != nil {
		t.Fatal(err)
	}
	parent := s.GetLeafID()
	if err := s.AppendEntry(map[string]any{"type": "model_change", "id": "hand-edited", "parentId": parent, "timestamp": "2026-10-05T00:00:00.000Z", "provider": 5, "modelId": "gpt-4o"}); err != nil {
		t.Fatal(err)
	}
	if model, _ := restoreSessionRuntimeState(s, services, fallback, ai.ThinkingLow, true); model != fallback {
		t.Fatalf("restored %v, want the fallback", model)
	}
}
