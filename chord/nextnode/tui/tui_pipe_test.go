package tui

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// pi: packages/chord/src/node/tui/pipe.ts
// Tests of the launcher's end of the sandbox's pipes (slice 11 §4; fix round 2): length-prefixed frames cut from the sandbox's
// output however it chunks them, each message checked, and anything malformed or oversized refused, so the sandbox ends and the
// launcher goes on (Opus E3). Ports packages/chord/test/node/tui/pipe.test.ts.

// sandboxFrame is a frame from the sandbox: header as JSON, then body.
func sandboxFrame(t testing.TB, header any, body []byte) []byte {
	t.Helper()
	text, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, 8)
	binary.BigEndian.PutUint32(prefix[0:], uint32(4+len(text)+len(body)))
	binary.BigEndian.PutUint32(prefix[4:], uint32(len(text)))
	return bytes.Join([][]byte{prefix, text, body}, nil)
}

func TestReadingTheSandboxsFramesCutsMessagesFromChunksOfAnySizeAByteAtATimeIncluded(t *testing.T) {
	data := bytes.Join([][]byte{
		sandboxFrame(t, map[string]any{"kind": "write"}, []byte("héllo")),
		sandboxFrame(t, map[string]any{"kind": "socket_send", "socket": 1}, []byte{1, 2, 3}),
		sandboxFrame(t, map[string]any{"kind": "request", "id": 7, "request": map[string]any{"kind": "copy", "text": "x"}}, nil),
	}, nil)
	whole, err := NewFrameReader().Push(data)
	if err != nil {
		t.Fatal(err)
	}
	reader := NewFrameReader()
	var bytewise []SandboxMessage
	for _, b := range data {
		messages, err := reader.Push([]byte{b})
		if err != nil {
			t.Fatal(err)
		}
		bytewise = append(bytewise, messages...)
	}
	want := []SandboxMessage{
		{Kind: "write", Data: "héllo"},
		{Kind: "socket_send", Socket: 1, Bytes: []byte{1, 2, 3}},
		{Kind: "request", ID: 7, Request: BridgeRequest{Kind: "copy", Text: "x"}},
	}
	for name, got := range map[string][]SandboxMessage{"whole": whole, "bytewise": bytewise} {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: messages = %+v, want %+v", name, got, want)
		}
	}
}

func TestReadingTheSandboxsFramesRefusesAFrameOver17MiBBeforeReadingItAndMessagesOfNoKnownShape(t *testing.T) {
	huge := make([]byte, 8)
	binary.BigEndian.PutUint32(huge, MaxPipeFrame+1)
	if _, err := NewFrameReader().Push(huge); !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("error = %v, want a malformed frame", err)
	}
	for _, header := range []any{
		nil,
		map[string]any{"kind": "nope"},
		map[string]any{"kind": "socket_send", "socket": "1"},
		map[string]any{"kind": "request", "id": 1, "request": map[string]any{"kind": "copy", "text": 5}},
		map[string]any{"kind": "request", "id": 1, "request": map[string]any{"kind": "run", "command": "sh"}},
	} {
		if _, err := NewFrameReader().Push(sandboxFrame(t, header, nil)); !errors.Is(err, ErrMalformedMessage) {
			t.Fatalf("header %v: error = %v, want a malformed message", header, err)
		}
	}
	if _, err := NewFrameReader().Push([]byte("ESCAPE-MARK")); !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("error = %v, want a malformed frame", err)
	}
}

func TestWritingTheLaunchersMessagesWithTheirTextOrBytesAsTheBody(t *testing.T) {
	input, err := LauncherFrame(LauncherMessage{Kind: "input", Data: "y"})
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.BigEndian.Uint32(input); int(got) != len(input)-4 {
		t.Fatalf("length prefix = %d, want %d", got, len(input)-4)
	}
	headerLength := int(binary.BigEndian.Uint32(input[4:]))
	var header map[string]any
	if err := json.Unmarshal(input[8:8+headerLength], &header); err != nil || !reflect.DeepEqual(header, map[string]any{"kind": "input"}) {
		t.Fatalf("header = %v (%v), want {kind: input}", header, err)
	}
	if got := string(input[8+headerLength:]); got != "y" {
		t.Fatalf("body = %q, want y", got)
	}
}

// FuzzFrameReader: whatever bytes arrive in whatever chunks, the reader returns messages or an error and never panics, and
// the messages are the same however the bytes are chunked.
func FuzzFrameReader(f *testing.F) {
	f.Add([]byte{0, 0, 0, 5, 0, 0, 0, 1, '{'}, 3)
	f.Add(sandboxFrame(f, map[string]any{"kind": "write"}, []byte("x")), 1)
	f.Fuzz(func(t *testing.T, data []byte, size int) {
		whole, wholeErr := NewFrameReader().Push(data)
		if size < 1 {
			size = 1
		}
		reader := NewFrameReader()
		var chunked []SandboxMessage
		var chunkedErr error
		for start := 0; start < len(data) && chunkedErr == nil; start += size {
			var messages []SandboxMessage
			messages, chunkedErr = reader.Push(data[start:min(start+size, len(data))])
			chunked = append(chunked, messages...)
		}
		if (wholeErr == nil) != (chunkedErr == nil) {
			t.Fatalf("whole error %v, chunked error %v", wholeErr, chunkedErr)
		}
		if wholeErr == nil && chunkedErr == nil && !reflect.DeepEqual(whole, chunked) && (len(whole) != 0 || len(chunked) != 0) {
			t.Fatalf("whole %+v != chunked %+v", whole, chunked)
		}
	})
}

// The header's fields are checked one by one, as Pi's `Number.isSafeInteger`, `typeof ... === "string"` and `String(value)` do:
// what is no JSON number is no integer, a number that is not an integer or is beyond 2^53 - 1 is refused for a socket or a request
// id, and an exit code that is none is 1. A header that is no JSON object is malformed.
func TestReadingTheSandboxsHeaderChecksEachFieldAsPiDoes(t *testing.T) {
	accepted := func(header string) (SandboxMessage, error) {
		messages, err := NewFrameReader().Push(sandboxFrame(t, json.RawMessage(header), []byte("body")))
		if err != nil {
			return SandboxMessage{}, err
		}
		if len(messages) != 1 {
			t.Fatalf("header %s: %d messages", header, len(messages))
		}
		return messages[0], nil
	}
	for _, header := range []string{
		`[]`, `"write"`, `7`, `null`, `{"kind":7}`, `{"kind":null}`, `{}`,
		`{"kind":"socket_open","socket":1.5}`, `{"kind":"socket_open","socket":"1"}`, `{"kind":"socket_open","socket":null}`,
		`{"kind":"socket_open","socket":true}`, `{"kind":"socket_open","socket":[1]}`, `{"kind":"socket_open"}`,
		`{"kind":"socket_close","socket":9007199254740992}`, `{"kind":"socket_close","socket":1e999}`,
		`{"kind":"socket_send","socket":-1.0e400}`,
		`{"kind":"request","id":1.5,"request":{"kind":"base"}}`, `{"kind":"request","id":1}`, `{"kind":"request","id":1,"request":null}`,
		`{"kind":"request","id":1,"request":[]}`, `{"kind":"request","id":1,"request":"base"}`, `{"kind":"request","id":1,"request":{"kind":7}}`,
		`{"kind":"request","id":1,"request":{"kind":"fetch","path":null}}`, `{"kind":"request","id":1,"request":{"kind":"open","url":["x"]}}`,
		`{"kind":"request","id":"1","request":{"kind":"base"}}`,
	} {
		if _, err := accepted(header); !errors.Is(err, ErrMalformedMessage) {
			t.Fatalf("header %s: error = %v, want a malformed message", header, err)
		}
	}
	for header, want := range map[string]SandboxMessage{
		`{"kind":"socket_open","socket":9007199254740991}`:                     {Kind: "socket_open", Socket: 9007199254740991},
		`{"kind":"socket_close","socket":-3}`:                                  {Kind: "socket_close", Socket: -3},
		`{"kind":"socket_open","socket":2.0}`:                                  {Kind: "socket_open", Socket: 2},
		`{"kind":"socket_open","socket":2e0,"extra":1e999}`:                    {Kind: "socket_open", Socket: 2},
		`{"kind":"socket_send","socket":4}`:                                    {Kind: "socket_send", Socket: 4, Bytes: []byte("body")},
		`{"kind":"request","id":6,"request":{"kind":"base"}}`:                  {Kind: "request", ID: 6, Request: BridgeRequest{Kind: "base"}},
		`{"kind":"request","id":6,"request":{"kind":"fetch","path":"/p"}}`:     {Kind: "request", ID: 6, Request: BridgeRequest{Kind: "fetch", Path: "/p"}},
		`{"kind":"request","id":6,"request":{"kind":"attach","path":"/a"}}`:    {Kind: "request", ID: 6, Request: BridgeRequest{Kind: "attach", Path: "/a"}},
		`{"kind":"request","id":6,"request":{"kind":"copy","text":"t\u00e9"}}`: {Kind: "request", ID: 6, Request: BridgeRequest{Kind: "copy", Text: "té"}},
		`{"kind":"request","id":6,"request":{"kind":"open","url":"u"}}`:        {Kind: "request", ID: 6, Request: BridgeRequest{Kind: "open", URL: "u"}},
		`{"kind":"outdated"}`:                                                  {Kind: "outdated"},
		`{"kind":"exit","code":3}`:                                             {Kind: "exit", Code: 3},
		`{"kind":"exit","code":3.5}`:                                           {Kind: "exit", Code: 1},
		`{"kind":"exit","code":"3"}`:                                           {Kind: "exit", Code: 1},
		`{"kind":"exit"}`:                                                      {Kind: "exit", Code: 1},
		`{"kind":"failed","message":"boom"}`:                                   {Kind: "failed", Message: "boom"},
		`{"kind":"failed"}`:                                                    {Kind: "failed", Message: "undefined"},
		`{"kind":"failed","message":null}`:                                     {Kind: "failed", Message: "undefined"},
		`{"kind":"failed","message":5}`:                                        {Kind: "failed", Message: "5"},
		`{"kind":"failed","message":true}`:                                     {Kind: "failed", Message: "true"},
		`{"kind":"write"}`:                                                     {Kind: "write", Data: "body"},
	} {
		got, err := accepted(header)
		if err != nil {
			t.Fatalf("header %s: %v", header, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("header %s: message = %+v, want %+v", header, got, want)
		}
	}
}

// Pi's `FrameReader.push` throws when a frame is malformed and returns nothing: the valid frames before it in the same chunk are not
// handed out (the sandbox fails and its caller gets none of that chunk's messages).
func TestReadingTheSandboxsFramesHandsOutNoMessageOfAChunkWithAMalformedFrame(t *testing.T) {
	valid := sandboxFrame(t, map[string]any{"kind": "write"}, []byte("one"))
	malformed := []byte{0, 0, 0, 0}
	badHeader := sandboxFrame(t, map[string]any{"kind": "nope"}, nil)
	for name, tail := range map[string][]byte{"a frame length below 4": malformed, "a message of no known shape": badHeader} {
		chunk := append(append(append([]byte(nil), valid...), valid...), tail...)
		messages, err := NewFrameReader().Push(chunk)
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		if len(messages) != 0 {
			t.Fatalf("%s: %d messages handed out with the error, want none (Pi's push throws before it returns any)", name, len(messages))
		}
	}
}
