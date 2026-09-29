package ai

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// upstream: packages/ai/src/api/google-generative-ai.ts:380-427 and google-vertex.ts:466-517 expose native model/contents/config before SDK lowering.
func TestGoogleNativePayloadAndReplacementUpstream(t *testing.T) {
	for _, vertex := range []bool{false, true} {
		name := "google"
		if vertex {
			name = "google-vertex"
		}
		for _, mode := range []string{"replace", "mutate"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				var wire map[string]any
				var requestPath string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requestPath = r.URL.Path
					if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					writeMatrixUsageResponse(t, w, APIGoogleGenerativeAI, "Hello.", false)
				}))
				defer server.Close()
				var provider Provider
				if vertex {
					provider = NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "test", Model: "gemini-2.5-flash", BaseURL: server.URL})
				} else {
					provider = NewGoogleProvider(GoogleConfig{APIKey: "test", Model: "gemini-2.5-flash", ProviderID: "google", BaseURL: server.URL})
				}
				defer func() {
					if err := provider.Close(); err != nil {
						t.Error(err)
					}
				}()
				var captured []byte
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "Be concise.", Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{IsReasoning: true, MaxTokens: 32, OnPayload: func(value any, _ *Model) (any, error) {
					var err error
					captured, err = json.Marshal(value)
					if err != nil {
						return nil, err
					}
					next := map[string]any{"model": "gemini-2.5-pro", "contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "Changed"}}}}, "config": map[string]any{"systemInstruction": "Changed system", "maxOutputTokens": 64, "temperature": 0, "topP": 0.8}}
					if mode == "mutate" {
						original := value.(map[string]any)
						maps.Copy(original, next)
						return nil, nil
					}
					return next, nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				response := stream.Result()
				if response.StopReason != StopReasonStop {
					t.Fatalf("response=%#v", response)
				}
				assertShapeJSON(t, captured, `{"model":"gemini-2.5-flash","contents":[{"role":"user","parts":[{"text":"Hello"}]}],"config":{"maxOutputTokens":32,"systemInstruction":"Be concise."}}`)
				if !strings.HasSuffix(requestPath, "/models/gemini-2.5-pro:streamGenerateContent") {
					t.Errorf("replacement model not applied: %s", requestPath)
				}
				encoded, err := json.Marshal(wire)
				if err != nil {
					t.Fatal(err)
				}
				assertShapeJSON(t, encoded, `{"contents":[{"role":"user","parts":[{"text":"Changed"}]}],"systemInstruction":{"role":"user","parts":[{"text":"Changed system"}]},"generationConfig":{"maxOutputTokens":64,"temperature":0,"topP":0.8}}`)
			})
		}
	}
}

// upstream: packages/ai/src/api/google-generative-ai.ts:407-419 adds no thinkingConfig for omitted native thinking; streamSimple:321-323 explicitly disables it.
func TestGoogleNativeOmittedThinkingDiffersFromSimpleUpstream(t *testing.T) {
	for _, api := range []API{APIGoogleGenerativeAI, APIGoogleVertex} {
		modes := []string{"raw omitted", "raw disabled", "logical off"}
		if api == APIGoogleGenerativeAI {
			modes = append(modes, "simple omitted")
		}
		for _, mode := range modes {
			t.Run(string(api)+"/"+mode, func(t *testing.T) {
				var thinking any
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					config, _ := body["generationConfig"].(map[string]any)
					thinking = config["thinkingConfig"]
					writeMatrixUsageResponse(t, w, APIGoogleGenerativeAI, "Hello.", false)
				}))
				defer server.Close()
				model, _ := LookupModelExact("google/gemini-2.5-flash")
				selected := model.ToModel()
				selected.ProviderMeta.API = api
				selected.ProviderMeta.BaseURL = server.URL
				selected.ProviderMeta.ProviderID = string(api)
				options := StreamOptions{APIKey: "test", IsReasoning: true}
				if mode == "raw disabled" {
					options.GoogleThinking = &GoogleThinkingOptions{Enabled: false}
				}
				if mode == "logical off" {
					options.Thinking = ThinkingOff
				}
				var stream *AssistantMessageEventStream
				var err error
				transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}})
				if mode == "simple omitted" {
					stream, err = StreamSimple(t.Context(), selected, transcript, options)
				} else {
					provider := googleNativeProviderForTest(api, server.URL)
					defer func() {
						if err := provider.Close(); err != nil {
							t.Error(err)
						}
					}()
					stream, err = provider.Stream(t.Context(), transcript, options)
				}
				if err != nil {
					t.Fatal(err)
				}
				if result := stream.Result(); result.StopReason != StopReasonStop {
					t.Fatalf("response=%#v", result)
				}
				var want any
				if mode != "raw omitted" {
					want = map[string]any{"thinkingBudget": float64(0)}
				}
				if !reflect.DeepEqual(thinking, want) {
					t.Fatalf("thinkingConfig=%#v, want %#v", thinking, want)
				}
			})
		}
	}
}

func googleNativeProviderForTest(api API, base string) Provider {
	if api == APIGoogleVertex {
		return NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "test", Model: "gemini-2.5-flash", BaseURL: base})
	}
	return NewGoogleProvider(GoogleConfig{APIKey: "test", Model: "gemini-2.5-flash", ProviderID: "google", BaseURL: base})
}
