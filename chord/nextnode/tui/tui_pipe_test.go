package tui

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

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
		if wholeErr == nil && chunkedErr == nil && !reflect.DeepEqual(whole, chunked) && !(len(whole) == 0 && len(chunked) == 0) {
			t.Fatalf("whole %+v != chunked %+v", whole, chunked)
		}
	})
}
