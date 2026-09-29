package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const upstreamCacheAffinitySessionID = "0195d6e4-4cf9-7f44-a2d8-f8f7f49ee9d3"

func runCacheAffinityCase(t *testing.T, codex bool, key, baseURL string) {
	t.Helper()
	expected := "openai cache affinity e2e success"
	var provider Provider
	if codex {
		expected = "cache affinity e2e success"
		provider = NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{Model: "gpt-5.5", APIKey: key, BaseURL: baseURL})
	} else {
		model, ok := LookupModelExact("openai/gpt-5.4")
		if !ok {
			t.Fatal("missing upstream model openai/gpt-5.4")
		}
		provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: model.ID, ModelMetadata: model.ToModel(), IsReasoning: model.Reasoning, APIKey: key, BaseURL: baseURL})
	}
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	options := StreamOptions{SessionID: upstreamCacheAffinitySessionID}
	if codex {
		options.Transport = TransportSSE
	}
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "You are a helpful assistant. Reply exactly as requested.", Messages: []Message{UserMessage{Content: UserText("Reply with exactly: " + expected)}}}), options)
	if err != nil {
		t.Fatal(err)
	}
	response := stream.Result()
	if response.StopReason == StopReasonError || response.ErrorMessage != "" {
		t.Fatalf("response=%#v", response)
	}
	var text strings.Builder
	for _, block := range response.Content {
		if block, ok := block.(TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	if !strings.Contains(text.String(), expected) {
		t.Fatalf("response text=%q, want %q", text.String(), expected)
	}
}

func cacheAffinityFauxEndpoint(t *testing.T, codex bool) string {
	t.Helper()
	expected := "openai cache affinity e2e success"
	if codex {
		expected = "cache affinity e2e success"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if codex {
			body, err = decodeZstdRawFrameForTest(body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
		}
		var payload map[string]any
		if err = json.Unmarshal(body, &payload); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		header := "session_id"
		if codex {
			header = "session-id"
		}
		if payload["prompt_cache_key"] != upstreamCacheAffinitySessionID || r.Header.Get(header) != upstreamCacheAffinitySessionID || r.Header.Get("x-client-request-id") != upstreamCacheAffinitySessionID {
			t.Errorf("unaligned affinity: payload=%#v headers=%#v", payload, r.Header)
			w.WriteHeader(400)
			return
		}
		// Pi 0.87.1 complete(model, context, options) for the original cache-affinity cases: capture before transport with onPayload.
		wantJSON := `{"model":"gpt-5.4","input":[{"role":"developer","content":"You are a helpful assistant. Reply exactly as requested."},{"role":"user","content":[{"type":"input_text","text":"Reply with exactly: openai cache affinity e2e success"}]}],"stream":true,"prompt_cache_key":"0195d6e4-4cf9-7f44-a2d8-f8f7f49ee9d3","store":false,"reasoning":{"effort":"none"}}` // gitleaks:allow (test fixture, not a credential)
		if codex {
			wantJSON = `{"model":"gpt-5.5","store":false,"stream":true,"instructions":"You are a helpful assistant. Reply exactly as requested.","input":[{"role":"user","content":[{"type":"input_text","text":"Reply with exactly: cache affinity e2e success"}]}],"text":{"verbosity":"low"},"include":["reasoning.encrypted_content"],"prompt_cache_key":"0195d6e4-4cf9-7f44-a2d8-f8f7f49ee9d3","tool_choice":"auto","parallel_tool_calls":true,"reasoning":{"effort":"none"}}` // gitleaks:allow (test fixture, not a credential)
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !reflect.DeepEqual(payload, want) {
			t.Errorf("cache-affinity request = %s, want Pi request %s", body, wantJSON)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_reply\",\"content\":[]}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_reply\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", expected)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// .upstream/v0.87.1/packages/ai/test/openai-codex-cache-affinity-e2e.test.ts:9
func TestOpenAICodexCacheAffinityE2EUpstream(t *testing.T) {
	t.Run("handles SSE requests with aligned cache-affinity identifiers/hermetic", func(t *testing.T) {
		runCacheAffinityCase(t, true, codexTestToken(t, "acct_affinity"), cacheAffinityFauxEndpoint(t, true))
	})
}

// .upstream/v0.87.1/packages/ai/test/openai-responses-cache-affinity-e2e.test.ts:6
func TestOpenAIResponsesCacheAffinityE2EUpstream(t *testing.T) {
	t.Run("handles direct OpenAI Responses requests with aligned cache-affinity identifiers/hermetic", func(t *testing.T) { runCacheAffinityCase(t, false, "test", cacheAffinityFauxEndpoint(t, false)) })
}
