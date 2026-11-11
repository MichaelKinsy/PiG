package ai

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/modelgen"
	"github.com/MichaelKinsy/PiG/internal/modelgen/providers"
)

// Pi 9ee7586a9 (#10954): an OpenCode Go or Zen Claude model that accepts per-message effort is generated with
// compat.supportsMidConvoEffort, so the anthropic-messages request carries a per-message effort marker and thinking.block_binding
// drop_block. OpenCode Go rejected replayed thinking with a 400 after the tool list or system prompt changed.
//
// The compat below is what the catalog generator writes for the model (internal/modelgen/providers), decoded the way the provider
// receives it, so the test follows the generator's output through the request converter.
func TestOpencodeClaudeCatalogCompatReachesTheAnthropicPayload(t *testing.T) {
	messages := `{"messages":{"base_url":"https://opencode.ai/zen/go/v1"}}`
	source, err := modelgen.ParseJSON([]byte(`{"opencode-go":{"models":{
		"claude-haiku-5-5":{"id":"claude-haiku-5-5","type":"chat","api":` + messages + `,"capabilities":{"tools":{"supported":true},"reasoning":{"supported":true}},"modalities":{"input":["text"],"output":["text"]},"limit":{"context":200000,"output":8000},"cost":{"text":{"input":{"price":1},"output":{"price":2}}}},
		"claude-sonnet-4-5":{"id":"claude-sonnet-4-5","type":"chat","api":` + messages + `,"capabilities":{"tools":{"supported":true},"reasoning":{"supported":true}},"modalities":{"input":["text"],"output":["text"]},"limit":{"context":200000,"output":8000},"cost":{"text":{"input":{"price":1},"output":{"price":2}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var definition modelgen.ProviderDefinition
	for _, candidate := range providers.All() {
		if candidate.ID == "opencode-go" {
			definition = candidate
		}
	}
	catalogs, err := modelgen.BuildCatalog([]modelgen.ProviderDefinition{definition}, &modelgen.CatalogSources{ModelsDev: source.(*modelgen.Obj)}, false)
	if err != nil {
		t.Fatal(err)
	}
	compatOf := func(id string) *AnthropicMessagesCompat {
		t.Helper()
		for _, model := range catalogs[0].Chat {
			if model.Str("id") != id {
				continue
			}
			var compat AnthropicMessagesCompat
			if raw := model.Object("compat"); raw != nil {
				encoded, err := json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(encoded, &compat); err != nil {
					t.Fatal(err)
				}
			}
			return &compat
		}
		t.Fatalf("catalog has no %s", id)
		return nil
	}
	payloadFor := func(id string) map[string]any {
		t.Helper()
		provider := NewAnthropicProvider(AnthropicConfig{Model: id, ProviderID: "opencode-go", APIKey: "test-key", BaseURL: "http://127.0.0.1:9", Compat: compatOf(id)})
		defer func() {
			if err := provider.Close(); err != nil {
				t.Error(err)
			}
		}()
		history := Context{Messages: []Message{
			UserMessage{Content: UserText("one"), Timestamp: 1},
			AssistantMessage{API: APIAnthropicMessages, Provider: "opencode-go", Model: id, ProviderThinkingLevel: "low", StopReason: StopReasonStop, Content: []AssistantContentBlock{ThinkingContent{Thinking: "reasoning", ThinkingSignature: "signature"}, TextContent{Text: "answer"}}, Timestamp: 1},
			UserMessage{Content: UserText("two"), Timestamp: 2},
		}}
		return captureSamplingPayload(t, provider, NormalizeContext(history), StreamOptions{ThinkingEnabled: new(true), Effort: AnthropicEffort(ThinkingHigh)})
	}

	payload := payloadFor("claude-haiku-5-5")
	var markers []map[string]any
	for _, item := range payload["messages"].([]any) {
		if message := item.(map[string]any); message["role"] == "system" {
			markers = append(markers, message)
		}
	}
	if len(markers) != 2 {
		t.Fatalf("effort markers = %d in %v, want the historical and the active one", len(markers), payload["messages"])
	}
	if got := markers[1]["output_config"].(map[string]any)["effort"]; got != "high" {
		t.Errorf("active marker effort = %v, want high", got)
	}
	thinking, _ := payload["thinking"].(map[string]any)
	binding, _ := thinking["block_binding"].(map[string]any)
	if thinking["type"] != "adaptive" || binding["prefix_mismatch_behavior"] != "drop_block" {
		t.Errorf("thinking = %#v, want adaptive with block_binding drop_block", thinking)
	}

	// A Claude model that does not accept per-message effort sends neither.
	control := payloadFor("claude-sonnet-4-5")
	controlThinking, _ := control["thinking"].(map[string]any)
	if _, has := controlThinking["block_binding"]; has {
		t.Errorf("claude-sonnet-4-5 thinking = %#v, want no block_binding", controlThinking)
	}
	for _, item := range control["messages"].([]any) {
		if item.(map[string]any)["role"] == "system" {
			t.Errorf("claude-sonnet-4-5 carries an effort marker: %v", control["messages"])
		}
	}
}
