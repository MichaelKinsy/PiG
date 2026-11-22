package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

// Ports Pi edfe6b2ef (#12273) and fcc33744b (#12269) from packages/ai/test/openai-codex-stream.test.ts:
// - Codex sends the system prompt as a leading developer input item and omits top-level instructions.
// - GPT-6 Codex models with supportsMidConvoEffort send thinking level changes (including off -> none)
//   as configuration_update input items with a fixed reasoning.effort of "medium".
// - Codex forwards maxTokens as max_output_tokens (at least 16) unless supportsMaxOutputTokens is false.

func codexTestModel(id string, compat *OpenAIResponsesCompat, levels ThinkingLevelMap) *Model {
	return &Model{
		ID:          id,
		DisplayName: id,
		Input:       []string{"text"},
		Capabilities: ModelCapabilities{
			ContextWindow:   400000,
			MaxOutputTokens: 128000,
			MaxThinking:     ThinkingLevelMax,
		},
		ProviderMeta: ProviderMetadata{
			API:        APIOpenAICodexResponses,
			ProviderID: "openai-codex",
			BaseURL:    "https://chatgpt.com/backend-api",
			Reasoning:  true,
			Compat:     compat,
		},
		ThinkingLevelMap: levels,
	}
}

func summarizeCodexInput(t *testing.T, body map[string]json.RawMessage) []string {
	t.Helper()
	var items []struct {
		Type      string `json:"type"`
		Role      string `json:"role"`
		Reasoning *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(body["input"], &items); err != nil {
		t.Fatalf("unmarshal input: %v (%s)", err, body["input"])
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch {
		case item.Type == "configuration_update" && item.Reasoning != nil:
			out = append(out, "configuration_update:"+item.Reasoning.Effort)
		case item.Role != "":
			out = append(out, item.Role)
		default:
			out = append(out, item.Type)
		}
	}
	return out
}

func captureCodexSSE(t *testing.T, model *Model, run func(fetch *http.Client) (*AssistantMessageEventStream, error)) ([]map[string]json.RawMessage, *AssistantMessage) {
	t.Helper()
	var bodies []map[string]json.RawMessage
	client := &http.Client{Transport: codexRoundTripper(func(r *http.Request) (*http.Response, error) {
		bodies = append(bodies, codexUpstreamBody(t, r))
		return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
	})}
	stream, err := run(client)
	if err != nil {
		t.Fatalf("stream setup: %v", err)
	}
	result := stream.Result()
	if result.StopReason == StopReasonError {
		t.Fatalf("stream error: %s", result.ErrorMessage)
	}
	return bodies, result
}

func TestCodexSystemPromptAsLeadingDeveloperItemUpstream(t *testing.T) {
	// Regression test for https://github.com/earendil-works/pi/issues/12273 (Pi edfe6b2ef)
	token := codexTestToken(t, "acc_test")
	model := codexTestModel("gpt-6.2-sol", nil, nil)
	userMsg := UserMessage{Content: UserText("Say hello"), Timestamp: 1}
	userItem := `{"role":"user","content":[{"type":"input_text","text":"Say hello"}]}`

	cases := []struct {
		name         string
		systemPrompt string
		wantInput    string
	}{
		{
			name:         "with system prompt sends leading developer item and no instructions",
			systemPrompt: "Be terse.",
			wantInput:    `[{"role":"developer","content":"Be terse."},` + userItem + `]`,
		},
		{
			name:         "without system prompt sends only user item and no instructions",
			systemPrompt: "",
			wantInput:    `[` + userItem + `]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bodies, _ := captureCodexSSE(t, model, func(fetch *http.Client) (*AssistantMessageEventStream, error) {
				return StreamOpenAICodexResponses(t.Context(), model, NormalizeContext(Context{
					SystemPrompt: tc.systemPrompt,
					Messages:     []Message{userMsg},
				}), StreamOptions{
					APIKey:    token,
					Transport: TransportSSE,
					Fetch:     fetch,
				})
			})
			if len(bodies) != 1 {
				t.Fatalf("captured %d requests, want 1", len(bodies))
			}
			if raw, ok := bodies[0]["instructions"]; ok {
				t.Fatalf("instructions = %s, want omitted", raw)
			}
			var got any
			if err := json.Unmarshal(bodies[0]["input"], &got); err != nil {
				t.Fatal(err)
			}
			assertCatalogJSON(t, got, tc.wantInput)
		})
	}
}

func TestCodexConfigurationUpdateEffortUpstream(t *testing.T) {
	// Regression tests for https://github.com/earendil-works/pi/issues/12269 (Pi fcc33744b)
	token := codexTestToken(t, "acc_test")

	t.Run("sends thinking level changes as configuration_update items with fixed request effort", func(t *testing.T) {
		model := codexTestModel("gpt-6.2-sol", &OpenAIResponsesCompat{SupportsMidConvoEffort: new(true)}, nil)
		firstMessages := []Message{UserMessage{Content: UserText("Say hello"), Timestamp: 1}}

		bodies1, first := captureCodexSSE(t, model, func(fetch *http.Client) (*AssistantMessageEventStream, error) {
			return StreamOpenAICodexResponses(t.Context(), model, NormalizeContext(Context{Messages: firstMessages}), StreamOptions{
				APIKey:          token,
				Transport:       TransportSSE,
				ReasoningEffort: "low",
				Fetch:           fetch,
			})
		})
		secondMessages := append(append([]Message{}, firstMessages...), *first, UserMessage{Content: UserText("Again"), Timestamp: 2})
		bodies2, second := captureCodexSSE(t, model, func(fetch *http.Client) (*AssistantMessageEventStream, error) {
			return StreamOpenAICodexResponses(t.Context(), model, NormalizeContext(Context{Messages: secondMessages}), StreamOptions{
				APIKey:          token,
				Transport:       TransportSSE,
				ReasoningEffort: "high",
				Fetch:           fetch,
			})
		})

		if first.ProviderThinkingLevel != "low" {
			t.Errorf("first.ProviderThinkingLevel = %q, want low", first.ProviderThinkingLevel)
		}
		if second.ProviderThinkingLevel != "high" {
			t.Errorf("second.ProviderThinkingLevel = %q, want high", second.ProviderThinkingLevel)
		}
		for i, body := range []map[string]json.RawMessage{bodies1[0], bodies2[0]} {
			var reasoning any
			if err := json.Unmarshal(body["reasoning"], &reasoning); err != nil {
				t.Fatalf("turn %d reasoning unmarshal: %v", i+1, err)
			}
			assertCatalogJSON(t, reasoning, `{"effort":"medium","summary":"auto"}`)
		}
		if got, want := summarizeCodexInput(t, bodies1[0]), []string{"user", "configuration_update:low"}; !reflect.DeepEqual(got, want) {
			t.Errorf("turn 1 input = %v, want %v", got, want)
		}
		if got, want := summarizeCodexInput(t, bodies2[0]), []string{"user", "configuration_update:low", "assistant", "user", "configuration_update:high"}; !reflect.DeepEqual(got, want) {
			t.Errorf("turn 2 input = %v, want %v", got, want)
		}
	})

	t.Run("sends the off level (omitted reasoning option) as a configuration_update item", func(t *testing.T) {
		model := codexTestModel("gpt-6-sol", &OpenAIResponsesCompat{SupportsMidConvoEffort: new(true)}, ThinkingLevelMap{ThinkingOff: new("none")})
		bodies, result := captureCodexSSE(t, model, func(fetch *http.Client) (*AssistantMessageEventStream, error) {
			return StreamSimpleOpenAICodexResponses(t.Context(), model, NormalizeContext(Context{
				Messages: []Message{UserMessage{Content: UserText("Say hello"), Timestamp: 1}},
			}), StreamOptions{
				APIKey:    token,
				Transport: TransportSSE,
				Fetch:     fetch,
			})
		})
		if result.ProviderThinkingLevel != "none" {
			t.Errorf("result.ProviderThinkingLevel = %q, want none", result.ProviderThinkingLevel)
		}
		var reasoning any
		if err := json.Unmarshal(bodies[0]["reasoning"], &reasoning); err != nil {
			t.Fatalf("reasoning unmarshal: %v", err)
		}
		assertCatalogJSON(t, reasoning, `{"effort":"medium","summary":"auto"}`)
		if got, want := summarizeCodexInput(t, bodies[0]), []string{"user", "configuration_update:none"}; !reflect.DeepEqual(got, want) {
			t.Errorf("input = %v, want %v", got, want)
		}
	})

	t.Run("streamSimple ignores a caller's raw ReasoningEffort and computes effort from Thinking alone", func(t *testing.T) {
		model := codexTestModel("gpt-6-sol", &OpenAIResponsesCompat{SupportsMidConvoEffort: new(true)}, ThinkingLevelMap{ThinkingOff: new("none")})
		bodies, result := captureCodexSSE(t, model, func(fetch *http.Client) (*AssistantMessageEventStream, error) {
			return StreamSimpleOpenAICodexResponses(t.Context(), model, NormalizeContext(Context{
				Messages: []Message{UserMessage{Content: UserText("Say hello"), Timestamp: 1}},
			}), StreamOptions{
				APIKey:          token,
				Transport:       TransportSSE,
				ReasoningEffort: "high",
				Fetch:           fetch,
			})
		})
		if result.ProviderThinkingLevel != "none" {
			t.Errorf("result.ProviderThinkingLevel = %q, want none (raw ReasoningEffort must not leak)", result.ProviderThinkingLevel)
		}
		if got, want := summarizeCodexInput(t, bodies[0]), []string{"user", "configuration_update:none"}; !reflect.DeepEqual(got, want) {
			t.Errorf("input = %v, want %v", got, want)
		}
	})

	t.Run("sends thinking levels as reasoning.effort without supportsMidConvoEffort", func(t *testing.T) {
		model := codexTestModel("gpt-5.6-sol", nil, nil)
		bodies, result := captureCodexSSE(t, model, func(fetch *http.Client) (*AssistantMessageEventStream, error) {
			return StreamOpenAICodexResponses(t.Context(), model, NormalizeContext(Context{
				Messages: []Message{UserMessage{Content: UserText("Say hello"), Timestamp: 1}},
			}), StreamOptions{
				APIKey:          token,
				Transport:       TransportSSE,
				ReasoningEffort: "high",
				Fetch:           fetch,
			})
		})
		if result.ProviderThinkingLevel != "" {
			t.Errorf("result.ProviderThinkingLevel = %q, want empty", result.ProviderThinkingLevel)
		}
		var reasoning any
		if err := json.Unmarshal(bodies[0]["reasoning"], &reasoning); err != nil {
			t.Fatalf("reasoning unmarshal: %v", err)
		}
		assertCatalogJSON(t, reasoning, `{"effort":"high","summary":"auto"}`)
		if got, want := summarizeCodexInput(t, bodies[0]), []string{"user"}; !reflect.DeepEqual(got, want) {
			t.Errorf("input = %v, want %v", got, want)
		}
	})

	t.Run("sends maxTokens as max_output_tokens unless the model opts out", func(t *testing.T) {
		ctx := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Say hello"), Timestamp: 1}}})
		cases := []struct {
			name      string
			compat    *OpenAIResponsesCompat
			simple    bool
			maxTokens int
			want      *int
		}{
			{name: "streamSimple forwards maxTokens", simple: true, maxTokens: 5000, want: new(5000)},
			{name: "stream clamps small maxTokens to 16", maxTokens: 4, want: new(16)},
			{name: "supportsMaxOutputTokens false omits max_output_tokens", compat: &OpenAIResponsesCompat{SupportsMaxOutputTokens: new(false)}, maxTokens: 5000, want: nil},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				model := codexTestModel("gpt-6.2-sol", tc.compat, nil)
				bodies, _ := captureCodexSSE(t, model, func(fetch *http.Client) (*AssistantMessageEventStream, error) {
					opts := StreamOptions{APIKey: token, Transport: TransportSSE, MaxTokens: tc.maxTokens, Fetch: fetch}
					if tc.simple {
						return StreamSimpleOpenAICodexResponses(t.Context(), model, ctx, opts)
					}
					return StreamOpenAICodexResponses(t.Context(), model, ctx, opts)
				})
				raw, ok := bodies[0]["max_output_tokens"]
				if tc.want == nil {
					if ok {
						t.Fatalf("max_output_tokens = %s, want omitted", raw)
					}
					return
				}
				if !ok {
					t.Fatalf("max_output_tokens missing, want %d", *tc.want)
				}
				var got int
				if err := json.Unmarshal(raw, &got); err != nil || got != *tc.want {
					t.Fatalf("max_output_tokens = %s (err=%v), want %d", raw, err, *tc.want)
				}
			})
		}
	})

	t.Run("poisoned history and model switch only stamp same-model completed turns", func(t *testing.T) {
		model := codexTestModel("gpt-6.2-sol", &OpenAIResponsesCompat{SupportsMidConvoEffort: new(true)}, nil)
		history := []Message{
			UserMessage{Content: UserText("u1"), Timestamp: 1},
			// Errored assistant turn: transformMessages drops it; no configuration_update emitted for it.
			AssistantMessage{API: APIOpenAICodexResponses, Provider: "openai-codex", Model: "gpt-6.2-sol", StopReason: StopReasonError, ProviderThinkingLevel: "low", Content: []AssistantContentBlock{TextContent{Text: "err"}}, Timestamp: 2},
			// Aborted assistant turn: transformMessages drops it; no configuration_update emitted for it.
			AssistantMessage{API: APIOpenAICodexResponses, Provider: "openai-codex", Model: "gpt-6.2-sol", StopReason: StopReasonAborted, ProviderThinkingLevel: "low", Content: []AssistantContentBlock{TextContent{Text: "aborted"}}, Timestamp: 3},
			// Different model reply: not stamped with configuration_update.
			AssistantMessage{API: APIOpenAICodexResponses, Provider: "openai-codex", Model: "gpt-5.6-sol", StopReason: StopReasonStop, ProviderThinkingLevel: "low", Content: []AssistantContentBlock{TextContent{Text: "other model"}}, Timestamp: 4},
			UserMessage{Content: UserText("u2"), Timestamp: 5},
			// Same model completed reply: stamped before its assistant item.
			AssistantMessage{API: APIOpenAICodexResponses, Provider: "openai-codex", Model: "gpt-6.2-sol", StopReason: StopReasonStop, ProviderThinkingLevel: "low", Content: []AssistantContentBlock{TextContent{Text: "same model"}}, Timestamp: 6},
			UserMessage{Content: UserText("u3"), Timestamp: 7},
		}
		bodies, result := captureCodexSSE(t, model, func(fetch *http.Client) (*AssistantMessageEventStream, error) {
			return StreamOpenAICodexResponses(t.Context(), model, NormalizeContext(Context{Messages: history}), StreamOptions{
				APIKey:          token,
				Transport:       TransportSSE,
				ReasoningEffort: "high",
				Fetch:           fetch,
			})
		})
		if result.ProviderThinkingLevel != "high" {
			t.Errorf("ProviderThinkingLevel = %q, want high", result.ProviderThinkingLevel)
		}
		want := []string{"user", "assistant", "user", "configuration_update:low", "assistant", "user", "configuration_update:high"}
		if got := summarizeCodexInput(t, bodies[0]); !reflect.DeepEqual(got, want) {
			t.Errorf("input = %v, want %v", got, want)
		}
	})

	t.Run("keeps sending input deltas in websocket-cached mode when thinking level changes", func(t *testing.T) {
		sessionID := "session-effort-deltas"
		defer CloseOpenAICodexWebSocketSessions(sessionID)
		ResetOpenAICodexWebSocketDebugStats(sessionID)

		peer := newCodexWSPeer(t, func(index, _ int, _ map[string]any) []string {
			return codexWSHello(fmt.Sprintf("resp_%d", index), "OK")
		})
		model := codexTestModel("gpt-6.2-sol", &OpenAIResponsesCompat{SupportsMidConvoEffort: new(true)}, nil)
		model.ProviderMeta.BaseURL = peer.server.URL

		var messages []Message
		for turn, effort := range []string{"low", "high", "high"} {
			messages = append(messages, UserMessage{Content: UserText(fmt.Sprintf("Turn %d", turn+1)), Timestamp: int64(turn)})
			stream, err := StreamOpenAICodexResponses(t.Context(), model, NormalizeContext(Context{Messages: messages}), StreamOptions{
				APIKey:          token,
				SessionID:       sessionID,
				Transport:       TransportWebSocketCached,
				ReasoningEffort: effort,
			})
			if err != nil {
				t.Fatalf("turn %d setup: %v", turn+1, err)
			}
			reply := stream.Result()
			if reply.StopReason == StopReasonError {
				t.Fatalf("turn %d error: %s", turn+1, reply.ErrorMessage)
			}
			messages = append(messages, *reply)
		}

		_, bodies, _ := peer.snapshot()
		if len(bodies) != 3 {
			t.Fatalf("sent %d frames, want 3", len(bodies))
		}
		wantPrev := []any{nil, "resp_1", "resp_2"}
		wantInputs := [][]string{
			{"user", "configuration_update:low"},
			{"user", "configuration_update:high"},
			{"user", "configuration_update:high"},
		}
		for i, req := range bodies {
			assertCatalogJSON(t, req.Body["reasoning"], `{"effort":"medium","summary":"auto"}`)
			if req.Body["previous_response_id"] != wantPrev[i] {
				t.Errorf("turn %d previous_response_id = %v, want %v", i+1, req.Body["previous_response_id"], wantPrev[i])
			}
			rawInput, err := json.Marshal(req.Body["input"])
			if err != nil {
				t.Fatal(err)
			}
			if got := summarizeCodexInput(t, map[string]json.RawMessage{"input": rawInput}); !reflect.DeepEqual(got, wantInputs[i]) {
				t.Errorf("turn %d input = %v, want %v", i+1, got, wantInputs[i])
			}
		}
		stats := GetOpenAICodexWebSocketDebugStats(sessionID)
		if stats == nil || stats.FullContextRequests != 1 || stats.DeltaRequests != 2 {
			t.Fatalf("stats = %#v, want FullContextRequests=1 DeltaRequests=2", stats)
		}
	})
}

func TestOpenAIResponsesSharedPathUnchangedMatrix(t *testing.T) {
	// Shared-path matrix: non-Codex openai-responses keeps leading developer/system item, no instructions field,
	// and unchanged pure converter + nearest caller behavior.
	t.Run("pure converter ConvertResponsesMessages always includes leading system prompt", func(t *testing.T) {
		model := &Model{
			ID:           "gpt-6.2-sol",
			Input:        []string{"text"},
			ProviderMeta: ProviderMetadata{API: APIOpenAIResponses, ProviderID: "openai", Reasoning: true},
		}
		transcript := NormalizeContext(Context{
			SystemPrompt: "Follow rules.",
			Messages:     []Message{UserMessage{Content: UserText("Hi"), Timestamp: 1}},
		})
		items, err := ConvertResponsesMessages(model, transcript, responsesToolCallProviders, ConvertResponsesMessagesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0].Role != "developer" || string(items[0].Content) != `"Follow rules."` || items[1].Role != "user" {
			t.Fatalf("items = %#v", items)
		}
	})

	t.Run("nearest caller openai-responses provider sends leading developer item and no instructions", func(t *testing.T) {
		cfg := OpenAIResponsesConfig{
			Model:       "gpt-6.2-sol",
			ProviderID:  "openai",
			BaseURL:     "https://api.openai.com/v1",
			APIKey:      "sk-test",
			IsReasoning: true,
		}
		payload, _, message := captureResponsesCompat(t, cfg, Context{
			SystemPrompt: "Follow rules.",
			Messages:     []Message{UserMessage{Content: UserText("Hi"), Timestamp: 1}},
		}, StreamOptions{}, midConvoEffortReply(false))
		if message.StopReason == StopReasonError {
			t.Fatalf("stream error: %s", message.ErrorMessage)
		}
		if _, ok := payload["instructions"]; ok {
			t.Fatalf("instructions = %v, want omitted", payload["instructions"])
		}
		assertCatalogJSON(t, payload["input"], `[{"role":"developer","content":"Follow rules."},{"role":"user","content":[{"type":"input_text","text":"Hi"}]}]`)
	})
}
