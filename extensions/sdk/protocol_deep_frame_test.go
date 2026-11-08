package sdk

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func deepFrame(kind, id, member string, depth int) []byte {
	deep := strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth)
	frame := `{"type":"` + kind + `","id":"` + id + `","` + member + `":{"args":` + deep + `,"s":"]}"}}`
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(frame))), frame...)
}

func readEnvelope(t *testing.T, r io.Reader) envelope {
	t.Helper()
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("%q: %v", body, err)
	}
	return env
}

// sendFrame writes frame to the host's end of the pipe on a goroutine of its own and returns a channel that closes when the write has returned. A net.Pipe is synchronous: Write
// returns only when the other end has read every byte of the frame, so a closed channel means the reader has consumed it, and the frames written one after another stay in order.
func sendFrame(host net.Conn, frame []byte) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = host.Write(frame)
	}()
	return done
}

// A frame nested past what the reader takes (the host sends none: its own marshal refuses a value past 10000 levels) does not end the connection: the request it
// carries is answered with Pi's RangeError, a notification is dropped, and the next frame is read. A frame the reader cannot decode for another reason ends it.
//
// Each frame is written after the one before it was read: the frames were once written from goroutines that nothing ordered, and the empty frame at the end (which ends the connection)
// could reach the reader before the ping, which then waited on the pipe for a reader that was gone.
func TestReadLoopAnswersARequestNestedPastTheReaderAndGoesOn(t *testing.T) {
	host, ext := net.Pipe()
	t.Cleanup(func() { _ = host.Close() })
	c := newConn(ext)
	c.start()
	t.Cleanup(func() { _ = ext.Close() })
	go func() {
		for range c.incoming {
		}
	}()
	for _, frame := range [][]byte{
		deepFrame("notify", "", "notify", 20000),
		deepFrame("request", "r-1", "request", 20000),
	} {
		written := sendFrame(host, frame)
		if strings.Contains(string(frame), `"type":"request"`) {
			if state := readEnvelope(t, host); state.Type != msgRequestState || state.RequestState.RequestID != "r-1" || state.RequestState.State != "completed" {
				t.Fatalf("first answer = %+v, want the request completed", state)
			}
			response := readEnvelope(t, host)
			if response.Type != msgResponse || response.ID != "r-1" || response.Response == nil || response.Response.Error == nil || response.Response.Error.Message != "Maximum call stack size exceeded" {
				t.Fatalf("answer = %+v, want Pi's RangeError", response)
			}
		}
		<-written
	}
	ping := `{"type":"ping","ping":{"nonce":"n"}}`
	written := sendFrame(host, append(binary.BigEndian.AppendUint32(nil, uint32(len(ping))), ping...))
	if pong := readEnvelope(t, host); pong.Type != msgPong || pong.Pong == nil || pong.Pong.Nonce != "n" {
		t.Fatalf("after the deep frames the connection answered %+v, want the pong", pong)
	}
	<-written
	// An empty frame is no JSON: it ends the connection, as any frame the reader cannot decode does.
	<-sendFrame(host, []byte("\x00\x00\x00\x00"))
	select {
	case <-c.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the connection is still open after an empty frame")
	}
}

func TestFrameHeadReadsTypeAndIDWithoutDecodingWhatNests(t *testing.T) {
	deep := strings.Repeat("[", 30000) + strings.Repeat("]", 30000)
	for _, tc := range []struct{ frame, kind, id string }{
		{`{"type":"request","id":"c-7","request":{"args":` + deep + `,"s":"]}"}}`, "request", "c-7"},
		{`{"id":"a\"b","pad":[{"type":"x"}],"type":"call_result","v":` + deep + `}`, "call_result", `a"b`},
		{`[1]`, "", ""},
		{`{"type":"request"`, "request", ""},
	} {
		if kind, id := frameHead([]byte(tc.frame)); kind != tc.kind || id != tc.id {
			t.Errorf("frameHead(%.60s…) = %q, %q, want %q, %q", tc.frame, kind, id, tc.kind, tc.id)
		}
	}
}
