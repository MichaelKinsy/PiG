package subprocess

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// supersedeHost returns a host with one generation that a Node runtime hosts, on a manual clock, and the peer end the test drives as that runtime.
func supersedeHost(t *testing.T, clock *manualLivenessClock) (*Host, *managedExt, net.Conn) {
	t.Helper()
	hostEnd, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := newConnWithOptions("old", hostEnd, connOptions{Clock: clock, HeartbeatInterval: 2 * time.Second, HeartbeatTimeout: time.Second})
	conn.Start(t.Context())
	<-conn.heartbeatReady
	h := NewHost(t.TempDir())
	me := withConn(&managedExt{
		config:      ExtConfig{Name: "old", Path: "/does/not/run"},
		host:        h,
		nodeRuntime: true,
		supervisor:  NewSupervisor(SupervisorConfig{MaxCrashes: 1, InitialDelay: time.Hour}),
	}, conn)
	h.exts["old"] = me
	go h.handleIncoming(me, conn)
	if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return h, me, peer
}

// readSupersedeInvalidate reads the invalidation a supersede sends: Pi's stale message and the number its acknowledgement carries.
func readSupersedeInvalidate(t *testing.T, peer net.Conn) InvalidateArgs {
	t.Helper()
	env := readLivenessEnvelope(t, peer)
	var args InvalidateArgs
	if env.Type != MsgNotify || env.Notify == nil || env.Notify.Method != NotifyInvalidate || json.Unmarshal(env.Notify.Args, &args) != nil {
		t.Fatalf("the supersede's first frame = %+v, want an invalidate notify", env)
	}
	if args.Message != (&inproc.StaleError{}).Error() || args.Ack == 0 {
		t.Fatalf("invalidate = %+v, want Pi's stale message and the number of its acknowledgement", args)
	}
	return args
}

// Pi's oldRunner.invalidate() is synchronous: the old ctx is stale before reload() goes on (agent-session.ts:3825-3826). A Node runtime applies the notification when its loop reads it, so the reload stops at its supersede point until the runtime has acknowledged, and the host marks the generation only then: a call the runtime made with a ctx that was still live is served.
func TestSupersedeWaitsForTheNodeRuntimeToAcknowledgeTheInvalidation(t *testing.T) {
	clock := newManualLivenessClock()
	h, me, peer := supersedeHost(t, clock)
	ended := make(chan func(), 1)
	go func() { ended <- h.supersedeGenerations(t.Context()) }()
	invalidate := readSupersedeInvalidate(t, peer)
	select {
	case <-ended:
		t.Fatal("the reload went on before the runtime had acknowledged the invalidation")
	default:
	}
	if h.supersedes(me) {
		t.Fatal("the generation was marked before its runtime had acknowledged: a call the runtime made with a live ctx would be refused")
	}
	ack, err := json.Marshal(InvalidateAckArgs{Seq: invalidate.Ack})
	if err != nil {
		t.Fatal(err)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyInvalidateAck, Args: ack}})
	var end func()
	select {
	case end = <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the reload did not go on once the runtime had acknowledged the invalidation")
	}
	if !h.supersedes(me) {
		t.Fatal("the acknowledged generation is not marked: its later calls would be served")
	}
	// The deadline ended with the wait: the time it was armed for fails nothing.
	clock.elapse(time.Second)
	if err := me.connection().failureError(); err != nil {
		t.Fatalf("the acknowledged connection failed: %v", err)
	}
	end()
	if h.supersedes(me) {
		t.Fatal("the generation is still marked after the reload's end")
	}
	h.shuttingDown.Store(true)
}

// An extension that hangs never reads the invalidation, and /reload must still replace it. The heartbeat does not find it: a runtime can answer its heartbeat while a handler holds the extension's thread. So the acknowledgement has the deadline of a pong: a runtime that has not acknowledged by then is failed as one that misses a pong is (ExtensionUnresponsiveError), the reload goes on with the generation marked, and the host reports the extension as it reports any unresponsive one.
func TestSupersedeOfANodeRuntimeThatNeverAcknowledgesIsBoundedByThePongDeadline(t *testing.T) {
	clock := newManualLivenessClock()
	h, me, peer := supersedeHost(t, clock)
	reports := make(chan string, 1)
	h.SetCrashHandler(func(_ string, _ time.Duration, _ bool, reason string) { reports <- reason })
	ended := make(chan func(), 1)
	go func() { ended <- h.supersedeGenerations(t.Context()) }()
	readSupersedeInvalidate(t, peer)
	// The deadline is armed at the send, which the read above does not order with this goroutine: each pass elapses a pong deadline until the reload has gone on.
	var end func()
	guard := time.After(10 * time.Second)
	for end == nil {
		clock.elapse(time.Second)
		select {
		case end = <-ended:
		case <-guard:
			t.Fatal("the wait for a runtime that never acknowledges was not bounded by the pong deadline")
		case <-time.After(20 * time.Millisecond):
		}
	}
	// Only the deadline fails the connection with this error: a wait that returned for any other reason leaves it unfailed.
	var unresponsive *ExtensionUnresponsiveError
	if err := me.connection().failureError(); !errors.As(err, &unresponsive) || !strings.Contains(unresponsive.Operation, "acknowledge") {
		t.Fatalf("failure = %T %v, want the ExtensionUnresponsiveError of the acknowledgement's deadline", err, err)
	}
	if !h.supersedes(me) {
		t.Fatal("the generation of the runtime that never acknowledged is not marked")
	}
	select {
	case reason := <-reports:
		if !strings.Contains(reason, "extension_unresponsive") {
			t.Fatalf("the host reported %q, want extension_unresponsive", reason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the host did not report the runtime that never acknowledged")
	}
	end()
	h.shuttingDown.Store(true)
}
