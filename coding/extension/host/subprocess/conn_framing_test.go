package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// countingConn counts the Write calls the connection makes, each of which is a
// system call on a socket.
type countingConn struct {
	net.Conn
	mu     sync.Mutex
	writes int
}

func (c *countingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writes++
	c.mu.Unlock()
	return c.Conn.Write(p)
}

func (c *countingConn) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

// A frame that fits one socket write costs one write: the 4-byte length and the
// payload travel together, so the peer wakes once per frame.
func TestSmallFrameIsOneSocketWrite(t *testing.T) {
	hostEnd, peerEnd := loopbackPair(t)
	counted := &countingConn{Conn: hostEnd}
	conn := NewConn("framing", counted)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	t.Cleanup(func() { _ = peerEnd.Close() })

	const frames = 50
	received := make(chan *Envelope, frames)
	go func() {
		for {
			env, err := readEnvelopeFrom(peerEnd)
			if err != nil {
				return
			}
			if env.Type == MsgNotify {
				received <- env
			}
		}
	}()
	before := counted.count()
	for range frames {
		if err := conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "state_update", Args: json.RawMessage(`{"isIdle":true}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	for range frames {
		if env := <-received; env.Notify.Method != "state_update" {
			t.Fatalf("frame arrived as %+v", env)
		}
	}
	if writes := counted.count() - before; writes != frames {
		t.Fatalf("%d notify frames took %d socket writes, want one each", frames, writes)
	}
}

// Encoding and queueing a frame allocates nothing in steady state: the frame
// buffer and its encoder come from a pool the writer refills. (Reading is
// pooled likewise: BenchmarkReadFramePayload.)
func TestSendAllocatesNothingInSteadyState(t *testing.T) {
	hostEnd, peerEnd := loopbackPair(t)
	go func() { _, _ = io.Copy(io.Discard, peerEnd) }()
	t.Cleanup(func() { _ = peerEnd.Close() })
	conn := NewConn("alloc", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	env := &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "state_update", Args: json.RawMessage(`{"isIdle":true,"thinkingLevel":"high"}`)}}
	for range 256 {
		if err := conn.Send(env); err != nil {
			t.Fatal(err)
		}
	}
	allocs := testing.AllocsPerRun(2000, func() {
		if err := conn.Send(env); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > raceAllocSlack {
		t.Fatalf("Send allocates %.2f objects per frame, want 0", allocs)
	}
}

// A frame larger than one socket write is written in bounded chunks, in order,
// and arrives intact.
func TestLargeFrameChunksArriveIntact(t *testing.T) {
	hostEnd, peerEnd := loopbackPair(t)
	conn := NewConn("large", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	t.Cleanup(func() { _ = peerEnd.Close() })
	payload := make([]byte, 3*writeChunk+17)
	for i := range payload {
		payload[i] = 'a' + byte(i%26)
	}
	args := json.RawMessage(`"` + string(payload) + `"`)
	go func() { _ = conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "large", Args: args}}) }()
	var length [4]byte
	if _, err := io.ReadFull(peerEnd, length[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(length[:]))
	if _, err := io.ReadFull(peerEnd, body); err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if string(env.Notify.Args) != string(args) {
		t.Fatalf("large frame corrupted: %d bytes, want %d", len(env.Notify.Args), len(args))
	}
}

// payloadBytes is a queued frame's JSON, whichever way the connection built it.
func (f outboundFrame) payloadBytes() []byte {
	if f.frame != nil {
		return f.frame.payload()
	}
	return f.data
}

// The host-side allocation budgets of the extension wire, in objects per
// operation, against a peer that allocates nothing. They only ratchet down: a
// change that allocates more fails, and so does a run two or more objects under
// its budget, until the budget is lowered. BenchmarkConnRequestRoundTrip and
// BenchmarkConnNotify report the same costs with bytes.
const (
	// A frame the host sends: encoded into a pooled buffer and written with one syscall.
	sendAllocBudget = 0
	// A request answered by the extension: the response channel, the decoded response envelope, and the request's bookkeeping. A request without an inactivity lease or suspension report has no state channel.
	requestAllocBudget = 11
)

func TestConnAllocationBudgets(t *testing.T) {
	hostEnd, peerEnd := loopbackPair(t)
	echoPeer(t, peerEnd)
	conn := NewConn("budget", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	notify := &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "state_update", Args: json.RawMessage(`{"isIdle":true}`)}}
	request := func() {
		if _, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "noop"}}); err != nil {
			t.Fatal(err)
		}
	}
	for range 256 {
		_ = conn.Send(notify)
		request()
	}
	for name, test := range map[string]struct {
		budget int
		run    func()
	}{
		"send":    {sendAllocBudget, func() { _ = conn.Send(notify) }},
		"request": {requestAllocBudget, request},
	} {
		got := testing.AllocsPerRun(1000, test.run)
		switch {
		case got > float64(test.budget+raceAllocSlack):
			t.Errorf("%s allocates %.1f objects, budget %d", name, got, test.budget)
		case !raceEnabled && got <= float64(test.budget-2):
			t.Errorf("%s allocates %.1f objects, two or more under its budget of %d: lower the budget", name, got, test.budget)
		}
	}
}

// A pooled frame carries exactly the bytes json.Marshal gives the envelope, behind its big-endian length: HTML characters and U+2028/U+2029 are escaped as before, which checkResultFrameSize counts when it measures a result against the frame limit without encoding it.
func TestEncodedFrameIsTheMarshaledEnvelope(t *testing.T) {
	env := &Envelope{Type: MsgCallResult, ID: "r7", CallResult: &CallResultPayload{Result: json.RawMessage(` {"html": "<a href=\"x\">&</a>", "sep": "\u2028\u2029 raw   "} `)}}
	want, err := sdkjson.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second frame comes from the pool
		frame, err := encodeFrame(env)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(frame.payload()); got != string(want) {
			t.Fatalf("frame payload = %s, want %s", got, want)
		}
		if n := binary.BigEndian.Uint32(frame.buf[:4]); int(n) != len(want) {
			t.Fatalf("length prefix = %d, want %d", n, len(want))
		}
		frame.release()
	}
}

// The channel that carries a frame's write result is pooled. Requests that
// finish, requests whose context ends before or while they are written, and a
// connection that closes under them must neither receive another request's
// write result nor answer with another request's response.
func TestPooledWriteResultsNeverCrossRequests(t *testing.T) {
	hostEnd, peerEnd := loopbackPair(t)
	echoPeer(t, peerEnd)
	conn := NewConn("pooled", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	var wg sync.WaitGroup
	for worker := range 24 {
		wg.Go(func() {
			for i := range 200 {
				ctx, cancel := context.WithCancel(t.Context())
				if (worker+i)%5 == 0 {
					cancel()
				}
				env := &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "noop"}}
				resp, err := conn.Request(ctx, env)
				cancel()
				switch {
				case err == nil && (resp == nil || resp.ID != env.ID):
					t.Errorf("request %s answered by %+v", env.ID, resp)
					return
				case err != nil && !errors.Is(err, context.Canceled):
					t.Errorf("request %s failed with %v", env.ID, err)
					return
				}
			}
		})
	}
	wg.Wait()
}
