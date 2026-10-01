package subprocess

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

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
	// onDispatch records the owner before a request can run in a shared process.
	onDispatch func()
	// inbound observes each decoded frame on the read loop, in wire order, before routing. Cross-process reference accounting uses it so a later release on this connection can never overtake an earlier transmission.
	inbound func(*Envelope)
	// onClosed runs once when the read loop ends.
	onClosed func()
	name     string
	conn     net.Conn
	closing  atomic.Bool
	closed   atomic.Bool

	// Outgoing message queue. Writer goroutine drains this. A frame that is
	// queued or being written is outstanding work, so the heartbeat runs while
	// the peer owes the host a read. writeProgress counts bytes the peer has
	// accepted, which proves liveness while a large frame drains slowly.
	outCh         chan outboundFrame
	writeProgress atomic.Uint64

	// Pending request tracking: maps request ID → response channel, and
	// request ID → the sink for that request's tool_update notifications.
	pendingMu     sync.Mutex
	pending       map[string]chan *Envelope
	updates       map[string]func(json.RawMessage)
	producerTasks sync.WaitGroup

	// Incoming messages that aren't responses go here for the host to process.
	inCh chan *Envelope

	stateMu       sync.Mutex
	requestStates map[string]chan RequestStatePayload
	// suspending counts the suspension frames of a request that the read loop queued for the host and the host has not yet delivered. suspensionsSettled is closed and replaced whenever a count drops.
	suspending         map[string]int
	suspendedRequests  map[string]bool
	suspensionsSettled chan struct{}

	hostCallMu      sync.Mutex
	hostCalls       map[string]map[string]context.CancelFunc
	cancelledParent map[string]struct{}
	// reservedCalls holds the context of each call frame the read loop admitted and the host has not yet started, by call ID.
	reservedCalls map[string]reservedHostCall

	// nextID is an atomic counter for generating request IDs.
	nextID atomic.Uint64

	abortForwards abortForwards

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
	retiring          atomic.Bool
	failureMu         sync.Mutex
	failure           error

	// cancel signals all goroutines to stop.
	cancel   context.CancelFunc
	lifetime context.Context
	done     chan struct{} // closed when reader, writer, and heartbeat exit
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
		name:               name,
		conn:               c,
		outCh:              make(chan outboundFrame, 64),
		pending:            make(map[string]chan *Envelope),
		updates:            make(map[string]func(json.RawMessage)),
		inCh:               make(chan *Envelope, 32),
		done:               make(chan struct{}),
		requestStates:      make(map[string]chan RequestStatePayload),
		suspending:         make(map[string]int),
		suspendedRequests:  make(map[string]bool),
		suspensionsSettled: make(chan struct{}),
		hostCalls:          make(map[string]map[string]context.CancelFunc),
		cancelledParent:    make(map[string]struct{}),
		reservedCalls:      make(map[string]reservedHostCall),
		clock:              options.Clock,
		heartbeatInterval:  options.HeartbeatInterval,
		heartbeatTimeout:   options.HeartbeatTimeout,
		workChanged:        make(chan struct{}, 1),
		pongCh:             make(chan string, 8),
		heartbeatReady:     make(chan struct{}),
		heartbeatIdle:      make(chan struct{}, 1),
	}
}

// Start launches the reader and writer goroutines. The provided context
// controls their lifetime. Cancellation stops all three goroutines.
func (c *Conn) Start(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	c.lifetime = ctx
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

// encodedEnvelope contains complete JSON produced by a host encoder. It is immutable while queued, so one notification can share its bytes across connections.
type encodedEnvelope []byte

func (c *Conn) sendEncoded(data encodedEnvelope) error {
	if err := c.checkSendState(); err != nil {
		return err
	}
	if err := checkFrameSize(data); err != nil {
		return err
	}
	return c.enqueue(context.Background(), outboundFrame{data: data, work: true})
}

func (c *Conn) checkSendState() error {
	if c.closing.Load() || c.closed.Load() {
		// A caller that keeps sending after the connection failed (for example
		// a retry loop racing the writer's queue drain: a Send already queued
		// when the connection fails can still succeed once the writer starts
		// draining on ctx.Done, so the caller's next Send lands here) must see
		// the connection's real terminal error, not a generic placeholder that
		// masks it.
		if failure := c.failureError(); failure != nil {
			return failure
		}
		return errors.New("connection closed")
	}
	return nil
}

func checkFrameSize(data []byte) error {
	if len(data) > MaxFrameSize {
		return &FrameTooLargeError{Size: len(data), Max: MaxFrameSize}
	}
	return nil
}

func (c *Conn) marshalEnvelope(env *Envelope) ([]byte, error) {
	if err := c.checkSendState(); err != nil {
		return nil, err
	}
	if err := checkResultFrameSize(env); err != nil {
		return nil, err
	}
	data, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	if err := checkFrameSize(data); err != nil {
		return nil, err
	}
	return data, nil
}

// checkResultFrameSize rejects a call result whose frame the encoder would build above the frame limit, without building it. Marshaling validates the raw JSON and copies every byte of it before the size check fails, and a rejected payload must not cost memory proportional to itself. The encoder removes only whitespace outside strings and adds bytes only by escaping <, >, & (five bytes each) and U+2028, U+2029 (three bytes each), so one pass over the raw bytes gives the exact size of json.Marshal(env) for valid JSON. Invalid JSON takes the marshal path so its error keeps the "marshal envelope" classification instead of becoming a size error. Sizes are int64 so a result that encodes above the largest int of a 32-bit build is still measured as oversized.
func checkResultFrameSize(env *Envelope) error {
	if env.CallResult == nil {
		return nil
	}
	raw := env.CallResult.Result
	// The encoder grows a byte to at most six, so the frame is at most six times the bytes it carries plus the envelope's fixed text. Frames that cannot reach the limit skip the measurement; the general check in marshalEnvelope still bounds them.
	if int64(len(raw))*6+resultShellBound(env) <= MaxFrameSize {
		return nil
	}
	resultSize := encodedRawSize(raw)
	shell := *env
	shell.CallResult = &CallResultPayload{Result: json.RawMessage("0"), Error: env.CallResult.Error}
	framed, err := json.Marshal(&shell)
	if err != nil {
		return nil
	}
	size := int64(len(framed)) - 1 + resultSize
	if size <= MaxFrameSize || !json.Valid(raw) {
		return nil
	}
	return oversizedFrameError(size)
}

// oversizedFrameError reports a frame size that may exceed the largest int of a 32-bit build; the reported size saturates there.
func oversizedFrameError(size int64) *FrameTooLargeError {
	return &FrameTooLargeError{Size: int(min(size, math.MaxInt)), Max: MaxFrameSize}
}

// resultShellBound is an upper bound on the encoded bytes of a call result envelope other than its raw result: the string fields grow at most six-fold when escaped, and the field names and punctuation take a fixed amount.
func resultShellBound(env *Envelope) int64 {
	const fixed = 512
	text := int64(len(env.Type) + len(env.ID))
	if e := env.CallResult.Error; e != nil {
		text += int64(len(e.Code) + len(e.Message) + len(e.Stack))
	}
	return text*6 + fixed
}

// encodedRawSize is the length the encoder emits for raw when raw is valid JSON: its length less the whitespace outside strings, plus the HTML and line-separator escapes.
func encodedRawSize(raw []byte) int64 {
	size := int64(len(raw))
	inString, escaped := false, false
	for i := range raw {
		c := raw[i]
		switch {
		case c == '<' || c == '>' || c == '&':
			size += 5
		case c == 0xE2 && i+2 < len(raw) && raw[i+1] == 0x80 && raw[i+2]&^1 == 0xA8:
			size += 3
		}
		switch {
		case inString && escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			size--
		}
	}
	return size
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
		if failure := c.failureError(); failure != nil {
			return nil, failure
		}
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
		delete(c.suspendedRequests, env.ID)
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
	if c.onDispatch != nil {
		c.onDispatch()
	}
	if err := c.sendAndWait(ctx, env); err != nil {
		return nil, err
	}
	// Admission ends only after the handler reaches an observable suspension
	// boundary or finishes, not when its request reaches the socket.
	defer invocation.Complete(ctx)

	var inactivityTimer livenessTimer
	var inactivityC <-chan time.Time
	if inactivity > 0 {
		inactivityTimer = c.clock.NewTimer(inactivity)
		inactivityC = inactivityTimer.C()
		defer inactivityTimer.Stop()
	}
	// Wait for response or meaningful request activity.
	cancelled := ctx.Done()
	suspensionApplied := false
	for {
		select {
		case resp := <-respCh:
			// A state frame that preceded the response was queued first; apply it before the response is delivered. The host delivers a suspension only after every earlier call of the connection applied, so the response waits for it only while a suspension can change what the response does.
			if requestSuspensionDecides(ctx) {
				c.awaitSuspensions(ctx, env.ID)
			}
			suspensionApplied = drainRequestStates(ctx, stateCh) || suspensionApplied
			// A suspension is never lost to a full state channel: it is also recorded per request.
			c.stateMu.Lock()
			recorded := c.suspendedRequests[env.ID]
			c.stateMu.Unlock()
			if recorded && !suspensionApplied {
				applyRequestSuspension(ctx, RequestStatePayload{RequestID: env.ID, State: "suspended"})
			}
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
			suspensionApplied = suspensionApplied || state.State == "suspended"
			applyRequestSuspension(ctx, state)
			switch state.State {
			case "blocked":
				invocation.Acknowledge(ctx)
			case "completed":
				invocation.Complete(ctx)
			}
			if inactivity > 0 && (state.State == "started" || state.State == "progress" || state.State == "blocked") {
				inactivityTimer.Stop()
				inactivityTimer = c.clock.NewTimer(inactivity)
				inactivityC = inactivityTimer.C()
			}
		case <-inactivityC:
			_ = c.Send(&Envelope{Type: MsgCancel, ID: env.ID, Cancel: &CancelPayload{RequestID: env.ID, Reason: "handler inactivity"}})
			c.cancelHostCalls(env.ID)
			return nil, &HandlerStalledError{Extension: c.name, Operation: operation, HeartbeatHealthy: !c.closed.Load()}
		case <-cancelled:
			// pig additive (D19): the cancel is queued before the request's calls are cancelled, so a call's cancellation result follows it on the wire. The extension cancels its own pending calls on the cancel, and the result finds none.
			_ = c.Send(&Envelope{
				Type: MsgCancel,
				ID:   env.ID,
				Cancel: &CancelPayload{
					RequestID: env.ID,
					Reason:    ctx.Err().Error(),
				},
			})
			c.cancelHostCalls(env.ID)
			if env.Request != nil && env.Request.Method == MethodProviderStream && (errors.Is(context.Cause(ctx), context.Canceled) || errors.Is(context.Cause(ctx), context.DeadlineExceeded)) {
				// Pi forwards the native stream's own terminal event after signaling
				// abort. The connection still owns liveness and closes on shutdown.
				cancelled = nil
				continue
			}
			return nil, ctx.Err()
		}
	}
}

// Incoming returns the channel of non-response messages from the extension.
// The host reads from this to handle calls, widget pushes, etc.
func (c *Conn) Incoming() <-chan *Envelope {
	return c.inCh
}

// Retire asks the peer to end its side and waits for it to close after tearing down, then closes. A retiring peer that shares its process with a live successor generation must finish its teardown, such as leaving the shared event bus, before the successor is visible as the only generation. The peer closing ends the read loop. The wait is outstanding work, so the heartbeat fails a peer that stops answering, and a peer that answers keeps its teardown running, as Pi awaits session_shutdown handlers without a deadline (agent-session.ts:3291-3314).
func (c *Conn) Retire(reason string) error {
	c.retiring.Store(true)
	return c.Close(reason)
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
	if c.retiring.Load() {
		// The wait for the peer to end its side is outstanding work, so a peer that stops answering is failed by the heartbeat instead of holding Retire open.
		defer c.holdLiveness()()
	}
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
	if c.retiring.Load() && queued && shutdownErr == nil {
		<-c.done
	}
	c.closed.Store(true)

	if c.cancel != nil {
		c.cancel()
	}
	err := c.conn.Close()

	// Wait for goroutines to exit.
	<-c.done

	// Fail any pending requests without erasing a terminal error's identity.
	failureCode := ""
	if c.failureError() != nil {
		failureCode = "extension_transport"
	}
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		select {
		case ch <- &Envelope{
			Type: MsgResponse,
			ID:   id,
			Response: &ResponsePayload{
				Error: &ErrorInfo{Code: failureCode, Message: "connection closed"},
			},
		}:
		default:
		}
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()

	if err != nil {
		c.producerTasks.Wait()
		return err
	}
	c.producerTasks.Wait()
	return shutdownErr
}

// startProducerTask admits a stream worker before close and joins it with connection shutdown. Pending requests wake on connection closure, so producer cleanup cannot outlive Close.
func (c *Conn) startProducerTask(run func()) bool {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.closed.Load() || c.closing.Load() {
		return false
	}
	c.producerTasks.Go(run)
	return true
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
		c.stopAbortForwards()
		if c.onClosed != nil {
			c.onClosed()
		}
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

		if c.inbound != nil {
			c.inbound(&env)
		}
		if env.Type == MsgRequestState && env.RequestState != nil {
			if env.RequestState.State == "suspended" {
				// A suspension reports that the calls sent before it are in flight. The host loop delivers it after those calls applied their synchronous parts.
				c.stateMu.Lock()
				c.suspending[env.RequestState.RequestID]++
				c.stateMu.Unlock()
				select {
				case c.inCh <- &env:
				case <-ctx.Done():
					return
				}
				continue
			}
			c.deliverRequestState(*env.RequestState)
			continue
		}
		if env.Type == MsgNotify && env.Notify != nil && (env.Notify.Method == NotifyToolUpdate || env.Notify.Method == NotifyProviderStreamEvent) {
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

		// A call frame owns its parent-scoped context from the moment it is read, while the frames before a response are still ahead of it.
		if env.Type == MsgCall && env.Call != nil && env.ID != "" {
			c.reserveHostCall(env.Call.ParentRequestID, env.ID)
		}

		// Everything else goes to the host.
		select {
		case c.inCh <- &env:
		case <-ctx.Done():
			return
		}
	}
}

// settleSuspension records that the host delivered or dropped a suspension frame that the read loop queued, and wakes the requests that wait on it.
func (c *Conn) settleSuspension(requestID string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.suspending[requestID] <= 1 {
		delete(c.suspending, requestID)
	} else {
		c.suspending[requestID]--
	}
	close(c.suspensionsSettled)
	c.suspensionsSettled = make(chan struct{})
}

// awaitSuspensions blocks until every suspension frame of the request that preceded its response has been delivered to the request. The host loop delivers a suspension asynchronously, after the calls received before it applied, while a response is routed directly; without this wait a response that follows a suspension immediately could overtake it.
func (c *Conn) awaitSuspensions(ctx context.Context, requestID string) {
	for {
		c.stateMu.Lock()
		pending := c.suspending[requestID]
		settled := c.suspensionsSettled
		c.stateMu.Unlock()
		if pending == 0 {
			return
		}
		select {
		case <-settled:
		case <-ctx.Done():
			return
		case <-c.done:
			return
		}
	}
}

// drainRequestStates applies the state frames already queued for a request whose response arrived, and reports whether one was a suspension. A frame that lost the select to the response still admits the caller that waits for the handler's first suspension: the response can be held afterwards, and then nothing else would.
func drainRequestStates(ctx context.Context, stateCh <-chan RequestStatePayload) (suspended bool) {
	for {
		select {
		case state := <-stateCh:
			suspended = suspended || state.State == "suspended"
			applyRequestSuspension(ctx, state)
			switch state.State {
			case "blocked":
				invocation.Acknowledge(ctx)
			case "completed":
				invocation.Complete(ctx)
			}
		default:
			return suspended
		}
	}
}

// deliverRequestState hands a request lifecycle frame to the request waiting for it.
func (c *Conn) deliverRequestState(state RequestStatePayload) {
	c.stateMu.Lock()
	ch := c.requestStates[state.RequestID]
	if ch != nil && state.State == "suspended" {
		c.suspendedRequests[state.RequestID] = true
	}
	c.stateMu.Unlock()
	if ch != nil {
		select {
		case ch <- state:
		default:
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
	if callID != "" {
		c.hostCallMu.Lock()
		reserved, ok := c.reservedCalls[callID]
		delete(c.reservedCalls, callID)
		c.hostCallMu.Unlock()
		if ok {
			return reserved.ctx, reserved.release
		}
	}
	return c.openHostCall(parentRequestID, callID)
}

type reservedHostCall struct {
	ctx     context.Context
	release func()
}

// reserveHostCall opens the call's context while the read loop still holds every frame the extension sent after it. Pi's calls are in-process, so a call the extension made before its command returned completes whatever order the host reaches it in; the read loop routes that command's response directly, and the request record can be gone before the host takes its turn on the call.
func (c *Conn) reserveHostCall(parentRequestID, callID string) {
	ctx, release := c.openHostCall(parentRequestID, callID)
	c.hostCallMu.Lock()
	c.reservedCalls[callID] = reservedHostCall{ctx: ctx, release: release}
	c.hostCallMu.Unlock()
}

// dropReservedHostCall ends the context of a call frame the host will not run.
func (c *Conn) dropReservedHostCall(callID string) {
	c.hostCallMu.Lock()
	reserved, ok := c.reservedCalls[callID]
	delete(c.reservedCalls, callID)
	c.hostCallMu.Unlock()
	if ok {
		reserved.release()
	}
}

func (c *Conn) openHostCall(parentRequestID, callID string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	if parentRequestID != "" {
		c.pendingMu.Lock()
		_, pending := c.pending[parentRequestID]
		if !pending {
			c.pendingMu.Unlock()
			cancel()
			return ctx, func() {}
		}
		c.hostCallMu.Lock()
		c.pendingMu.Unlock()
	} else {
		c.hostCallMu.Lock()
	}
	_, cancelled := c.cancelledParent[parentRequestID]
	if cancelled || c.closed.Load() {
		c.hostCallMu.Unlock()
		cancel()
		return ctx, func() {}
	}
	if callID == "" {
		callID = fmt.Sprintf("host-%d", c.nextID.Add(1))
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
	clear(c.reservedCalls)
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
		// A closing connection still pings: Retire waits for its peer while closing, and only the heartbeat bounds that wait.
		ping, err := json.Marshal(&Envelope{Type: MsgPing, Ping: &PingPayload{Nonce: nonce}})
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

type requestSuspensionKey struct{}

type requestSuspension struct {
	report func(suspended bool)
	// decides reports whether a suspension can still change what the response does; nil means always.
	decides func() bool
}

// withRequestSuspension has request report the runtime's suspended state of the request to fn, in frame order before the response. A suspension affects the response only while decides reports true, or always when decides is nil; otherwise the response does not wait for a suspension that the host has not delivered yet.
func withRequestSuspension(ctx context.Context, fn func(suspended bool), decides func() bool) context.Context {
	return context.WithValue(ctx, requestSuspensionKey{}, requestSuspension{report: fn, decides: decides})
}

// requestSuspensionDecides reports whether the request's response must wait for the suspensions that preceded it.
func requestSuspensionDecides(ctx context.Context) bool {
	suspension, ok := ctx.Value(requestSuspensionKey{}).(requestSuspension)
	if !ok || suspension.report == nil {
		return false
	}
	return suspension.decides == nil || suspension.decides()
}

func applyRequestSuspension(ctx context.Context, state RequestStatePayload) {
	suspension, _ := ctx.Value(requestSuspensionKey{}).(requestSuspension)
	fn := suspension.report
	if fn == nil {
		return
	}
	if state.State == "suspended" {
		fn(true)
	}
}
