package codemode

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The JSON of the remote protocol is the JSON of upstream's types (protocol.ts, remote.ts), so a Go client and a Go
// remote agree with each other and with Pi's `RemoteCodemodeSandbox` and `serveCodemodeRemote` across a
// structured-clone or JSON boundary.

func TestWireMessagesHaveUpstreamsJSONShape(t *testing.T) {
	text, writes, errText := `"v"`, `[["k","1"]]`, `{"message":"boom"}`
	args := `{"a":1}`
	for _, tc := range []struct {
		name string
		msg  RemoteMessage
		json string
	}{
		{"call", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageCall, ID: 0, Target: "tool", Name: "add", Args: &args}},
			`{"type":"call","id":0,"target":"tool","name":"add","args":"{\"a\":1}"}`},
		{"call without args", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageCall, ID: 2, Target: "global", Name: "g"}},
			`{"type":"call","id":2,"target":"global","name":"g"}`},
		{"text output", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageOutput, Item: &OutputItem{Type: OutputItemText, Text: "hi", Console: true}}},
			`{"type":"output","item":{"type":"text","text":"hi","console":true}}`},
		// text: string is required upstream, so text('') and console.log() cross as "text":"".
		{"empty text output", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageOutput, Item: &OutputItem{Type: OutputItemText}}},
			`{"type":"output","item":{"type":"text","text":""}}`},
		{"image output", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageOutput, Item: &OutputItem{Type: OutputItemImage, Data: "AAAA", MimeType: "image/png"}}},
			`{"type":"output","item":{"type":"image","data":"AAAA","mimeType":"image/png"}}`},
		{"done ok", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageDone, Ok: true, Value: &text, Writes: &writes}},
			`{"type":"done","ok":true,"value":"\"v\"","writes":"[[\"k\",\"1\"]]"}`},
		{"done ok undefined value", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageDone, Ok: true, Writes: &writes}},
			`{"type":"done","ok":true,"writes":"[[\"k\",\"1\"]]"}`},
		{"done error", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageDone, Error: &errText}},
			`{"type":"done","ok":false,"error":"{\"message\":\"boom\"}"}`},
		{"crash", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageCrash, Message: "trap"}},
			`{"type":"crash","message":"trap"}`},
		{"failure", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageFailure}, Failure: &Error{Kind: ErrorTimeout, Message: "late"}},
			`{"type":"failure","error":{"kind":"timeout","message":"late"}}`},
		{"failure with a script error", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageFailure}, Failure: &Error{Kind: ErrorScript, Name: "TypeError", Message: "m", Stack: "s"}},
			`{"type":"failure","error":{"kind":"script","name":"TypeError","message":"m","stack":"s"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.msg)
			if err != nil || string(got) != tc.json {
				t.Fatalf("marshal = %s, %v\nwant      %s", got, err, tc.json)
			}
			var back RemoteMessage
			if err := json.Unmarshal([]byte(tc.json), &back); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(back, tc.msg) {
				t.Fatalf("round trip = %+v, want %+v", back, tc.msg)
			}
		})
	}
}

func TestWireRepliesAndRequestsHaveUpstreamsJSONShape(t *testing.T) {
	payload := `[1]`
	for _, tc := range []struct {
		name  string
		reply RemoteReply
		json  string
	}{
		{"result", RemoteReply{HostToWorkerMessage{Type: MessageResult, ID: 0, Ok: true, Payload: &payload}}, `{"type":"result","id":0,"ok":true,"payload":"[1]"}`},
		{"error result", RemoteReply{HostToWorkerMessage{Type: MessageResult, ID: 4, Ok: false, Payload: &payload}}, `{"type":"result","id":4,"ok":false,"payload":"[1]"}`},
		{"undefined result", RemoteReply{HostToWorkerMessage{Type: MessageResult, ID: 1, Ok: true}}, `{"type":"result","id":1,"ok":true}`},
		{"stop", RemoteReply{HostToWorkerMessage{Type: MessageStop}}, `{"type":"stop"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.reply)
			if err != nil || string(got) != tc.json {
				t.Fatalf("marshal = %s, %v\nwant      %s", got, err, tc.json)
			}
			var back RemoteReply
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			want := tc.reply
			if want.Type == MessageStop {
				want.ID, want.Ok = 0, false
			}
			if !reflect.DeepEqual(back, want) {
				t.Fatalf("round trip = %+v, want %+v", back, want)
			}
		})
	}

	request := RemoteRequest{Data: VmStartData{Code: "c", Tools: []VmStartTool{{Name: "n", JSName: "j", Description: ""}}, Globals: []VmStartGlobal{{Name: "g"}},
		MemoryLimitBytes: 8, Store: map[string]string{"k": "1"}}, InterruptBudget: 5}
	got, _ := json.Marshal(request)
	const want = `{"data":{"code":"c","tools":[{"name":"n","jsName":"j","description":""}],"globals":[{"name":"g","spread":false}],"memoryLimitBytes":8,"store":{"k":"1"}},"interruptBudget":5}`
	if string(got) != want {
		t.Fatalf("request = %s\nwant      %s", got, want)
	}
}

// protocol.ts: the VM's messages survive the conversion to the wire and back to what the host decodes.
func TestVMMessagesRoundTripThroughTheWire(t *testing.T) {
	for _, message := range []vmMessage{
		{kind: vmMessageCall, id: 3, isTool: true, name: "t", args: "[1]", argsDefined: true},
		{kind: vmMessageCall, id: 4, isTool: false, name: "g"},
		{kind: vmMessageOutput, item: OutputItem{Type: OutputItemImage, Data: "QQ==", MimeType: "image/png"}},
		{kind: vmMessageDone, ok: true, value: "1", valueDefined: true, writes: "[]", writesDefined: true},
		{kind: vmMessageDone, ok: true, writes: "[]", writesDefined: true},
		{kind: vmMessageDone, ok: true, value: "1", valueDefined: true},
		{kind: vmMessageDone, errorText: `{"message":"m"}`},
		{kind: vmMessageCrash, message: "trap"},
		{},
	} {
		wire, err := json.Marshal(RemoteMessage{WorkerToHostMessage: messageToWire(message)})
		if err != nil {
			t.Fatal(err)
		}
		var back RemoteMessage
		if err := json.Unmarshal(wire, &back); err != nil {
			t.Fatal(err)
		}
		if got := messageFromWire(back.WorkerToHostMessage); got != message {
			t.Errorf("%+v became %s and then %+v", message, wire, got)
		}
	}
	if got := replyFromWire(replyToWire(callResult{id: 5, ok: true, payload: "x", defined: true}).HostToWorkerMessage); got != (callResult{id: 5, ok: true, payload: "x", defined: true}) {
		t.Errorf("reply = %+v", got)
	}
}
