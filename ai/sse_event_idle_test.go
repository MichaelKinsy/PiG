package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pi-mono #10761 / #10762: an OpenAI-compatible gateway that keeps the connection alive with comment lines but sends no event left `pi -p`
// parked forever, because the idle timeout only measures bytes.
// pig divergence (D131): a text/event-stream response with no semantic event for httpIdleTimeoutMs ends with an error.
func TestSSEStreamWithOnlyKeepalivesEndsAfterTheIdleTimeout(t *testing.T) {
	testSSEKeepalivesEndAfterIdleTimeout(t, StreamOptions{})
}

// Every coding-agent session sets TimeoutMs, which wraps the body in providerResponseBody above the idle body.
func TestSSEStreamWithOnlyKeepalivesEndsAfterTheIdleTimeoutWithTimeoutMs(t *testing.T) {
	timeout := 60000
	testSSEKeepalivesEndAfterIdleTimeout(t, StreamOptions{TimeoutMs: &timeout})
}

func testSSEKeepalivesEndAfterIdleTimeout(t *testing.T, options StreamOptions) {
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureHTTPDispatcher(previous) })

	for _, tc := range []struct {
		name      string
		keepalive string
	}{
		{"comment lines", ": keepalive\n\n"},
		{"ping events", "event: ping\ndata: {\"type\":\"ping\"}\n\n"},
		{"ping events with CRLF line ends", "event: ping\r\ndata: {\"type\":\"ping\"}\r\n\r\n"},
		{"ping events with CR line ends", "event: ping\rdata: {\"type\":\"ping\"}\r\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				flusher := w.(http.Flusher)
				for {
					if _, err := io.WriteString(w, tc.keepalive); err != nil {
						return
					}
					flusher.Flush()
					select {
					case <-r.Context().Done():
						return
					case <-time.After(30 * time.Millisecond):
					}
				}
			}))
			defer server.Close()
			// Cancelled before server.Close on every exit, so a stream that never ends fails this test instead of blocking Close until the package timeout.
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			provider := NewOpenAIProvider(OpenAIConfig{Model: "m", APIKey: "k", BaseURL: server.URL})
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), options)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan *AssistantMessage, 1)
			go func() { done <- stream.Result() }()
			select {
			case message := <-done:
				if message.StopReason != StopReasonError || !strings.Contains(message.ErrorMessage, "SSE Event Timeout Error") {
					t.Fatalf("stop=%s error=%q", message.StopReason, message.ErrorMessage)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("a stream with only keepalives did not end")
			}
		})
	}
}

// Events that arrive within the timeout keep the stream alive for longer than the timeout, with keepalives between them.
func TestSSEStreamWithSlowEventsIsNotCutOff(t *testing.T) {
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureHTTPDispatcher(previous) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for range 6 {
			_, _ = io.WriteString(w, ": keepalive\n\n"+`data: {"id":"c","choices":[{"index":0,"delta":{"content":"x"}}]}`+"\n\n")
			flusher.Flush()
			time.Sleep(120 * time.Millisecond)
		}
		_, _ = io.WriteString(w, `data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider := NewOpenAIProvider(OpenAIConfig{Model: "m", APIKey: "k", BaseURL: server.URL})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	message := stream.Result()
	if message.StopReason != StopReasonStop {
		t.Fatalf("stop=%s error=%q", message.StopReason, message.ErrorMessage)
	}
}

// sseKeepaliveStream serves events, then keepalive comment lines until the client goes away or for = elapsed, then closes; it reports what the provider made of it.
func runSSEWithKeepalives(t *testing.T, head []string, keepaliveFor time.Duration, tail []string) *AssistantMessage {
	t.Helper()
	previous := ConfiguredHTTPIdleTimeoutMs()
	if err := ConfigureHTTPDispatcher(300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureHTTPDispatcher(previous) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, event := range head {
			_, _ = io.WriteString(w, event)
		}
		flusher.Flush()
		deadline := time.After(keepaliveFor)
		for {
			select {
			case <-r.Context().Done():
				return
			case <-deadline:
				for _, event := range tail {
					_, _ = io.WriteString(w, event)
				}
				flusher.Flush()
				return
			case <-time.After(30 * time.Millisecond):
				if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	provider := NewOpenAIProvider(OpenAIConfig{Model: "m", APIKey: "k", BaseURL: server.URL})
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *AssistantMessage, 1)
	go func() { done <- stream.Result() }()
	select {
	case message := <-done:
		return message
	case <-time.After(10 * time.Second):
		t.Fatal("the stream did not end")
		return nil
	}
}

const (
	sseReasoningChunk = `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"thinking it over"}}]}` + "\n\n"
	sseTextChunk      = `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"
	sseFinishChunk    = `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" done"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
)

// Owner sign-off 2026-10-10 (D131): a model that reasons silently behind a gateway's keepalives has not stalled. After a reasoning event the keepalives count as
// progress, so a pause three times the idle timeout does not end the stream.
func TestSSEKeepalivesAfterAReasoningEventDoNotEndTheStream(t *testing.T) {
	message := runSSEWithKeepalives(t, []string{sseReasoningChunk}, 900*time.Millisecond, []string{sseFinishChunk})
	if message.StopReason != StopReasonStop {
		t.Fatalf("stop=%s error=%q, want a finished turn after 900 ms of keepalives behind a reasoning event", message.StopReason, message.ErrorMessage)
	}
}

// The other way: keepalives after an event that is not reasoning still end the stream after the idle timeout, and so do keepalives after reasoning that
// a text event followed.
func TestSSEKeepalivesAfterANonReasoningEventStillEndTheStream(t *testing.T) {
	for name, head := range map[string][]string{
		"after text":                    {sseTextChunk},
		"after reasoning then text":     {sseReasoningChunk, sseTextChunk},
		"after only a role chunk":       {`data: {"id":"c","choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n"},
		"after a reasoning token tally": {`data: {"id":"c","choices":[{"index":0,"delta":{}}],"usage":{"completion_tokens_details":{"reasoning_tokens":5}}}` + "\n\n"},
	} {
		t.Run(name, func(t *testing.T) {
			message := runSSEWithKeepalives(t, head, 5*time.Second, []string{sseFinishChunk})
			if message.StopReason != StopReasonError || !strings.Contains(message.ErrorMessage, "SSE Event Timeout Error") {
				t.Fatalf("stop=%s error=%q, want the idle timeout", message.StopReason, message.ErrorMessage)
			}
		})
	}
}

// The tracker marks an event as reasoning from the event name or from a reasoning member of its JSON, with or without spaces in the JSON, for each provider's shape.
func TestSSEIdleTrackerRecognizesReasoningEvents(t *testing.T) {
	for name, tc := range map[string]struct {
		stream string
		want   bool
	}{
		"anthropic thinking block":             {"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n", true},
		"anthropic thinking delta":             {"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hm\"}}\n\n", true},
		"anthropic signature delta":            {"data: {\"delta\":{\"type\":\"signature_delta\",\"signature\":\"x\"}}\n\n", true},
		"anthropic redacted thinking":          {"data: {\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"x\"}}\n\n", true},
		"anthropic text block":                 {"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n", false},
		"responses reasoning item":             {"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"summary\":[]}}\n\n", true},
		"responses reasoning event":            {"event: response.reasoning_summary_text.delta\ndata: {\"delta\":\"x\"}\n\n", true},
		"responses created echo":               {"data: {\"type\":\"response.created\",\"response\":{\"reasoning\":{\"effort\":null,\"summary\":null}}}\n\n", false},
		"chat reasoning_content":               {"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"x\"}}]}\n\n", true},
		"chat reasoning with spaces":           {"data: {\"choices\": [{\"delta\": {\"reasoning\": \"x\"}}]}\n\n", true},
		"openrouter reasoning details":         {"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.text\"}]}}]}\n\n", true},
		"gemini thought part":                  {"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"x\",\"thought\":true}]}}]}\n\n", true},
		"chat text with empty reasoning":       {"data: {\"choices\":[{\"delta\":{\"content\":\"x\",\"reasoning_content\":\"\",\"reasoning\": \"\"}}]}\n\n", false},
		"openrouter text with no details":      {"data: {\"choices\":[{\"delta\":{\"content\":\"x\",\"reasoning\":null,\"reasoning_details\":[ ]}}]}\n\n", false},
		"event name ends with its event":       {"event: response.reasoning_summary_text.delta\ndata: {\"delta\":\"x\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"y\"}\n\n", false},
		"chat text":                            {"data: {\"choices\":[{\"delta\":{\"content\":\"I was thinking about it\"}}]}\n\n", false},
		"ping after reasoning is not an event": {"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"x\"}}]}\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			body := &sseIdleBody{}
			body.scan([]byte(tc.stream))
			if body.reasoning != tc.want {
				t.Fatalf("reasoning=%v, want %v", body.reasoning, tc.want)
			}
		})
	}
}

// A marker after a value longer than the sniff window is still found: Gemini writes "thought": true after the thought's text, and a chat chunk may carry its
// reasoning after a long content member. The sweep crosses each window cut, and feeding one byte at a time must give the same answer.
func TestSSEIdleTrackerFindsReasoningMarkersPastTheSniffWindow(t *testing.T) {
	for _, n := range []int{0, 900, sseLineSniffBytes - 80, sseLineSniffBytes - 40, sseLineSniffBytes, 3 * sseLineSniffBytes, 10_000} {
		for pad := range 64 {
			text := strings.Repeat("a", n+pad)
			for name, tc := range map[string]struct {
				stream string
				want   bool
			}{
				"gemini thought":         {`data: {"candidates": [{"content": {"parts": [{"text": "` + text + `","thought": true}],"role": "model"},"index": 0}]}` + "\n\n", true},
				"gemini text":            {`data: {"candidates": [{"content": {"parts": [{"text": "` + text + `"}],"role": "model"},"index": 0}]}` + "\n\n", false},
				"chat reasoning after":   {`data: {"choices":[{"delta":{"content":"` + text + `","reasoning":"x"}}]}` + "\n\n", true},
				"chat empty after":       {`data: {"choices":[{"delta":{"content":"` + text + `","reasoning_content":""}}]}` + "\n\n", false},
				"comment then reasoning": {": " + text + "\n" + `data: {"choices":[{"delta":{"reasoning_content":"x"}}]}` + "\n\n", true},
			} {
				whole := &sseIdleBody{}
				whole.scan([]byte(tc.stream))
				single := &sseIdleBody{}
				for i := range len(tc.stream) {
					single.scan([]byte(tc.stream[i : i+1]))
				}
				if whole.reasoning != tc.want || single.reasoning != tc.want || len(whole.line) != 0 {
					t.Fatalf("%s with %d bytes of text: reasoning=%v (one byte at a time %v), want %v", name, len(text), whole.reasoning, single.reasoning, tc.want)
				}
			}
		}
	}
}

// Both keepalive forms, a comment line and a ping event, are progress after a reasoning event and are not progress after a text event.
func TestSSEIdleTrackerCountsBothKeepaliveFormsOnlyWhileReasoning(t *testing.T) {
	const reasoning = "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"x\"}}]}\n\n"
	const text = "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"
	for name, keepalive := range map[string]string{"comment": ": keepalive\n\n", "ping": "event: ping\ndata: {\"type\":\"ping\"}\n\n"} {
		for head, want := range map[string]bool{reasoning: true, text: false} {
			body := &sseIdleBody{}
			body.scan([]byte(head))
			if got := body.scan([]byte(keepalive)); got != want {
				t.Errorf("%s after %q: progress=%v, want %v", name, head, got, want)
			}
		}
	}
}
