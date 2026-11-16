package ai

import (
	"context"
	"testing"
)

// Pi bb1a2b3542b5 (#12229, closes #12124): the Codex backend closes a Responses WebSocket after 60 minutes, even mid-response. The error the
// provider ends the message with is retried by the shared classifier (utils/retry.ts), and the outer retry opens a fresh WebSocket.
// upstream: packages/ai/test/openai-codex-stream.test.ts "retries a connection lifetime limit after $name on a fresh websocket"
func TestCodexWebSocketRetriesAConnectionLifetimeLimitOnAFreshWebSocket(t *testing.T) {
	limit := `{"type":"error","error":{"code":"websocket_connection_limit_reached","message":"Responses websocket connection limit reached (60 minutes). Create a new websocket connection to continue."}}`
	for _, tc := range []struct {
		name   string
		events []string
	}{
		{"response metadata", []string{`{"type":"response.created","response":{"id":"resp_expired"}}`}},
		{"rate-limit metadata", []string{`{"type":"codex.rate_limits","rate_limits":{"allowed":true,"limit_reached":false}}`}},
		{"partial thinking", []string{
			`{"type":"response.output_item.added","item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
			`{"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":""}}`,
			`{"type":"response.reasoning_summary_text.delta","delta":"unfinished thinking"}`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recovered := []string{
				`{"type":"response.output_item.added","item":{"type":"message","id":"msg_2","role":"assistant","content":[]}}`,
				`{"type":"response.output_item.done","item":{"type":"message","id":"msg_2","role":"assistant","content":[{"type":"output_text","text":"Recovered"}]}}`,
				codexWSTerminal("resp_2"),
			}
			peer := newCodexWSPeer(t, func(_, connection int, _ map[string]any) []string {
				if connection == 1 {
					return append(append([]string{}, tc.events...), limit)
				}
				return recovered
			})
			provider := peer.provider(t, "acc_test")
			var attempts []AssistantMessage
			result, err := RetryAssistantCall(context.Background(), func() (AssistantMessage, error) {
				message, _ := codexWSResult(t, provider, NormalizeContext(Context{}), StreamOptions{SessionID: "connection-limit-" + tc.name, Transport: TransportWebSocketCached})
				attempts = append(attempts, *message)
				return *message, nil
			}, &RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: 0}, RetryCallbacks{})
			if err != nil {
				t.Fatal(err)
			}
			if result.StopReason != StopReasonStop || codexUpstreamText(&result) != "Recovered" {
				t.Fatalf("result = %#v", result)
			}
			if len(attempts) != 2 || attempts[0].StopReason != StopReasonError {
				t.Fatalf("attempts = %#v", attempts)
			}
			if tc.name == "partial thinking" {
				if len(attempts[0].Content) != 1 {
					t.Fatalf("first attempt content = %#v", attempts[0].Content)
				}
				if thinking, ok := attempts[0].Content[0].(ThinkingContent); !ok || thinking.Thinking != "unfinished thinking" {
					t.Fatalf("first attempt content = %#v", attempts[0].Content)
				}
			}
			headers, bodies, fetches := peer.snapshot()
			if len(headers) != 2 || len(bodies) != 2 || fetches != 0 {
				t.Fatalf("connections=%d requests=%d sse fetches=%d, want two WebSockets, two requests and no fallback", len(headers), len(bodies), fetches)
			}
			if bodies[1].Connection != 2 {
				t.Fatalf("second request on connection %d, want 2", bodies[1].Connection)
			}
			if _, ok := bodies[1].Body["previous_response_id"]; ok {
				t.Fatalf("second request carries previous_response_id: %#v", bodies[1].Body)
			}
			select {
			case closed := <-peer.closed:
				if closed != 1 {
					t.Fatalf("closed connection %d, want 1", closed)
				}
			case <-t.Context().Done():
				t.Fatal("the first websocket was not closed")
			}
		})
	}
}
