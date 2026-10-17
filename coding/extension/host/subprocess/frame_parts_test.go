package subprocess

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A split frame (Args that are json.Marshal output) is byte for byte json.Marshal of its envelope, members after args included, and an oversized one fails as an unsplit one does (FD-1, FD-4).
func TestSplitFrameIsTheEnvelopesEncoding(t *testing.T) {
	timeout := 1.5
	args, err := json.Marshal(map[string]any{"text": "a<b>&c\u2028\u2029", "n": 1e21, "z": 0.1, "list": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]RequestPayload{
		"event":           {Method: "event", Event: "turn_end", HandlerID: 3, Args: args},
		"members after":   {Method: "tool_call", Tool: "t\"args\":0", ToolCallID: "c1", Args: args, SignalTimeoutMS: &timeout, OwnSignal: true, ExecuteID: "x", Stream: &EventStream{Seq: 2, Base: 1}},
		"empty args":      {Method: "event", Event: "e"},
		"string with key": {Method: "event", Event: `"args":0`, Args: args},
	} {
		t.Run(name, func(t *testing.T) {
			request := payload
			request.encodedArgs = true
			env := &Envelope{Type: MsgRequest, ID: "r1", Request: &request}
			want, err := json.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := encodeFrame(env)
			if err != nil {
				t.Fatal(err)
			}
			defer frame.release()
			if (frame.argsAt != 0) != (len(payload.Args) > 0) {
				t.Fatalf("split = %v for args %q", frame.argsAt != 0, payload.Args)
			}
			if got := frame.payload(); !bytes.Equal(got, want) {
				t.Fatalf("frame\n%s\nwant\n%s", got, want)
			}
			if frame.size() != len(want) {
				t.Fatalf("size %d, want %d", frame.size(), len(want))
			}
			if !bytes.Equal(env.Request.Args, payload.Args) {
				t.Fatal("encoding changed the envelope's Args")
			}
		})
	}
	big := RequestPayload{Method: "event", Event: "e", Args: json.RawMessage(`"` + strings.Repeat("x", MaxFrameSize) + `"`), encodedArgs: true}
	_, err = NewConn("big", nil).encodeEnvelope(&Envelope{Type: MsgRequest, ID: "r1", Request: &big})
	var tooLarge *FrameTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Size != MaxFrameSize+len(`{"type":"request","id":"r1","request":{"method":"event","event":"e","args":""}}`) {
		t.Fatalf("oversized split frame: %v", err)
	}
}

// fusedConn hands frames over as net.Pipe hands bytes: a write waits for the reader, a close ends both sides, a deadline expires a blocked call (FD-6).
func TestFusedConnBehavesAsAPipe(t *testing.T) {
	host, ext := newFusedConn()
	done := make(chan error, 1)
	go func() { done <- host.WriteFrameParts([]byte(`{"a":`), []byte(`[1]`), []byte(`}`)) }()
	select {
	case err := <-done:
		t.Fatalf("write returned before a read: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	shell, args, err := ext.ReadFrameParts()
	if err != nil || string(shell) != `{"a":0}` || string(args) != `[1]` {
		t.Fatalf("read %q %q %v", shell, args, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// A byte reader gets the socket's framing.
	go func() { done <- host.WriteFrameParts([]byte(`{"a":`), []byte(`[1]`), []byte(`}`)) }()
	raw, err := io.ReadAll(io.LimitReader(ext, int64(4+len(`{"a":[1]}`))))
	if err != nil || string(raw[4:]) != `{"a":[1]}` || raw[3] != byte(len(`{"a":[1]}`)) {
		t.Fatalf("byte read %q %v", raw, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := ext.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ext.ReadFrameParts(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read past deadline: %v", err)
	}
	if err := ext.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := host.SetWriteDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := host.WriteFrameParts([]byte(`{}`), nil, nil); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("write past deadline: %v", err)
	}
	if err := host.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	go func() { done <- host.WriteFrameParts([]byte(`{}`), nil, nil) }()
	if err := ext.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("blocked write after close: %v", err)
	}
	if _, _, err := ext.ReadFrameParts(); !errors.Is(err, io.EOF) {
		t.Fatalf("read after close: %v", err)
	}
}
