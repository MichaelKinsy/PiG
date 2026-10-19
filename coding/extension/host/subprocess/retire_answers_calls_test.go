package subprocess

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// readRetiringPeerFrame reads the next frame the host wrote to peer, failing with what the host did not do when none arrives.
func readRetiringPeerFrame(t *testing.T, peer net.Conn, what string) Envelope {
	t.Helper()
	if err := peer.SetReadDeadline(time.Now().Add(testbudget.Wait(t))); err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	if _, err := io.ReadFull(peer, header[:]); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(peer, body); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

// A peer that shares its process with a live generation is retired, not closed: the host waits for it to close, and until it does the peer's calls still need their results. A Node runtime blocks its event loop in a synchronous host call (xref.hello, made while it handles its first `ready`) and reads no shutdown frame until the result arrives, so a connection that refused the result left the peer waiting for the host and the host waiting for the peer to close: `pig --list-models` with a project_trust handler never exited (parity project-trust 08, 6 of 60 runs at GOMAXPROCS=1).
func TestRetiringConnSendsTheResultOfACallTheShutdownFrameDidNotBeat(t *testing.T) {
	t.Parallel()
	hostEnd, peerEnd := net.Pipe()
	defer func() { _ = peerEnd.Close() }()
	conn := NewConn("retiring", hostEnd)
	conn.Start(t.Context())
	retired := make(chan error, 1)
	go func() { retired <- conn.Retire("quit") }()
	// The shutdown frame is queued after the connection is marked closing, so the peer has read it once the host no longer accepts new sends.
	if frame := readRetiringPeerFrame(t, peerEnd, "the host sent no shutdown frame"); frame.Type != MsgShutdown {
		t.Fatalf("first frame = %+v, want shutdown", frame)
	}
	if err := conn.Send(&Envelope{Type: MsgCallResult, ID: "call-1", CallResult: &CallResultPayload{Result: json.RawMessage("null")}}); err != nil {
		t.Fatalf("a retiring connection refused the result of the peer's call: %v", err)
	}
	if frame := readRetiringPeerFrame(t, peerEnd, "the peer received no result for its call"); frame.Type != MsgCallResult || frame.ID != "call-1" {
		t.Fatalf("frame = %+v, want the result of call-1", frame)
	}
	// Only the answer is allowed: a request or notification to a peer that was told to shut down stays refused.
	if err := conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "late"}}); err == nil {
		t.Fatal("a retiring connection accepted a notification after the shutdown frame")
	}
	_ = peerEnd.Close()
	select {
	case <-retired:
	case <-t.Context().Done():
		t.Fatal("Retire did not return after the peer closed")
	}
	if err := conn.Send(&Envelope{Type: MsgCallResult, ID: "call-2", CallResult: &CallResultPayload{Result: json.RawMessage("null")}}); err == nil {
		t.Fatal("a closed connection accepted a call result")
	}
}

// The Host path of the same defect: Host.Shutdown retires a generation whose process another generation holds, and the retiring peer's xref.hello is answered while the host waits for it to close.
func TestShutdownAnswersTheCallOfAGenerationItRetires(t *testing.T) {
	t.Parallel()
	hostEnd, peerEnd := net.Pipe()
	defer func() { _ = peerEnd.Close() }()
	conn := NewConn("trust-probe", hostEnd)
	conn.Start(t.Context())
	h := NewHost(t.TempDir())
	me := &managedExt{config: ExtConfig{Name: "trust-probe"}, host: h}
	share := newProcessShare("node:cell", me, make(chan struct{}), discardAdmitWriter{io.Discard}, nil)
	// The sibling generation's reference keeps the process alive past this generation, which makes stopping it a retirement.
	if !share.acquire() {
		t.Fatal("share was not acquirable")
	}
	me.share = share
	withConn(me, conn)
	h.exts[me.config.Name] = me
	go h.handleIncoming(me, conn)

	shutdown := make(chan struct{})
	go func() {
		defer close(shutdown)
		h.Shutdown("quit")
	}()
	if frame := readRetiringPeerFrame(t, peerEnd, "the host sent no shutdown frame"); frame.Type != MsgShutdown {
		t.Fatalf("first frame = %+v, want shutdown", frame)
	}
	writeLivenessEnvelope(t, peerEnd, Envelope{Type: MsgCall, ID: "hello-1", Call: &CallPayload{Method: callXrefHello, Args: json.RawMessage(`{"realm":"realm-1"}`)}})
	// The host pings a peer it waits on; a ping before the result is the heartbeat, not the answer.
	frame := readRetiringPeerFrame(t, peerEnd, "the retiring peer's xref.hello was never answered")
	for frame.Type == MsgPing {
		frame = readRetiringPeerFrame(t, peerEnd, "the retiring peer's xref.hello was never answered")
	}
	if frame.Type != MsgCallResult || frame.ID != "hello-1" {
		t.Fatalf("frame = %+v, want the result of hello-1", frame)
	}
	if frame.CallResult == nil || frame.CallResult.Error != nil {
		t.Fatalf("xref.hello result = %+v", frame.CallResult)
	}
	_ = peerEnd.Close()
	select {
	case <-shutdown:
	case <-t.Context().Done():
		t.Fatal("Shutdown did not return after the retired peer closed")
	}
}
