package subprocess

import (
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// supersedeHost returns a host with one generation that a Node runtime hosts, on a manual clock, the peer end the test drives as that runtime, and a channel that closes when the host's read loop of the connection has ended and handled its end.
func supersedeHost(t *testing.T, clock *manualLivenessClock) (*Host, *managedExt, net.Conn, <-chan struct{}) {
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
	handled := make(chan struct{})
	go func() {
		defer close(handled)
		h.handleIncoming(me, conn)
	}()
	if err := peer.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return h, me, peer, handled
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
	h, me, peer, _ := supersedeHost(t, clock)
	ended := make(chan func(), 1)
	go func() {
		unacknowledged, end := h.supersedeGenerations(t.Context())
		if len(unacknowledged) != 0 {
			t.Errorf("the supersede returned %d generations as unacknowledged, want none", len(unacknowledged))
		}
		ended <- end
	}()
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

// An extension that hangs never reads the invalidation, and /reload must still replace it. The heartbeat does not find it: a runtime can answer its heartbeat while a handler holds the extension's thread. So the acknowledgement has the deadline of a pong, and no other bound: at the deadline the reload ends the connection with an ExtensionUnresponsiveError that carries that deadline, returns the generation for its report, and goes on with the generation marked. The end is the reload's to report (ReloadReport.Unacknowledged): the host tells the crash handler nothing, so the user reads one line and not a crash notice beside it.
// mutation-checked: with the wait off (invalidateAcknowledged's wait returns true) the connection has no failure; with the ReloadDeadline branch of handleIncoming off the crash handler is told "extension_unresponsive"; with twice the pong deadline the failure carries 2s.
func TestSupersedeOfANodeRuntimeThatNeverAcknowledgesIsBoundedByThePongDeadline(t *testing.T) {
	clock := newManualLivenessClock()
	h, me, peer, handled := supersedeHost(t, clock)
	reports := make(chan string, 1)
	h.SetCrashHandler(func(_ string, _ time.Duration, _ bool, reason string) { reports <- reason })
	type superseded struct {
		unacknowledged []unacknowledgedGeneration
		end            func()
	}
	ended := make(chan superseded, 1)
	go func() {
		unacknowledged, end := h.supersedeGenerations(t.Context())
		ended <- superseded{unacknowledged, end}
	}()
	readSupersedeInvalidate(t, peer)
	// The deadline is armed at the send, which the read above does not order with this goroutine: each pass elapses a pong deadline until the reload has gone on.
	var got superseded
	guard := time.After(10 * time.Second)
	for got.end == nil {
		clock.elapse(time.Second)
		select {
		case got = <-ended:
		case <-guard:
			t.Fatal("the wait for a runtime that never acknowledges was not bounded by the pong deadline")
		case <-time.After(20 * time.Millisecond):
		}
	}
	// Only the deadline ends the connection with this error, and the deadline it carries is the connection's pong deadline (supersedeHost: one second).
	var unresponsive *ExtensionUnresponsiveError
	if err := me.connection().failureError(); !errors.As(err, &unresponsive) || unresponsive.ReloadDeadline != time.Second {
		t.Fatalf("failure = %T %v, want the ExtensionUnresponsiveError of a reload whose deadline is the connection's pong deadline, 1s", err, err)
	}
	if len(got.unacknowledged) != 1 || got.unacknowledged[0].me != me || got.unacknowledged[0].deadline != time.Second {
		t.Fatalf("the supersede returned %+v, want the generation whose runtime did not acknowledge, with the pong deadline", got.unacknowledged)
	}
	if !h.supersedes(me) {
		t.Fatal("the generation of the runtime that never acknowledged is not marked")
	}
	select {
	case <-handled:
	case <-time.After(10 * time.Second):
		t.Fatal("the host's read loop did not end with the connection")
	}
	select {
	case reason := <-reports:
		t.Fatalf("the host reported a crash for the generation the reload ended: %q", reason)
	default:
	}
	got.end()
	h.shuttingDown.Store(true)
}

// A request in flight on a connection that a reload ends has one answer: the error that names the reload, its deadline and the request. Nothing is left pending.
// mutation-checked: with the request's error built without the connection's ReloadDeadline (Conn.Request), the answer is the heartbeat's text: "did not answer heartbeat for request".
func TestRequestInFlightWhenAReloadEndsItsConnectionIsAnsweredWithTheReload(t *testing.T) {
	h, me, peer, _ := supersedeHost(t, newManualLivenessClock())
	conn := me.connection()
	answered := make(chan error, 2)
	go func() {
		_, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "hang"}})
		answered <- err
	}()
	if env := readLivenessEnvelope(t, peer); env.Type != MsgRequest {
		t.Fatalf("the first frame = %+v, want the request", env)
	}
	conn.fail(&ExtensionUnresponsiveError{Extension: "old", ReloadDeadline: conn.heartbeatTimeout})
	select {
	case err := <-answered:
		var unresponsive *ExtensionUnresponsiveError
		if !errors.As(err, &unresponsive) || unresponsive.ReloadDeadline != time.Second || unresponsive.RequestID == "" || !strings.Contains(err.Error(), "did not acknowledge a reload within 1s") {
			t.Fatalf("the request in flight was answered %T %v, want the ExtensionUnresponsiveError that names the reload, its deadline and the request", err, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the request in flight on the connection a reload ended was never answered")
	}
	if got := conn.requestsEndedByFailure(); got != 1 {
		t.Fatalf("the failure ended %d requests, want 1", got)
	}
	select {
	case err := <-answered:
		t.Fatalf("the request has a second answer: %v", err)
	default:
	}
	h.shuttingDown.Store(true)
}

// The reload's report has one line for an extension that lost a request or has its process to itself, and says whether the reload loaded it again. An extension that shares the process of a named one and lost nothing has no line: nothing of it was cut short, and it is not reported for its neighbour's handler. When no extension of a shared process lost a request, one line names the runtime and those that share it.
func TestUnacknowledgedNoticesNameTheExtensionsThatLostARequest(t *testing.T) {
	h := NewHost(t.TempDir())
	shared, quiet := &processShare{}, &processShare{}
	generation := func(name string, share *processShare, lost int) unacknowledgedGeneration {
		return unacknowledgedGeneration{me: &managedExt{config: ExtConfig{Name: name}, share: share}, deadline: defaultHeartbeatTimeout, lostRequests: lost}
	}
	ended := []unacknowledgedGeneration{
		generation("timer-b", quiet, 0),
		generation("solo", nil, 0),
		generation("neighbour", shared, 0),
		generation("gone", nil, 1),
		generation("busy", shared, 1),
		generation("timer-a", quiet, 0),
	}
	for _, one := range ended {
		// The reload loaded every extension again but "gone".
		if one.me.config.Name != "gone" {
			h.exts[one.me.config.Name] = &managedExt{config: one.me.config}
		}
	}
	within := "did not acknowledge the reload within " + defaultHeartbeatTimeout.String()
	want := []string{
		`extension "busy" ` + within + ` and was restarted`,
		`extension "gone" ` + within + ` and was stopped`,
		`extension "solo" ` + within + ` and was restarted`,
		`the runtime that extensions "timer-a", "timer-b" share ` + within + ` and was restarted`,
	}
	if got := h.unacknowledgedNotices(ended); !slices.Equal(got, want) {
		t.Fatalf("the reload's lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := h.unacknowledgedNotices(nil); got != nil {
		t.Fatalf("a reload every runtime acknowledged has the lines %q", got)
	}
}
