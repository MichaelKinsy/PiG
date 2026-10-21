package subprocess

import (
	"net"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// timerDeferHost returns a host with one Node runtime whose peer end the test drives.
func timerDeferHost(t *testing.T) (*Host, *Conn, *Conn) {
	t.Helper()
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	hostEnd, peerEnd := net.Pipe()
	t.Cleanup(func() { _ = hostEnd.Close(); _ = peerEnd.Close() })
	conn := NewConn("node", hostEnd)
	h.observeXref(conn)
	conn.Start(t.Context())
	peer := NewConn("node-peer", peerEnd)
	peer.Start(t.Context())
	me := withConn(&managedExt{config: ExtConfig{Name: "node"}, host: h, nodeRuntime: true}, conn)
	h.mu.Lock()
	h.exts["node"] = me
	h.mu.Unlock()
	return h, conn, peer
}

// nextTimerDefer reads the peer's frames until the next timer_defer notify and returns its args.
func nextTimerDefer(t *testing.T, peer *Conn) TimerDeferArgs {
	t.Helper()
	for {
		select {
		case env := <-peer.Incoming():
			if env.Type != MsgNotify || env.Notify == nil || env.Notify.Method != NotifyTimerDefer {
				continue
			}
			var args TimerDeferArgs
			if err := json.Unmarshal(env.Notify.Args, &args); err != nil {
				t.Fatal(err)
			}
			return args
		case <-time.After(10 * time.Second):
			t.Fatal("no timer_defer frame")
		}
	}
}

func ackTimerDefer(t *testing.T, peer *Conn, seq uint64) {
	t.Helper()
	args, err := json.Marshal(TimerDeferAckArgs{Seq: seq})
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyTimerDeferAck, Args: args}}); err != nil {
		t.Fatal(err)
	}
}

// pig additive (D189): a stdin command's first event must reach no runtime that still runs its timer callbacks, so the host waits for the runtime's ack of the hold before it lets the command go on (Turns.Arm), and the hold is on the wire ahead of every frame the command sends.
func TestArmWaitsForTheRuntimeToAcknowledgeTheHold(t *testing.T) {
	h, _, peer := timerDeferHost(t)
	var turns jsloop.Turns
	h.SetTimerTurns(&turns)
	armed := make(chan func(), 1)
	go func() { armed <- turns.Arm(t.Context()) }()
	args := nextTimerDefer(t, peer)
	if !args.On || args.Seq != 1 {
		t.Fatalf("hold frame = %+v, want on with number 1", args)
	}
	select {
	case <-armed:
		t.Fatal("Arm returned before the runtime acknowledged the hold")
	default:
	}
	ackTimerDefer(t, peer, args.Seq)
	var end func()
	select {
	case end = <-armed:
	case <-time.After(10 * time.Second):
		t.Fatal("Arm did not return after the ack")
	}
	end()
	if release := nextTimerDefer(t, peer); release.On || release.Seq != 2 {
		t.Fatalf("release frame = %+v, want off with number 2", release)
	}
}

// A runtime that ended has no callback left to hold, and a command must not wait for it.
func TestArmDoesNotWaitForARuntimeThatEnded(t *testing.T) {
	h, conn, peer := timerDeferHost(t)
	var turns jsloop.Turns
	h.SetTimerTurns(&turns)
	armed := make(chan func(), 1)
	go func() { armed <- turns.Arm(t.Context()) }()
	nextTimerDefer(t, peer)
	_ = conn.Close("test")
	select {
	case end := <-armed:
		end()
	case <-time.After(10 * time.Second):
		t.Fatal("Arm still waits for a closed connection")
	}
}
