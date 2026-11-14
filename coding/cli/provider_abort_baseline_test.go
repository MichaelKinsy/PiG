package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestAbortedBuiltInProviderTurnKeepsItsPartialAcrossAPIKinds guards the built-in providers' side of the response consumer, which the fix for extension providers shares
// (agent consumeStream: a stream that ended with a result gives that result also under an aborted signal, packages/agent/src/agent-loop.ts:470-478). A built-in provider
// answers the abort with its own error event, so its aborted turn is unchanged: the partial text it had streamed, stopReason aborted, and the API kind's own errorMessage
// (the aborted field below says which of them were compared with Pi by a run). The history is poisoned first with the message PiG's extension host used to persist for an
// aborted extension provider turn, empty content with "context canceled": it stays in the transcript, and the next request sends neither aborted turn (transform-messages
// drops aborted assistant messages whole) and completes.
func TestAbortedBuiltInProviderTurnKeepsItsPartialAcrossAPIKinds(t *testing.T) {
	// aborted is the errorMessage the API kind's own error event carries for a request aborted in the middle of its text. openai-completions: Pi 1.5.2 and PiG, both run, give
	// this text. openai-responses and anthropic-messages: PiG's text, the same before and after the consumer change (this test run on both commits); Pi was not run for these two
	// kinds, so the test does not state that they equal Pi.
	type sse struct{ hold, whole, aborted string }
	kinds := map[ai.API]sse{
		ai.APIOpenAICompletions: {
			aborted: "Request was aborted",
			hold:    "data: {\"choices\":[{\"delta\":{\"content\":\"alpha \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"beta \"}}]}\n\n",
			whole:   "data: {\"choices\":[{\"delta\":{\"content\":\"whole answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		},
		ai.APIOpenAIResponses: {
			aborted: "OpenAI Responses stream ended before a terminal response event",
			hold:    "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg\",\"role\":\"assistant\",\"content\":[]}}\n\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"alpha \"}\n\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"beta \"}\n\n",
			whole:   "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg\",\"role\":\"assistant\",\"content\":[]}}\n\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"whole answer\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
		},
		ai.API("anthropic-messages"): {
			aborted: "This operation was aborted",
			hold:    "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"probe\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"alpha \"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"beta \"}}\n\n",
			whole:   "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"probe\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"whole answer\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		},
	}
	for api, body := range kinds {
		t.Run(string(api), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			// The endpoint's first request streams "alpha beta " and stays open until the client goes away; its second is answered whole. Each request's body is kept.
			var mu sync.Mutex
			var bodies []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, string(raw))
				first := len(bodies) == 1
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				if !first {
					_, _ = io.WriteString(w, body.whole)
					return
				}
				_, _ = io.WriteString(w, body.hold)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			var provider ai.Provider
			switch api {
			case ai.APIOpenAICompletions:
				provider = ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
			case ai.APIOpenAIResponses:
				provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
			default:
				provider = ai.NewAnthropicProvider(ai.AnthropicConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
			}
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()

			var a *agent.Agent
			var calls atomic.Int32
			abort := sync.OnceFunc(func() { go a.Abort() })
			a = mustNewAgent(agent.AgentOptions{
				Model: &ai.Model{ID: "probe", Provider: provider},
				StreamFn: func(ctx context.Context, _ *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					if calls.Add(1) > 1 {
						return provider.Stream(ctx, transcript, options)
					}
					// The first turn ends as the extension host used to end an aborted extension provider stream: its own error event, empty content, "context canceled".
					poisoned := ai.NewAssistantMessageEventStream()
					_ = poisoned.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: &ai.AssistantMessage{API: api, Provider: "probe-provider", Model: "probe", Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonAborted, ErrorMessage: "context canceled"}})
					return poisoned, nil
				},
				OnEvent: func(event agent.AgentEvent) {
					// The turn is aborted once the update that shows the second delta is out.
					frame, err := rpcAgentEvent(event)
					if err != nil {
						return
					}
					if raw, err := json.Marshal(frame); err == nil && strings.Contains(string(raw), `"delta":"beta "`) {
						abort()
					}
				},
			})
			// The results of the two aborted turns are read from the transcript; Send's error for an aborted turn is not what this test states.
			_, _ = a.Send(ctx, "first prompt")
			_, _ = a.Send(ctx, "HOLD the stream")
			a.WaitForIdle()
			if _, err := a.Send(ctx, "after the aborts"); err != nil {
				t.Fatal(err)
			}

			type message struct {
				Role    string `json:"role"`
				Content json.RawMessage
				Stop    string `json:"stopReason"`
				Error   string `json:"errorMessage"`
			}
			var assistants []string
			for _, entry := range a.Messages() {
				raw, err := json.Marshal(entry)
				if err != nil {
					t.Fatal(err)
				}
				var decoded message
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				if decoded.Role != "assistant" {
					continue
				}
				var blocks []struct{ Type, Text string }
				if err := json.Unmarshal(decoded.Content, &blocks); err != nil {
					t.Fatal(err)
				}
				state, _ := json.Marshal(map[string]any{"blocks": blocks, "stopReason": decoded.Stop, "errorMessage": decoded.Error})
				assistants = append(assistants, string(state))
			}
			want := []string{
				`{"blocks":[],"errorMessage":"context canceled","stopReason":"aborted"}`,
				`{"blocks":[{"Type":"text","Text":"alpha beta "}],"errorMessage":"` + body.aborted + `","stopReason":"aborted"}`,
				`{"blocks":[{"Type":"text","Text":"whole answer"}],"errorMessage":"","stopReason":"stop"}`,
			}
			if strings.Join(assistants, "\n") != strings.Join(want, "\n") {
				t.Errorf("the transcript's assistant messages are\n%s\nwant\n%s", strings.Join(assistants, "\n"), strings.Join(want, "\n"))
			}
			mu.Lock()
			defer mu.Unlock()
			if len(bodies) != 2 {
				t.Fatalf("the endpoint received %d requests, want 2", len(bodies))
			}
			last := bodies[1]
			for _, sent := range []string{"first prompt", "HOLD the stream", "after the aborts"} {
				if !strings.Contains(last, sent) {
					t.Errorf("the request after the aborted turns does not hold the user message %q: %s", sent, last)
				}
			}
			for _, dropped := range []string{"context canceled", "alpha beta", body.aborted} {
				if strings.Contains(last, dropped) {
					t.Errorf("the request after the aborted turns holds %q, part of an aborted assistant turn: %s", dropped, last)
				}
			}
		})
	}
}
