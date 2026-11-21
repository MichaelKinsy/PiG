package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tests for Pi #12371 (fe8a6f5dc741e9214a20f70f359c68c72b494d1e): prefixed Gemini model IDs
// across getGeminiMajorVersion, requiresToolCallId, supportsGoogleStrictToolSampling,
// supportsMultimodalFunctionResponse, and request boundaries for both Google Generative AI and Vertex.

func TestGeminiPrefixedModelIDHelpers(t *testing.T) {
	t.Run("getGeminiMajorVersion", func(t *testing.T) {
		for _, tc := range []struct {
			id      string
			want    float64
			wantOK  bool
		}{
			{"gemini-3-pro", 3, true},
			{"google/gemini-3-pro", 3, true},
			{"models/gemini-3-flash", 3, true},
			{"publishers/google/models/gemini-3.1-pro", 3, true},
			{"randomprefix-gemini-4-argon", 4, true},
			{"vendor-gemini-2.5-flash", 2, true},
			{"google/gemini-2.5-flash", 2, true},
			{"prefix-gemini-live-3-flash", 3, true},
			{"argon-latest", 0, false},
		} {
			got, ok := getGeminiMajorVersion(tc.id)
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Errorf("getGeminiMajorVersion(%q) = (%v, %v), want (%v, %v)", tc.id, got, ok, tc.want, tc.wantOK)
			}
		}
	})

	t.Run("requiresToolCallId", func(t *testing.T) {
		for _, tc := range []struct {
			id   string
			want bool
		}{
			{"google/gemini-3-pro", true},
			{"models/gemini-3-flash", true},
			{"publishers/google/models/gemini-3.1-pro", true},
			{"randomprefix-gemini-4-argon", true},
			{"vendor-gemini-2.5-flash", false},
			{"google/gemini-2.5-flash", false},
			{"argon-latest", false},
		} {
			if got := requiresToolCallId(tc.id); got != tc.want {
				t.Errorf("requiresToolCallId(%q) = %v, want %v", tc.id, got, tc.want)
			}
		}
	})

	t.Run("supportsMultimodalFunctionResponse", func(t *testing.T) {
		for _, tc := range []struct {
			id   string
			want bool
		}{
			{"google/gemini-3-pro", true},
			{"models/gemini-3-flash", true},
			{"publishers/google/models/gemini-3.1-pro", true},
			{"randomprefix-gemini-4-argon", true},
			{"google/gemini-2.5-flash", false},
			{"vendor-gemini-2.5-flash", false},
			{"prefix-gemini-2", false},
			{"argon-latest", true},
		} {
			if got := supportsMultimodalFunctionResponse(tc.id); got != tc.want {
				t.Errorf("supportsMultimodalFunctionResponse(%q) = %v, want %v", tc.id, got, tc.want)
			}
		}
	})

	t.Run("supportsGoogleStrictToolSampling_prefixed_and_compat", func(t *testing.T) {
		// Note: usesGoogleStrictToolSampling / supportsGoogleStrictToolSampling takes *Model after Pi fe8a6f5dc.
		for _, tc := range []struct {
			name   string
			id     string
			compat *ModelCompat
			want   bool
		}{
			{"prefixed gemini-4", "randomprefix-gemini-4-argon", nil, true},
			{"prefixed google/gemini-3-pro", "google/gemini-3-pro", nil, true},
			{"prefixed models/gemini-3-flash", "models/gemini-3-flash", nil, true},
			{"prefixed vertex gemini-3.1-pro", "publishers/google/models/gemini-3.1-pro", nil, true},
			{"prefixed gemini-2.5-pro", "vendor-gemini-2.5-pro", nil, false},
			{"unversioned default false", "argon-latest", nil, false},
			{"unversioned compat true", "argon-latest", &ModelCompat{SupportsStrictMode: new(true)}, true},
			{"unversioned compat false", "argon-latest", &ModelCompat{SupportsStrictMode: new(false)}, false},
			{"versioned gemini-3 compat false", "gemini-3.1-pro-preview", &ModelCompat{SupportsStrictMode: new(false)}, false},
			{"versioned gemini-2.5 compat true", "vendor-gemini-2.5-pro", &ModelCompat{SupportsStrictMode: new(true)}, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				model := &Model{ID: tc.id, ProviderMeta: ProviderMetadata{API: APIGoogleGenerativeAI, Compat: tc.compat}}
				if got := supportsGoogleStrictToolSampling(model); got != tc.want {
					t.Errorf("supportsGoogleStrictToolSampling(%q, compat=%+v) = %v, want %v", tc.id, tc.compat, got, tc.want)
				}
			})
		}
	})
}

func TestGeminiPrefixedModelIDRequestBoundaryBothAPIs(t *testing.T) {
	const sseStop = "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"
	preferTool := ToolSchema{
		Name:                "test_tool",
		Description:         "A test tool",
		Parameters:          JsonObject{"type": "object", "properties": JsonObject{}},
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
	}

	makeProvider := func(api API, url, modelID string, compat *ModelCompat) Provider {
		meta := &Model{
			ID:    modelID,
			Input: []string{"text", "image"},
			ProviderMeta: ProviderMetadata{
				API:        api,
				ProviderID: "test-" + string(api),
				Compat:     compat,
			},
		}
		if api == APIGoogleVertex {
			return NewGoogleVertexProvider(GoogleVertexConfig{
				Model:         modelID,
				APIKey:        "test",
				ProviderID:    meta.ProviderMeta.ProviderID,
				BaseURL:       url,
				ModelMetadata: meta,
			})
		}
		return NewGoogleProvider(GoogleConfig{
			Model:         modelID,
			APIKey:        "test",
			ProviderID:    meta.ProviderMeta.ProviderID,
			BaseURL:       url,
			ModelMetadata: meta,
		})
	}

	for _, api := range []API{APIGoogleGenerativeAI, APIGoogleVertex} {
		t.Run(string(api), func(t *testing.T) {
			t.Run("strict_VALIDATED_for_prefixed_and_compat", func(t *testing.T) {
				for _, tc := range []struct {
					name     string
					id       string
					compat   *ModelCompat
					wantMode string
				}{
					{"prefixed_gemini_4", "randomprefix-gemini-4-argon", nil, "VALIDATED"},
					{"prefixed_google_gemini_3", "google/gemini-3-pro", nil, "VALIDATED"},
					{"prefixed_models_gemini_3", "models/gemini-3-flash", nil, "VALIDATED"},
					{"prefixed_vertex_gemini_3_1", "publishers/google/models/gemini-3.1-pro", nil, "VALIDATED"},
					{"unversioned_no_compat", "argon-latest", nil, ""},
					{"unversioned_compat_true", "argon-latest", &ModelCompat{SupportsStrictMode: new(true)}, "VALIDATED"},
					{"versioned_compat_false", "gemini-3.1-pro-preview", &ModelCompat{SupportsStrictMode: new(false)}, ""},
				} {
					t.Run(tc.name, func(t *testing.T) {
						body := captureShapeRequest(t, func(url string) Provider {
							return makeProvider(api, url, tc.id, tc.compat)
						}, []Message{
							SystemMessage{ToolsAdded: []ToolSchema{preferTool}},
							UserMessage{Content: UserText("Hi")},
						}, StreamOptions{}, sseStop)
						var gotMode string
						if raw, ok := body["toolConfig"]; ok {
							var cfg struct {
								FunctionCallingConfig struct {
									Mode string `json:"mode"`
								} `json:"functionCallingConfig"`
							}
							if err := json.Unmarshal(raw, &cfg); err != nil {
								t.Fatalf("unmarshal toolConfig: %v", err)
							}
							gotMode = cfg.FunctionCallingConfig.Mode
						}
						if gotMode != tc.wantMode {
							t.Errorf("mode=%q, want %q (toolConfig=%s)", gotMode, tc.wantMode, body["toolConfig"])
						}
					})
				}
			})

			t.Run("echoes_tool_call_ids_on_prefixed_gemini_3", func(t *testing.T) {
				modelID := "google/gemini-3-pro"
				msgs := []Message{
					UserMessage{Content: UserText("run")},
					AssistantMessage{
						Provider:   "test-" + string(api),
						Model:      modelID,
						API:        api,
						StopReason: StopReasonToolUse,
						Content: []AssistantContentBlock{
							ToolCall{ID: "call_prefixed_1", Name: "bash", Arguments: JsonObject{"cmd": "ls"}},
						},
					},
					ToolResultMessage{
						ToolCallID: "call_prefixed_1",
						ToolName:   "bash",
						Content:    []ToolResultMessageContent{TextContent{Text: "ok"}},
					},
				}
				body := captureShapeRequest(t, func(url string) Provider {
					return makeProvider(api, url, modelID, nil)
				}, msgs, StreamOptions{}, sseStop)
				var contents []geminiContent
				if err := json.Unmarshal(body["contents"], &contents); err != nil {
					t.Fatalf("unmarshal contents: %v", err)
				}
				call := findFunctionCall(contents)
				if call == nil || call.ID != "call_prefixed_1" {
					t.Errorf("functionCall = %#v, want ID call_prefixed_1", call)
				}
				resp := findFunctionResponse(contents)
				if resp == nil || resp.ID != "call_prefixed_1" {
					t.Errorf("functionResponse = %#v, want ID call_prefixed_1", resp)
				}
			})

			t.Run("prior_tool_call_without_id_omitted_by_tool_flow_on_prefixed_gemini_3", func(t *testing.T) {
				// Acceptance matrix: an old persisted tool call without an ID sent to a prefixed Gemini 3+ model
				// is omitted by prepareProviderToolFlow (D120), regardless of requiresToolCallId turning true.
				modelID := "google/gemini-3-pro"
				msgs := []Message{
					UserMessage{Content: UserText("run")},
					AssistantMessage{
						Provider:   "test-" + string(api),
						Model:      modelID,
						API:        api,
						StopReason: StopReasonToolUse,
						Content: []AssistantContentBlock{
							ToolCall{ID: "", Name: "bash", Arguments: JsonObject{"cmd": "ls"}},
						},
					},
					ToolResultMessage{
						ToolCallID: "",
						ToolName:   "bash",
						Content:    []ToolResultMessageContent{TextContent{Text: "ok"}},
					},
				}
				body := captureShapeRequest(t, func(url string) Provider {
					return makeProvider(api, url, modelID, nil)
				}, msgs, StreamOptions{}, sseStop)
				var contents []geminiContent
				if err := json.Unmarshal(body["contents"], &contents); err != nil {
					t.Fatalf("unmarshal contents: %v", err)
				}
				if call := findFunctionCall(contents); call != nil {
					t.Errorf("unreplayable empty-ID functionCall = %#v, want omitted", call)
				}
				if resp := findFunctionResponse(contents); resp != nil {
					t.Errorf("unreplayable empty-ID functionResponse = %#v, want omitted", resp)
				}
				if strings.Contains(string(body["contents"]), `"id":`) {
					t.Errorf("unexpected id on the wire, got contents=%s", body["contents"])
				}
			})

			t.Run("image_tool_result_routing_prefixed_gemini_2_5_vs_4", func(t *testing.T) {
				makeMsgs := func(modelID string) []Message {
					return []Message{
						UserMessage{Content: UserText("look")},
						AssistantMessage{
							Provider:   "test-" + string(api),
							Model:      modelID,
							API:        api,
							StopReason: StopReasonToolUse,
							Content: []AssistantContentBlock{
								ToolCall{ID: "call_img_1", Name: "shot", Arguments: JsonObject{}},
							},
						},
						ToolResultMessage{
							ToolCallID: "call_img_1",
							ToolName:   "shot",
							Content:    []ToolResultMessageContent{ImageContent{MimeType: "image/png", Data: "AAAA"}},
						},
					}
				}

				// Prefixed Gemini 2.5: separate user turn for image, not nested in functionResponse.parts.
				body25 := captureShapeRequest(t, func(url string) Provider {
					return makeProvider(api, url, "google/gemini-2.5-flash", nil)
				}, makeMsgs("google/gemini-2.5-flash"), StreamOptions{}, sseStop)
				var contents25 []geminiContent
				if err := json.Unmarshal(body25["contents"], &contents25); err != nil {
					t.Fatalf("unmarshal contents25: %v", err)
				}
				if len(contents25) != 4 {
					t.Fatalf("prefixed gemini-2.5 contents len = %d, want 4 (separate image turn): %s", len(contents25), body25["contents"])
				}
				if resp := findFunctionResponse(contents25); resp == nil || len(resp.Parts) != 0 {
					t.Errorf("prefixed gemini-2.5 functionResponse.Parts = %#v, want empty", resp)
				}
				if contents25[3].Role != "user" || len(contents25[3].Parts) < 2 || contents25[3].Parts[1].InlineData == nil {
					t.Errorf("prefixed gemini-2.5 trailing image turn = %#v", contents25[3])
				}

				// Prefixed Gemini 4: nested inside functionResponse.parts, 3 turns total.
				body4 := captureShapeRequest(t, func(url string) Provider {
					return makeProvider(api, url, "randomprefix-gemini-4-argon", nil)
				}, makeMsgs("randomprefix-gemini-4-argon"), StreamOptions{}, sseStop)
				var contents4 []geminiContent
				if err := json.Unmarshal(body4["contents"], &contents4); err != nil {
					t.Fatalf("unmarshal contents4: %v", err)
				}
				if len(contents4) != 3 {
					t.Fatalf("prefixed gemini-4 contents len = %d, want 3 (nested image): %s", len(contents4), body4["contents"])
				}
				if resp := findFunctionResponse(contents4); resp == nil || len(resp.Parts) != 1 || resp.Parts[0].InlineData == nil {
					t.Errorf("prefixed gemini-4 functionResponse.Parts = %#v, want 1 inlineData", resp)
				}
			})
		})
	}
}
