package ai_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// Authentic model-generated signatures are live-only. These fixtures exercise every available pinned pair's real converter with deterministic thinking/tool cycles and the original combined-history handoff assertion.
func TestCrossProviderHandoffUpstream(t *testing.T) {
	pairs := []struct {
		provider, model, label string
		api                    ai.API
	}{
		{"anthropic", "claude-sonnet-4-5", "anthropic-claude-sonnet-4-5", ""}, {"google", "gemini-3-flash-preview", "google-gemini-3-flash-preview", ""},
		{"openai", "gpt-4o-mini", "openai-completions-gpt-4o-mini", ai.APIOpenAICompletions}, {"openai", "gpt-5-mini", "openai-responses-gpt-5-mini", ""}, {"azure", "gpt-4o-mini", "azure-gpt-4o-mini", ""},
		{"openai-codex", "gpt-5.5", "openai-codex-gpt-5.5", ""}, {"github-copilot", "claude-sonnet-4.5", "copilot-claude-sonnet-4.5", ""}, {"github-copilot", "gpt-5.1-codex", "copilot-gpt-5.1-codex", ""},
		{"github-copilot", "gemini-3-flash-preview", "copilot-gemini-3-flash-preview", ""}, {"github-copilot", "grok-code-fast-1", "copilot-grok-code-fast-1", ""},
		{"amazon-bedrock", "global.anthropic.claude-sonnet-4-5-20250929-v1:0", "bedrock-claude-sonnet-4-5", ""}, {"xai", "grok-4.3", "xai-grok-4.3", ""}, {"cerebras", "zai-glm-4.7", "cerebras-zai-glm-4.7", ""},
		{"cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6", "cloudflare-kimi-k2.6", ""}, {"cloudflare-ai-gateway", "workers-ai/@cf/moonshotai/kimi-k2.6", "cloudflare-gateway-kimi-k2.6", ""},
		{"cloudflare-ai-gateway", "claude-sonnet-4-5", "cloudflare-gateway-claude-sonnet-4-5", ""}, {"cloudflare-ai-gateway", "gpt-5.1", "cloudflare-gateway-gpt-5.1", ""},
		{"groq", "openai/gpt-oss-120b", "groq-gpt-oss-120b", ""}, {"huggingface", "moonshotai/Kimi-K2.5", "huggingface-kimi-k2.5", ""}, {"together", "moonshotai/Kimi-K3", "together-kimi-k3", ""},
		{"baseten", "zai-org/GLM-5.2", "baseten-glm-5.2", ""}, {"kimi-coding", "kimi-for-coding", "kimi-for-coding", ""}, {"meta", "muse-spark-1.3", "meta-muse-spark-1.3", ""}, {"mistral", "devstral-medium-latest", "mistral-devstral-medium", ""},
		{"minimax", "MiniMax-M2.7", "minimax-m2.7", ""}, {"minimax-cn", "MiniMax-M2.7", "minimax-m2.7", ""},
		{"opencode", "big-pickle", "zen-big-pickle", ""}, {"opencode", "claude-sonnet-4-5", "zen-claude-sonnet-4-5", ""}, {"opencode", "gemini-3-flash", "zen-gemini-3-flash", ""}, {"opencode", "glm-4.7-free", "zen-glm-4.7-free", ""},
		{"opencode", "gpt-5.2-codex", "zen-gpt-5.2-codex", ""}, {"opencode", "minimax-m2.1-free", "zen-minimax-m2.1-free", ""}, {"opencode-go", "kimi-k2.5", "go-kimi-k2.5", ""}, {"opencode-go", "minimax-m2.5", "go-minimax-m2.5", ""},
		{"xiaomi", "mimo-v2.5-pro", "xiaomi-mimo-v2.5-pro", ""}, {"xiaomi-token-plan-cn", "mimo-v2.5-pro", "xiaomi-token-plan-cn-mimo-v2.5-pro", ""}, {"xiaomi-token-plan-ams", "mimo-v2.5-pro", "xiaomi-token-plan-ams-mimo-v2.5-pro", ""}, {"xiaomi-token-plan-sgp", "mimo-v2.5-pro", "xiaomi-token-plan-sgp-mimo-v2.5-pro", ""},
		{"qwen-token-plan", "qwen3.7-max", "qwen-token-plan-qwen3.7-max", ""}, {"qwen-token-plan-cn", "qwen3.7-max", "qwen-token-plan-cn-qwen3.7-max", ""}, {"qwen-token-plan-individual", "qwen3.8-max", "qwen-token-plan-individual-qwen3.8-max", ""},
		{"qwen-token-plan-individual", "deepseek-v4-flash-0731", "qwen-token-plan-individual-deepseek-v4-flash-0731", ""}, {"qwen-token-plan-individual", "glm-5.2", "qwen-token-plan-individual-glm-5.2", ""},
	}
	tool := ai.ToolSchema{Name: "double_number", Description: "Doubles a number and returns the result", Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "number", "description": "A number to double"}}, "required": []string{"value"}}}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	contexts := map[string][]ai.Message{}
	labels := []string{}
	models := map[int]ai.GeneratedModel{}
	for index, pair := range pairs {
		found, ok := ai.LookupModelExact(pair.provider + "/" + pair.model)
		if !ok {
			t.Logf("upstream generateContext returns null for missing model %s/%s", pair.provider, pair.model)
			continue
		}
		model := *found
		if pair.api != "" {
			model.API = pair.api
		}
		models[index] = model
		provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: pair.provider, Model: pair.model})
		id := fmt.Sprintf("call_%d", index)
		switch model.API {
		case ai.APIAnthropicMessages:
			id = fmt.Sprintf("toolu_%d", index)
		case ai.APIOpenAIResponses, ai.APIAzureOpenAIResponses, ai.APIOpenAICodexResponses:
			id += fmt.Sprintf("|fc_%d", index)
		case ai.APIMistralConversations:
			id = fmt.Sprintf("call%05d", index)
		}
		provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxThinking("Double 21 to get 42"), ai.FauxToolCall("double_number", map[string]any{"value": 21}, id)}, StopReason: "toolUse"}), ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("42")}, StopReason: "stop"})})
		user := ai.UserMessage{Content: ai.UserText("Please double the number 21 using the double_number tool.")}
		target := &ai.Model{ID: pair.model, Provider: provider}
		first := services.ModelRuntime().Complete(t.Context(), target, ai.Context{SystemPrompt: "You are a helpful assistant. Use the provided tool to complete the task.", Messages: []ai.Message{user}, Tools: []ai.ToolSchema{tool}}, ai.StreamOptions{})
		if first.StopReason != ai.StopReasonToolUse {
			t.Fatal(first.ErrorMessage)
		}
		first.API = model.API
		messages := []ai.Message{user, *first, ai.ToolResultMessage{ToolCallID: id, ToolName: "double_number", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "42"}}}}
		final := services.ModelRuntime().Complete(t.Context(), target, ai.Context{SystemPrompt: "You are a helpful assistant.", Messages: messages, Tools: []ai.ToolSchema{tool}}, ai.StreamOptions{})
		if final.StopReason != ai.StopReasonStop {
			t.Fatal(final.ErrorMessage)
		}
		final.API = model.API
		messages = append(messages, *final)
		if _, exists := contexts[pair.label]; !exists {
			labels = append(labels, pair.label)
		}
		contexts[pair.label] = messages
	}
	// .upstream/v0.99.1/packages/ai/test/cross-provider-handoff.test.ts:392
	t.Run("should have at least 2 fixtures to test handoffs", func(t *testing.T) {
		if len(contexts) < 2 {
			t.Fatalf("fixtures=%d", len(contexts))
		}
	})
	// .upstream/v0.99.1/packages/ai/test/cross-provider-handoff.test.ts:396
	t.Run("should handle cross-provider handoffs for each target", func(t *testing.T) {
		failures := []string{}
		for index, pair := range pairs {
			model, exists := models[index]
			if !exists {
				continue
			}
			messages := []ai.Message{}
			for _, label := range labels {
				if label != pair.label {
					messages = append(messages, contexts[label]...)
				}
			}
			messages = append(messages, ai.UserMessage{Content: ai.UserText("Great, thanks for all that help! Now just say 'Hello, handoff successful!' to confirm you received everything.")})
			request := ai.Context{SystemPrompt: "You are a helpful assistant.", Messages: messages, Tools: []ai.ToolSchema{tool}}
			payload := captureImageToolPayload(t, matrixProvider(t, &model, "https://example.invalid"), request)
			calls, results := handoffToolCounts(t, payload)
			paired := len(calls) > 0 && reflect.DeepEqual(calls, results)
			provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: pair.provider, Model: pair.model})
			provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(context ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
				if !paired || len(context.Messages()) != len(messages)+1 {
					return ai.FauxResponse{StopReason: "error", ErrorMessage: "handoff lost history or tool pairing"}, nil
				}
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Hello, handoff successful!")}, StopReason: "stop"}, nil
			})})
			response := services.ModelRuntime().Complete(t.Context(), &ai.Model{ID: pair.model, Provider: provider}, request, ai.StreamOptions{})
			if response.StopReason == ai.StopReasonError {
				failures = append(failures, pair.label+": "+response.ErrorMessage)
			}
		}
		if len(failures) != 0 {
			t.Fatalf("handoff failures: %v", failures)
		}
	})
}

func handoffToolCounts(t *testing.T, payload string) (map[string]int, map[string]int) {
	t.Helper()
	var root any
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		t.Fatal(err)
	}
	calls, results := map[string]int{}, map[string]int{}
	add := func(target map[string]int, value any) {
		if text, ok := value.(string); ok && text != "" {
			target[text]++
		}
	}
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, child := range value {
				visit(child)
			}
		case map[string]any:
			switch value["type"] {
			case "tool_use":
				add(calls, value["id"])
			case "tool_result":
				add(results, value["tool_use_id"])
			case "function_call", "custom_tool_call":
				add(calls, value["call_id"])
			case "function_call_output", "custom_tool_call_output":
				add(results, value["call_id"])
			}
			// Mistral onPayload exposes SDK names before wire lowering; OpenAI exposes wire names.
			for _, fields := range [][2]string{{"tool_calls", "tool_call_id"}, {"toolCalls", "toolCallId"}} {
				if toolCalls, ok := value[fields[0]].([]any); ok {
					for _, call := range toolCalls {
						add(calls, call.(map[string]any)["id"])
					}
				}
				if value["role"] == "tool" {
					add(results, value[fields[1]])
				}
			}
			if call, ok := value["functionCall"].(map[string]any); ok {
				add(calls, call["name"])
			}
			if result, ok := value["functionResponse"].(map[string]any); ok {
				add(results, result["name"])
			}
			if id, ok := value["ToolUseId"].(string); ok {
				if _, call := value["Input"]; call {
					add(calls, id)
				} else if _, result := value["Content"]; result {
					add(results, id)
				}
			}
			for _, key := range slices.Sorted(maps.Keys(value)) {
				visit(value[key])
			}
		}
	}
	visit(root)
	return calls, results
}
