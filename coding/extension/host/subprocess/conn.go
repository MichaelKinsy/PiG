package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// livenessClock supplies monotonic timers to the connection state machine.
// Tests inject a manual clock; production uses the runtime monotonic clock.
type livenessTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type livenessClock interface {
	NewTimer(time.Duration) livenessTimer
}

type realLivenessClock struct{}
type realLivenessTimer struct{ timer *time.Timer }

func (realLivenessClock) NewTimer(d time.Duration) livenessTimer {
	return realLivenessTimer{timer: time.NewTimer(d)}
}
func (t realLivenessTimer) C() <-chan time.Time { return t.timer.C }
func (t realLivenessTimer) Stop() bool          { return t.timer.Stop() }

// pig divergence (D56): heartbeat interval and pong deadline are the host's
// only liveness bound on a subprocess; upstream extensions run in process.
const (
	defaultHeartbeatInterval = 2 * time.Second
	// Five seconds is deliberately conservative for local IPC. It matches the
	// established renderer freeze boundary and tolerates scheduler contention;
	// request inactivity remains a separate signal and is never renewed by pong.
	defaultHeartbeatTimeout = 5 * time.Second
)

// connOptions configures host-owned transport liveness. Durations are host
// policy, never extension configuration.
type connOptions struct {
	Clock             livenessClock
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration
}

// ExtensionUnresponsiveError reports a missed dispatcher heartbeat.
type ExtensionUnresponsiveError struct {
	Extension string
	RequestID string
	Operation string
}

func (e *ExtensionUnresponsiveError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("extension_unresponsive: extension %q did not answer heartbeat for request %q (%s)", e.Extension, e.RequestID, e.Operation)
	}
	return fmt.Sprintf("extension_unresponsive: extension %q did not answer heartbeat while %s", e.Extension, e.Operation)
}

// HandlerStalledError reports a request-inactivity lease expiration.
type HandlerStalledError struct {
	Extension        string
	Operation        string
	HeartbeatHealthy bool
}

func (e *HandlerStalledError) Error() string {
	return fmt.Sprintf("handler_stalled: extension %q %s made no request progress (heartbeat healthy: %t)", e.Extension, e.Operation, e.HeartbeatHealthy)
}

// TransportError reports a concrete socket read or write failure.
type TransportError struct {
	Extension string
	Operation string
	Err       error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("extension_transport: extension %q %s failed: %v", e.Extension, e.Operation, e.Err)
}
func (e *TransportError) Unwrap() error { return e.Err }

// Conn manages one extension socket with reader, writer, and heartbeat
// goroutines. Only the reader reads the socket, and only the writer writes it.
// pig-specific: no upstream equivalent.
type Conn struct {
	name    string
	conn    net.Conn
	closing atomic.Bool
	closed  atomic.Bool

	// Outgoing message queue. Writer goroutine drains this. A frame that is
	// queued or being written is outstanding work, so the heartbeat runs while
	// the peer owes the host a read. writeProgress counts bytes the peer has
	// accepted, which proves liveness while a large frame drains slowly.
	outCh         chan outboundFrame
	writeProgress atomic.Uint64

	// Pending request tracking: maps request ID → response channel, and
	// request ID → the sink for that request's tool_update notifications.
	pendingMu sync.Mutex
	pending   map[string]chan *Envelope
	updates   map[string]func(json.RawMessage)

	// Incoming messages that aren't responses go here for the host to process.
	inCh chan *Envelope

	stateMu       sync.Mutex
	requestStates map[string]chan RequestStatePayload

	hostCallMu      sync.Mutex
	hostCalls       map[string]map[string]context.CancelFunc
	cancelledParent map[string]struct{}

	// nextID is an atomic counter for generating request IDs.
	nextID atomic.Uint64

	// lastLargeSize/lastLargeAtNs record the most recent frame written to
	// the peer above legacyFrameSize. A disconnect that follows can then be
	// attributed to a binary built against an SDK whose receive cap is below
	// the host's, rather than reported as a mystery exit. See
	// RecentOversizedFrame.
	lastLargeSize atomic.Uint64
	lastLargeAtNs atomic.Int64

	// pig divergence (D56): subprocess liveness has no upstream in-process equivalent.
	// Liveness state machine:
	// idle --work/live-state--> waiting --interval--> awaiting-pong
	// awaiting-pong --matching-pong--> waiting
	// waiting/awaiting-pong --no-work--> idle
	// awaiting-pong --deadline--> failed(extension_unresponsive)
	// Any state --transport error/cancel--> closed. Request cancellation removes
	// correlation before a late response can be accepted.
	clock             livenessClock
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	workMu            sync.Mutex
	work              int
	workChanged       chan struct{}
	pongCh            chan string
	heartbeatReady    chan struct{}
	heartbeatIdle     chan struct{}
	heartbeatID       atomic.Uint64
	failOnce          sync.Once
	failureMu         sync.Mutex
	failure           error

	// cancel signals all goroutines to stop.
	cancel context.CancelFunc
	done   chan struct{} // closed when reader, writer, and heartbeat exit
}

// NewConn wraps a connected socket into a managed connection.
// Call [Conn.Start] to begin read/write goroutines.
func NewConn(name string, c net.Conn) *Conn {
	return newConnWithOptions(name, c, connOptions{})
}

func newConnWithOptions(name string, c net.Conn, options connOptions) *Conn {
	if options.Clock == nil {
		options.Clock = realLivenessClock{}
	}
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = defaultHeartbeatInterval
	}
	if options.HeartbeatTimeout <= 0 {
		options.HeartbeatTimeout = defaultHeartbeatTimeout
	}
	return &Conn{
		name:              name,
		conn:              c,
		outCh:             make(chan outboundFrame, 64),
		pending:           make(map[string]chan *Envelope),
		updates:           make(map[string]func(json.RawMessage)),
		inCh:              make(chan *Envelope, 32),
		done:              make(chan struct{}),
		requestStates:     make(map[string]chan RequestStatePayload),
		hostCalls:         make(map[string]map[string]context.CancelFunc),
		cancelledParent:   make(map[string]struct{}),
		clock:             options.Clock,
		heartbeatInterval: options.HeartbeatInterval,
		heartbeatTimeout:  options.HeartbeatTimeout,
		workChanged:       make(chan struct{}, 1),
		pongCh:            make(chan string, 8),
		heartbeatReady:    make(chan struct{}),
		heartbeatIdle:     make(chan struct{}, 1),
	}
}

// Start launches the reader and writer goroutines. The provided context
// controls their lifetime. Cancellation stops all three goroutines.
func (c *Conn) Start(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		c.readLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		c.writeLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		c.heartbeatLoop(ctx)
	}()
	go func() {
		<-ctx.Done()
		if c.closing.Load() {
			_ = c.conn.Close()
			return
		}
		c.fail(ctx.Err())
	}()
	go func() {
		wg.Wait()
		close(c.done)
	}()
}

type outboundFrame struct {
	data   []byte
	result chan error
	// work marks a frame counted as outstanding work until written. Heartbeat
	// pings are not work: an idle connection must be able to go quiet.
	work bool
}

// FrameTooLargeError reports that a marshaled envelope exceeds MaxFrameSize and
// was therefore not sent. Every SDK reader rejects a frame above MaxFrameSize,
// so writing one would silently kill even a current-SDK extension; callers that
// can degrade gracefully (e.g. a call result) should send a small error frame
// instead.
type FrameTooLargeError struct {
	Size int
	Max  int
}

func (e *FrameTooLargeError) Error() string {
	return fmt.Sprintf("frame of %d bytes exceeds the %d byte IPC frame limit", e.Size, e.Max)
}

// Send queues a message without waiting for the socket write. A full queue
// waits for the writer rather than dropping the frame: a lost call result or
// notification would leave the extension waiting forever. The wait ends when
// the peer drains the queue or the connection fails, which the heartbeat
// guarantees for a peer that stops reading.
func (c *Conn) Send(env *Envelope) error {
	data, err := c.marshalEnvelope(env)
	if err != nil {
		return err
	}
	return c.enqueue(context.Background(), outboundFrame{data: data, work: true})
}

// enqueue commits frame to the writer queue, waiting for space until ctx ends
// or the connection closes.
func (c *Conn) enqueue(ctx context.Context, frame outboundFrame) error {
	if frame.work {
		c.beginWork()
	}
	select {
	case c.outCh <- frame:
		return nil
	case <-ctx.Done():
		if frame.work {
			c.endWork()
		}
		return ctx.Err()
	case <-c.done:
		if frame.work {
			c.endWork()
		}
		if failure := c.failureError(); failure != nil {
			return failure
		}
		return errors.New("connection closed")
	}
}

func (c *Conn) sendAndWait(ctx context.Context, env *Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := c.marshalEnvelope(env)
	if err != nil {
		return err
	}
	result := make(chan error, 1)
	if err := c.enqueue(ctx, outboundFrame{data: data, result: result, work: true}); err != nil {
		return err
	}
	// Queueing commits the frame. Wait for the writer result even if ctx is
	// cancelled now, because the peer may already own the request and must
	// receive the correlated cancel from Request. A connection that ends first
	// ends the wait with its terminal error: no writer remains to answer.
	select {
	case err := <-result:
		return err
	case <-c.done:
		select {
		case err := <-result:
			return err
		default:
		}
		if failure := c.failureError(); failure != nil {
			return failure
		}
		return errors.New("connection closed")
	}
}

func (c *Conn) marshalEnvelope(env *Envelope) ([]byte, error) {
	if c.closing.Load() || c.closed.Load() {
		// A caller that keeps sending after the connection failed (for example
		// a retry loop racing the writer's queue drain: a Send already queued
		// when the connection fails can still succeed once the writer starts
		// draining on ctx.Done, so the caller's next Send lands here) must see
		// the connection's real terminal error, not a generic placeholder that
		// masks it.
		if failure := c.failureError(); failure != nil {
			return nil, failure
		}
		return nil, errors.New("connection closed")
	}
	data, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	if len(data) > MaxFrameSize {
		return nil, &FrameTooLargeError{Size: len(data), Max: MaxFrameSize}
	}
	return data, nil
}

// Request sends a message and waits for the correlated response. Blocks until
// the response arrives or the context expires.
func (c *Conn) Request(ctx context.Context, env *Envelope) (*Envelope, error) {
	return c.request(ctx, env, 0, "")
}

// requestWithInactivity applies a host-owned activity lease. Heartbeat pongs
// prove transport liveness but never renew this lease.
func (c *Conn) requestWithInactivity(ctx context.Context, env *Envelope, inactivity time.Duration, operation string) (*Envelope, error) {
	return c.request(ctx, env, inactivity, operation)
}

// requestWithUpdates is Request for a tool call whose extension streams
// partial results. onUpdate runs for each NotifyToolUpdate the request
// receives, in frame order, off the read loop, and every update that arrived
// before the response has run when this returns.
func (c *Conn) requestWithUpdates(ctx context.Context, env *Envelope, onUpdate func(json.RawMessage)) (*Envelope, error) {
	if env.ID == "" {
		env.ID = fmt.Sprintf("r%d", c.nextID.Add(1))
	}
	updates := newCallLanes()
	var queued sync.WaitGroup
	c.pendingMu.Lock()
	c.updates[env.ID] = func(result json.RawMessage) {
		queued.Add(1)
		updates.push("", func() {
			defer queued.Done()
			onUpdate(result)
		})
	}
	c.pendingMu.Unlock()
	resp, err := c.request(ctx, env, 0, "")
	c.pendingMu.Lock()
	delete(c.updates, env.ID)
	c.pendingMu.Unlock()
	queued.Wait()
	return resp, err
}

func (c *Conn) request(ctx context.Context, env *Envelope, inactivity time.Duration, operation string) (*Envelope, error) {
	if c.closed.Load() {
		return nil, errors.New("connection closed")
	}

	// Assign a unique ID if not already set.
	if env.ID == "" {
		env.ID = fmt.Sprintf("r%d", c.nextID.Add(1))
	}
	if operation == "" {
		operation = requestOperation(env)
	}

	// Register the pending response channel.
	respCh := make(chan *Envelope, 1)
	c.pendingMu.Lock()
	c.pending[env.ID] = respCh
	c.pendingMu.Unlock()
	stateCh := make(chan RequestStatePayload, 8)
	c.stateMu.Lock()
	c.requestStates[env.ID] = stateCh
	c.stateMu.Unlock()

	// Clean up on exit.
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, env.ID)
		c.pendingMu.Unlock()
		c.stateMu.Lock()
		delete(c.requestStates, env.ID)
		c.stateMu.Unlock()
		c.hostCallMu.Lock()
		delete(c.cancelledParent, env.ID)
		c.hostCallMu.Unlock()
	}()

	// Own request liveness before queueing the frame. The frame itself also owns
	// work until its write completes; overlapping the two keeps the connection
	// continuously active instead of exposing a zero-work gap between write
	// completion and response waiting.
	c.beginWork()
	defer c.endWork()

	// Send the request.
	if err := c.sendAndWait(ctx, env); err != nil {
		return nil, err
	}
	// Admission ends only after the handler reaches an observable suspension
	// boundary or finishes, not when its request reaches the socket.
	defer invocation.Acknowledge(ctx)

	var inactivityTimer livenessTimer
	var inactivityC <-chan time.Time
	if inactivity > 0 {
		inactivityTimer = c.clock.NewTimer(inactivity)
		inactivityC = inactivityTimer.C()
		defer inactivityTimer.Stop()
	}
	// Wait for response or meaningful request activity.
	for {
		select {
		case resp := <-respCh:
			if resp != nil && resp.Response != nil && resp.Response.Error != nil {
				switch resp.Response.Error.Code {
				case "extension_unresponsive":
					return nil, &ExtensionUnresponsiveError{Extension: c.name, RequestID: env.ID, Operation: operation}
				case "extension_transport":
					c.failureMu.Lock()
					failure := c.failure
					c.failureMu.Unlock()
					if failure != nil {
						return nil, failure
					}
				}
			}
			return resp, nil
		case state := <-stateCh:
			if state.State == "blocked" || state.State == "completed" {
				invocation.Acknowledge(ctx)
			}
			if inactivity > 0 && (state.State == "started" || state.State == "progress" || state.State == "blocked") {
				inactivityTimer.Stop()
				inactivityTimer = c.clock.NewTimer(inactivity)
				inactivityC = inactivityTimer.C()
			}
		case <-inactivityC:
			c.cancelHostCalls(env.ID)
			_ = c.Send(&Envelope{Type: MsgCancel, ID: env.ID, Cancel: &CancelPayload{RequestID: env.ID, Reason: "handler inactivity"}})
			return nil, &HandlerStalledError{Extension: c.name, Operation: operation, HeartbeatHealthy: !c.closed.Load()}
		case <-ctx.Done():
			c.cancelHostCalls(env.ID)
			_ = c.Send(&Envelope{
				Type: MsgCancel,
				ID:   env.ID,
				Cancel: &CancelPayload{
					RequestID: env.ID,
					Reason:    ctx.Err().Error(),
				},
			})
			return nil, ctx.Err()
		}
	}
}

// Incoming returns the channel of non-response messages from the extension.
// The host reads from this to handle calls, widget pushes, etc.
func (c *Conn) Incoming() <-chan *Envelope {
	return c.inCh
}

// Close gracefully shuts down the connection. Sends a shutdown message if
// possible, then closes the socket.
func (c *Conn) Close(reason string) error {
	if !c.closing.CompareAndSwap(false, true) {
		return nil // already closed
	}

	// Best-effort shutdown message. Close has already marked the connection
	// closed to new callers, so queue this final barrier directly instead of
	// routing it through Send (which correctly rejects closed connections).
	shutdown, _ := json.Marshal(&Envelope{
		Type:     MsgShutdown,
		Shutdown: &ShutdownPayload{Reason: reason},
	})
	// The shutdown frame is work, so a peer that stops reading is bounded by
	// the heartbeat instead of holding Close open.
	writeResult := make(chan error, 1)
	queued := false
	c.beginWork()
	select {
	case c.outCh <- outboundFrame{data: shutdown, result: writeResult, work: true}:
		queued = true
	default:
		c.endWork()
	}
	var shutdownErr error
	if queued {
		select {
		case shutdownErr = <-writeResult:
		case <-c.done:
			shutdownErr = c.failureError()
		}
	}
	c.closed.Store(true)

	if c.cancel != nil {
		c.cancel()
	}
	err := c.conn.Close()

	// Wait for goroutines to exit.
	<-c.done

	// Fail any pending requests.
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		select {
		case ch <- &Envelope{
			Type: MsgResponse,
			ID:   id,
			Response: &ResponsePayload{
				Error: &ErrorInfo{Message: "connection closed"},
			},
		}:
		default:
		}
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()

	if err != nil {
		return err
	}
	return shutdownErr
}

// failureError returns the terminal transport or liveness error, if any.
func (c *Conn) failureError() error {
	c.failureMu.Lock()
	defer c.failureMu.Unlock()
	return c.failure
}

// Done returns a channel that closes when all connection goroutines have exited.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// Name returns the extension name this connection serves.
func (c *Conn) Name() string {
	return c.name
}

// frameSkewWindow bounds how long after an oversized write a disconnect is
// still attributed to frame-cap skew.
const frameSkewWindow = 5 * time.Second

// RecentOversizedFrame reports the size of the most recent frame the host
// wrote to this extension above the legacy frame cap, if it was written
// within frameSkewWindow. The host uses it to explain a disconnect that a
// binary built against an older SDK (receive cap below the host's) causes.
func (c *Conn) RecentOversizedFrame() (uint64, bool) {
	at := c.lastLargeAtNs.Load()
	if at == 0 || time.Since(time.Unix(0, at)) > frameSkewWindow {
		return 0, false
	}
	return c.lastLargeSize.Load(), true
}

// ── Internal ─────────────────────────────────────────────────────────────────

// readLoop reads length-prefixed JSON frames from the socket and dispatches them.
func (c *Conn) readLoop(ctx context.Context) {
	defer func() {
		if c.cancel != nil {
			c.cancel()
		}
		// On reader exit, close inCh so the host knows we're done.
		close(c.inCh)
	}()

	for {
		// Check context before each frame.
		if ctx.Err() != nil || c.closed.Load() {
			return
		}

		// Read 4-byte big-endian length. No deadline on the length read -
		// we use a blocking read and check ctx between frames. This avoids
		// the partial-read bug: if we set a deadline and ReadFull gets 2 of
		// 4 bytes before timeout, continuing would discard those bytes and
		// permanently break framing.
		//
		// Instead, we close the socket from Close() which unblocks ReadFull
		// with an error.
		var lenBuf [4]byte
		_, err := io.ReadFull(c.conn, lenBuf[:])
		if err != nil {
			if ctx.Err() != nil || c.closed.Load() {
				return
			}
			// Real read error: connection lost.
			c.fail(&TransportError{Extension: c.name, Operation: "read", Err: err})
			return
		}

		msgLen := binary.BigEndian.Uint32(lenBuf[:])
		if msgLen == 0 || msgLen > MaxFrameSize {
			c.fail(&TransportError{Extension: c.name, Operation: "read frame", Err: fmt.Errorf("invalid frame size %d", msgLen)})
			return
		}

		// Read the JSON payload.
		buf := make([]byte, msgLen)
		_, err = io.ReadFull(c.conn, buf)
		if err != nil {
			c.fail(&TransportError{Extension: c.name, Operation: "read", Err: err})
			return
		}

		// Decode the envelope.
		var env Envelope
		if err := json.Unmarshal(buf, &env); err != nil {
			c.fail(&TransportError{Extension: c.name, Operation: "decode frame", Err: err})
			return
		}

		if env.Type == MsgRequestState && env.RequestState != nil {
			c.stateMu.Lock()
			ch := c.requestStates[env.RequestState.RequestID]
			c.stateMu.Unlock()
			if ch != nil {
				select {
				case ch <- *env.RequestState:
				default:
				}
			}
			continue
		}
		if env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == NotifyToolUpdate {
			c.deliverToolUpdate(env.Notify.Args)
			continue
		}
		if env.Type == MsgPong && env.Pong != nil {
			select {
			case c.pongCh <- env.Pong.Nonce:
			default:
			}
			continue
		}
		// Route responses to pending requests.
		if env.Type == MsgResponse || env.Type == MsgCallResult {
			if env.ID != "" {
				c.pendingMu.Lock()
				ch, ok := c.pending[env.ID]
				c.pendingMu.Unlock()
				if ok {
					select {
					case ch <- &env:
					default:
					}
				}
			}
			// Response-shaped frames never become unsolicited host messages.
			// A missing correlation means cancellation already won; discard the
			// late response so it cannot fill the incoming request queue.
			continue
		}

		// Everything else goes to the host.
		select {
		case c.inCh <- &env:
		case <-ctx.Done():
			return
		}
	}
}

// writeLoop serializes outgoing messages to the socket.
func (c *Conn) writeLoop(ctx context.Context) {
	write := func(frame outboundFrame) error {
		err := c.writeFrame(frame.data, frame.work)
		if frame.work {
			c.endWork()
		}
		if err != nil {
			err = &TransportError{Extension: c.name, Operation: "write", Err: err}
		}
		if frame.result != nil {
			frame.result <- err
		}
		return err
	}
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case frame := <-c.outCh:
					_ = write(frame)
				default:
					return
				}
			}
		case frame := <-c.outCh:
			if err := write(frame); err != nil {
				c.fail(err)
				c.abandonQueuedFrames(err)
				return
			}
		}
	}
}

// abandonQueuedFrames completes every frame still queued when the writer
// stops after a write failure, so no waiter blocks on a frame nothing will
// write.
func (c *Conn) abandonQueuedFrames(err error) {
	for {
		select {
		case frame := <-c.outCh:
			if frame.work {
				c.endWork()
			}
			if frame.result != nil {
				frame.result <- err
			}
		default:
			return
		}
	}
}

// deliverToolUpdate hands a partial tool result to the request it names. An
// update for a request that already finished is discarded, like a late
// response.
func (c *Conn) deliverToolUpdate(args json.RawMessage) {
	var update ToolUpdatePayload
	if json.Unmarshal(args, &update) != nil {
		return
	}
	// The sink only queues, so it runs under the lock that also removes it:
	// no update is queued after its request stopped accepting them.
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if sink := c.updates[update.RequestID]; sink != nil {
		sink(update.Result)
	}
}

func (c *Conn) hostCallContext(parentRequestID, callID string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	if parentRequestID == "" {
		return ctx, cancel
	}
	c.pendingMu.Lock()
	_, pending := c.pending[parentRequestID]
	if !pending {
		c.pendingMu.Unlock()
		cancel()
		return ctx, func() {}
	}
	c.hostCallMu.Lock()
	c.pendingMu.Unlock()
	if _, cancelled := c.cancelledParent[parentRequestID]; cancelled {
		c.hostCallMu.Unlock()
		cancel()
		return ctx, func() {}
	}
	calls := c.hostCalls[parentRequestID]
	if calls == nil {
		calls = make(map[string]context.CancelFunc)
		c.hostCalls[parentRequestID] = calls
	}
	calls[callID] = cancel
	c.hostCallMu.Unlock()
	return ctx, func() {
		cancel()
		c.hostCallMu.Lock()
		delete(calls, callID)
		if len(calls) == 0 {
			delete(c.hostCalls, parentRequestID)
		}
		c.hostCallMu.Unlock()
	}
}

func (c *Conn) cancelHostCalls(parentRequestID string) {
	c.hostCallMu.Lock()
	c.cancelledParent[parentRequestID] = struct{}{}
	var cancels []context.CancelFunc
	for _, cancel := range c.hostCalls[parentRequestID] {
		cancels = append(cancels, cancel)
	}
	delete(c.hostCalls, parentRequestID)
	c.hostCallMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (c *Conn) cancelAllHostCalls() {
	c.hostCallMu.Lock()
	var cancels []context.CancelFunc
	for parent, calls := range c.hostCalls {
		c.cancelledParent[parent] = struct{}{}
		for _, cancel := range calls {
			cancels = append(cancels, cancel)
		}
	}
	clear(c.hostCalls)
	c.hostCallMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func requestOperation(env *Envelope) string {
	if env == nil || env.Request == nil {
		return "request"
	}
	name := env.Request.Tool
	if name == "" {
		name = env.Request.Event
	}
	if name == "" {
		return env.Request.Method
	}
	return env.Request.Method + " " + name
}

func (c *Conn) beginWork() {
	c.workMu.Lock()
	c.work++
	c.workMu.Unlock()
	c.signalWorkChanged()
}

func (c *Conn) endWork() {
	c.workMu.Lock()
	if c.work > 0 {
		c.work--
	}
	c.workMu.Unlock()
	c.signalWorkChanged()
}

// holdLiveness keeps heartbeat active for registered live state not represented
// by an outstanding request. The returned release function is idempotent.
func (c *Conn) holdLiveness() func() {
	c.beginWork()
	var once sync.Once
	return func() { once.Do(c.endWork) }
}

func (c *Conn) signalWorkChanged() {
	select {
	case c.workChanged <- struct{}{}:
	default:
	}
}

func (c *Conn) hasWork() bool {
	c.workMu.Lock()
	defer c.workMu.Unlock()
	return c.work > 0
}

func (c *Conn) heartbeatLoop(ctx context.Context) {
	close(c.heartbeatReady)
heartbeat:
	for {
		for !c.hasWork() {
			select {
			case c.heartbeatIdle <- struct{}{}:
			default:
			}
			select {
			case <-ctx.Done():
				return
			case <-c.workChanged:
			}
		}
		select {
		case <-c.workChanged:
			continue
		default:
		}
		interval := c.clock.NewTimer(c.heartbeatInterval)
		fired := false
		for !fired {
			select {
			case <-ctx.Done():
				interval.Stop()
				return
			case <-c.workChanged:
				// A work-count change while the interval is already running
				// (e.g. an unrelated request beginning or its frame finishing
				// its write) does not restart the cadence: only an actual
				// idle transition does. Restarting on every such signal would
				// let a steady trickle of unrelated work starve the interval
				// forever, so re-check only whether work dropped to zero.
				if !c.hasWork() {
					interval.Stop()
					continue heartbeat
				}
			case <-interval.C():
				fired = true
			}
		}
		if !c.hasWork() {
			continue
		}
		nonce := fmt.Sprintf("h%d", c.heartbeatID.Add(1))
		ping, err := c.marshalEnvelope(&Envelope{Type: MsgPing, Ping: &PingPayload{Nonce: nonce}})
		if err != nil {
			c.fail(&TransportError{Extension: c.name, Operation: "heartbeat write", Err: err})
			return
		}
		// The pong deadline covers queueing the ping too: a peer that stops
		// reading leaves the queue full. Bytes the peer accepts meanwhile prove
		// it is alive, so a large frame draining slowly renews the deadline.
		progress := c.writeProgress.Load()
		deadline := c.clock.NewTimer(c.heartbeatTimeout)
		pingQueued := false
		for {
			var queue chan outboundFrame
			if !pingQueued {
				queue = c.outCh
			}
			select {
			case <-ctx.Done():
				deadline.Stop()
				return
			case queue <- outboundFrame{data: ping}:
				pingQueued = true
			case <-c.workChanged:
				if !c.hasWork() {
					deadline.Stop()
					goto next
				}
			case got := <-c.pongCh:
				if got == nonce {
					deadline.Stop()
					goto next
				}
			case <-deadline.C():
				if current := c.writeProgress.Load(); current != progress {
					progress = current
					deadline = c.clock.NewTimer(c.heartbeatTimeout)
					continue
				}
				c.fail(&ExtensionUnresponsiveError{Extension: c.name, Operation: "outstanding request"})
				return
			}
		}
	next:
	}
}

func (c *Conn) fail(err error) {
	c.failOnce.Do(func() {
		c.failureMu.Lock()
		c.failure = err
		c.failureMu.Unlock()
		c.closed.Store(true)
		code := "extension_transport"
		if _, ok := errors.AsType[*ExtensionUnresponsiveError](err); ok {
			code = "extension_unresponsive"
		}
		c.pendingMu.Lock()
		for id, ch := range c.pending {
			select {
			case ch <- &Envelope{Type: MsgResponse, ID: id, Response: &ResponsePayload{Error: &ErrorInfo{Code: code, Message: err.Error()}}}:
			default:
			}
		}
		c.pendingMu.Unlock()
		c.cancelAllHostCalls()
		if c.cancel != nil {
			c.cancel()
		}
		_ = c.conn.Close()
	})
}

// writeFrame writes a length-prefixed JSON frame to the socket.
// Only work frames count toward writeProgress: the heartbeat's own ping says
// nothing about whether the peer is reading.
func (c *Conn) writeFrame(data []byte, countProgress bool) error {
	// Note any frame larger than the legacy cap so a subsequent disconnect
	// can be attributed to an extension whose receive cap is below ours.
	if n := uint64(len(data)); n > legacyFrameSize {
		c.lastLargeSize.Store(n)
		c.lastLargeAtNs.Store(time.Now().UnixNano())
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))

	// No write deadline: a peer that stops reading is detected by the heartbeat
	// (D56), which counts every accepted byte as liveness and closes the
	// socket on failure, unblocking this write.
	if _, err := c.conn.Write(lenBuf[:]); err != nil {
		return err
	}
	if countProgress {
		c.writeProgress.Add(uint64(len(lenBuf)))
	}
	for len(data) > 0 {
		chunk := data[:min(len(data), writeChunk)]
		if _, err := c.conn.Write(chunk); err != nil {
			return err
		}
		if countProgress {
			c.writeProgress.Add(uint64(len(chunk)))
		}
		data = data[len(chunk):]
	}
	return nil
}

// writeChunk bounds one socket write so writeProgress advances while a large
// frame drains.
const writeChunk = 64 * 1024
