package subprocess

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// responsePeer is the extension side of a connection on a pipe: it records the request frames the host writes and writes the frames a test scripts.
type responsePeer struct {
	conn     net.Conn
	requests chan []byte
	writeMu  sync.Mutex
}

// newResponsePeer returns a started connection and its peer. A connection with an inbound observer, as every connection of a Host has (Host.observeXref), reads a response by the full decode; one without it first tries the fast route (routeResponseFrame).
func newResponsePeer(t *testing.T, observed bool) (*Conn, *responsePeer) {
	t.Helper()
	conn, extSide := newPipeConn()
	if observed {
		conn.inbound = func(*Envelope) {}
	}
	return conn, startResponsePeer(t, conn, extSide)
}

// newPipeConn returns a connection that is not started and the extension's end of its pipe.
func newPipeConn() (*Conn, net.Conn) {
	hostSide, extSide := net.Pipe()
	return newConnWithOptions("peer", hostSide, connOptions{HeartbeatInterval: time.Hour, HeartbeatTimeout: time.Hour}), extSide
}

// startResponsePeer starts conn and returns its peer on extSide; the test closes both when it ends.
func startResponsePeer(t *testing.T, conn *Conn, extSide net.Conn) *responsePeer {
	t.Helper()
	conn.Start(t.Context())
	peer := &responsePeer{conn: extSide, requests: make(chan []byte, 16)}
	go peer.read()
	t.Cleanup(func() {
		_ = conn.Close("test done")
		_ = extSide.Close()
	})
	return peer
}

func (p *responsePeer) read() {
	var length [4]byte
	for {
		if _, err := io.ReadFull(p.conn, length[:]); err != nil {
			return
		}
		frame := make([]byte, binary.BigEndian.Uint32(length[:]))
		if _, err := io.ReadFull(p.conn, frame); err != nil {
			return
		}
		if bytes.Contains(frame, []byte(`"type":"request"`)) {
			select {
			case p.requests <- frame:
			default:
			}
		}
	}
}

// write sends the frames of stream, each already length-prefixed, in one write; it returns once the host's read loop took them.
func (p *responsePeer) write(stream []byte) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, _ = p.conn.Write(stream)
}

// writeFrame sends one frame.
func (p *responsePeer) writeFrame(frame string) {
	buf := binary.BigEndian.AppendUint32(nil, uint32(len(frame)))
	p.write(append(buf, frame...))
}

func eventRequest(id string) *Envelope {
	return &Envelope{ID: id, Type: MsgRequest, Request: &RequestPayload{Method: "event", Event: "message_update", Args: []byte(`{}`)}}
}

// answerCase is a way for a response to reach its request: the shape of its frame and whether the connection reads it by the full decode.
type answerCase struct {
	name     string
	observed bool
	frame    func(id, result string) string
}

func sdkFrame(id, result string) string {
	return `{"type":"response","id":"` + id + `","response":{"result":` + result + `}}`
}

// pythonFrame is the response frame of the Python SDK (Extension._respond): it always writes the error member, so the fast route declines it.
func pythonFrame(id, result string) string {
	return `{"type":"response","id":"` + id + `","response":{"result":` + result + `,"error":null}}`
}

var answerCases = []answerCase{
	{"SDK frame, fast route", false, sdkFrame},
	{"SDK frame, full decode", true, sdkFrame},
	{"Python frame, fast route declines", false, pythonFrame},
	{"Python frame, full decode", true, pythonFrame},
}

// A request returns its response channel for reuse empty. The failure paths and a second answer for the same request (a failure racing the response) can still send to the channel while the request is registered, between its receive and its return; the request must drop that answer with the channel, or the next request that takes the channel from the pool receives an answer to a request that ended. The second answer is sent at the one point the request's own goroutine pauses after its receive, the decides callback of its suspension.
func TestResponseChannelIsEmptyWhenItsRequestReturnsIt(t *testing.T) {
	for _, tc := range answerCases {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := newResponsePeer(t, tc.observed)
			answered := make(chan struct{}, 4)
			var ch chan *Envelope
			ctx := withRequestAnswered(context.Background(), func() { answered <- struct{}{} })
			ctx = withRequestSuspension(ctx, func(bool) {}, func() bool {
				conn.pendingMu.Lock()
				ch = conn.pending["first"]
				conn.pendingMu.Unlock()
				// The first answer was received; the request is still registered. The second answer lands in its channel.
				peer.writeFrame(tc.frame("first", `"second answer"`))
				<-answered
				<-answered
				return false
			})
			type outcome struct {
				resp *Envelope
				err  error
			}
			done := make(chan outcome, 1)
			go func() {
				resp, err := conn.Request(ctx, eventRequest("first"))
				done <- outcome{resp, err}
			}()
			<-peer.requests
			peer.writeFrame(tc.frame("first", `"first answer"`))
			got := <-done
			if got.err != nil || got.resp == nil || got.resp.Response == nil || string(got.resp.Response.Result) != `"first answer"` {
				t.Fatalf("the request received %+v, %v", got.resp, got.err)
			}
			releaseResponse(got.resp)
			if ch == nil {
				t.Fatal("the request's channel was never captured: the decides callback did not run")
			}
			if len(ch) != 0 {
				t.Fatalf("the response channel the request returned for reuse holds %d answer(s) to a request that ended", len(ch))
			}

			// The next request is answered by its own response, not by what the first one left.
			next := make(chan outcome, 1)
			go func() {
				resp, err := conn.Request(context.Background(), eventRequest("next"))
				next <- outcome{resp, err}
			}()
			<-peer.requests
			peer.writeFrame(tc.frame("next", `"next answer"`))
			got = <-next
			if got.err != nil || got.resp == nil || got.resp.ID != "next" || got.resp.Response == nil || string(got.resp.Response.Result) != `"next answer"` {
				t.Fatalf("the next request received %+v, %v", got.resp, got.err)
			}
			releaseResponse(got.resp)
		})
	}
}

// A response read for a request that ended before it was delivered is not sent to the request's channel. The read loop looked the request up, runs the request's arrival callback, and sends only afterwards; the request is cancelled in between and returns, and the send must find it unregistered. withRequestArrived holds the read loop at that point.
func TestResponseReadForAnEndedRequestIsNotSent(t *testing.T) {
	for _, tc := range answerCases {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := newResponsePeer(t, tc.observed)
			read, hold, answered := make(chan struct{}), make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = withRequestArrived(ctx, func() {
				close(read)
				<-hold
			})
			ctx = withRequestAnswered(ctx, func() { close(answered) })
			done := make(chan error, 1)
			go func() {
				_, err := conn.Request(ctx, eventRequest("ended"))
				done <- err
			}()
			<-peer.requests
			conn.pendingMu.Lock()
			ch := conn.pending["ended"]
			conn.pendingMu.Unlock()
			peer.writeFrame(tc.frame("ended", `"too late"`))
			<-read
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("the cancelled request returned %v", err)
			}
			close(hold)
			<-answered
			if len(ch) != 0 {
				t.Fatalf("a response read for a request that had already returned was sent to its channel (%d queued)", len(ch))
			}
		})
	}
}

// A send for a request that is not registered with the channel is dropped, also when the channel is registered for another request: the channel an ended request returned is taken from the pool by the next request, and a late sender that still holds it must not hand that request a response to another id.
func TestLateResponseNeverReachesTheRequestHoldingTheChannel(t *testing.T) {
	conn := NewConn("late", nil)
	ch := make(chan *Envelope, 1)
	conn.pending["next"] = ch
	late := &Envelope{Type: MsgResponse, ID: "ended", Response: &ResponsePayload{}}
	if conn.sendResponse("ended", ch, late) {
		t.Fatal("a response for an unregistered request was sent")
	}
	if len(ch) != 0 {
		t.Fatalf("the channel of request next holds %d late response(s) of request ended", len(ch))
	}
	own := &Envelope{Type: MsgResponse, ID: "next", Response: &ResponsePayload{}}
	if !conn.sendResponse("next", ch, own) || <-ch != own {
		t.Fatal("the response of the registered request was not delivered")
	}
}

// The Python SDK's own response frames reach their requests through the full decode: Extension._respond writes {"type":"response","id":...,"response":{"result":...,"error":null}}, which parseResponseFrame declines, and the connection delivers what decodeEnvelope reads. The frames are the bytes the real SDK writes, with the request_state frame it writes before each response.
func TestPythonSDKResponseFramesAreRoutedByTheFullDecode(t *testing.T) {
	sdk := filepath.Join(findModuleRoot(t), "extensions", "sdk-py")
	const script = `
import sys
sys.path.insert(0, sys.argv[1])
import pig_sdk

class Sock:
    def __init__(self):
        self.chunks = []

    def sendall(self, data):
        self.chunks.append(bytes(data))

ext = pig_sdk.Extension("frame-probe")
ext._sock = Sock()
ext._respond("t1", {"a": [1, 2], "s": "\u00e9"}, None)
ext._respond("t2", None, None)
ext._respond("t3", None, {"code": "tool_failed", "message": "it failed"})
sys.stdout.buffer.write(b"".join(ext._sock.chunks))
`
	stream, err := exec.Command("python3", "-c", script, sdk).Output()
	if err != nil {
		t.Fatalf("python3 could not write the SDK's frames: %v", err)
	}
	var responses [][]byte
	for rest := stream; len(rest) > 0; {
		n := int(binary.BigEndian.Uint32(rest[:4]))
		frame := rest[4 : 4+n]
		rest = rest[4+n:]
		if bytes.Contains(frame, []byte(`"type":"response"`)) {
			responses = append(responses, frame)
		}
	}
	if len(responses) != 3 {
		t.Fatalf("the SDK wrote %d response frames, want 3", len(responses))
	}
	for _, frame := range responses {
		if _, _, ok := parseResponseFrame(frame); ok {
			t.Errorf("the fast route took the Python SDK's frame %s", frame)
		}
		var env Envelope
		if err := decodeEnvelope(frame, &env); err != nil || env.Type != MsgResponse || env.Response == nil {
			t.Errorf("the full decode reads %s as %+v, %v", frame, env, err)
		}
	}

	conn, peer := newResponsePeer(t, false)
	type outcome struct {
		resp *Envelope
		err  error
	}
	results := map[string]chan outcome{"t1": make(chan outcome, 1), "t2": make(chan outcome, 1), "t3": make(chan outcome, 1)}
	for id, result := range results {
		go func() {
			resp, err := conn.Request(context.Background(), eventRequest(id))
			result <- outcome{resp, err}
		}()
	}
	for range results {
		<-peer.requests
	}
	peer.write(stream)
	t1, t2, t3 := <-results["t1"], <-results["t2"], <-results["t3"]
	if t1.err != nil || t1.resp.ID != "t1" || t1.resp.Response == nil || string(t1.resp.Response.Result) != `{"a":[1,2],"s":"\u00e9"}` || t1.resp.Response.Error != nil {
		t.Errorf("t1 received %+v (%+v), %v", t1.resp, t1.resp.Response, t1.err)
	}
	if t2.err != nil || t2.resp.ID != "t2" || t2.resp.Response == nil || t2.resp.Response.Error != nil {
		t.Errorf("t2 received %+v (%+v), %v", t2.resp, t2.resp.Response, t2.err)
	}
	if t3.err != nil || t3.resp.ID != "t3" || t3.resp.Response == nil || t3.resp.Response.Error == nil || t3.resp.Response.Error.Code != "tool_failed" || t3.resp.Response.Error.Message != "it failed" {
		t.Errorf("t3 received %+v (%+v), %v", t3.resp, t3.resp.Response, t3.err)
	}
}
