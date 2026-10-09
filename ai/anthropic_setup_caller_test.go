package ai_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// Pi's provider owns the errored response metadata before lazy/runtime forwarding. A runtime-generated generic error must not replace the provider's active effort.
func TestAnthropicModelRuntimePreservesSetupResult(t *testing.T) {
	dir := t.TempDir()
	config := `{"providers":{"managed-proxy":{"api":"anthropic-messages","baseUrl":"https://example.invalid","apiKey":"test-key","compat":{"supportsMidConvoEffort":true,"forceAdaptiveThinking":true},"models":[{"id":"managed-model","name":"Managed Model","reasoning":true}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("managed-proxy", "managed-model")
	if model == nil {
		t.Fatal("configured model was not loaded")
	}
	for _, history := range []struct {
		name     string
		messages []ai.Message
	}{
		{"fresh", []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}},
		{"poisoned", []ai.Message{
			ai.AssistantMessage{API: ai.APIOpenAIResponses, Provider: "old-provider", Model: "old-model", StopReason: ai.StopReasonError, ErrorMessage: "old failure", Content: nil},
			ai.AssistantMessage{API: ai.APIAnthropicMessages, Provider: "managed-proxy", Model: "managed-model", StopReason: ai.StopReasonAborted, ProviderThinkingLevel: "high", Content: []ai.AssistantContentBlock{ai.TextContent{Text: ""}, ai.ToolCall{ID: "orphaned", Name: "read", Arguments: ai.JsonObject{}}}},
			ai.UserMessage{Content: ai.UserText("continue")},
		}},
	} {
		t.Run(history.name, func(t *testing.T) {
			called := false
			result := services.ModelRuntime().Complete(t.Context(), model, ai.Context{Messages: history.messages}, ai.StreamOptions{Effort: "low", OnPayload: func(_ any, selected *ai.Model) (any, error) {
				called = true
				if selected.DisplayName != model.DisplayName || selected.ProviderMeta.API != model.ProviderMeta.API || !selected.ProviderMeta.Reasoning || selected.ProviderMeta.Compat == nil || selected.ProviderMeta.Compat.SupportsMidConvoEffort == nil || !*selected.ProviderMeta.Compat.SupportsMidConvoEffort {
					t.Errorf("ModelRuntime callback lost selected metadata: %#v", selected)
				}
				return nil, errors.New("payload captured")
			}})
			if !called || result == nil || result.StopReason != ai.StopReasonError || result.ErrorMessage != "payload captured" || result.ProviderThinkingLevel != "low" || result.Provider != "managed-proxy" || result.Model != "managed-model" || result.API != ai.APIAnthropicMessages {
				t.Fatalf("called=%v result=%#v", called, result)
			}
		})
	}
}
