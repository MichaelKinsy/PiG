package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type manualLivenessTimer struct {
	clock    *manualLivenessClock
	at       time.Time
	duration time.Duration
	ch       chan time.Time
	active   bool
}

func (t *manualLivenessTimer) C() <-chan time.Time { return t.ch }
func (t *manualLivenessTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := t.active
	t.active = false
	if wasActive && t.clock.requested[t.duration] > 0 {
		t.clock.requested[t.duration]--
	}
	return wasActive
}

type manualLivenessClock struct {
	mu         sync.Mutex
	now        time.Time
	timers     []*manualLivenessTimer
	requested  map[time.Duration]int
	timerReady chan struct{}
}

func newManualLivenessClock() *manualLivenessClock {
	return &manualLivenessClock{now: time.Unix(1, 0), requested: make(map[time.Duration]int), timerReady: make(chan struct{}, 1)}
}
func (c *manualLivenessClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *manualLivenessClock) NewTimer(d time.Duration) livenessTimer {
	c.mu.Lock()
	t := &manualLivenessTimer{clock: c, at: c.now.Add(d), duration: d, ch: make(chan time.Time, 1), active: true}
	c.timers = append(c.timers, t)
	c.requested[d]++
	c.mu.Unlock()
	select {
	case c.timerReady <- struct{}{}:
	default:
	}
	return t
}
func (c *manualLivenessClock) waitForTimer(t *testing.T, want time.Duration) {
	t.Helper()
	for {
		c.mu.Lock()
		if c.requested[want] > 0 {
			c.requested[want]--
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
		select {
		case <-c.timerReady:
		case <-t.Context().Done():
			t.Fatalf("timer %s was not requested", want)
		}
	}
}

func (c *manualLivenessClock) expectTimer(t *testing.T, want time.Duration) {
	c.waitForTimer(t, want)
}

func (c *manualLivenessClock) elapse(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var keep []*manualLivenessTimer
	for _, timer := range c.timers {
		if timer.active && !timer.at.After(now) {
			timer.active = false
			if c.requested[timer.duration] > 0 {
				c.requested[timer.duration]--
			}
			timer.ch <- now
			close(timer.ch)
		} else if timer.active {
			keep = append(keep, timer)
		}
	}
	c.timers = keep
	c.mu.Unlock()
}

func (c *manualLivenessClock) advance(t *testing.T, want time.Duration) {
	t.Helper()
	c.waitForTimer(t, want)
	c.elapse(want)
}

func readLivenessEnvelope(t *testing.T, conn net.Conn) Envelope {
	t.Helper()
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	return env
}
func writeLivenessEnvelope(t *testing.T, conn net.Conn, env Envelope) {
	t.Helper()
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if _, err := conn.Write(append(header[:], body...)); err != nil {
		t.Fatal(err)
	}
}

// waitForWriteProgress blocks until conn has recorded at least atLeast bytes
// of work-frame write progress. readLivenessEnvelope observes bytes directly
// off the wire the instant the peer's Read call is satisfied; writeFrame's
// own writeProgress.Add call for those same bytes runs afterward, in the
// writer goroutine, with no happens-before edge forcing it to complete
// first. A caller that reads a frame and then immediately drives a manual
// clock past a heartbeat deadline can therefore observe a stale
// writeProgress snapshot left over from that still-settling frame, which
// the deadline handler misreads as fresh progress and wrongly renews
// instead of failing (coding/extension/host/subprocess/conn.go
// heartbeatLoop's "case <-deadline.C()" branch). This closes that gap
// deterministically, without a fixed sleep.
func waitForWriteProgress(t *testing.T, conn *Conn, atLeast uint64) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for conn.writeProgress.Load() < atLeast {
		if time.Now().After(deadline) {
			t.Fatalf("writeProgress did not reach %d before timeout (have %d)", atLeast, conn.writeProgress.Load())
		}
		runtime.Gosched()
	}
}

func startLivenessRequest(t *testing.T, conn *Conn) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		_, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "tool_call", Tool: "slow"}})
		result <- err
	}()
	return result
}

func TestConnectionParentCancellationInterruptsBlockedReadAndReleasesPending(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	conn := NewConn("cancel-lifetime", host)
	conn.Start(ctx)
	<-conn.heartbeatReady

	pendingResult := make(chan error, 1)
	go func() {
		_, err := conn.Request(context.Background(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "tool_call", Tool: "blocked"}})
		pendingResult <- err
	}()
	_ = readLivenessEnvelope(t, peer)

	cancel()
	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation left the socket reader blocked and Done open")
	}
	select {
	case err := <-pendingResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pending request error = %T %v, want context.Canceled", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not release pending request")
	}
	conn.pendingMu.Lock()
	pending := len(conn.pending)
	conn.pendingMu.Unlock()
	conn.hostCallMu.Lock()
	hostCalls := len(conn.hostCalls)
	conn.hostCallMu.Unlock()
	if pending != 0 || hostCalls != 0 {
		t.Fatalf("parent cancellation retained pending=%d hostCalls=%d", pending, hostCalls)
	}
}

func TestRequestLivenessOverlapsQueuedFrameOwnership(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = host.Close() }()
	defer func() { _ = peer.Close() }()
	conn := NewConn("continuous-work", host)
	result := make(chan error, 1)
	go func() {
		_, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "tool_call", Tool: "slow"}})
		result <- err
	}()

	frame := <-conn.outCh
	conn.workMu.Lock()
	work := conn.work
	conn.workMu.Unlock()
	if work != 2 {
		t.Fatalf("queued Request work = %d, want frame and response ownership to overlap", work)
	}

	var request Envelope
	if err := json.Unmarshal(frame.data, &request); err != nil {
		t.Fatal(err)
	}
	conn.pendingMu.Lock()
	response := conn.pending[request.ID]
	conn.pendingMu.Unlock()
	conn.endWork()
	frame.result <- nil
	response <- &Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{}}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Request error = %v, want nil", err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("completed Request did not release response ownership")
	}
	conn.workMu.Lock()
	work = conn.work
	conn.workMu.Unlock()
	if work != 0 {
		t.Fatalf("completed Request retained work = %d", work)
	}
}

func TestHeartbeatRunsOnlyWhileConnectionOwnsWork(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("idle", host, connOptions{Clock: clock, HeartbeatInterval: 10 * time.Second, HeartbeatTimeout: 3 * time.Second})
	conn.Start(t.Context())
	<-conn.heartbeatReady
	select {
	case <-conn.heartbeatIdle:
	default:
	}
	clock.mu.Lock()
	idleTimers := len(clock.requested)
	clock.mu.Unlock()
	if idleTimers != 0 {
		t.Fatalf("idle connection requested %d heartbeat timer(s)", idleTimers)
	}
	result := startLivenessRequest(t, conn)
	request := readLivenessEnvelope(t, peer)
	clock.advance(t, 10*time.Second)
	ping := readLivenessEnvelope(t, peer)
	if ping.Type != MsgPing {
		t.Fatalf("heartbeat = %+v", ping)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: ping.Ping.Nonce}})
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{}})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.heartbeatIdle:
	case <-t.Context().Done():
		t.Fatal("completed work did not return heartbeat machine to idle")
	}
}

func TestHealthyPongKeepsLongRunningToolAlive(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("slow", host, connOptions{Clock: clock, HeartbeatInterval: 10 * time.Second, HeartbeatTimeout: 3 * time.Second})
	conn.Start(t.Context())
	result := startLivenessRequest(t, conn)
	request := readLivenessEnvelope(t, peer)
	for range 3 {
		clock.advance(t, 10*time.Second)
		ping := readLivenessEnvelope(t, peer)
		clock.expectTimer(t, 3*time.Second)
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: ping.Ping.Nonce}})
	}
	select {
	case err := <-result:
		t.Fatalf("long tool ended before response: %v", err)
	default:
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{}})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestMissingPongFailsPendingWorkAsExtensionUnresponsive(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("silent", host, connOptions{Clock: clock, HeartbeatInterval: 10 * time.Second, HeartbeatTimeout: 3 * time.Second})
	conn.Start(t.Context())
	result := startLivenessRequest(t, conn)
	request := readLivenessEnvelope(t, peer)
	requestBody, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	waitForWriteProgress(t, conn, uint64(4+len(requestBody)))
	clock.advance(t, 10*time.Second)
	_ = readLivenessEnvelope(t, peer)
	clock.expectTimer(t, 3*time.Second)
	clock.elapse(3 * time.Second)
	err = <-result
	var unresponsive *ExtensionUnresponsiveError
	if !errors.As(err, &unresponsive) || unresponsive.Extension != "silent" || unresponsive.RequestID == "" || unresponsive.Operation != "tool_call slow" {
		t.Fatalf("error = %T %v", err, err)
	}
	select {
	case <-conn.Done():
	case <-t.Context().Done():
		t.Fatal("unresponsive connection remained open")
	}
}

func TestRequestCancellationWinsOverLateResponse(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("cancelled", host, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "tool_call", Tool: "slow"}})
		result <- err
	}()
	request := readLivenessEnvelope(t, peer)
	cancel()
	cancelEnvelope := readLivenessEnvelope(t, peer)
	if cancelEnvelope.Type != MsgCancel || cancelEnvelope.Cancel.RequestID != request.ID {
		t.Fatalf("cancel envelope = %+v", cancelEnvelope)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("request error = %v, want context.Canceled", err)
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(`"late"`)}})
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "after-late"}})
	if incoming := <-conn.Incoming(); incoming.Type != MsgNotify || incoming.Notify == nil || incoming.Notify.Method != "after-late" {
		t.Fatalf("late response leaked to Incoming: %+v", incoming)
	}
	conn.pendingMu.Lock()
	_, retained := conn.pending[request.ID]
	conn.pendingMu.Unlock()
	if retained {
		t.Fatal("late response correlation remained pending after cancellation")
	}
}

type failingWriterConn struct {
	net.Conn
	err error
}

func (c failingWriterConn) Write([]byte) (int, error) { return 0, c.err }

func TestWriterFailureReturnsPreciseTransportError(t *testing.T) {
	writeErr := errors.New("writer exploded")
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := NewConn("writer", failingWriterConn{Conn: host, err: writeErr})
	conn.Start(t.Context())
	_, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "tool_call", Tool: "slow"}})
	var transport *TransportError
	if !errors.As(err, &transport) || transport.Extension != "writer" || transport.Operation != "write" || !errors.Is(transport, writeErr) {
		t.Fatalf("error = %T %v", err, err)
	}
}

func TestEventAndShortcutWaitWhileHeartbeatStaysHealthy(t *testing.T) {
	cases := []struct {
		name    string
		request func(*managedExt) func(context.Context) error
	}{
		{
			name: "event",
			request: func(managed *managedExt) func(context.Context) error {
				handler := managed.makeEventHandler("turn_end", 7)
				return func(ctx context.Context) error {
					_, err := handler(map[string]any{"turn": 1}, ctx)
					return err
				}
			},
		},
		{name: "shortcut", request: func(managed *managedExt) func(context.Context) error {
			return managed.host.makeShortcutHandler(managed, "ctrl+x")
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			clock := newManualLivenessClock()
			hostEnd, peer := net.Pipe()
			defer func() { _ = peer.Close() }()
			conn := newConnWithOptions(test.name, hostEnd, connOptions{Clock: clock, HeartbeatInterval: 2 * time.Second, HeartbeatTimeout: time.Second})
			conn.Start(t.Context())
			host := NewHost(t.TempDir())
			managed := &managedExt{config: ExtConfig{Name: test.name}, host: host, conn: conn}
			result := make(chan error, 1)
			go func() { result <- test.request(managed)(t.Context()) }()
			request := readLivenessEnvelope(t, peer)
			for range 4 {
				select {
				case err := <-result:
					t.Fatalf("handler ended without response: %v", err)
				default:
				}
				clock.advance(t, 2*time.Second)
				ping := readLivenessEnvelope(t, peer)
				clock.expectTimer(t, time.Second)
				writeLivenessEnvelope(t, peer, Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: ping.Ping.Nonce}})
			}
			writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{}})
			if err := <-result; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBlockedUserToolWaitsWhileHeartbeatStaysHealthy(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("interactive", host, connOptions{Clock: clock, HeartbeatInterval: 2 * time.Second, HeartbeatTimeout: time.Second})
	conn.Start(t.Context())
	result := startLivenessRequest(t, conn)
	request := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: request.ID, State: "blocked", Reason: "user"}})
	for range 4 {
		select {
		case err := <-result:
			t.Fatalf("blocked:user request ended without response: %v", err)
		default:
		}
		clock.advance(t, 2*time.Second)
		ping := readLivenessEnvelope(t, peer)
		if ping.Type != MsgPing || ping.Ping == nil {
			t.Fatalf("blocked request ended before heartbeat: %+v", ping)
		}
		clock.expectTimer(t, time.Second)
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: ping.Ping.Nonce}})
	}
	select {
	case err := <-result:
		t.Fatalf("blocked:user tool ended without response: %v", err)
	default:
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{}})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestStalledRendererCancelsGenerationAndRetainsLastFrame(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("renderer", host, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	invalidated := make(chan struct{}, 2)
	component := newRenderProxyComponent("renderer", "custom", map[string]any{}, extension.MessageRenderOptions{}, conn, 5*time.Second, func() { invalidated <- struct{}{} })

	if got := component.Render(40); len(got) != 1 || got[0] != "[custom]" {
		t.Fatalf("initial frame = %v", got)
	}
	first := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: first.ID, Response: &ResponsePayload{Result: json.RawMessage(`{"lines":["LAST-GOOD-FRAME"]}`)}})
	select {
	case <-invalidated:
	case <-t.Context().Done():
		t.Fatal("completed renderer frame was not published")
	}
	if got := component.Render(40); len(got) != 1 || got[0] != "LAST-GOOD-FRAME" {
		t.Fatalf("completed frame = %v", got)
	}

	component.Invalidate()
	second := readLivenessEnvelope(t, peer)
	clock.waitForTimer(t, 5*time.Second)
	clock.elapse(5 * time.Second)
	cancel := readLivenessEnvelope(t, peer)
	if cancel.Type == MsgPing {
		cancel = readLivenessEnvelope(t, peer)
	}
	if cancel.Type != MsgCancel || cancel.Cancel.RequestID != second.ID {
		t.Fatalf("stalled renderer cancellation = %+v", cancel)
	}
	if got := component.Render(40); len(got) != 1 || got[0] != "LAST-GOOD-FRAME" {
		t.Fatalf("stalled generation replaced last completed frame: %v", got)
	}
	if conn.closed.Load() {
		t.Fatal("stalled renderer closed a heartbeat-healthy connection")
	}
}

func TestSlowPackedMemberDoesNotKillHealthySibling(t *testing.T) {
	clock := newManualLivenessClock()
	hostA, peerA := net.Pipe()
	defer func() { _ = peerA.Close() }()
	hostB, peerB := net.Pipe()
	defer func() { _ = peerB.Close() }()
	connA := newConnWithOptions("member-a", hostA, connOptions{Clock: clock, HeartbeatInterval: 2 * time.Second, HeartbeatTimeout: time.Second})
	connB := newConnWithOptions("member-b", hostB, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	connA.Start(t.Context())
	connB.Start(t.Context())
	host := NewHost(t.TempDir())
	memberA := &managedExt{config: ExtConfig{Name: "member-a"}, host: host, conn: connA, packedCellKey: "cell-live"}
	memberB := &managedExt{config: ExtConfig{Name: "member-b"}, host: host, conn: connB, packedCellKey: "cell-live"}
	host.exts["member-a"] = memberA
	host.exts["member-b"] = memberB

	result := make(chan error, 1)
	handler := memberA.makeEventHandler("turn_end", 11)
	go func() {
		_, err := handler(map[string]any{"turn": 1}, t.Context())
		result <- err
	}()
	request := readLivenessEnvelope(t, peerA)
	for range 4 {
		clock.advance(t, 2*time.Second)
		ping := readLivenessEnvelope(t, peerA)
		clock.expectTimer(t, time.Second)
		writeLivenessEnvelope(t, peerA, Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: ping.Ping.Nonce}})
	}
	if host.exts["member-a"] != memberA || host.exts["member-b"] != memberB {
		t.Fatalf("slow member changed packed registry: %#v", host.exts)
	}
	if connA.closed.Load() || connB.closed.Load() || host.QuarantinedCells()["cell-live"] != "" {
		t.Fatal("slow healthy handler killed or quarantined its packed cell")
	}
	writeLivenessEnvelope(t, peerA, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{}})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

const packedProcessExitHelperEnv = "PIG_TEST_PACKED_PROCESS_EXIT_HELPER"

// TestPackedProcessExitHelper re-executes the Go test binary as a portable
// packed process that exits unexpectedly. The parent test owns its lifecycle.
func TestPackedProcessExitHelper(t *testing.T) {
	if os.Getenv(packedProcessExitHelperEnv) != "1" {
		t.Skip("helper process entry point")
	}
	os.Exit(7)
}

func TestPackedProcessDeathQuarantinesAffectedCell(t *testing.T) {
	host := NewHost(t.TempDir())
	quarantined := make(chan string, 2)
	host.SetCrashHandler(func(name string, _ time.Duration, disabled bool, _ string) {
		if disabled {
			quarantined <- name
		}
	})
	cmd := exec.Command(os.Args[0], "-test.run=^TestPackedProcessExitHelper$")
	cmd.Env = append(os.Environ(), packedProcessExitHelperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := &packedProcessState{key: "cell-dead", cmd: cmd}
	memberA := &managedExt{config: ExtConfig{Name: "member-a"}, host: host, packedCellKey: "cell-dead", packedProcess: process}
	memberB := &managedExt{config: ExtConfig{Name: "member-b"}, host: host, packedCellKey: "cell-dead", packedProcess: process}
	host.exts["member-a"] = memberA
	host.exts["member-b"] = memberB
	go host.watchPackedProcess(process)
	select {
	case <-quarantined:
	case <-t.Context().Done():
		t.Fatal("packed process death did not quarantine cell")
	}
	if host.QuarantinedCells()["cell-dead"] == "" {
		t.Fatal("packed process death has no quarantine reason")
	}
	if host.exts["member-a"] != nil || host.exts["member-b"] != nil {
		t.Fatalf("dead packed cell remained registered: %#v", host.exts)
	}
}

func TestHeartbeatRunsWhileLiveExtensionStateIsHeld(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("provider", host, connOptions{Clock: clock, HeartbeatInterval: 10 * time.Second, HeartbeatTimeout: 3 * time.Second})
	conn.Start(t.Context())
	release := conn.holdLiveness()
	clock.advance(t, 10*time.Second)
	ping := readLivenessEnvelope(t, peer)
	if ping.Type != MsgPing {
		t.Fatalf("live-state heartbeat = %+v", ping)
	}
	clock.expectTimer(t, 3*time.Second)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: ping.Ping.Nonce}})
	clock.waitForTimer(t, 10*time.Second)
	select {
	case <-conn.heartbeatIdle:
	default:
	}
	release()
	clock.elapse(10 * time.Second)
	select {
	case <-conn.heartbeatIdle:
	case <-t.Context().Done():
		t.Fatal("released live state did not return heartbeat machine to idle")
	}
	if got := conn.heartbeatID.Load(); got != 1 {
		t.Fatalf("released live state emitted heartbeat %d, want 1", got)
	}
}

func TestCancelledEventHandlerGenerationCanRunAgain(t *testing.T) {
	clock := newManualLivenessClock()
	hostEnd, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("events", hostEnd, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	host := NewHost(t.TempDir())
	managed := &managedExt{config: ExtConfig{Name: "events"}, host: host, conn: conn}
	handler := managed.makeEventHandler("turn_end", 7)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, err := handler(map[string]any{"turn": 1}, ctx); result <- err }()
	request := readLivenessEnvelope(t, peer)
	cancel()
	cancelFrame := readLivenessEnvelope(t, peer)
	if cancelFrame.Type != MsgCancel || cancelFrame.Cancel.RequestID != request.ID {
		t.Fatalf("event cancellation = %+v", cancelFrame)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("event error = %T %v", err, err)
	}

	second := make(chan error, 1)
	go func() { _, err := handler(map[string]any{"turn": 2}, t.Context()); second <- err }()
	secondRequest := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: secondRequest.ID, Response: &ResponsePayload{}})
	if err := <-second; err != nil {
		t.Fatalf("handler generation remained disabled: %v", err)
	}
}

func TestBlockedUserCommandWaitsWhileHeartbeatStaysHealthy(t *testing.T) {
	clock := newManualLivenessClock()
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("interactive-command", host, connOptions{Clock: clock, HeartbeatInterval: 2 * time.Second, HeartbeatTimeout: time.Second})
	conn.Start(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "configure"}})
		result <- err
	}()
	request := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgRequestState, RequestState: &RequestStatePayload{RequestID: request.ID, State: "blocked", Reason: "user"}})
	for range 4 {
		select {
		case err := <-result:
			t.Fatalf("blocked:user request ended without response: %v", err)
		default:
		}
		clock.advance(t, 2*time.Second)
		ping := readLivenessEnvelope(t, peer)
		if ping.Type != MsgPing || ping.Ping == nil {
			t.Fatalf("blocked request ended before heartbeat: %+v", ping)
		}
		clock.expectTimer(t, time.Second)
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: ping.Ping.Nonce}})
	}
	select {
	case err := <-result:
		t.Fatalf("blocked:user command ended without response: %v", err)
	default:
	}
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{}})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestPackedMemberHeartbeatFailureDisablesOnlyLogicalMember(t *testing.T) {
	clock := newManualLivenessClock()
	hostA, peerA := net.Pipe()
	defer func() { _ = peerA.Close() }()
	hostB, peerB := net.Pipe()
	defer func() { _ = peerB.Close() }()
	connA := newConnWithOptions("member-a", hostA, connOptions{Clock: clock, HeartbeatInterval: 2 * time.Second, HeartbeatTimeout: time.Second})
	connB := newConnWithOptions("member-b", hostB, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	connA.Start(t.Context())
	connB.Start(t.Context())
	host := NewHost(t.TempDir())
	disabled := make(chan string, 1)
	host.SetCrashHandler(func(name string, _ time.Duration, isDisabled bool, _ string) {
		if isDisabled {
			disabled <- name
		}
	})
	memberA := &managedExt{config: ExtConfig{Name: "member-a"}, host: host, conn: connA, packedCellKey: "cell-live"}
	memberB := &managedExt{config: ExtConfig{Name: "member-b"}, host: host, conn: connB, packedCellKey: "cell-live"}
	host.exts["member-a"] = memberA
	host.exts["member-b"] = memberB
	go host.handleIncoming(memberA)
	result := startLivenessRequest(t, connA)
	request := readLivenessEnvelope(t, peerA)
	requestBody, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	waitForWriteProgress(t, connA, uint64(4+len(requestBody)))
	clock.advance(t, 2*time.Second)
	if ping := readLivenessEnvelope(t, peerA); ping.Type != MsgPing {
		t.Fatalf("heartbeat = %+v", ping)
	}
	clock.advance(t, time.Second)
	var unresponsive *ExtensionUnresponsiveError
	if err := <-result; !errors.As(err, &unresponsive) {
		t.Fatalf("pending request error = %T %v", err, err)
	}
	select {
	case name := <-disabled:
		if name != "member-a" {
			t.Fatalf("disabled member = %q", name)
		}
	case <-t.Context().Done():
		t.Fatal("heartbeat failure did not route through packed-member supervision")
	}
	if host.exts["member-a"] != nil || host.exts["member-b"] != memberB {
		t.Fatalf("packed registry after heartbeat failure = %#v", host.exts)
	}
	if connB.closed.Load() || host.QuarantinedCells()["cell-live"] != "" {
		t.Fatal("logical heartbeat failure killed sibling or quarantined shared process")
	}
}

// A saturated writer queue makes Send wait for the peer instead of dropping
// the frame; once the peer reads, every frame arrives in order.
func TestSaturatedWriterDeliversEveryFrame(t *testing.T) {
	host, peer := net.Pipe()
	conn := newConnWithOptions("saturated", host, connOptions{Clock: newManualLivenessClock(), HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	defer func() {
		_ = peer.Close()
		_ = conn.Close("test done")
	}()
	total := cap(conn.outCh) * 3
	sent := make(chan error, 1)
	go func() {
		for i := range total {
			if err := conn.Send(&Envelope{Type: MsgCallResult, ID: fmt.Sprintf("c%d", i), CallResult: &CallResultPayload{}}); err != nil {
				sent <- fmt.Errorf("send %d: %w", i, err)
				return
			}
		}
		sent <- nil
	}()
	for i := range total {
		env := readLivenessEnvelope(t, peer)
		if env.ID != fmt.Sprintf("c%d", i) {
			t.Fatalf("frame %d id = %q", i, env.ID)
		}
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}

// A Send waiting on a peer that never reads fails with the connection's
// terminal error once the connection fails, instead of blocking forever.
func TestSaturatedWriterFailsWithConnection(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("stalled", host, connOptions{Clock: newManualLivenessClock(), HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	sent := make(chan error, 1)
	go func() {
		for {
			if err := conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "queued"}}); err != nil {
				sent <- err
				return
			}
		}
	}()
	waitSignal(t, conn.heartbeatReady, "writer start")
	for len(conn.outCh) < cap(conn.outCh) {
		runtime.Gosched()
	}
	failure := &ExtensionUnresponsiveError{Extension: "stalled", Operation: "outstanding request"}
	conn.fail(failure)
	select {
	case err := <-sent:
		if _, ok := errors.AsType[*ExtensionUnresponsiveError](err); !ok {
			t.Fatalf("blocked Send error = %T %v", err, err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Send stayed blocked after the connection failed")
	}
}

func TestCancelledShortcutHandlerGenerationCanRunAgain(t *testing.T) {
	clock := newManualLivenessClock()
	hostEnd, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("shortcuts", hostEnd, connOptions{Clock: clock, HeartbeatInterval: time.Hour})
	conn.Start(t.Context())
	host := NewHost(t.TempDir())
	managed := &managedExt{config: ExtConfig{Name: "shortcuts"}, host: host, conn: conn}
	handler := host.makeShortcutHandler(managed, "ctrl+x")
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- handler(ctx) }()
	request := readLivenessEnvelope(t, peer)
	cancel()
	cancelFrame := readLivenessEnvelope(t, peer)
	if cancelFrame.Type != MsgCancel || cancelFrame.Cancel.RequestID != request.ID {
		t.Fatalf("shortcut cancellation = %+v", cancelFrame)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("shortcut error = %T %v", err, err)
	}

	second := make(chan error, 1)
	go func() { second <- handler(t.Context()) }()
	secondRequest := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: secondRequest.ID, Response: &ResponsePayload{}})
	if err := <-second; err != nil {
		t.Fatalf("handler generation remained disabled: %v", err)
	}
}

type cancellableInputUI struct {
	extension.UIContext
	started   chan struct{}
	cancelled chan struct{}
}

func (u *cancellableInputUI) Input(ctx context.Context, _, _ string, _ extension.ExtensionUIDialogOptions) (string, error) {
	close(u.started)
	<-ctx.Done()
	close(u.cancelled)
	return "", ctx.Err()
}

func TestParentCancellationCancelsBlockedHostCall(t *testing.T) {
	hostEnd, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := NewConn("parent-cancel", hostEnd)
	conn.Start(t.Context())
	ui := &cancellableInputUI{UIContext: extension.NoopUIContext, started: make(chan struct{}), cancelled: make(chan struct{})}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	host := NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	managed := &managedExt{config: ExtConfig{Name: "parent-cancel"}, host: host, conn: conn}
	go host.handleIncoming(managed)

	ctx, cancel := context.WithCancel(t.Context())
	requestResult := make(chan error, 1)
	go func() {
		_, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "ask"}})
		requestResult <- err
	}()
	parent := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{
		Type: MsgCall,
		ID:   "child-call",
		Call: &CallPayload{Method: "ui.input", ParentRequestID: parent.ID, Args: json.RawMessage(`{"title":"Question"}`)},
	})
	select {
	case <-ui.started:
	case <-t.Context().Done():
		t.Fatal("blocked host call did not start")
	}
	cancel()
	select {
	case <-ui.cancelled:
	case <-t.Context().Done():
		t.Fatal("parent cancellation did not cancel blocked host call")
	}
	if err := <-requestResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("parent request error = %v", err)
	}
	seenCancel := false
	seenChildResult := false
	for !seenCancel || !seenChildResult {
		env := readLivenessEnvelope(t, peer)
		switch env.Type {
		case MsgCancel:
			seenCancel = env.Cancel != nil && env.Cancel.RequestID == parent.ID
		case MsgCallResult:
			seenChildResult = env.ID == "child-call"
		}
	}
}

func TestIsolatedHeartbeatFailureRoutesThroughSupervisor(t *testing.T) {
	clock := newManualLivenessClock()
	hostEnd, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn := newConnWithOptions("isolated", hostEnd, connOptions{Clock: clock, HeartbeatInterval: 2 * time.Second, HeartbeatTimeout: time.Second})
	conn.Start(t.Context())
	host := NewHost(t.TempDir())
	crash := make(chan struct {
		delay    time.Duration
		disabled bool
		reason   string
	}, 1)
	host.SetCrashHandler(func(_ string, delay time.Duration, disabled bool, reason string) {
		crash <- struct {
			delay    time.Duration
			disabled bool
			reason   string
		}{delay, disabled, reason}
	})
	managed := &managedExt{
		config:     ExtConfig{Name: "isolated", Path: "/does/not/run"},
		host:       host,
		conn:       conn,
		supervisor: NewSupervisor(SupervisorConfig{MaxCrashes: 1, InitialDelay: time.Hour}),
	}
	host.exts["isolated"] = managed
	go host.handleIncoming(managed)
	result := startLivenessRequest(t, conn)
	request := readLivenessEnvelope(t, peer)
	requestBody, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	waitForWriteProgress(t, conn, uint64(4+len(requestBody)))
	clock.advance(t, 2*time.Second)
	_ = readLivenessEnvelope(t, peer)
	clock.advance(t, time.Second)
	var unresponsive *ExtensionUnresponsiveError
	if err := <-result; !errors.As(err, &unresponsive) {
		t.Fatalf("pending request error = %T %v", err, err)
	}
	select {
	case got := <-crash:
		if !got.disabled || got.delay != 0 || !strings.Contains(got.reason, "extension_unresponsive") {
			t.Fatalf("supervisor decision = %+v", got)
		}
	case <-t.Context().Done():
		t.Fatal("heartbeat failure bypassed supervisor")
	}
	host.shuttingDown.Store(true)
}

func TestPackedMemberTransportClosureDoesNotProveProcessDeath(t *testing.T) {
	hostA, peerA := net.Pipe()
	hostB, peerB := net.Pipe()
	defer func() { _ = peerB.Close() }()
	connA := NewConn("member-a", hostA)
	connB := NewConn("member-b", hostB)
	connA.Start(t.Context())
	connB.Start(t.Context())
	host := NewHost(t.TempDir())
	type crashEvent struct {
		name   string
		reason string
	}
	disabled := make(chan crashEvent, 1)
	host.SetCrashHandler(func(name string, _ time.Duration, isDisabled bool, reason string) {
		if isDisabled {
			disabled <- crashEvent{name, reason}
		}
	})
	process := &packedProcessState{key: "cell-live"}
	memberA := &managedExt{config: ExtConfig{Name: "member-a"}, host: host, conn: connA, packedCellKey: "cell-live", packedProcess: process, stderrLogPath: "/tmp/pig-packed-cell-live-fake.log"}
	memberB := &managedExt{config: ExtConfig{Name: "member-b"}, host: host, conn: connB, packedCellKey: "cell-live", packedProcess: process}
	host.exts["member-a"] = memberA
	host.exts["member-b"] = memberB
	go host.handleIncoming(memberA)
	_ = peerA.Close()
	var got crashEvent
	select {
	case got = <-disabled:
		if got.name != "member-a" {
			t.Fatalf("disabled member = %q", got.name)
		}
	case <-t.Context().Done():
		t.Fatal("logical transport closure was not surfaced")
	}
	// This is the regression case for ATTACK-POINTS #5: a closed connection
	// does not prove the shared process died, so the notice must not claim
	// the process "remained alive" (a lie whenever the process actually did
	// die and this handler won the race against watchPackedProcess). It must
	// also surface the stderr log path (ATTACK-POINTS #9) instead of leaving
	// it undiscoverable.
	const want = "packed member connection closed (stderr: /tmp/pig-packed-cell-live-fake.log)"
	if got.reason != want {
		t.Fatalf("crash reason = %q, want %q", got.reason, want)
	}
	if strings.Contains(got.reason, "remained alive") {
		t.Fatalf("crash reason falsely claims the shared process remained alive: %q", got.reason)
	}
	if host.exts["member-a"] != nil || host.exts["member-b"] != memberB {
		t.Fatalf("packed registry after logical closure = %#v", host.exts)
	}
	if connB.closed.Load() || host.QuarantinedCells()["cell-live"] != "" {
		t.Fatal("one socket closure killed sibling or quarantined unproven process death")
	}
}
