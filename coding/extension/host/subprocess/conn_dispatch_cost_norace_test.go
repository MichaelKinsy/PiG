//go:build !race

package subprocess

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"runtime/trace"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	xtrace "golang.org/x/exp/trace"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// readCountingConn counts the reads the host makes on its socket.
type readCountingConn struct {
	net.Conn
	reads atomic.Int64
}

func (c *readCountingConn) Read(p []byte) (int, error) {
	c.reads.Add(1)
	return c.Conn.Read(p)
}

// runtimePeer answers like the Node runtime does for an event handler that returns at once: a request gets its started state, then its completed state and the response, all in one write (the runtime's IO worker coalesces the frames one event-loop turn wrote). Notifications get no answer. It returns the ID of the goroutine that plays the runtime, which is the test's harness, not host work.
func runtimePeer(tb testing.TB, peer net.Conn) uint64 {
	tb.Helper()
	done := make(chan struct{})
	started := make(chan uint64, 1)
	go func() {
		defer close(done)
		started <- testGoroutineID()
		var length [4]byte
		frame := make([]byte, 0, 4096)
		reply := make([]byte, 0, 512)
		appendFrame := func(out []byte, parts ...[]byte) []byte {
			start := len(out)
			out = append(out, 0, 0, 0, 0)
			for _, part := range parts {
				out = append(out, part...)
			}
			binary.BigEndian.PutUint32(out[start:], uint32(len(out)-start-4))
			return out
		}
		for {
			if _, err := io.ReadFull(peer, length[:]); err != nil {
				return
			}
			n := int(binary.BigEndian.Uint32(length[:]))
			if cap(frame) < n {
				frame = make([]byte, n)
			}
			frame = frame[:n]
			if _, err := io.ReadFull(peer, frame); err != nil {
				return
			}
			if !bytes.Contains(frame, []byte(`"type":"request"`)) {
				continue
			}
			_, rest, ok := bytes.Cut(frame, []byte(`"id":"`))
			if !ok {
				continue
			}
			id, _, _ := bytes.Cut(rest, []byte(`"`))
			reply = appendFrame(reply[:0], []byte(`{"type":"request_state","request_state":{"request_id":"`), id, []byte(`","state":"started"}}`))
			reply = appendFrame(reply, []byte(`{"type":"request_state","request_state":{"request_id":"`), id, []byte(`","state":"completed"}}`))
			reply = appendFrame(reply, []byte(`{"type":"response","id":"`), id, []byte(`","response":{"result":null}}`))
			if _, err := peer.Write(reply); err != nil {
				return
			}
		}
	}()
	tb.Cleanup(func() { _ = peer.Close(); <-done })
	return <-started
}

// testGoroutineID is the calling goroutine's ID, read from its stack header ("goroutine N [...").
func testGoroutineID() uint64 {
	var buf [64]byte
	header := strings.TrimPrefix(string(buf[:runtime.Stack(buf[:], false)]), "goroutine ")
	id, err := strconv.ParseUint(header[:strings.IndexByte(header, ' ')], 10, 64)
	if err != nil {
		panic(err)
	}
	return id
}

// goroutineWakes counts the goroutines a trace shows made runnable after blocking, except the goroutines in exclude.
func goroutineWakes(tb testing.TB, r io.Reader, exclude ...uint64) int {
	tb.Helper()
	reader, err := xtrace.NewReader(r)
	if err != nil {
		tb.Fatal(err)
	}
	wakes := 0
	for {
		event, err := reader.ReadEvent()
		if errors.Is(err, io.EOF) {
			return wakes
		}
		if err != nil {
			tb.Fatal(err)
		}
		if event.Kind() != xtrace.EventStateTransition {
			continue
		}
		transition := event.StateTransition()
		if transition.Resource.Kind != xtrace.ResourceGoroutine {
			continue
		}
		if from, to := transition.Goroutine(); from == xtrace.GoWaiting && to == xtrace.GoRunnable && !slices.Contains(exclude, uint64(transition.Resource.Goroutine())) {
			wakes++
		}
	}
}

// One event dispatch to an extension process is a state push and the event request, which the runtime answers with its started state, its completed state and the response. The host side of it must cost one socket read per answer batch and few goroutine hand-offs: the caller writes its own frames when nothing is queued ahead of them, the heartbeat hears only idle/busy transitions, and request states nobody waits on apply on the read loop. Only host goroutines count: the fake runtime's wakes are the harness's, and they depend on how the OS schedules it between the host's two writes (it reads both frames after one wake on macOS, after two on Linux).
func TestConnDispatchReadsAndWakesPerEvent(t *testing.T) {
	hostEnd, peerEnd := loopbackPair(t)
	counted := &readCountingConn{Conn: hostEnd}
	peerGoroutine := runtimePeer(t, peerEnd)
	conn := NewConn("dispatch-cost", counted)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	<-conn.heartbeatReady

	ctx := context.Background()
	state := []byte(`{"state":{"isIdle":true,"thinkingLevel":"high"}}`)
	dispatch := func() {
		// The live work of a handler dispatch spans its state push and its request, as makeEventHandler holds it.
		conn.beginWork()
		defer conn.endWork()
		if err := conn.sendAndWait(ctx, &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "state_update", Args: state}}); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "event", Event: "turn_end"}}); err != nil {
			t.Fatal(err)
		}
	}
	for range 50 {
		dispatch()
	}

	const dispatches = 400
	readsBefore := counted.reads.Load()
	var traced bytes.Buffer
	runtime.GC()
	if err := trace.Start(&traced); err != nil {
		t.Skipf("runtime trace unavailable: %v", err)
	}
	for range dispatches {
		dispatch()
	}
	trace.Stop()
	reads := float64(counted.reads.Load()-readsBefore) / dispatches
	wakes := float64(goroutineWakes(t, &traced, peerGoroutine)) / dispatches
	t.Logf("per dispatch: %.2f socket reads, %.2f host goroutine wakes", reads, wakes)
	// Base a5df22bd3a: 6.00 reads (length and payload per frame) and 9.1 wakes counting the peer (writer hand-off and back per frame, heartbeat per work-count change, requester per request state).
	// Now 2 host wakes: the host's read loop and the requester's response. The ceiling extension.dispatch.host_wakes = 2.5 is the former 3.5 (peer read 1 + host read loop 1 + requester 1, plus 0.5)
	// without the peer term: a tightening, so one extra host wake per dispatch (3.0) fails it.
	if reads > 1.5 {
		t.Errorf("socket reads per dispatch = %.2f, want <= 1.5: one buffered read per answer batch", reads)
	}
	perfgate.Check(t, "extension.dispatch.host_wakes", wakes)
}
