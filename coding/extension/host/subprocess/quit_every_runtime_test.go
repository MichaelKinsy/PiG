package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// quitPeer is the connection of one extension process as the host holds it, with the test at the process's end.
type quitPeer struct {
	t      *testing.T
	conn   *Conn
	ext    net.Conn
	cancel context.CancelFunc
	done   chan struct{}
}

func newQuitPeer(t *testing.T, h *Host, name string, node bool) *quitPeer {
	t.Helper()
	hostSide, extSide := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	conn := NewConn(name, hostSide)
	conn.Start(ctx)
	me := withConn(&managedExt{config: ExtConfig{Name: name}, host: h, nodeRuntime: node, nodeRealm: node}, conn)
	me.shuttingDown.Store(true)
	h.mu.Lock()
	h.exts[name] = me
	h.mu.Unlock()
	p := &quitPeer{t: t, conn: conn, ext: extSide, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		h.handleIncoming(me, conn)
	}()
	go func() {
		buf := make([]byte, 1<<16)
		for {
			if _, err := extSide.Read(buf); err != nil {
				return
			}
		}
	}()
	return p
}

func (p *quitPeer) write(env Envelope) {
	p.t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		p.t.Fatal(err)
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	if _, err := p.ext.Write(append(length[:], data...)); err != nil {
		p.t.Fatal(err)
	}
}

func (p *quitPeer) notify(method string) {
	p.t.Helper()
	p.write(Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: method}})
}

// request starts a host request on the connection, which the process answers with respond.
func (p *quitPeer) request(id string, payload RequestPayload) <-chan error {
	answered := make(chan error, 1)
	go func() {
		_, err := p.conn.request(p.t.Context(), &Envelope{Type: MsgRequest, ID: id, Request: &payload}, 0, id)
		answered <- err
	}()
	return answered
}

func (p *quitPeer) respond(id string) {
	p.t.Helper()
	p.write(Envelope{Type: MsgResponse, ID: id, Response: &ResponsePayload{}})
}

// end is the process's exit.
func (p *quitPeer) end() {
	p.cancel()
	_ = p.ext.Close()
	<-p.conn.Done()
	<-p.done
}

// quitDrainHost is a host whose drain handler counts its runs, after input ended.
func quitDrainHost(t *testing.T) (*Host, *atomic.Int32) {
	t.Helper()
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	handled := new(atomic.Int32)
	h.SetRuntimeDrainHandler(func() { handled.Add(1) })
	return h, handled
}

// Pi's one event loop holds every extension's timers and sockets: a quit whose dispose cannot return exits only when nothing holds it (Pi 1.5.2 under tmux: a silent
// session_shutdown handler beside an extension with an interval does not exit; beside a 3 s timer it exits after 3.09 s). pig's Node extensions can run in several
// processes, so the drained loop of the process whose handler is pending is not Pi's drained loop while another Node process has not reported its own.
func TestQuitDrainWaitsForEveryNodeProcess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, handled := quitDrainHost(t)
		silent, timer := newQuitPeer(t, h, "silent", true), newQuitPeer(t, h, "timer", true)
		defer silent.end()
		defer timer.end()
		h.EndInput()
		silent.notify(notifyRuntimeDrained)
		synctest.Wait()
		if n := handled.Load(); n != 0 {
			t.Fatalf("the host acted %d times on the drained loop of one Node process while another Node process had not reported its own", n)
		}
		// The timer ended: the second process has nothing unanswered and reports its drained loop.
		timer.notify(notifyRuntimeCommandsDrained)
		synctest.Wait()
		if n := handled.Load(); n != 1 {
			t.Fatalf("the host acted %d times once every Node process had reported its drained loop, want once", n)
		}
	})
}

// A Go, Rust, Python or Lisp runtime has no event loop to report. What the host can see of it is a command it has not answered, which in Pi would hold the loop
// through whatever it waits on (Pi 1.5.2 under tmux: a command in flight on a TCP read holds the quit; the process exits 53 ms after the read ends). The quit is
// evaluated again when the command ends.
func TestQuitDrainWaitsForANativeCommandInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, handled := quitDrainHost(t)
		silent, native := newQuitPeer(t, h, "silent", true), newQuitPeer(t, h, "native", false)
		defer silent.end()
		defer native.end()
		answered := native.request("held", RequestPayload{Method: "command", Tool: "hold"})
		synctest.Wait()
		h.EndInput()
		silent.notify(notifyRuntimeDrained)
		synctest.Wait()
		if n := handled.Load(); n != 0 {
			t.Fatalf("the host acted %d times on the Node process's drained loop while a native runtime's command was in flight", n)
		}
		native.respond("held")
		synctest.Wait()
		if err := <-answered; err != nil {
			t.Fatalf("the native request: %v", err)
		}
		if n := handled.Load(); n != 1 {
			t.Fatalf("the host acted %d times once the native command had ended, want once", n)
		}
	})
}

// A Node process that reported its drained loop runs again when the host sends it a request, and what it starts then (a timer, a socket) holds the quit like anything
// else. So the report counts only until the next request: one sent after it, or one that crossed it on the wire and that the process had therefore not seen. The
// process reports again once it answered and its loop drained again.
func TestQuitDrainReportEndsWithALaterRequest(t *testing.T) {
	for _, crossing := range []bool{false, true} {
		name := "a request sent after the report"
		if crossing {
			name = "a request that crossed the report"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, handled := quitDrainHost(t)
				silent, other := newQuitPeer(t, h, "silent", true), newQuitPeer(t, h, "other", true)
				defer silent.end()
				defer other.end()
				h.EndInput()
				var answered <-chan error
				if crossing {
					answered = other.request("later", RequestPayload{Method: "event", Event: "turn_end"})
					synctest.Wait()
					other.notify(notifyRuntimeCommandsDrained)
				} else {
					other.notify(notifyRuntimeCommandsDrained)
					synctest.Wait()
					answered = other.request("later", RequestPayload{Method: "event", Event: "turn_end"})
				}
				synctest.Wait()
				silent.notify(notifyRuntimeDrained)
				synctest.Wait()
				if n := handled.Load(); n != 0 {
					t.Fatalf("the host acted %d times while a Node process ran a request it received after its report", n)
				}
				other.respond("later")
				synctest.Wait()
				if err := <-answered; err != nil {
					t.Fatalf("the later request: %v", err)
				}
				if n := handled.Load(); n != 0 {
					t.Fatalf("the host acted %d times on the answer alone: the process has not reported its loop drained since it ran", n)
				}
				other.notify(notifyRuntimeCommandsDrained)
				synctest.Wait()
				if n := handled.Load(); n != 1 {
					t.Fatalf("the host acted %d times once the process had reported again, want once", n)
				}
			})
		})
	}
}

// A call of the extension is the process running too: a report before it no longer counts.
func TestQuitDrainReportEndsWithALaterCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, handled := quitDrainHost(t)
		h.SetCallHandler(func(string, *CallPayload) (*CallResultPayload, error) { return &CallResultPayload{}, nil })
		silent, other := newQuitPeer(t, h, "silent", true), newQuitPeer(t, h, "other", true)
		defer silent.end()
		defer other.end()
		h.EndInput()
		other.notify(notifyRuntimeCommandsDrained)
		synctest.Wait()
		other.write(Envelope{Type: MsgCall, ID: "call-1", Call: &CallPayload{Method: "ui.notify", Args: json.RawMessage(`{"message":"still here"}`)}})
		synctest.Wait()
		silent.notify(notifyRuntimeDrained)
		synctest.Wait()
		if n := handled.Load(); n != 0 {
			t.Fatalf("the host acted %d times though a Node process had made a call after its report", n)
		}
		other.notify(notifyRuntimeCommandsDrained)
		synctest.Wait()
		if n := handled.Load(); n != 1 {
			t.Fatalf("the host acted %d times once the process had reported again, want once", n)
		}
	})
}

// A process that ended holds nothing.
func TestQuitDrainCountsAProcessThatEnded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, handled := quitDrainHost(t)
		silent, timer := newQuitPeer(t, h, "silent", true), newQuitPeer(t, h, "timer", true)
		defer silent.end()
		h.EndInput()
		silent.notify(notifyRuntimeDrained)
		synctest.Wait()
		if n := handled.Load(); n != 0 {
			timer.end()
			t.Fatalf("the host acted %d times while another Node process had not reported", n)
		}
		timer.end()
		synctest.Wait()
		if n := handled.Load(); n != 1 {
			t.Fatalf("the host acted %d times once the other process had ended, want once", n)
		}
	})
}

// A quit yield is Pi's RPC exit at the stdout flush: only the handler of both kinds takes it. The handler of a drained loop, which the interactive quit installs,
// does not, and the drained loop is handed over once.
func TestQuitYieldRunsOnlyTheHandlerOfBothKinds(t *testing.T) {
	h := NewHost(t.TempDir())
	both, loop := 0, 0
	h.SetRuntimeDrainHandler(func() { both++ })
	h.SetRuntimeLoopDrainHandler(func() { loop++ })
	h.applyRuntimeDrain(&managedExt{nodeRuntime: true}, notifyRuntimeQuitYield)
	if both != 1 || loop != 0 {
		t.Fatalf("after a quit yield the handler of both kinds ran %d times and the handler of a drained loop %d times, want 1 and 0", both, loop)
	}
	h.applyRuntimeDrain(&managedExt{nodeRuntime: true}, notifyRuntimeDrained)
	if both != 2 || loop != 1 {
		t.Fatalf("after a drained loop the handlers ran %d and %d times, want 2 and 1", both, loop)
	}
	h.applyRuntimeDrain(&managedExt{nodeRuntime: true}, notifyRuntimeDrained)
	if both != 2 || loop != 1 {
		t.Fatalf("a second drained loop ran the handlers again (%d and %d times): the drained loop is handed over once", both, loop)
	}
}

// A handler of the run or of its settle tail in a runtime that reports no loop does not hold the quit. The dispose awaits the run itself, and RPC mode counts a
// settle-tail handler of such a runtime as waiting once input ended, because it may wait for the very shutdown to cancel it: a quit that waited for it would never
// end (TestRPCInputEndDuringGoSDKSettleTailHandlerShutsDown).
func TestQuitDrainDoesNotWaitForANativeSettleTailHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, handled := quitDrainHost(t)
		silent, native := newQuitPeer(t, h, "silent", true), newQuitPeer(t, h, "native", false)
		defer silent.end()
		defer native.end()
		answered := native.request("settled", RequestPayload{Method: "event", Event: "agent_settled"})
		synctest.Wait()
		h.EndInput()
		silent.notify(notifyRuntimeDrained)
		synctest.Wait()
		if n := handled.Load(); n != 1 {
			t.Fatalf("the host acted %d times while a native runtime's agent_settled handler was in flight, want once: the quit must not wait for it", n)
		}
		native.respond("settled")
		synctest.Wait()
		if err := <-answered; err != nil {
			t.Fatalf("the agent_settled request: %v", err)
		}
	})
}
