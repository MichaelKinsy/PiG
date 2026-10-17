package codemode

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func serveTestMessages() []RemoteMessage {
	wire := func(m WorkerToHostMessage) RemoteMessage { return RemoteMessage{WorkerToHostMessage: m} }
	text := strings.Repeat("é<>&\"\\\n\t\u2028", 50) + "\x00\x1f"
	return []RemoteMessage{
		wire(WorkerToHostMessage{Type: MessageCall, ID: 7, Target: "tool", Name: "add", Args: new(`{"a":1}`)}),
		wire(WorkerToHostMessage{Type: MessageCall, ID: 8, Target: "global", Name: "x.y"}),
		wire(WorkerToHostMessage{Type: MessageCall, ID: 9, Target: "tool", Name: "n", Args: new("")}),
		wire(WorkerToHostMessage{Type: MessageOutput, Item: &OutputItem{Type: OutputItemText, Text: text}}),
		wire(WorkerToHostMessage{Type: MessageOutput, Item: &OutputItem{Type: OutputItemText, Text: "", Console: true}}),
		wire(WorkerToHostMessage{Type: MessageOutput, Item: &OutputItem{Type: OutputItemImage, Data: "iVBOR", MimeType: "image/png"}}),
		wire(WorkerToHostMessage{Type: MessageOutput, Item: &OutputItem{Type: OutputItemImage}}),
		wire(WorkerToHostMessage{Type: MessageOutput}),
		wire(WorkerToHostMessage{Type: MessageDone, Ok: true, Value: new(`"x"`), Writes: new("[]")}),
		wire(WorkerToHostMessage{Type: MessageDone, Ok: true, Writes: new("[]")}),
		wire(WorkerToHostMessage{Type: MessageDone, Ok: false, Error: new(`{"name":"Error","message":"boom"}`)}),
		wire(WorkerToHostMessage{Type: MessageCrash, Message: text}),
		{WorkerToHostMessage: WorkerToHostMessage{Type: MessageFailure}, Failure: &Error{Kind: ErrorTimeout, Message: "Execution exceeded its interrupt budget of 1000"}},
		{WorkerToHostMessage: WorkerToHostMessage{Type: MessageFailure}, Failure: &Error{Kind: ErrorScript, Name: "RangeError", Message: "m", Stack: "s\n  at x"}},
	}
}

// The hand-written encoder says what encoding/json says about the same messages (RemoteMessage.MarshalJSON): the same value to
// any JSON reader.
func TestAppendExchangeMatchesEncodingJSON(t *testing.T) {
	messages := serveTestMessages()
	for _, subset := range [][]RemoteMessage{nil, messages[:1], messages} {
		got := appendExchange(nil, 3, subset)
		want, err := json.Marshal(struct {
			ID       int             `json:"id"`
			Take     int             `json:"take"`
			Messages []RemoteMessage `json:"messages"`
		}{3, 0, subset})
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		if err := json.Unmarshal(got, &a); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, got)
		}
		if err := json.Unmarshal(want, &b); err != nil {
			t.Fatal(err)
		}
		if subset == nil {
			// encoding/json writes a nil slice as null; the module always sends a list.
			b.(map[string]any)["messages"] = []any{}
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%d messages:\nhand: %s\njson: %s", len(subset), got, want)
		}
	}
}

// A JavaScript string keeps a lone surrogate (WTF-8 in a Go string) through the exchange, where encoding/json writes U+FFFD.
func TestAppendExchangeKeepsALoneSurrogate(t *testing.T) {
	lone := "a\xed\xa0\x80b"
	got := string(appendExchange(nil, 1, []RemoteMessage{{WorkerToHostMessage: WorkerToHostMessage{Type: MessageOutput, Item: &OutputItem{Type: OutputItemText, Text: lone}}}}))
	if !strings.Contains(got, `"a\ud800b"`) {
		t.Fatalf("lone surrogate: %s", got)
	}
}

// The output items the host holds stand in a batch for a placeholder, which the encoder turns into the number of items the host puts in
// front of the messages (the Worker host, serve.ts, takes that many from what the module handed it).
func TestAppendExchangeCountsTheHostedOutput(t *testing.T) {
	call := RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageCall, ID: 7, Target: "tool", Name: "echo"}}
	hosted := RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: messageHostedOutput, ID: 12}}
	got := string(appendExchange(nil, 3, []RemoteMessage{hosted, call}))
	want := `{"id":3,"take":12,"messages":[{"type":"call","id":7,"target":"tool","name":"echo"}]}`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// With a host that keeps the output (keepOutput), the server counts the items it handed over: the exchange at the flush threshold, at a
// call and at the end names how many held items it carries, and the server keeps none itself.
func TestARemoteServerThatHandsItsOutputToTheHostCountsIt(t *testing.T) {
	var kept, flushed []int
	var sent [][]RemoteMessage
	s := &remoteServer{loop: newServeLoop(), keepOutput: func(WorkerToHostMessage) { kept = append(kept, 1) }}
	s.exchange = func(_ context.Context, messages []RemoteMessage, _ func(*RemoteReply, error)) {
		sent = append(sent, messages)
		flushed = append(flushed, len(kept))
	}
	output := vmMessage{kind: vmMessageOutput, item: OutputItem{Type: OutputItemText, Text: "x"}}
	for range flushItems + 5 {
		s.onMessage(output)
	}
	s.onMessage(vmMessage{kind: vmMessageCall, id: 1, isTool: true, name: "t"})
	if len(s.buffered) != 0 || len(kept) != flushItems+5 {
		t.Fatalf("the server buffered %d items and handed over %d", len(s.buffered), len(kept))
	}
	if len(sent) != 2 || len(sent[0]) != 1 || sent[0][0].Type != messageHostedOutput || sent[0][0].ID != flushItems || flushed[0] != flushItems {
		t.Fatalf("the flush at %d items: %+v", flushItems, sent)
	}
	if len(sent[1]) != 2 || sent[1][0].Type != messageHostedOutput || sent[1][0].ID != 5 || sent[1][1].Type != MessageCall {
		t.Fatalf("the call: %+v", sent[1])
	}
}
