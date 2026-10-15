package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/openai-responses-shared.ts createSlot, openai-completions.ts normalizeToolCallId, google-shared.ts convertMessages

// Pi stores a streamed function_call or custom_tool_call under `${item.call_id}|${item.id}`: an absent member is "undefined" and null is
// "null". The pinned pi-ai and PiG read the same streams and must store the same ids. An empty call_id is D129 and is tested in
// TestResponsesToolCallWithoutCallIDDoesNotWedgeReplay.
func TestOpenAIResponsesToolCallIDPartsMatchPi(t *testing.T) {
	stream := func(itemType, members string, added bool) string {
		body := `"arguments":"{}"`
		if itemType == "custom_tool_call" {
			body = `"input":"x"`
		}
		item := fmt.Sprintf(`{"type":%q,%s"name":"read",%s}`, itemType, members, body)
		sse := ""
		if added {
			sse += `data: {"type":"response.output_item.added","output_index":0,"item":` + item + "}\n\n"
		}
		return sse + `data: {"type":"response.output_item.done","output_index":0,"item":` + item + "}\n\n" +
			`data: {"type":"response.completed","response":{"id":"r1","status":"completed","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}` + "\n\n"
	}
	var probes []responseProbe
	for _, itemType := range []string{"function_call", "custom_tool_call"} {
		for _, members := range []string{``, `"call_id":null,`, `"id":"fc_1",`, `"id":"fc_1","call_id":null,`, `"call_id":"call_1",`, `"call_id":"call_1","id":null,`, `"id":null,"call_id":null,`} {
			for _, added := range []bool{true, false} {
				probes = append(probes, responseProbe{Provider: "openai", SSE: stream(itemType, members, added)})
			}
		}
	}
	checkResponses(t, "openai-responses.js", "openai-responses", ai.APIOpenAIResponses, ai.StreamOpenAIResponses, probes)
}

// A stored Responses id with an empty call part ("|fc_1") replays to Chat Completions and to Gemini as in Pi: Completions sends "_fc_1" and
// Gemini sends no id. D129 omits the call only from a Responses request, which would send call_id "".
func TestStoredEmptyCallPartReplaysToOtherTargetsAsPi(t *testing.T) {
	context := json.RawMessage(`{"messages":[{"role":"user","content":"x","timestamp":1},` +
		`{"role":"assistant","api":"openai-responses","provider":"openai","model":"gpt-x","content":[{"type":"text","text":"t"},{"type":"toolCall","id":"|fc_1","name":"read","arguments":{"a":1}}],"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},` +
		`{"role":"toolResult","toolCallId":"|fc_1","toolName":"read","content":[{"type":"text","text":"r"}],"isError":false,"timestamp":3},` +
		`{"role":"user","content":"y","timestamp":4}]}`)
	capture := func(t *testing.T, run func(*ai.Model, ai.StreamOptions) (*ai.AssistantMessageEventStream, error), model *ai.Model) string {
		t.Helper()
		var got string
		options := ai.StreamOptions{APIKey: "fake-key", OnPayload: func(payload any, _ *ai.Model) (any, error) {
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			got = string(encoded)
			return nil, errors.New("payload captured")
		}}
		if stream, err := run(model, options); err == nil {
			stream.Result()
		}
		if got == "" {
			t.Fatal("PiG produced no payload")
		}
		return got
	}
	t.Run("openai-completions", func(t *testing.T) {
		type probe struct {
			Provider string          `json:"provider"`
			BaseURL  string          `json:"baseUrl"`
			Input    []string        `json:"input"`
			Options  map[string]any  `json:"options"`
			Context  json.RawMessage `json:"context"`
		}
		p := probe{Provider: "custom", BaseURL: "http://127.0.0.1:9", Input: []string{"text"}, Options: map[string]any{}, Context: context}
		want := piPayloads(t, "testdata/openai_completions_payload.mjs", []probe{p})
		model := &ai.Model{ID: "m", DisplayName: "m", Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: ai.APIOpenAICompletions, ProviderID: p.Provider, BaseURL: p.BaseURL}}
		got := capture(t, func(model *ai.Model, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return ai.StreamOpenAICompletions(payloadContext(t), model, payloadTranscript(t, context), options)
		}, model)
		failures := 0
		comparePayload(t, "completions replay of |fc_1", got, want[0], &failures, map[string]int{})
	})
	t.Run("google-generative-ai", func(t *testing.T) {
		type probe struct {
			ID       string          `json:"id"`
			Provider string          `json:"provider"`
			BaseURL  string          `json:"baseUrl"`
			APIKey   string          `json:"apiKey"`
			Input    []string        `json:"input"`
			Options  map[string]any  `json:"options"`
			Context  json.RawMessage `json:"context"`
		}
		p := probe{ID: "gemini-2.5-flash", Provider: "google", BaseURL: "https://generativelanguage.googleapis.com/v1beta", APIKey: "fake-key", Input: []string{"text"}, Options: map[string]any{}, Context: context}
		want := piPayloads(t, "testdata/google_generative_ai_payload.mjs", []probe{p})
		model := &ai.Model{ID: p.ID, DisplayName: p.ID, Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 1000000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: ai.APIGoogleGenerativeAI, ProviderID: p.Provider, BaseURL: p.BaseURL}}
		got := capture(t, func(model *ai.Model, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return ai.StreamGoogle(payloadContext(t), model, payloadTranscript(t, context), options)
		}, model)
		failures := 0
		comparePayload(t, "google replay of |fc_1", got, piPayloadWithD115(want[0]), &failures, map[string]int{})
	})
}
