package agent

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/agent/test/proxy.test.ts describe "streamProxy"

func proxyServerWriting(t *testing.T, events []ProxyAssistantMessageEvent, terminateLastLine bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeProxyEvents(t, writer, events, terminateLastLine)
	}))
}

func TestStreamProxyPreservesToolCallMetadataReceivedOnlyOnToolCallEnd(t *testing.T) {
	// upstream: proxy.test.ts:33 "preserves tool-call metadata received only on toolcall_end"
	usage := ai.Usage{}
	server := proxyServerWriting(t, []ProxyAssistantMessageEvent{
		{Type: "start"},
		{Type: "toolcall_start", ContentIndex: 0, ID: "call_test|fc_test", ToolName: "lookup"},
		{Type: "toolcall_delta", ContentIndex: 0, Delta: `{"value":"hello"}`},
		{Type: "toolcall_end", ContentIndex: 0, ToolCall: &ai.ToolCall{
			ID: "call_test|fc_test", Name: "lookup", Arguments: ai.JsonObject{"value": "hello"}, Namespace: "dynamic_tools",
		}},
		{Type: "done", Reason: ai.StopReasonToolUse, Usage: usage},
	}, true)
	defer server.Close()

	events, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "test-token", ProxyURL: server.URL,
	}))

	var end ai.ToolCallEndEvent
	for _, event := range events {
		if candidate, ok := event.(ai.ToolCallEndEvent); ok {
			end = candidate
		}
	}
	if end.ToolCall.Namespace != "dynamic_tools" {
		t.Fatalf("toolcall_end tool call = %#v, want namespace dynamic_tools", end.ToolCall)
	}
	toolCall, ok := result.Content[0].(ai.ToolCall)
	if !ok || toolCall.Arguments["value"] != "hello" || toolCall.Namespace != "dynamic_tools" {
		t.Fatalf("persisted content = %#v", result.Content[0])
	}
}

func TestStreamProxyProcessesTerminalMetadataWhenTheEventIsNotNewlineTerminated(t *testing.T) {
	// upstream: proxy.test.ts:78 "processes terminal metadata when the event is not newline-terminated"
	// Regression guard for https://github.com/earendil-works/pi/issues/8996.
	level := "high"
	server := proxyServerWriting(t, []ProxyAssistantMessageEvent{
		{Type: "start"},
		{Type: "done", Reason: ai.StopReasonStop, Usage: ai.Usage{}, ProviderThinkingLevel: &level},
	}, false)
	defer server.Close()

	events, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "test-token", ProxyURL: server.URL,
	}))

	if got, want := proxyEventTypes(events), []ai.AssistantEventType{ai.EventStart, ai.EventDone}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if result.StopReason != ai.StopReasonStop || result.ProviderThinkingLevel != "high" {
		t.Fatalf("result = stop %q, provider thinking level %q", result.StopReason, result.ProviderThinkingLevel)
	}
}

func TestStreamProxyEmitsAnErrorInsteadOfHangingWhenTheStreamEndsWithoutATerminalEvent(t *testing.T) {
	// upstream: proxy.test.ts:99 "emits an error instead of hanging when the stream ends without a terminal event"
	server := proxyServerWriting(t, []ProxyAssistantMessageEvent{{Type: "start"}}, true)
	defer server.Close()

	events, result := collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "test-token", ProxyURL: server.URL,
	}))

	if got, want := proxyEventTypes(events), []ai.AssistantEventType{ai.EventStart, ai.EventError}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "Connection closed by proxy server before the response completed" {
		t.Fatalf("result = stop %q, error %q", result.StopReason, result.ErrorMessage)
	}
}
