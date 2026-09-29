package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// scriptedChunks is a chunk source that returns its chunks and then a terminal error.
type scriptedChunks struct {
	chunks []string
	end    error
}

func (source *scriptedChunks) ReadChunk() ([]byte, error) {
	if len(source.chunks) == 0 {
		return nil, source.end
	}
	chunk := source.chunks[0]
	source.chunks = source.chunks[1:]
	return []byte(chunk), nil
}

func readAnthropicSSE(t *testing.T, chunks ...string) (events []serverSentEvent, err error) {
	t.Helper()
	reader := &anthropicSSEReader{ctx: t.Context(), source: &scriptedChunks{chunks: chunks, end: io.EOF}, suspend: func() {}}
	for {
		event, ok, err := reader.next()
		if err != nil || !ok {
			return events, err
		}
		events = append(events, event)
	}
}

// packages/ai/src/api/anthropic-messages.ts:iterateSseMessages and its helpers decide every record; the fixtures below are that source's own edge cases.
func TestAnthropicSSEReaderLineDecoding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   []serverSentEvent
	}{
		{"lf records", []string{"event: a\ndata: 1\n\nevent: b\ndata: 2\n\n"}, []serverSentEvent{
			{Event: "a", Data: "1", Raw: []string{"event: a", "data: 1"}},
			{Event: "b", Data: "2", Raw: []string{"event: b", "data: 2"}},
		}},
		{"crlf pair is one line break", []string{"event: a\r\ndata: 1\r\n\r\n"}, []serverSentEvent{{Event: "a", Data: "1", Raw: []string{"event: a", "data: 1"}}}},
		// consumeLine ends a line at a carriage return even when its line feed arrives in the next chunk; that line feed then ends an empty line and flushes the record early.
		{"carriage return at a chunk end", []string{"event: a\r", "\ndata: 1\r\n\r\n"}, []serverSentEvent{
			{Event: "a", Data: "", Raw: []string{"event: a"}},
			{Event: "", Data: "1", Raw: []string{"data: 1"}},
		}},
		{"comments and unknown fields are raw only", []string{": keep-alive\nid: 7\nevent: a\ndata:x\n\n"}, []serverSentEvent{{Event: "a", Data: "x", Raw: []string{": keep-alive", "id: 7", "event: a", "data:x"}}}},
		{"data lines join with a line feed", []string{"event: a\ndata: 1\ndata: 2\n\n"}, []serverSentEvent{{Event: "a", Data: "1\n2", Raw: []string{"event: a", "data: 1", "data: 2"}}}},
		{"a record without a blank line is flushed at the end", []string{"event: a\ndata: 1"}, []serverSentEvent{{Event: "a", Data: "1", Raw: []string{"event: a", "data: 1"}}}},
		{"a line split across chunks", []string{"event: mess", "age_start\nda", "ta: 1\n", "\n"}, []serverSentEvent{{Event: "message_start", Data: "1", Raw: []string{"event: message_start", "data: 1"}}}},
		{"a multi-byte character split across chunks", []string{"data: \xe2\x9c", "\x93\n\n"}, []serverSentEvent{{Data: "\u2713", Raw: []string{"data: \u2713"}}}},
		// TextDecoder strips a leading BOM only; a later one is part of the field name, so that record has no event or data.
		{"a byte order mark is stripped once", []string{"\xef\xbb\xbfdata: 1\n\n\xef\xbb\xbfdata: 2\n\n"}, []serverSentEvent{{Data: "1", Raw: []string{"data: 1"}}}},
		{"ill-formed bytes become one replacement per maximal subpart", []string{"data: a\xffb\xe2\x82c\n\n"}, []serverSentEvent{{Data: "a\ufffdb\ufffdc", Raw: []string{"data: a\ufffdb\ufffdc"}}}},
		{"an incomplete sequence at the end is one replacement", []string{"data: a\xe2\x82"}, []serverSentEvent{{Data: "a\ufffd", Raw: []string{"data: a\ufffd"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readAnthropicSSE(t, tc.chunks...)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("events=%#v\nwant   %#v", got, tc.want)
			}
		})
	}
}

// The iterator checks the abort signal before every read and throws Pi's message (anthropic-messages.ts:422-424); a read error is thrown as it is.
func TestAnthropicSSEReaderAbortAndReadErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	reader := &anthropicSSEReader{ctx: ctx, source: &scriptedChunks{chunks: []string{"data: 1\n\n"}, end: io.EOF}, suspend: func() {}}
	if _, ok, err := reader.next(); ok || !errors.Is(err, errAnthropicRequestAborted) || err.Error() != "Request was aborted" {
		t.Fatalf("aborted: ok=%t err=%v", ok, err)
	}
	failure := errors.New("terminated")
	reader = &anthropicSSEReader{ctx: t.Context(), source: &scriptedChunks{chunks: []string{"data: 1\n\n"}, end: failure}, suspend: func() {}}
	if event, ok, err := reader.next(); !ok || err != nil || event.Data != "1" {
		t.Fatalf("first: %#v ok=%t err=%v", event, ok, err)
	}
	if _, ok, err := reader.next(); ok || !errors.Is(err, failure) {
		t.Fatalf("read error: ok=%t err=%v", ok, err)
	}
}

// Each await of the generator chain is one reaction; the counts are pinned end to end by TestAnthropicMicrotaskTrace, and here per construct so a regression names the construct.
func TestAnthropicEventReaderSuspendCounts(t *testing.T) {
	const record = "event: message_start\ndata: {}\n\n"
	count := func(chunks []string, end error, run func(*anthropicEventReader)) int {
		suspends := 0
		reader := newAnthropicEventReader(t.Context(), &scriptedChunks{chunks: chunks, end: end}, func() { suspends++ })
		run(reader)
		return suspends
	}
	// H yield, G awaits H, G yield, provider awaits G.
	if got := count([]string{record}, io.EOF, func(reader *anthropicEventReader) {
		if _, ok, err := reader.next(); !ok || err != nil {
			t.Fatalf("next: ok=%t err=%v", ok, err)
		}
	}); got != 4 {
		t.Fatalf("value: %d suspends, want 4", got)
	}
	// An ignored record costs H's yield and G's await only; the provider still awaits the next value.
	if got := count([]string{"event: ping\ndata: {}\n\n" + record}, io.EOF, func(reader *anthropicEventReader) {
		if event, ok, err := reader.next(); !ok || err != nil || event.Event != "message_start" {
			t.Fatalf("next: %#v ok=%t err=%v", event, ok, err)
		}
	}); got != 6 {
		t.Fatalf("skipped record: %d suspends, want 6", got)
	}
	// Completion: G awaits H's done result, the provider awaits G's.
	if got := count(nil, io.EOF, func(reader *anthropicEventReader) {
		if _, ok, err := reader.next(); ok || err != nil {
			t.Fatalf("next: ok=%t err=%v", ok, err)
		}
	}); got != 2 {
		t.Fatalf("completion: %d suspends, want 2", got)
	}
	// A thrown read error: G awaits the rejection, the provider awaits G's rejection.
	if got := count(nil, errors.New("terminated"), func(reader *anthropicEventReader) {
		if _, ok, err := reader.next(); ok || err == nil {
			t.Fatalf("next: ok=%t err=%v", ok, err)
		}
	}); got != 2 {
		t.Fatalf("read error: %d suspends, want 2", got)
	}
	// An error event: H's yield, G's await, G's throw closes H (return await, G awaits the return), then the provider awaits G's rejection.
	if got := count([]string{"event: error\ndata: boom\n\n"}, io.EOF, func(reader *anthropicEventReader) {
		if _, ok, err := reader.next(); ok || err == nil || err.Error() != "boom" {
			t.Fatalf("next: ok=%t err=%v", ok, err)
		}
	}); got != 5 {
		t.Fatalf("error event: %d suspends, want 5", got)
	}
}

func openAnthropicWireBlocks(t *testing.T, message *AssistantMessage) []map[string]any {
	t.Helper()
	encoded := deliveredJSON(t, message)
	var decoded struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Content
}

// Pi's blocks carry the provider's event index (and a tool call its partial JSON) while their content block is open, deletes them at content_block_stop, and deletes both on failure (anthropic-messages.ts:626-750,817-826).
func TestAnthropicOpenBlocksCarryProviderScratch(t *testing.T) {
	events := anthropicFixtureEvent("message_start", `{"message":{"id":"m","usage":{"input_tokens":1,"output_tokens":1}}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":4,"content_block":{"type":"thinking","thinking":"","signature":""}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":5,"content_block":{"type":"text","text":""}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":6,"content_block":{"type":"tool_use","id":"t","name":"read","input":{}}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":6,"delta":{"type":"input_json_delta","partial_json":"{\"a\":1"}}`) +
		anthropicFixtureEvent("content_block_stop", `{"index":4}`) +
		anthropicFixtureEvent("message_delta", `{"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`) +
		anthropicFixtureEvent("message_stop", `{}`)
	var open [][]map[string]any
	_, emitted := runAnthropicWire(t, AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key"}, Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}, StreamOptions{}, func(map[string]any) string { return events })
	for _, event := range emitted {
		if partial := eventPartial(event); partial != nil {
			open = append(open, openAnthropicWireBlocks(t, partial))
		}
	}
	want := [][]map[string]any{
		{{"type": "thinking", "thinking": "", "thinkingSignature": "", "index": float64(4)}},
		{{"type": "thinking", "thinking": "", "thinkingSignature": "", "index": float64(4)}, {"type": "text", "text": "", "index": float64(5)}},
		{{"type": "thinking", "thinking": "", "thinkingSignature": "", "index": float64(4)}, {"type": "text", "text": "", "index": float64(5)}, {"type": "toolCall", "id": "t", "name": "read", "arguments": map[string]any{}, "partialJson": "", "index": float64(6)}},
		{{"type": "thinking", "thinking": "", "thinkingSignature": "", "index": float64(4)}, {"type": "text", "text": "", "index": float64(5)}, {"type": "toolCall", "id": "t", "name": "read", "arguments": map[string]any{"a": float64(1)}, "partialJson": `{"a":1`, "index": float64(6)}},
		{{"type": "thinking", "thinking": "", "thinkingSignature": ""}, {"type": "text", "text": "", "index": float64(5)}, {"type": "toolCall", "id": "t", "name": "read", "arguments": map[string]any{"a": float64(1)}, "partialJson": `{"a":1`, "index": float64(6)}},
	}
	if !reflect.DeepEqual(open, want) {
		t.Fatalf("open blocks:\n got %v\nwant %v", open, want)
	}
	// Blocks that never stop keep their scratch into the terminal message, as Pi's do.
	final := emitted[len(emitted)-1].(DoneEvent).Message
	if blocks := openAnthropicWireBlocks(t, final); blocks[1]["index"] != float64(5) || blocks[2]["partialJson"] != `{"a":1` {
		t.Fatalf("terminal blocks=%v", blocks)
	}
}

// A failure deletes the scratch of every block (anthropic-messages.ts:817-826) but synthesizes no end events.
func TestAnthropicFailureDeletesScratchWithoutEndEvents(t *testing.T) {
	events := anthropicFixtureEvent("message_start", `{"message":{"id":"m","usage":{}}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":0,"content_block":{"type":"text","text":""}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":1,"content_block":{"type":"tool_use","id":"t","name":"read","input":{}}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\""}}`) +
		anthropicFixtureEvent("error", "overloaded")
	_, emitted := runAnthropicWire(t, AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key"}, Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}, StreamOptions{}, func(map[string]any) string { return events })
	failure, ok := emitted[len(emitted)-1].(ErrorEvent)
	if !ok {
		t.Fatalf("last event %T", emitted[len(emitted)-1])
	}
	result := failure.Error
	var types []AssistantEventType
	for _, event := range emitted {
		types = append(types, event.EventType())
	}
	wantTypes := []AssistantEventType{EventStart, EventTextStart, EventToolCallStart, EventToolCallDelta, EventError}
	if !reflect.DeepEqual(types, wantTypes) || result.StopReason != StopReasonError || result.ErrorMessage != "overloaded" {
		t.Fatalf("types=%v result=%#v", types, result)
	}
	for _, block := range openAnthropicWireBlocks(t, result) {
		if _, ok := block["index"]; ok {
			t.Fatalf("index survived the failure: %v", block)
		}
		if _, ok := block["partialJson"]; ok {
			t.Fatalf("partialJson survived the failure: %v", block)
		}
	}
}

// Pi mutates its output object where its consumers can read it, not only where it pushes. A consumer that holds the start event's partial while the body is pending must see message_start's metadata, which no event announces (anthropic-messages.ts:602-625).
func TestAnthropicPublishesMutationsWithoutAPush(t *testing.T) {
	cases, plans := loadAnthropicOracle(t)
	_ = cases
	plan := plans["text"]["pending-per-record"]
	seen := map[int]string{}
	runAnthropicTraceObserved(t, plan, func(epoch int, start *AssistantMessage) {
		if start != nil {
			seen[epoch] = start.Observe().ResponseID
		}
	})
	// Epoch 1 is the headers, epoch 2 is the message_start record: no push yet, but the live partial has the response ID and usage.
	if seen[1] != "" || seen[2] != "msg_2" {
		t.Fatalf("response IDs visible at each macrotask's end: %v", seen)
	}
}

func goroutineCount(t *testing.T, want int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		if count := runtime.NumGoroutine(); count <= want || time.Now().After(deadline) {
			return count
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The body reader goroutine is owned by the provider's turn: streams that complete, fail or are canceled leave nothing behind.
func TestAnthropicManagedStreamsLeaveNoGoroutines(t *testing.T) {
	serve := func(handler http.HandlerFunc) *httptest.Server {
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return server
	}
	complete := serve(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, anthropicMinimalFixture())
	})
	failing := serve(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, anthropicFixtureEvent("error", "overloaded"))
	})
	held := serve(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	provider := func(server *httptest.Server) Provider {
		return NewAnthropicProvider(AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key", BaseURL: server.URL})
	}
	run := func(server *httptest.Server, cancelAtStart bool) *AssistantMessage {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		stream, err := provider(server).Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for event := range stream.Events(ctx) {
			if _, ok := event.(StartEvent); ok && cancelAtStart {
				cancel()
			}
		}
		return stream.Result()
	}
	before := runtime.NumGoroutine()
	for range 25 {
		if result := run(complete, false); result.StopReason != StopReasonStop {
			t.Fatalf("complete: %#v", result)
		}
		if result := run(failing, false); result.StopReason != StopReasonError || result.ErrorMessage != "overloaded" {
			t.Fatalf("failing: %#v", result)
		}
		if result := run(held, true); result.StopReason != StopReasonAborted {
			t.Fatalf("canceled: %#v", result)
		}
	}
	http.DefaultClient.CloseIdleConnections()
	for _, server := range []*httptest.Server{complete, failing, held} {
		server.CloseClientConnections()
	}
	if after := goroutineCount(t, before+4); after > before+4 {
		buffer := make([]byte, 1<<16)
		t.Fatalf("goroutines: %d before, %d after\n%s", before, after, buffer[:runtime.Stack(buffer, true)])
	}
}

// A connection that dies mid-body ends the stream with the transport's error and the retained state, without hanging the executor.
func TestAnthropicManagedStreamSurvivesTransportFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buffer := make([]byte, 4096)
		_, _ = conn.Read(buffer)
		record := anthropicFixtureEvent("message_start", `{"message":{"id":"m","usage":{"input_tokens":5,"output_tokens":1}}}`)
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: %d\r\n\r\n%s", len(record)+1000, record)
	}()
	provider := NewAnthropicProvider(AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key", BaseURL: "http://" + listener.Addr().String()})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var types []AssistantEventType
	for event := range stream.Events(t.Context()) {
		types = append(types, event.EventType())
	}
	result := stream.Result()
	if !reflect.DeepEqual(types, []AssistantEventType{EventStart, EventError}) || result.StopReason != StopReasonError || result.Usage.Input != 5 || strings.TrimSpace(result.ErrorMessage) == "" {
		t.Fatalf("types=%v result=%#v", types, result)
	}
}
