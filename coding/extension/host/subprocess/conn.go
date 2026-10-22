package subprocess

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// ToolAbortGrace is how long an aborted extension tool call waits for the tool to settle after its AbortSignal fires.
//
// pig divergence (D111): Pi awaits an aborted tool indefinitely (agent-loop.ts:831-853); a subprocess call must not block the agent loop forever.
const ToolAbortGrace = 3 * time.Second

// readBufferBytes sizes the connection's read buffer: a burst of small frames costs one socket read.
// pig additive (D112): Pi loads extensions in process and has no IPC reads; a frame longer than the buffer is read across refills with the same bytes.
const readBufferBytes = 64 << 10

// ErrToolAborted fails an aborted extension tool call that did not settle within ToolAbortGrace. Its text is Pi's abort tool result (agent-loop.ts:623).
var ErrToolAborted = errors.New("Operation aborted")

// livenessClock supplies monotonic timers to the connection state machine.
// Tests inject a manual clock; production uses the runtime monotonic clock.
type livenessTimer interface {
	C() <-chan time.Time
	Stop() bool
	// Reset re-arms the timer for d, stopped or fired, so a waiter that re-arms a deadline reuses one timer; C may change.
	Reset(d time.Duration)
}

type livenessClock interface {
	NewTimer(time.Duration) livenessTimer
}

type realLivenessClock struct{}
type realLivenessTimer struct{ timer *time.Timer }

func (realLivenessClock) NewTimer(d time.Duration) livenessTimer {
	return realLivenessTimer{timer: time.NewTimer(d)}
}
func (t realLivenessTimer) C() <-chan time.Time   { return t.timer.C }
func (t realLivenessTimer) Stop() bool            { return t.timer.Stop() }
func (t realLivenessTimer) Reset(d time.Duration) { t.timer.Reset(d) }

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
	// Settled closes when the abandoned handler's late response arrives or the connection ends. Nil when the request had nothing left to settle.
	Settled <-chan struct{}
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
	// toolOrder keeps the tool_call requests of one batch in source order on the wire.
	toolOrder jsloop.Lane
	// toolUpdates runs the onUpdate callbacks of every tool_call request of the connection on one lane, in the order the update frames were read: Pi's tools of a parallel batch call onUpdate on one thread, so the update called first reaches the listeners first.
	toolUpdates *callLanes
	// onDispatch records the owner before a request can run in a shared process.
	onDispatch func()
	// beforeCancel runs before the cancel frame of a cancelled request is queued. The Host uses it to send the run signal's abort first, so a handler the run's abort cancels finds ctx.signal aborted when it sees its own cancellation: upstream aborts one signal for both.
	beforeCancel func()
	// inbound observes each decoded frame on the read loop, in wire order, before routing. Cross-process reference accounting uses it so a later release on this connection can never overtake an earlier transmission.
	inbound func(*Envelope)
	// outbound observes each frame's JSON on the write loop, in write order, before it is written (Host.SetOutboundObserver).
	outbound func(frame []byte)
	// parts is conn when it takes frames by parts (a fused extension's fusedConn); nil for a socket.
	parts framePartsWriter
	// onLoopTurned runs on the read loop for each NotifyLoopTurned frame, with its arguments, before the frames after it are read.
	onLoopTurned func(args json.RawMessage)
	// onLoopTurnTaken runs on the read loop for each NotifyLoopTurnTaken frame, with its arguments.
	onLoopTurnTaken func(args json.RawMessage)
	// onRequestStart, when set, runs on the goroutine that sends a request, before the request is written (Host.handleIncoming sets it for a Go, Rust or Python extension: the request's handler may await a call the host holds, so the host releases those calls first).
	onRequestStart atomic.Pointer[func()]
	// onTimerDeferAck runs on the read loop for each NotifyTimerDeferAck frame, with its arguments.
	onTimerDeferAck func(args json.RawMessage)
	// timerDefer numbers the NotifyTimerDefer frames sent on this connection and records the acks (timer_defer.go).
	timerDefer timerDeferState
	// loopTurnsClosed is set under the Host's loopTurns mutex once the read loop ended, so the connection is no recipient a forwarded turn waits for.
	loopTurnsClosed bool
	// onClosed runs once when the read loop ends.
	onClosed func()
	// fileMutations holds the file mutation queue tickets this connection was admitted to and has not exited; sharedTurns replaces it for a connection of a process several extensions run in. turnsLeft is guarded by the registry's mutex (host_file_mutation.go).
	fileMutations fileMutationTurns
	sharedTurns   *fileMutationTurns
	turnsLeft     bool
	// viewImages holds the image bytes this connection's views sent (D107).
	viewImagesOnce sync.Once
	viewImages     *viewImageStore
	// reportView reports a rejected view of this connection's extension
	// (D107). Set by the Host before the connection starts reading.
	reportView func(surface string, err error)
	// terminalRows is the host's terminal height, which a view's editor reads
	// (D107). Set by the Host before the connection starts reading.
	terminalRows func() int
	name         string
	conn         net.Conn
	closing      atomic.Bool
	closed       atomic.Bool
	// modelRegistry records RegisterPayload.WantsModelRegistry. UIBridge.RegisterExtConn sets it and PublishModelCatalog reads it, both under the bridge's mutex.
	modelRegistry bool

	// Outgoing message queue. Writer goroutine drains this. A frame that is
	// queued or being written is outstanding work, so the heartbeat runs while
	// the peer owes the host a read. writeProgress counts bytes the peer has
	// accepted, which proves liveness while a large frame drains slowly.
	outCh         chan outboundFrame
	writeProgress atomic.Uint64
	// writeMu serializes socket writes between the writer goroutine and a caller that writes its own frame (writeInline). queued counts the frames enqueued and not yet written or abandoned: a caller writes inline only when it is zero, so no frame overtakes one queued before it. writerStarted is set once Start launched the writer that owns the queue.
	writeMu       sync.Mutex
	queued        atomic.Int64
	writerStarted atomic.Bool

	// Pending request tracking: maps request ID → response channel, and
	// request ID → the sink for that request's tool_update notifications.
	pendingMu sync.Mutex
	pending   map[string]chan *Envelope
	// abandoned holds the channel each stalled request closes when its late response or the connection end settles it; guarded by pendingMu.
	abandoned map[string]chan struct{}
	// abandonedClosed is set once the reader has exited: no late response can arrive.
	abandonedClosed bool
	updates         map[string]func(json.RawMessage)
	// toolRequests holds the pending tool_call requests; guarded by pendingMu.
	toolRequests map[string]struct{}
	// startWindows closes the start window of a pending tool_call request that holds one (withRequestWindow); guarded by pendingMu.
	startWindows map[string]requestWindowHandle
	// answered runs on the read loop when the response of a request routes to its waiter.
	answered map[string]func()
	// dialogOrder orders the replies to the connection's dialog calls by the order their answers arrived (extension.WithDialogSettle).
	dialogOrder jsloop.SettleGate
	// arrived runs on the read loop when the response of a request is read, before the response reaches its waiter.
	arrived map[string]func()
	// hostCalls counts, per request, the host calls the host applies for it.
	hostCallCounts map[string]func(dialog bool, delta int)
	// drainable holds, per request, the function that records that its runtime reported its event loop drained with the request unanswered, and whether the runtime's started state for the request has been read. drainCovered holds, per drain report the read loop passed to the host and the host has not handled, the drained functions of the requests the report covers.
	drainable     map[string]*drainableRequest
	drainCovered  map[*Envelope][]func()
	producerTasks sync.WaitGroup

	// Incoming messages that aren't responses go here for the host to process.
	inCh chan *Envelope

	// runSignal is the last run signal state the runtime was sent.
	runSignal runSignalSync

	stateMu       sync.Mutex
	requestStates map[string]requestStateSink
	// writeResults pools the channels that carry a frame's write result (writeResult).
	writeResults sync.Pool
	// suspending counts the suspension frames of a request that the read loop queued for the host and the host has not yet delivered. suspensionsSettled is closed and replaced whenever a count drops.
	suspending        map[string]int
	suspendedRequests map[string]bool
	// provisionalBlocks holds the requests whose runtime reported blocked on a host call that the read loop has not yet seen: the suspension is reported once the call frame names the call, because a waitForIdle that finds the Session idle suspends nothing (settleBlockedCall).
	provisionalBlocks  map[string]struct{}
	suspensionsSettled chan struct{}
	// eventLoopTurns holds the channel of each RequestEventLoopTurn awaiting its NotifyEventLoopTurned, by turn number; lastEventLoopTurn numbers them.
	eventLoopTurns    map[uint64]chan struct{}
	lastEventLoopTurn uint64

	hostCallMu      sync.Mutex
	hostCalls       map[string]map[string]context.CancelFunc
	cancelledParent map[string]struct{}
	// reservedCalls holds the context of each call frame the read loop admitted and the host has not yet started, by call ID.
	reservedCalls map[string]reservedHostCall

	// withSessionRoutes holds, by with_session request ID, the replacement Session that serves the calls of that request.
	withSessionMu     sync.Mutex
	withSessionRoutes map[string]*withSessionRoute

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
	// work counts outstanding work. Its idle/busy transitions arm and stop the heartbeat interval.
	work        atomic.Int64
	workChanged chan struct{}
	// armMu guards the heartbeat interval timer (made at the first busy transition), whether it runs, and whether a ping is in flight.
	armMu          sync.Mutex
	interval       livenessTimer
	intervalArmed  bool
	pongPhase      bool
	pongCh         chan string
	heartbeatReady chan struct{}
	heartbeatIdle  chan struct{}
	heartbeatID    atomic.Uint64
	failOnce       sync.Once
	retiring       atomic.Bool
	failureMu      sync.Mutex
	failure        error

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
	parts, _ := c.(framePartsWriter)
	return &Conn{
		fileMutations:      fileMutationTurns{live: 1},
		name:               name,
		conn:               c,
		parts:              parts,
		outCh:              make(chan outboundFrame, 64),
		pending:            make(map[string]chan *Envelope),
		abandoned:          make(map[string]chan struct{}),
		updates:            make(map[string]func(json.RawMessage)),
		toolRequests:       make(map[string]struct{}),
		startWindows:       make(map[string]requestWindowHandle),
		toolUpdates:        newCallLanes(),
		answered:           make(map[string]func()),
		arrived:            make(map[string]func()),
		hostCallCounts:     make(map[string]func(dialog bool, delta int)),
		drainable:          make(map[string]*drainableRequest),
		drainCovered:       make(map[*Envelope][]func()),
		inCh:               make(chan *Envelope, 32),
		done:               make(chan struct{}),
		requestStates:      make(map[string]requestStateSink),
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
	c.writerStarted.Store(true)
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
	// data is a complete payload without its length prefix. Frames the connection encodes itself carry frame instead, which holds the prefix too.
	data   []byte
	frame  *encodedFrame
	result chan error
	// work marks a frame counted as outstanding work until written. Heartbeat
	// pings are not work: an idle connection must be able to go quiet.
	work bool
	// queued marks a frame counted in Conn.queued until written or abandoned.
	queued bool
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
//
//perf:hot
func (c *Conn) Send(env *Envelope) error {
	frame, err := c.encodeEnvelope(env)
	if err != nil {
		return err
	}
	if err := c.enqueue(context.Background(), outboundFrame{frame: frame, work: true}); err != nil {
		frame.release()
		return err
	}
	return nil
}

// enqueue commits frame to the writer queue, waiting for space until ctx ends
// or the connection closes.
func (c *Conn) enqueue(ctx context.Context, frame outboundFrame) error {
	if frame.work {
		c.beginWork()
	}
	frame.queued = true
	c.queued.Add(1)
	select {
	case c.outCh <- frame:
		return nil
	case <-ctx.Done():
		c.queued.Add(-1)
		if frame.work {
			c.endWork()
		}
		return ctx.Err()
	case <-c.done:
		c.queued.Add(-1)
		if frame.work {
			c.endWork()
		}
		if failure := c.failureError(); failure != nil {
			return failure
		}
		return errors.New("connection closed")
	}
}

// writeInline writes frame on the caller when the writer has nothing queued ahead of it, which saves the hand-off to the writer goroutine and back. written is false when the frame must take the queue instead: frames are queued ahead of it, or no writer owns the connection yet. A write failure fails the connection as the writer's does; the writer then finds the socket closed and abandons what is queued.
//
//perf:hot
func (c *Conn) writeInline(frame *encodedFrame) (written bool, err error) {
	if !c.writerStarted.Load() {
		return false, nil
	}
	// A write in progress, inline or the writer's, sends the frame to the queue rather than holding the caller behind it.
	if !c.writeMu.TryLock() {
		return false, nil
	}
	if c.queued.Load() != 0 {
		c.writeMu.Unlock()
		return false, nil
	}
	if err := c.checkSendState(false); err != nil {
		c.writeMu.Unlock()
		frame.release()
		return true, err
	}
	c.beginWork()
	err = c.writeOne(outboundFrame{frame: frame, work: true})
	c.writeMu.Unlock()
	frame.release()
	c.endWork()
	if err != nil {
		err = &TransportError{Extension: c.name, Operation: "write", Err: err}
		c.fail(err)
	}
	return true, err
}

// writeResult returns an empty one-slot channel for a frame's write result. The pool belongs to the connection, not the package: a channel made inside a testing/synctest bubble is bound to it, and a package pool would hand it to a connection of another bubble or of none.
func (c *Conn) writeResult() chan error {
	if result, ok := c.writeResults.Get().(chan error); ok {
		return result
	}
	return make(chan error, 1)
}

// The writer answers a queued frame exactly once and the sender receives that answer before it returns the channel, so a pooled channel is empty and no writer holds it. A channel whose answer was not received is never returned.
func (c *Conn) sendAndWait(ctx context.Context, env *Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	frame, err := c.encodeEnvelope(env)
	if err != nil {
		return err
	}
	if written, err := c.writeInline(frame); written {
		return err
	}
	result := c.writeResult()
	if err := c.enqueue(ctx, outboundFrame{frame: frame, result: result, work: true}); err != nil {
		frame.release()
		c.writeResults.Put(result)
		return err
	}
	// Queueing commits the frame. Wait for the writer result even if ctx is
	// cancelled now, because the peer may already own the request and must
	// receive the correlated cancel from Request. A connection that ends first
	// ends the wait with its terminal error: no writer remains to answer.
	select {
	case err := <-result:
		c.writeResults.Put(result)
		return err
	case <-c.done:
		select {
		case err := <-result:
			c.writeResults.Put(result)
			return err
		default:
		}
		if failure := c.failureError(); failure != nil {
			return failure
		}
		return errors.New("connection closed")
	}
}

// sendOrdered is sendAndWait for a frame whose contents follow the frames queued before it. The order's mutex is held from prepare until the writer answers, so the next frame is prepared against one known to be written or failed; the peer's response is waited for after it is released.
func (c *Conn) sendOrdered(ctx context.Context, env *Envelope, order *sendOrder) error {
	if order == nil {
		return c.sendAndWait(ctx, env)
	}
	order.mu.Lock()
	if err := order.prepare(); err != nil {
		order.mu.Unlock()
		return err
	}
	err := c.sendAndWait(ctx, env)
	order.sent(err)
	order.mu.Unlock()
	return err
}

// encodedEnvelope contains complete JSON produced by a host encoder. It is immutable while queued, so one notification can share its bytes across connections.
type encodedEnvelope []byte

func (c *Conn) sendEncoded(data encodedEnvelope) error {
	if err := c.checkSendState(false); err != nil {
		return err
	}
	if err := checkFrameSize(data); err != nil {
		return err
	}
	return c.enqueue(context.Background(), outboundFrame{data: data, work: true})
}

// checkSendState rejects a send on a connection that is closing or closed. answer marks a call result, the one frame a retiring connection still sends: Retire leaves the peer running until it closes, and a call it made before it read the shutdown frame, or that its teardown makes, waits for a result only this connection can carry. A peer that blocks on the result, as a Node runtime does in a synchronous host call, cannot read the shutdown frame until it arrives, so refusing it leaves the peer waiting for the result and the host waiting for the peer to close.
func (c *Conn) checkSendState(answer bool) error {
	if c.closed.Load() || (c.closing.Load() && (!answer || !c.retiring.Load())) {
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

func (c *Conn) encodeEnvelope(env *Envelope) (*encodedFrame, error) {
	if err := c.checkSendState(env.Type == MsgCallResult); err != nil {
		return nil, err
	}
	if err := checkResultFrameSize(env); err != nil {
		return nil, err
	}
	frame, err := encodeFrame(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	if size := frame.size(); size > MaxFrameSize {
		frame.release()
		return nil, &FrameTooLargeError{Size: size, Max: MaxFrameSize}
	}
	return frame, nil
}

// checkResultFrameSize rejects a call result whose frame the encoder would build above the frame limit, without building it. Marshaling validates the raw JSON and copies every byte of it before the size check fails, and a rejected payload must not cost memory proportional to itself. The encoder removes only whitespace outside strings and adds bytes only by escaping <, >, & (five bytes each) and U+2028, U+2029 (three bytes each), so one pass over the raw bytes gives the exact size of json.Marshal(env) for valid JSON. Invalid JSON takes the marshal path so its error keeps the "marshal envelope" classification instead of becoming a size error. Sizes are int64 so a result that encodes above the largest int of a 32-bit build is still measured as oversized.
func checkResultFrameSize(env *Envelope) error {
	if env.CallResult == nil {
		return nil
	}
	raw := env.CallResult.Result
	// The encoder grows a byte to at most six, so the frame is at most six times the bytes it carries plus the envelope's fixed text. Frames that cannot reach the limit skip the measurement; the general check in encodeEnvelope still bounds them.
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

// sendOrder makes the frame's contents depend on the frames queued before it: prepare fills the request while the order's mutex is held, the frame is queued under it, and sent reports whether it was. The next frame prepares only after that.
type sendOrder struct {
	mu      *sync.Mutex
	prepare func() error
	sent    func(error)
}

// requestOrdered is Request for a frame whose contents are computed against the frames queued before it.
func (c *Conn) requestOrdered(ctx context.Context, env *Envelope, order *sendOrder) (*Envelope, error) {
	return c.requestWithOrder(ctx, env, 0, "", order)
}

// requestWithInactivity applies a host-owned activity lease. Heartbeat pongs
// prove transport liveness but never renew this lease.
func (c *Conn) requestWithInactivity(ctx context.Context, env *Envelope, inactivity time.Duration, operation string) (*Envelope, error) {
	return c.request(ctx, env, inactivity, operation)
}

// callLane returns the ordering lane of a host call whose parent is the request parentID. The calls of every pending tool_call request share one lane:
// Pi runs the tools of a parallel batch on one thread, so the calls they make apply in the order the extension sent them, whichever tool made each.
// A tool call is never the nested request of a host call that waits for it, so the calls of one tool cannot be held behind a call of another that
// waits for the first. Every other request, which a host call may send the extension and wait for, keeps a lane of its own.
func (c *Conn) callLane(parentID string) string {
	c.pendingMu.Lock()
	_, tool := c.toolRequests[parentID]
	c.pendingMu.Unlock()
	if tool {
		return toolCallLane
	}
	return parentID
}

// toolCallLane is the lane of the calls made by tool_call requests. A request id never starts with a space.
const toolCallLane = " tool"

// requestWithUpdates is Request for a tool call whose extension streams
// partial results. onUpdate runs for each NotifyToolUpdate the request
// receives, in frame order, off the read loop, and every update that arrived
// before the response has run when this returns. The updates of the
// connection's tool_call requests share one lane, so they also run in frame
// order across tools; another request (a provider stream, a user bash
// command) has a lane of its own, so a slow consumer holds back no tool.
func (c *Conn) requestWithUpdates(ctx context.Context, env *Envelope, onUpdate func(json.RawMessage)) (*Envelope, error) {
	if env.ID == "" {
		env.ID = "r" + strconv.FormatUint(c.nextID.Add(1), 10)
	}
	updates := c.toolUpdates
	if env.Request == nil || env.Request.Method != MethodToolCall {
		updates = newCallLanes()
	}
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
	return c.requestWithOrder(ctx, env, inactivity, operation, nil)
}

func (c *Conn) requestWithOrder(ctx context.Context, env *Envelope, inactivity time.Duration, operation string, order *sendOrder) (*Envelope, error) {
	if c.closed.Load() {
		if failure := c.failureError(); failure != nil {
			return nil, failure
		}
		return nil, errors.New("connection closed")
	}

	// Assign a unique ID if not already set.
	if env.ID == "" {
		env.ID = "r" + strconv.FormatUint(c.nextID.Add(1), 10)
	}
	// The operation names the request in an error only, so it is built when one is returned.
	operationName := func() string {
		if operation == "" {
			operation = requestOperation(env)
		}
		return operation
	}

	// Register the pending response channel.
	respCh := make(chan *Envelope, 1)
	c.pendingMu.Lock()
	c.pending[env.ID] = respCh
	if env.Request != nil && env.Request.Method == MethodToolCall {
		c.toolRequests[env.ID] = struct{}{}
	}
	if fn := requestAnswered(ctx); fn != nil {
		c.answered[env.ID] = fn
	}
	if fn := requestArrived(ctx); fn != nil {
		c.arrived[env.ID] = fn
	}
	if fn := requestHostCalls(ctx); fn != nil {
		c.hostCallCounts[env.ID] = fn
	}
	if window := requestWindow(ctx); window != nil {
		c.startWindows[env.ID] = window
	}
	if fn := requestDrained(ctx); fn != nil {
		c.drainable[env.ID] = &drainableRequest{drained: fn}
	}
	c.pendingMu.Unlock()
	if start := c.onRequestStart.Load(); start != nil {
		(*start)()
	}
	// A request with no inactivity lease and no suspension report has nothing to do on a started or progress state, and its blocked and completed states only release the invocation acknowledgment, which the read loop applies itself (deliverRequestState). Only a request that reacts to its states waits for them.
	var stateCh chan RequestStatePayload
	if inactivity > 0 || requestReportsSuspension(ctx) {
		stateCh = make(chan RequestStatePayload, 8)
	}
	c.stateMu.Lock()
	c.requestStates[env.ID] = requestStateSink{ch: stateCh, ctx: ctx}
	c.stateMu.Unlock()

	// Clean up on exit.
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, env.ID)
		delete(c.toolRequests, env.ID)
		delete(c.answered, env.ID)
		delete(c.arrived, env.ID)
		delete(c.hostCallCounts, env.ID)
		delete(c.startWindows, env.ID)
		delete(c.drainable, env.ID)
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
	if err := c.sendOrdered(ctx, env, order); err != nil {
		return nil, err
	}
	if issued := requestIssued(ctx); issued != nil {
		issued()
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
	var abortGraceC <-chan time.Time
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
					return nil, &ExtensionUnresponsiveError{Extension: c.name, RequestID: env.ID, Operation: operationName()}
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
				inactivityTimer.Reset(inactivity)
				inactivityC = inactivityTimer.C()
			}
		case <-inactivityC:
			_ = c.Send(&Envelope{Type: MsgCancel, ID: env.ID, Cancel: &CancelPayload{RequestID: env.ID, Reason: "handler inactivity"}})
			c.cancelHostCalls(env.ID)
			return nil, &HandlerStalledError{Extension: c.name, Operation: operationName(), HeartbeatHealthy: !c.closed.Load(), Settled: c.abandon(env.ID)}
		case <-cancelled:
			if c.beforeCancel != nil {
				c.beforeCancel()
			}
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
			if env.Request != nil && (env.Request.Method == MethodProviderStream || env.Request.Method == RequestUserBashExec) && (errors.Is(context.Cause(ctx), context.Canceled) || errors.Is(context.Cause(ctx), context.DeadlineExceeded)) {
				// Pi forwards the native stream's own terminal event after signaling
				// abort, and executeBashWithOperations awaits operations.exec until it
				// settles after its signal aborts (bash-executor.ts:120-128). The
				// connection still owns liveness and closes on shutdown.
				cancelled = nil
				continue
			}
			if env.Request != nil && env.Request.Method == MethodToolCall {
				// pig divergence (D111): the call waits ToolAbortGrace for the tool to settle, as Pi awaits tool.execute after its signal aborts (agent-loop.ts:831-853), and then fails as aborted instead of awaiting a tool that ignores its signal forever.
				cancelled = nil
				grace := c.clock.NewTimer(ToolAbortGrace)
				defer grace.Stop()
				abortGraceC = grace.C()
				continue
			}
			return nil, ctx.Err()
		case <-abortGraceC:
			return nil, ErrToolAborted
		}
	}
}

// Incoming returns the channel of non-response messages from the extension.
// The host reads from this to handle calls, widget pushes, etc.
func (c *Conn) Incoming() <-chan *Envelope {
	return c.inCh
}

// Retire asks the peer to end its side and waits for it to close after tearing down, then closes. A retiring peer that shares its process with a live successor generation must finish its teardown, such as leaving the shared event bus, before the successor is visible as the only generation. The peer closing ends the read loop. The wait is outstanding work, so the heartbeat fails a peer that stops answering, and a peer that answers keeps its teardown running, as Pi awaits session_shutdown handlers without a deadline (agent-session.ts:3291-3314). Until the peer closes, the connection still sends the result of every call the peer makes, as Pi's in-process calls never go unanswered: a Node runtime blocked in a synchronous host call reads no shutdown frame until its result arrives (checkSendState).
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
	c.queued.Add(1)
	select {
	case c.outCh <- outboundFrame{data: shutdown, result: writeResult, work: true, queued: true}:
		queued = true
	default:
		c.queued.Add(-1)
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

// startProducerTask admits a stream worker or a retry waiter (afterRetryBackoff) before close and joins it with connection shutdown. Pending requests wake on connection closure, so producer cleanup cannot outlive Close.
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
//
//perf:hot
func (c *Conn) readLoop(ctx context.Context) {
	defer func() {
		if c.cancel != nil {
			c.cancel()
		}
		// On reader exit, close inCh so the host knows we're done.
		close(c.inCh)
		c.settleAllAbandoned()
		c.stopAbortForwards()
		c.turns().leave(c)
		if c.onClosed != nil {
			c.onClosed()
		}
	}()

	// One length buffer for the connection: it escapes through the conn interface, so a buffer declared per frame would be allocated per frame.
	var lenBuf [4]byte
	// The runtime answers a request with its started state, its completed state and the response, often in one write; a buffered reader takes such a batch in one read instead of two per frame. A frame larger than the buffer is read straight into its own buffer.
	reader := bufio.NewReaderSize(c.conn, readBufferBytes)
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
		_, err := io.ReadFull(reader, lenBuf[:])
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
		frame, err := readFrameBuffer(reader, int(msgLen))
		if err != nil {
			c.fail(&TransportError{Extension: c.name, Operation: "read", Err: err})
			return
		}

		// Decode the envelope. Unmarshal copies every string and json.RawMessage it keeps, so the frame is free once it returns.
		var env Envelope
		err = json.Unmarshal(frame.data, &env)
		frame.release()
		if err != nil {
			c.fail(&TransportError{Extension: c.name, Operation: "decode frame", Err: err})
			return
		}

		if c.inbound != nil {
			c.inbound(&env)
		}
		if env.Type == MsgRequestState && env.RequestState != nil {
			switch {
			case env.RequestState.State == "blocked" && env.RequestState.Reason == "host_call":
				// The call that follows names what the handler awaits.
				c.stateMu.Lock()
				if c.provisionalBlocks == nil {
					c.provisionalBlocks = make(map[string]struct{})
				}
				c.provisionalBlocks[env.RequestState.RequestID] = struct{}{}
				c.stateMu.Unlock()
			case env.RequestState.State == "blocked" || env.RequestState.State == "suspended":
				c.reportRequestPhase(env.RequestState.RequestID, invocation.Suspended)
			case env.RequestState.State == "progress":
				c.stateMu.Lock()
				delete(c.provisionalBlocks, env.RequestState.RequestID)
				c.stateMu.Unlock()
			}
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
			if env.RequestState.State == "started" {
				c.markRequestStarted(env.RequestState.RequestID)
			}
			if c.endsStartWindow(*env.RequestState) {
				// The handler's synchronous prefix is over. The host loop delivers the report after the calls received before it applied their synchronous parts, so the prefix's registration in the file mutation queue precedes the window's end.
				select {
				case c.inCh <- &env: //perf:ok once per tool call whose handler awaits something that is not a host call (request_state blocked), not per frame
				case <-ctx.Done(): //perf:ok the same select
					return
				}
				continue
			}
			if c.holdsNoStartWindow(*env.RequestState) {
				// A tool call outside the dispatcher's parallel batch (a sequential batch, a direct Execute) holds no start window, so no edit or write call waits for it: the runtime holds the tool's next
				// fileMutation.enter after this report until the host says the waiting calls registered (NotifyToolWindowSettled), and with none waiting it says so now.
				c.sendToolWindowSettled(env.RequestState.RequestID)
			}
			c.deliverRequestState(*env.RequestState)
			continue
		}
		if env.Type == MsgNotify && env.Notify != nil && (env.Notify.Method == NotifyToolUpdate || env.Notify.Method == NotifyProviderStreamEvent) {
			c.deliverToolUpdate(env.Notify.Args)
			continue
		}
		// The host forwards a turn before this reader routes the response that follows it, so the forwarded frame precedes on every other connection whatever the host sends after that response.
		if env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == NotifyLoopTurned {
			if c.onLoopTurned != nil {
				c.onLoopTurned(env.Notify.Args)
			}
			continue
		}
		if env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == NotifyLoopTurnTaken {
			if c.onLoopTurnTaken != nil {
				c.onLoopTurnTaken(env.Notify.Args)
			}
			continue
		}
		if env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == NotifyTimerDeferAck {
			if c.onTimerDeferAck != nil {
				c.onTimerDeferAck(env.Notify.Args)
			}
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
				answered := c.answered[env.ID]
				arrived := c.arrived[env.ID]
				c.pendingMu.Unlock()
				if !ok {
					c.settleAbandoned(env.ID)
				}
				if ok {
					// The read loop reads responses in the order the peer wrote them; the goroutines that wait for them wake in no order.
					if arrived != nil {
						arrived()
					}
					select {
					case ch <- &env:
					default:
					}
					// The frames after this response, such as a drain notification, are read after answered ran.
					if answered != nil {
						answered()
					}
				}
			}
			// Response-shaped frames never become unsolicited host messages.
			// A missing correlation means cancellation already won; discard the
			// late response so it cannot fill the incoming request queue.
			continue
		}

		if env.Type == MsgNotify && env.Notify != nil && coversStartedRequests(env.Notify.Method) {
			c.coverDrainReport(&env)
		}
		// A call frame owns its parent-scoped context from the moment it is read, while the frames before a response are still ahead of it.
		if env.Type == MsgCall && env.Call != nil && env.ID != "" {
			c.reportProvisionalBlocks(env.Call)
			c.reserveHostCall(env.Call.ParentRequestID, env.ID)
			if startsRun(env.Call.Method) && env.Call.ParentRequestID != "" {
				c.reportRequestPhase(env.Call.ParentRequestID, invocation.Resumed)
			}
		}

		// Everything else goes to the host.
		select {
		case c.inCh <- &env:
		case <-ctx.Done():
			return
		}
	}
}

// reportProvisionalBlocks reports the suspension of each request whose blocked report awaits this call frame (the call of that request, or one no request owns), in frame order: a runtime reports the suspension before it sends the call, because its handler waits for the answer. A waitForIdle call keeps its request's report for the host action (settleBlockedCall), which knows whether the Session is idle: Pi's `await ctx.waitForIdle()` returns at once then (agent-session.ts waitForIdle), so the handler is not suspended and the run its command sent starts after the command's response.
func (c *Conn) reportProvisionalBlocks(call *CallPayload) {
	c.stateMu.Lock()
	if len(c.provisionalBlocks) == 0 {
		c.stateMu.Unlock()
		return
	}
	var report []string
	for id := range c.provisionalBlocks {
		// A call of another request is not the call this request's handler awaits; a call no request owns may be, for a detached call.
		if call.ParentRequestID != "" && call.ParentRequestID != id {
			continue
		}
		if call.Method == "waitForIdle" && id == call.ParentRequestID {
			continue
		}
		report = append(report, id)
		delete(c.provisionalBlocks, id)
	}
	c.stateMu.Unlock()
	for _, id := range report {
		c.reportRequestPhase(id, invocation.Suspended)
	}
}

// settleBlockedCall ends the provisional block a waitForIdle call kept: the handler is suspended if the call waits, and is not if the Session was idle.
func (c *Conn) settleBlockedCall(requestID string, waits bool) {
	c.stateMu.Lock()
	_, provisional := c.provisionalBlocks[requestID]
	delete(c.provisionalBlocks, requestID)
	c.stateMu.Unlock()
	if provisional && waits {
		c.reportRequestPhase(requestID, invocation.Suspended)
	}
}

// startsRun reports whether method is a call Pi makes inside the handler's synchronous part and that can start a run:
// pi.sendMessage and pi.sendUserMessage return to the handler before the run they start emits agent_start (agent-session.ts
// sendCustomMessage, sendUserMessage). A runtime reports the call's suspension before it sends the call, because its handler
// waits for the answer, so the call itself reports that the handler runs again.
func startsRun(method string) bool {
	return method == "sendMessage" || method == "sendUserMessage"
}

// reportRequestPhase reports a change of the request's run phase to the RunPhase bound to its context. The read loop reports
// in frame order, so a suspension reported before a call is followed by the call's own report.
func (c *Conn) reportRequestPhase(requestID string, report func(context.Context)) {
	c.stateMu.Lock()
	sink, ok := c.requestStates[requestID]
	c.stateMu.Unlock()
	if ok && sink.ctx != nil {
		report(sink.ctx)
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

// awaitEventLoopTurn asks the Node runtime for one turn of its event loop (RequestEventLoopTurn) and returns once the runtime took it and the host applied the calls the connection sent before NotifyEventLoopTurned. A closed connection takes no turn and is not waited for; a failed request is returned.
func (c *Conn) awaitEventLoopTurn(ctx context.Context) error {
	taken := make(chan struct{})
	c.stateMu.Lock()
	c.lastEventLoopTurn++
	turn := c.lastEventLoopTurn
	if c.eventLoopTurns == nil {
		c.eventLoopTurns = make(map[uint64]chan struct{})
	}
	c.eventLoopTurns[turn] = taken
	c.stateMu.Unlock()
	defer func() {
		c.stateMu.Lock()
		delete(c.eventLoopTurns, turn)
		c.stateMu.Unlock()
	}()
	args, err := json.Marshal(LoopTurnArgs{Turn: turn})
	if err != nil {
		return err
	}
	if _, err := c.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestEventLoopTurn, Args: args}}); err != nil {
		select {
		case <-c.done:
			return nil
		default:
			return err
		}
	}
	select {
	case <-taken:
		return nil
	case <-c.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// settleEventLoopTurn records that the host applied the calls the connection sent before the NotifyEventLoopTurned that carries raw.
func (c *Conn) settleEventLoopTurn(raw json.RawMessage) {
	var args LoopTurnArgs
	if json.Unmarshal(raw, &args) != nil {
		return
	}
	c.stateMu.Lock()
	taken := c.eventLoopTurns[args.Turn]
	delete(c.eventLoopTurns, args.Turn)
	c.stateMu.Unlock()
	if taken != nil {
		close(taken)
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

// requestStateSink is where a request's lifecycle frames go: its waiting goroutine's channel, or, for a request that does not react to them (a nil ch), the read loop applies them to the request's context.
type requestStateSink struct {
	ch  chan RequestStatePayload
	ctx context.Context
}

// requestReportsSuspension reports whether ctx reports the request's suspension, which the request's own goroutine applies in frame order.
func requestReportsSuspension(ctx context.Context) bool {
	suspension, _ := ctx.Value(requestSuspensionKey{}).(requestSuspension)
	return suspension.report != nil
}

// deliverRequestState hands a request lifecycle frame to the request waiting for it.
func (c *Conn) deliverRequestState(state RequestStatePayload) {
	c.stateMu.Lock()
	sink, ok := c.requestStates[state.RequestID]
	if ok && state.State == "suspended" {
		c.suspendedRequests[state.RequestID] = true
	}
	c.stateMu.Unlock()
	if !ok {
		return
	}
	if sink.ch == nil {
		// What the request's goroutine would do with the state, without waking it.
		switch state.State {
		case "blocked":
			invocation.Acknowledge(sink.ctx)
		case "completed":
			invocation.Complete(sink.ctx)
		}
		return
	}
	select {
	case sink.ch <- state:
	default:
	}
}

// endsStartWindow reports whether state tells that the synchronous prefix of a tool_call request that holds a start window is over: the Node runtime reports
// the handler blocked on something that is not a host call (external_io), or on the user, after the tool's execute returned its Promise. A handler of another SDK
// is not reported this way: a native handler that waits does so in a host call, which closes the window when the host applies it.
func (c *Conn) endsStartWindow(state RequestStatePayload) bool {
	if state.State != "blocked" || (state.Reason != "external_io" && state.Reason != "user") {
		return false
	}
	c.pendingMu.Lock()
	_, ok := c.startWindows[state.RequestID]
	c.pendingMu.Unlock()
	return ok
}

// holdsNoStartWindow reports whether state is the report of endsStartWindow from a pending tool_call request that holds no start window.
func (c *Conn) holdsNoStartWindow(state RequestStatePayload) bool {
	if state.State != "blocked" || state.Reason != "external_io" {
		return false
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if _, tool := c.toolRequests[state.RequestID]; !tool {
		return false
	}
	_, windowed := c.startWindows[state.RequestID]
	return !windowed
}

// closeRequestWindow closes the start window of the request requestID, when it holds one: the request's handler has run to its first await.
func (c *Conn) closeRequestWindow(requestID string) {
	if requestID == "" {
		return
	}
	c.pendingMu.Lock()
	window := c.startWindows[requestID]
	c.pendingMu.Unlock()
	if window != nil {
		window.Close()
	}
}

// endRequestWindowByReport closes the start window of the request requestID after its runtime reported that the handler awaits something that is not a host
// call, and tells the runtime when the calls that waited for the window registered in the file mutation queue (NotifyToolWindowSettled): Pi registered them
// before the handler's next call, which the runtime holds until then.
func (c *Conn) endRequestWindowByReport(requestID string) {
	c.pendingMu.Lock()
	window := c.startWindows[requestID]
	c.pendingMu.Unlock()
	if window == nil {
		return
	}
	window.Close()
	//portlint:allow golifetime AwaitSettled returns once the calls that waited for the window registered, which the dispatcher forces when the tool call ends, so the goroutine ends with the call
	go func() {
		window.AwaitSettled()
		c.sendToolWindowSettled(requestID)
	}()
}

// sendToolWindowSettled tells the runtime that the writes that waited for the start window of the tool_call request requestID registered, or that none waited.
func (c *Conn) sendToolWindowSettled(requestID string) {
	// A request id is the host's own "r" and a number, or the id of the request it nests in: plain ASCII, so strconv's quoting is JSON's.
	args := []byte(`{"request_id":` + strconv.Quote(requestID) + `}`)
	_ = c.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyToolWindowSettled, Args: args}})
}

// writeBatchFrames and writeBatchBytes bound the encoded frames the writer coalesces into one socket write.
const (
	writeBatchFrames = 16
	writeBatchBytes  = writeChunk
)

// coalescable reports whether the writer may put frame in a coalesced write: an encoded frame no larger than a batch. Pings and frames that drain in chunks are written on their own.
func coalescable(frame outboundFrame) bool {
	return frame.frame != nil && frame.frame.argsAt == 0 && len(frame.frame.buf) <= writeBatchBytes
}

// writeLoop serializes outgoing messages to the socket. Encoded frames that are queued together leave in one write.
func (c *Conn) writeLoop(ctx context.Context) {
	finish := func(frame outboundFrame, err error) {
		if frame.frame != nil {
			frame.frame.release()
		}
		if frame.queued {
			c.queued.Add(-1)
		}
		if frame.work {
			c.endWork()
		}
		if frame.result != nil {
			frame.result <- err
		}
	}
	var batch []outboundFrame
	var buffers net.Buffers
	// write writes first and the encoded frames queued right behind it. The frames stay counted in queued until their bytes are on the socket, so a caller cannot write inline ahead of them. A frame that cannot join the batch is written after it, on its own.
	write := func(first outboundFrame) error {
		batch = append(batch[:0], first)
		var tail outboundFrame
		hasTail := false
		if c.parts == nil && coalescable(first) {
			size := len(first.frame.buf)
		gather:
			for len(batch) < writeBatchFrames {
				select {
				case next := <-c.outCh:
					if c.parts != nil || !coalescable(next) || size+len(next.frame.buf) > writeBatchBytes {
						tail, hasTail = next, true
						break gather
					}
					size += len(next.frame.buf)
					batch = append(batch, next)
				default:
					break gather
				}
			}
		}
		c.writeMu.Lock()
		var err, tailErr error
		if len(batch) == 1 {
			err = c.writeOne(first)
		} else {
			buffers = buffers[:0]
			work := 0
			for _, frame := range batch {
				c.observeOutbound(frame)
				buffers = append(buffers, frame.frame.buf)
				if frame.work {
					work += len(frame.frame.buf)
				}
			}
			var n int64
			n, err = buffers.WriteTo(c.conn)
			c.writeProgress.Add(uint64(min(n, int64(work))))
		}
		if hasTail && err == nil {
			tailErr = c.writeOne(tail)
		}
		c.writeMu.Unlock()
		if err != nil {
			err = &TransportError{Extension: c.name, Operation: "write", Err: err}
		}
		for _, frame := range batch {
			finish(frame, err)
		}
		clear(batch)
		if hasTail {
			// A tail behind a failed batch was never written and fails with it.
			if err != nil {
				tailErr = err
			} else if tailErr != nil {
				tailErr = &TransportError{Extension: c.name, Operation: "write", Err: tailErr}
				err = tailErr
			}
			finish(tail, tailErr)
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

// writeOne writes one queued frame; the caller holds writeMu.
func (c *Conn) writeOne(frame outboundFrame) error {
	c.observeOutbound(frame)
	switch {
	case c.parts != nil:
		return c.writeFrameParts(frame)
	case frame.frame != nil && frame.frame.argsAt != 0:
		return c.writeSplit(frame.frame, frame.work)
	case frame.frame != nil:
		return c.writeBytes(frame.frame.buf, frame.work)
	}
	return c.writeFrame(frame.data, frame.work)
}

// observeOutbound reports a frame to the outbound hook before it is written.
func (c *Conn) observeOutbound(frame outboundFrame) {
	if c.outbound == nil {
		return
	}
	if frame.frame != nil {
		c.outbound(frame.frame.payload())
	} else {
		c.outbound(frame.data)
	}
}

// abandonQueuedFrames completes every frame still queued when the writer
// stops after a write failure, so no waiter blocks on a frame nothing will
// write.
func (c *Conn) abandonQueuedFrames(err error) {
	for {
		select {
		case frame := <-c.outCh:
			if frame.frame != nil {
				frame.frame.release()
			}
			if frame.queued {
				c.queued.Add(-1)
			}
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

// beginWork and endWork count outstanding work. Only the idle/busy transitions concern liveness, and the goroutine that makes one arms or stops the heartbeat interval itself (workBusy, workIdle), so a dispatch wakes the heartbeat only when its interval fires.
func (c *Conn) beginWork() {
	if c.work.Add(1) == 1 {
		c.workBusy()
	}
}

func (c *Conn) endWork() {
	for {
		work := c.work.Load()
		if work <= 0 {
			return
		}
		if c.work.CompareAndSwap(work, work-1) {
			if work == 1 {
				c.workIdle()
			}
			return
		}
	}
}

// workBusy starts the interval after an idle→busy transition. Transitions race each other, so the arming follows the count read under armMu, not the transition that called it: the last of two racing calls leaves the timer as the count is.
func (c *Conn) workBusy() {
	c.armMu.Lock()
	defer c.armMu.Unlock()
	if c.work.Load() <= 0 || c.intervalArmed || c.pongPhase {
		// A ping already in flight re-arms the interval when it settles.
		return
	}
	c.intervalArmed = true
	if c.interval == nil {
		c.interval = c.clock.NewTimer(c.heartbeatInterval)
		// The heartbeat waits for a timer it does not have yet.
		c.signalWorkChanged()
		return
	}
	c.interval.Reset(c.heartbeatInterval)
}

// workIdle stops the interval after a busy→idle transition; a ping in flight is abandoned.
func (c *Conn) workIdle() {
	c.armMu.Lock()
	defer c.armMu.Unlock()
	if c.work.Load() > 0 {
		return
	}
	if c.intervalArmed {
		c.interval.Stop()
		c.intervalArmed = false
	}
	if c.pongPhase {
		c.signalWorkChanged()
		return
	}
	c.noteHeartbeatIdle()
}

// noteHeartbeatIdle reports to a test that the liveness machine is idle: no interval runs and no ping is in flight.
func (c *Conn) noteHeartbeatIdle() {
	select {
	case c.heartbeatIdle <- struct{}{}:
	default:
	}
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
	return c.work.Load() > 0
}

// heartbeatLoop pings the peer each interval the connection stays busy and fails it when a pong misses its deadline. The interval is armed and stopped by the idle/busy transitions (workBusy, workIdle), so this goroutine sleeps until the interval fires; it is woken otherwise only to pick up the interval timer once and, while a ping is in flight, when work ends.
func (c *Conn) heartbeatLoop(ctx context.Context) {
	close(c.heartbeatReady)
	c.noteHeartbeatIdle()
	// The deadline timer is made once and re-armed with Reset at each ping.
	var deadline livenessTimer
	for {
		c.armMu.Lock()
		var fire <-chan time.Time
		if c.interval != nil {
			fire = c.interval.C()
		}
		c.armMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-c.workChanged:
			continue
		case <-fire:
		}
		c.armMu.Lock()
		c.intervalArmed = false
		busy := c.work.Load() > 0
		c.pongPhase = busy
		c.armMu.Unlock()
		if !busy {
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
		if deadline == nil {
			deadline = c.clock.NewTimer(c.heartbeatTimeout)
		} else {
			deadline.Reset(c.heartbeatTimeout)
		}
		pingQueued := false
	pong:
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
					break pong
				}
			case got := <-c.pongCh:
				if got == nonce {
					deadline.Stop()
					break pong
				}
			case <-deadline.C():
				if current := c.writeProgress.Load(); current != progress {
					progress = current
					deadline.Reset(c.heartbeatTimeout)
					continue
				}
				c.fail(&ExtensionUnresponsiveError{Extension: c.name, Operation: "outstanding request"})
				return
			}
		}
		// The next interval starts when the ping settles, as long as the connection stays busy.
		c.armMu.Lock()
		c.pongPhase = false
		if c.work.Load() > 0 {
			c.intervalArmed = true
			c.interval.Reset(c.heartbeatInterval)
		} else {
			c.noteHeartbeatIdle()
		}
		c.armMu.Unlock()
	}
}

// abandon records a request whose caller stopped waiting and returns the channel that closes when its handler answers late or the connection ends.
func (c *Conn) abandon(id string) <-chan struct{} {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	done := make(chan struct{})
	if c.abandonedClosed {
		close(done)
		return done
	}
	c.abandoned[id] = done
	return done
}

func (c *Conn) settleAbandoned(id string) {
	c.pendingMu.Lock()
	done, ok := c.abandoned[id]
	delete(c.abandoned, id)
	c.pendingMu.Unlock()
	if ok {
		close(done)
	}
}

func (c *Conn) settleAllAbandoned() {
	c.pendingMu.Lock()
	abandoned := c.abandoned
	c.abandoned = make(map[string]chan struct{})
	c.abandonedClosed = true
	c.pendingMu.Unlock()
	for _, done := range abandoned {
		close(done)
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
//
//perf:hot
func (c *Conn) writeFrame(data []byte, countProgress bool) error {
	c.noteLargeFrame(len(data))
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if len(data) <= writeChunk {
		// One writev for the prefix and the payload: the peer wakes once per frame.
		buffers := net.Buffers{lenBuf[:], data}
		n, err := buffers.WriteTo(c.conn)
		if countProgress {
			c.writeProgress.Add(uint64(n))
		}
		return err
	}
	if _, err := c.conn.Write(lenBuf[:]); err != nil {
		return err
	}
	if countProgress {
		c.writeProgress.Add(uint64(len(lenBuf)))
	}
	return c.writeChunks(data, countProgress)
}

// writeSplit writes a split frame (encodedFrame) as one vectored write of its length, the envelope before Args, Args and the rest, when it fits one chunk; a larger one goes out in chunks so writeProgress advances while it drains.
func (c *Conn) writeSplit(frame *encodedFrame, countProgress bool) error {
	c.noteLargeFrame(frame.size())
	prefix, args, suffix := frame.parts()
	if frame.size() <= writeChunk {
		buffers := net.Buffers{frame.buf[:4], prefix, args, suffix}
		n, err := buffers.WriteTo(c.conn)
		if countProgress {
			c.writeProgress.Add(uint64(n))
		}
		return err
	}
	for _, part := range [][]byte{frame.buf[:4+len(prefix)], args, suffix} {
		if err := c.writeChunks(part, countProgress); err != nil {
			return err
		}
	}
	return nil
}

// writeFrameParts hands a frame to an in-process peer (framePartsWriter) without copying it into a byte stream. The peer has taken the frame when the call returns; it copies the envelope's bytes and keeps only Args, which are immutable.
func (c *Conn) writeFrameParts(frame outboundFrame) error {
	var prefix, args, suffix []byte
	if frame.frame != nil {
		prefix, args, suffix = frame.frame.parts()
	} else {
		prefix = frame.data
	}
	c.noteLargeFrame(len(prefix) + len(args) + len(suffix))
	if err := c.parts.WriteFrameParts(prefix, args, suffix); err != nil {
		return err
	}
	if frame.work {
		c.writeProgress.Add(uint64(4 + len(prefix) + len(args) + len(suffix)))
	}
	return nil
}

// noteLargeFrame notes any frame larger than the legacy cap so a subsequent disconnect can be attributed to an extension whose receive cap is below ours.
func (c *Conn) noteLargeFrame(payload int) {
	if n := uint64(payload); n > legacyFrameSize {
		c.lastLargeSize.Store(n)
		c.lastLargeAtNs.Store(time.Now().UnixNano())
	}
}

// writeBytes writes a length-prefixed frame. A frame that fits one chunk is one socket write; a larger one is written in chunks so writeProgress advances while it drains.
func (c *Conn) writeBytes(frame []byte, countProgress bool) error {
	c.noteLargeFrame(len(frame) - 4)
	return c.writeChunks(frame, countProgress)
}

func (c *Conn) writeChunks(data []byte, countProgress bool) error {
	// No write deadline: a peer that stops reading is detected by the heartbeat
	// (D56), which counts every accepted byte as liveness and closes the
	// socket on failure, unblocking this write.
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

type requestAnsweredKey struct{}

// withRequestAnswered has request run fn on the connection's read loop when the request's response routes to its waiter, before any later frame is read. It marks that the response exists before the goroutine that waits for it has run.
func withRequestAnswered(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, requestAnsweredKey{}, fn)
}

func requestAnswered(ctx context.Context) func() {
	fn, _ := ctx.Value(requestAnsweredKey{}).(func())
	return fn
}

type requestArrivedKey struct{}

// withRequestArrived has request run fn on the connection's read loop when the request's response is read, before the response reaches its waiter and before any later frame is read. Responses are read in the order the peer wrote them, which the goroutines that wait for them do not keep: fn records that order.
func withRequestArrived(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, requestArrivedKey{}, fn)
}

func requestArrived(ctx context.Context) func() {
	fn, _ := ctx.Value(requestArrivedKey{}).(func())
	return fn
}

type requestIssuedKey struct{}

// withRequestIssued has request run fn once the request's frame is written. A call that holds a place in a call order hands the place on then: the next call's frame follows this one on the wire.
func withRequestIssued(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, requestIssuedKey{}, fn)
}

func requestIssued(ctx context.Context) func() {
	fn, _ := ctx.Value(requestIssuedKey{}).(func())
	return fn
}

type requestWindowKey struct{}

// requestWindowHandle is the start window of a tool_call request (agent.StartWindow): the request closes it once its handler ran to its first await: when the host
// applied a host call the request made that returns a Promise in Pi (startsAsync), or when the runtime reported the handler blocked on something else. The window
// ends with the request in any case.
type requestWindowHandle interface {
	Close()
	AwaitSettled()
}

func withRequestWindow(ctx context.Context, window requestWindowHandle) context.Context {
	return context.WithValue(ctx, requestWindowKey{}, window)
}

func requestWindow(ctx context.Context) requestWindowHandle {
	window, _ := ctx.Value(requestWindowKey{}).(requestWindowHandle)
	return window
}

type requestHostCallsKey struct{}

// withRequestHostCalls has request run fn each time the host starts (delta 1) or finishes (delta -1) applying a host call that names the request as its parent. dialog reports a user dialog. fn runs on the goroutine that applies the call.
func withRequestHostCalls(ctx context.Context, fn func(dialog bool, delta int)) context.Context {
	return context.WithValue(ctx, requestHostCallsKey{}, fn)
}

func requestHostCalls(ctx context.Context) func(dialog bool, delta int) {
	fn, _ := ctx.Value(requestHostCallsKey{}).(func(dialog bool, delta int))
	return fn
}

type requestDrainedKey struct{}

// drainableRequest is one request whose runtime may report its event loop drained with the request unanswered. started records, on the read loop, that the runtime's started state for the request was read.
type drainableRequest struct {
	drained func()
	started bool
}

// withRequestDrained has request run fn when the connection's runtime reports that its event loop drained after the runtime had started the request (requestsDrained).
func withRequestDrained(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, requestDrainedKey{}, fn)
}

func requestDrained(ctx context.Context) func() {
	fn, _ := ctx.Value(requestDrainedKey{}).(func())
	return fn
}

// markRequestStarted records, on the read loop, that the runtime started the request, so a drain report read after it covers the request.
func (c *Conn) markRequestStarted(requestID string) {
	c.pendingMu.Lock()
	if r := c.drainable[requestID]; r != nil {
		r.started = true
	}
	c.pendingMu.Unlock()
}

// coversStartedRequests reports whether a runtime notification reports that the runtime's event loop drained with the requests it had started unanswered.
func coversStartedRequests(method string) bool {
	return method == notifyRuntimeCommandsDrained || method == notifyRuntimeDrained
}

// coverDrainReport records, on the read loop as it reads the drain report report, the requests whose started state it read before the report. The runtime sends a request's started state as it reads the request, so these are exactly the requests the runtime held when its loop drained; a request whose started state follows the report is not covered.
func (c *Conn) coverDrainReport(report *Envelope) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	var fns []func()
	for _, r := range c.drainable {
		if r.started {
			fns = append(fns, r.drained)
		}
	}
	c.drainCovered[report] = fns
}

// requestsDrained runs the drained function of every request the drain report report covers (coverDrainReport). The host calls it when it handles the report, after the calls the runtime sent before it.
func (c *Conn) requestsDrained(report *Envelope) {
	c.pendingMu.Lock()
	fns := c.drainCovered[report]
	delete(c.drainCovered, report)
	c.pendingMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// countHostCall counts one host call of the request parentRequestID and returns the function that uncounts it. A call without a running command of that request is not counted.
func (c *Conn) countHostCall(parentRequestID string, dialog bool) func() {
	if parentRequestID == "" {
		return func() {}
	}
	c.pendingMu.Lock()
	fn := c.hostCallCounts[parentRequestID]
	c.pendingMu.Unlock()
	if fn == nil {
		return func() {}
	}
	fn(dialog, 1)
	return func() { fn(dialog, -1) }
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

// dispatchBarrier queues a marker behind the frames the read loop has passed to the host and returns a channel that closes once the host dispatched them. It is called on the read loop, so every frame read before the caller's is ahead of the marker.
func (c *Conn) dispatchBarrier(ctx context.Context) <-chan struct{} {
	mark := &dispatchMark{done: make(chan struct{})}
	select {
	case c.inCh <- &Envelope{barrier: mark}:
	case <-ctx.Done():
		mark.release()
	}
	return mark.done
}

// requestsInFlight reports whether a request the host sent has no response yet.
func (c *Conn) requestsInFlight() bool {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	return len(c.pending) > 0
}
