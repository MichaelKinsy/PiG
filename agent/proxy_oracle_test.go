package agent

import (
	"context"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

const proxyOracleUsage = `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`

func streamProxyOracle(t *testing.T, lines ...string) ([]ai.AssistantMessageEvent, *ai.AssistantMessage) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		for _, line := range lines {
			_, _ = writer.Write([]byte("data: " + line + "\n\n"))
		}
	}))
	defer server.Close()
	return collectProxyEvents(t, StreamProxy(t.Context(), proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{
		AuthToken: "token", ProxyURL: server.URL,
	}))
}

// Oracle: Pi 1.0.4 streamProxy probe. toolcall_end applies Object.assign(content, toolCall), so members the
// wire call omits keep the streamed value (packages/agent/src/proxy.ts processProxyEvent toolcall_end).
func TestStreamProxyToolCallEndKeepsStreamedMembersTheWireOmits(t *testing.T) {
	_, result := streamProxyOracle(t,
		`{"type":"start"}`,
		`{"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"t"}`,
		`{"type":"toolcall_delta","contentIndex":0,"delta":"{\"b\":2,\"a\":1}"}`,
		`{"type":"toolcall_end","contentIndex":0,"toolCall":{"type":"toolCall","namespace":"ns"}}`,
		`{"type":"done","reason":"toolUse","usage":`+proxyOracleUsage+`}`,
	)
	call, ok := result.Content[0].(ai.ToolCall)
	if !ok || call.ID != "c1" || call.Name != "t" || call.Namespace != "ns" {
		t.Fatalf("merged tool call = %#v", result.Content[0])
	}
	if want := (ai.JsonObject{"a": float64(1), "b": float64(2)}); !reflect.DeepEqual(call.Arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", call.Arguments, want)
	}
	encoded, err := call.ArgumentsJSON()
	if err != nil || string(encoded) != `{"b":2,"a":1}` {
		t.Fatalf("argument order = %s, %v", encoded, err)
	}
}

func TestStreamProxyToolCallEndWireMembersOverrideStreamedOnes(t *testing.T) {
	_, result := streamProxyOracle(t,
		`{"type":"start"}`,
		`{"type":"toolcall_start","contentIndex":0,"id":"c1","toolName":"t"}`,
		`{"type":"toolcall_delta","contentIndex":0,"delta":"{\"a\":1}"}`,
		`{"type":"toolcall_end","contentIndex":0,"toolCall":{"type":"toolCall","id":"c2","name":"u","arguments":{"z":true}}}`,
		`{"type":"done","reason":"toolUse","usage":`+proxyOracleUsage+`}`,
	)
	call := result.Content[0].(ai.ToolCall)
	if call.ID != "c2" || call.Name != "u" || !reflect.DeepEqual(call.Arguments, ai.JsonObject{"z": true}) {
		t.Fatalf("overridden tool call = %#v", call)
	}
}

// Oracle: Pi's EventStream ignores pushes after done, so a later event is not delivered and does not change the stop reason.
// Pi still applies the late event to the delivered message (its final content becomes [{text:""}]); PiG leaves the message
// as the terminal event delivered it, because changing it afterwards races with the consumer (proposed divergence).
func TestStreamProxyEventAfterDoneIsNotDelivered(t *testing.T) {
	events, result := streamProxyOracle(t,
		`{"type":"start"}`,
		`{"type":"done","reason":"stop","usage":`+proxyOracleUsage+`}`,
		`{"type":"text_start","contentIndex":0}`,
	)
	if got, want := proxyEventTypes(events), []ai.AssistantEventType{ai.EventStart, ai.EventDone}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if result.StopReason != ai.StopReasonStop || len(result.Content) != 0 {
		t.Fatalf("result = %s %#v, want stop with no content", result.StopReason, result.Content)
	}
}

// Pi's catch block sets stopReason "error" on the already delivered message when a line after done is not JSON (probed);
// PiG keeps the delivered message unchanged (proposed divergence, see TestStreamProxyEventAfterDoneIsNotDelivered).
func TestStreamProxyMalformedLineAfterDoneLeavesTheResult(t *testing.T) {
	events, result := streamProxyOracle(t,
		`{"type":"start"}`,
		`{"type":"done","reason":"stop","usage":`+proxyOracleUsage+`}`,
		`GARBAGE`,
	)
	if got, want := proxyEventTypes(events), []ai.AssistantEventType{ai.EventStart, ai.EventDone}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if result.StopReason != ai.StopReasonStop || result.ErrorMessage != "" {
		t.Fatalf("result = %s %q, want stop with no error", result.StopReason, result.ErrorMessage)
	}
}

// Oracle: aborting before the response headers rejects fetch with the signal reason: "This operation was aborted" for
// abort(), "The operation was aborted due to timeout" for AbortSignal.timeout, the reason's message for abort(reason).
// Aborting during the body surfaces "Request aborted by user" whatever the reason.
func TestStreamProxyAbortMessageDependsOnWhereTheSignalFired(t *testing.T) {
	release := make(chan struct{})
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/hdr") {
			received <- struct{}{}
			select {
			case <-release:
			case <-request.Context().Done():
			}
			return
		}
		_, _ = writer.Write([]byte("data: {\"type\":\"start\"}\n\n"))
		writer.(http.Flusher).Flush()
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	custom := errors.New("custom reason")
	for _, tc := range []struct {
		name, path, want string
		abort            func(context.CancelCauseFunc)
		body             bool
	}{
		{"abort before headers", "/hdr", "This operation was aborted", func(cancel context.CancelCauseFunc) { cancel(nil) }, false},
		{"timeout before headers", "/hdr", "The operation was aborted due to timeout", func(cancel context.CancelCauseFunc) { cancel(context.DeadlineExceeded) }, false},
		{"reason before headers", "/hdr", "custom reason", func(cancel context.CancelCauseFunc) { cancel(custom) }, false},
		{"abort during body", "/body", "Request aborted by user", func(cancel context.CancelCauseFunc) { cancel(nil) }, true},
		{"timeout during body", "/body", "Request aborted by user", func(cancel context.CancelCauseFunc) { cancel(context.DeadlineExceeded) }, true},
	} {
		ctx, cancel := context.WithCancelCause(t.Context())
		stream := StreamProxy(ctx, proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{AuthToken: "token", ProxyURL: server.URL + tc.path})
		next, stop := iter.Pull(stream.Events(t.Context()))
		if tc.body {
			if event, ok := next(); !ok || event.EventType() != ai.EventStart {
				t.Fatalf("%s: first event = %v, %v", tc.name, event, ok)
			}
		} else {
			<-received
		}
		tc.abort(cancel)
		for _, ok := next(); ok; _, ok = next() {
		}
		stop()
		result := stream.Result()
		if result.StopReason != ai.StopReasonAborted || result.ErrorMessage != tc.want {
			t.Fatalf("%s: result = %s %q, want aborted %q", tc.name, result.StopReason, result.ErrorMessage, tc.want)
		}
	}
}

// A context deadline is AbortSignal.timeout: it aborts the request with Pi's TimeoutError message.
func TestStreamProxyDeadlineBeforeHeadersIsATimeoutAbort(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	result := StreamProxy(ctx, proxyTestModel(), ai.NormalizeContext(ai.Context{}), ProxyStreamOptions{AuthToken: "token", ProxyURL: server.URL}).Result()
	if result.StopReason != ai.StopReasonAborted || result.ErrorMessage != "The operation was aborted due to timeout" {
		t.Fatalf("result = %s %q", result.StopReason, result.ErrorMessage)
	}
}

// Oracle: Pi warns "Unhandled proxy event type: <type>" through console.warn and skips the event.
func TestStreamProxyWarnsOnUnknownEventAndSkipsIt(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	events, result := streamProxyOracle(t,
		`{"type":"start"}`, `{"type":"wat"}`, `{"type":"done","reason":"stop","usage":`+proxyOracleUsage+`}`)
	os.Stderr = original
	_ = writer.Close()
	warned, _ := io.ReadAll(reader)
	if string(warned) != "Unhandled proxy event type: wat\n" {
		t.Fatalf("stderr = %q", warned)
	}
	if got, want := proxyEventTypes(events), []ai.AssistantEventType{ai.EventStart, ai.EventDone}; !reflect.DeepEqual(got, want) || result.StopReason != ai.StopReasonStop {
		t.Fatalf("events = %v result = %s", got, result.StopReason)
	}
}
