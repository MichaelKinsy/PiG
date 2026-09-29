package ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func terminalEventsUpstream(t *testing.T, sse string, wire bool) (*AssistantMessage, []AssistantMessageEvent) {
	t.Helper()
	var stream *AssistantMessageEventStream
	var events []AssistantMessageEvent
	if wire {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, sse)
		}))
		defer server.Close()
		provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: "gpt-5-mini", ProviderID: "openai", APIKey: "test", BaseURL: server.URL})
		var err error
		stream, err = provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}}}}, Tools: []ToolSchema{}}), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
	} else {
		provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{Model: "gpt-5-mini", ProviderID: "openai"}}
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "openai", "gpt-5-mini")
		stream = builder.stream
		var frames []string
		for frame := range strings.SplitSeq(sse, "\n\n") {
			if frame != "" {
				frames = append(frames, frame+"\n\n")
			}
		}
		reader := &providerScratchReader{frames: frames, beforeRead: func(int) {
			events = append(events, drainObservedAssistantEvents(t, stream)...)
		}}
		provider.parseResponsesSSE(t.Context(), reader, builder, nil)
	}
	for event := range stream.Events(t.Context()) {
		events = append(events, mapAssistantEventPartial(event, (*AssistantMessage).Observe))
	}
	return stream.Result(), events
}

func TestOpenAIResponsesTerminalEventUpstream(t *testing.T) {
	for _, wire := range []bool{false, true} {
		// .upstream/v0.87.1/packages/ai/test/openai-responses-terminal-event.test.ts:208,218
		name := "rejects streams that end before a terminal response event"
		id, text := "early_eof", "partial reasoning before the stream ends"
		if wire {
			name = "emits an error final result when the wrapper stream ends before a terminal response event"
			id = "wrapper_early_eof"
			text = "partial reasoning before the wrapper stream ends"
		}
		t.Run(name, func(t *testing.T) {
			sse := fmt.Sprintf("data: {\"type\":\"response.created\",\"response\":{\"id\":%q}}\n\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":%q,\"summary\":[]}}\n\ndata: {\"type\":\"response.reasoning_text.delta\",\"output_index\":0,\"delta\":%q}\n\n", "resp_"+id, "rs_"+id, text)
			result, events := terminalEventsUpstream(t, sse, wire)
			if result.StopReason != StopReasonError || result.ErrorMessage != "OpenAI Responses stream ended before a terminal response event" {
				t.Fatal(result)
			}
			if wire {
				if len(events) == 0 || events[len(events)-1].EventType() != EventError {
					t.Fatalf("events=%#v", events)
				}
				found := false
				for _, event := range events {
					if start, ok := event.(StartEvent); ok {
						found = true
						if start.Partial.StopReason != StopReasonPending {
							t.Fatalf("start=%#v", start.Partial)
						}
					}
				}
				if !found {
					t.Fatal("missing start")
				}
			}
		})
	}
	for _, tc := range []struct {
		initial, final string
		incomplete     bool
		want           []StopReason
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-responses-terminal-event.test.ts:242
		{"commentary", "commentary", false, []StopReason{StopReasonPending, StopReasonPending}},
		{"final_answer", "final_answer", false, []StopReason{StopReasonStop, StopReasonStop}},
		{"commentary", "final_answer", false, []StopReason{StopReasonPending, StopReasonStop}},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-terminal-event.test.ts:263
		{"final_answer", "final_answer", true, []StopReason{StopReasonStop, StopReasonStop}},
	} {
		t.Run(fmt.Sprintf("tracks message phases %s/%s incomplete=%v", tc.initial, tc.final, tc.incomplete), func(t *testing.T) {
			terminal := `{"type":"response.completed","response":{"id":"resp_phase","status":"completed"}}`
			if tc.incomplete {
				terminal = `{"type":"response.incomplete","response":{"id":"resp_phase","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`
			}
			sse := fmt.Sprintf("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_phase\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[],\"phase\":%q}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_phase\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\",\"annotations\":[]}],\"phase\":%q}}\n\ndata: %s\n\n", tc.initial, tc.final, terminal)
			result, events := terminalEventsUpstream(t, sse, false)
			var observed []StopReason
			for _, event := range events {
				switch event := event.(type) {
				case TextStartEvent:
					observed = append(observed, event.Partial.StopReason)
				case TextEndEvent:
					observed = append(observed, event.Partial.StopReason)
				}
			}
			if !reflect.DeepEqual(observed, tc.want) {
				t.Fatalf("phases=%v want=%v", observed, tc.want)
			}
			want := StopReasonStop
			if tc.incomplete {
				want = StopReasonLength
			}
			if result.StopReason != want {
				t.Fatal(result)
			}
		})
	}
	for _, tc := range []struct {
		name, event, id, status, reason   string
		stop                              StopReason
		raw, message                      string
		input, output, read, write, total int
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-responses-terminal-event.test.ts:285
		{"finalizes completed terminal events as stop", "response.completed", "resp_completed", "completed", "", StopReasonStop, "completed", "", 15, 7, 2, 3, 27},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-terminal-event.test.ts:304
		{"finalizes incomplete terminal events as length stops", "response.incomplete", "resp_incomplete", "incomplete", "max_output_tokens", StopReasonLength, "incomplete.max_output_tokens", "", 25, 12, 5, 0, 42},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-terminal-event.test.ts:323
		{"finalizes content-filtered incomplete responses as non-retryable errors", "response.incomplete", "resp_incomplete", "incomplete", "content_filter", StopReasonError, "incomplete.content_filter", "Response incomplete: content_filter", 25, 12, 5, 0, 42},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-terminal-event.test.ts:335
		{"preserves unknown provider incomplete reasons as non-retryable errors", "response.incomplete", "resp_incomplete", "incomplete", "max_time_limit", StopReasonError, "incomplete.max_time_limit", "Response incomplete: max_time_limit", 25, 12, 5, 0, 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sse := fmt.Sprintf("data: {\"type\":%q,\"response\":{\"id\":%q,\"status\":%q,\"incomplete_details\":{\"reason\":%q},\"usage\":{\"input_tokens\":%d,\"output_tokens\":%d,\"total_tokens\":%d,\"input_tokens_details\":{\"cached_tokens\":%d,\"cache_write_tokens\":%d}}}}\n\n", tc.event, tc.id, tc.status, tc.reason, tc.input+tc.read+tc.write, tc.output, tc.total, tc.read, tc.write)
			result, _ := terminalEventsUpstream(t, sse, false)
			if result.ResponseID != tc.id || result.StopReason != tc.stop || result.RawStopReason != tc.raw || result.ErrorMessage != tc.message {
				t.Fatal(result)
			}
			u := result.Usage
			if u.Input != tc.input || u.Output != tc.output || u.CacheRead != tc.read || u.CacheWrite != tc.write || u.TotalTokens != tc.total {
				t.Fatalf("usage=%#v", u)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/openai-responses-terminal-event.test.ts:347
	t.Run("rejects failed terminal events with the provider error", func(t *testing.T) {
		result, _ := terminalEventsUpstream(t, "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_failed\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"boom\"}}}\n\n", false)
		if result.StopReason != StopReasonError || result.RawStopReason != "failed" || !strings.Contains(result.ErrorMessage, "server_error: boom") {
			t.Fatal(result)
		}
	})
}
