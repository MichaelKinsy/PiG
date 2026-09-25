package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Forward routes calls for services not provided by this peer. Its context carries the dispatch turn for Yield and nested calls, but no cancellation signal from this hop.
type Forward func(ctx context.Context, method string, args []json.RawMessage) (any, error)

type CallOptions struct {
	// TimeoutMs bounds the call only when present. The context passed to CallWith supplies cancellation.
	TimeoutMs *float64
}

type PeerOptions struct {
	Forward Forward
	// DeadMs defaults to 15000; zero disables liveness.
	DeadMs *float64
}

// Handler receives wire arguments and the process-local cancellation signal. Its synchronous prefix runs before the next frame. Before blocking, it must call Yield or an RPC Call with this context; completion may then overlap later handlers.
type Handler func(context.Context, []json.RawMessage) (any, error)

type Service map[string]Handler

type dispatchTurnKey struct{}

type dispatchTurn struct {
	done chan struct{}
	once sync.Once
}

func (turn *dispatchTurn) release() { turn.once.Do(func() { close(turn.done) }) }

// Yield ends an incoming handler's synchronous prefix so the reader can deliver subsequent frames. Call it immediately before a blocking wait corresponding to an upstream await. Call and CallWith yield automatically after sending. Yield does not detach work: the peer owns the entire handler until it returns. It is a no-op outside a handler or after the first yield.
func Yield(ctx context.Context) {
	if turn, ok := ctx.Value(dispatchTurnKey{}).(*dispatchTurn); ok {
		turn.release()
	}
}

type frame struct {
	Kind     string            `json:"kind"`
	ID       uint64            `json:"id"`
	Method   string            `json:"method"`
	Args     []json.RawMessage `json:"args"`
	Result   json.RawMessage   `json:"result"`
	Error    string            `json:"error"`
	Service  string            `json:"service"`
	Payload  json.RawMessage   `json:"payload"`
	To       *string           `json:"to"`
	Services []string          `json:"services"`
}

type reply struct {
	value json.RawMessage
	err   error
}

type inflightCall struct{ cancel context.CancelCauseFunc }

type eventHandler func(string, json.RawMessage, *string)

// RpcPeer is a bidirectional peer. Call blocks until the answer, cancellation, timeout, or connection close. Incoming handlers enter in frame order and may complete concurrently after Yield; Wait joins them after Close. Handlers must respect their contexts to permit shutdown to finish.
type RpcPeer struct {
	connection    Connection
	options       PeerOptions
	mu            sync.Mutex
	services      map[string]Service
	provided      []string
	announced     []string
	pending       map[uint64]chan reply
	inflight      map[uint64]*inflightCall
	eventHandlers []eventHandler
	nextID        uint64
	lastFrameAt   time.Time
	closed        bool
	err           error
	done          chan struct{}
	wg            sync.WaitGroup
}

func CreatePeer(connection Connection, options PeerOptions) *RpcPeer {
	p := &RpcPeer{connection: connection, options: options, services: make(map[string]Service), pending: make(map[uint64]chan reply), inflight: make(map[uint64]*inflightCall), nextID: 1, lastFrameAt: time.Now(), done: make(chan struct{})}
	deadMs := float64(15000)
	if options.DeadMs != nil {
		deadMs = *options.DeadMs
	}
	if deadMs > 0 {
		p.wg.Go(func() { p.liveness(deadMs) })
	}
	connection.OnClose(p.shutdown)
	connection.OnMessage(p.onMessage)
	return p
}

func (p *RpcPeer) shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	close(p.done)
	for _, waiter := range p.pending {
		waiter <- reply{err: errors.New("Connection closed")}
	}
	clear(p.pending)
	for _, call := range p.inflight {
		call.cancel(errors.New("Connection closed"))
	}
	clear(p.inflight)
	p.eventHandlers = nil
}

func (p *RpcPeer) Close() error {
	// A pluggable connection may not synchronously notify its close callbacks.
	p.shutdown()
	return p.connection.Close()
}

func (p *RpcPeer) Wait() { p.wg.Wait() }

// Err returns a fatal protocol or send error. Remote method errors belong to their Call result instead.
func (p *RpcPeer) Err() error { p.mu.Lock(); defer p.mu.Unlock(); return p.err }

func (p *RpcPeer) fail(err error) {
	p.mu.Lock()
	if p.err == nil {
		p.err = err
	}
	p.mu.Unlock()
	_ = p.Close()
}

func (p *RpcPeer) OnClose(handler func()) { p.connection.OnClose(handler) }

func (p *RpcPeer) Provided() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.provided)
}
func (p *RpcPeer) Announced() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.announced)
}

// Provide accepts a Service map or a struct of API function fields. Replacing an implementation preserves its announcement order.
func (p *RpcPeer) Provide(token Token, implementation any) error {
	service, err := serviceHandlers(implementation)
	if err != nil {
		return err
	}
	p.mu.Lock()
	name := token.ServiceName()
	if _, exists := p.services[name]; !exists {
		p.provided = append(p.provided, name)
	}
	p.services[name] = service
	names := slices.Clone(p.provided)
	// Enqueue under the state lock so simultaneous registrations cannot announce an older inventory last.
	err = p.connection.Send(struct {
		Kind     string   `json:"kind"`
		Services []string `json:"services"`
	}{"announce", names})
	p.mu.Unlock()
	return err
}

func Provide[API, Event any](peer *RpcPeer, token ServiceToken[API, Event], implementation API) error {
	return peer.Provide(token, implementation)
}

func (p *RpcPeer) Call(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
	return p.CallWith(ctx, CallOptions{}, method, args...)
}

func (p *RpcPeer) CallWith(ctx context.Context, options CallOptions, method string, args ...any) (json.RawMessage, error) {
	p.mu.Lock()
	id := p.nextID
	p.nextID++
	if ctx.Err() != nil {
		p.mu.Unlock()
		return nil, errors.New("Call cancelled")
	}
	waiter := make(chan reply, 1)
	p.pending[id] = waiter
	p.mu.Unlock()
	if args == nil {
		args = []any{}
	}
	err := p.connection.Send(struct {
		Kind   string `json:"kind"`
		ID     uint64 `json:"id"`
		Method string `json:"method"`
		Args   []any  `json:"args"`
	}{"call", id, method, args})
	if err != nil {
		p.settle(id, reply{err: err})
	}
	var timer *time.Timer
	var timeout <-chan time.Time
	if options.TimeoutMs != nil {
		timer = time.NewTimer(nodeTimerDelay(*options.TimeoutMs))
		timeout = timer.C
		defer timer.Stop()
	}
	Yield(ctx)
	select {
	case response := <-waiter:
		return response.value, response.err
	case <-ctx.Done():
		p.abandon(id, errors.New("Call cancelled"))
	case <-timeout:
		p.abandon(id, fmt.Errorf("%s timed out after %sms", method, strconv.FormatFloat(*options.TimeoutMs, 'f', -1, 64)))
	}
	response := <-waiter
	return response.value, response.err
}

func (p *RpcPeer) settle(id uint64, response reply) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if waiter := p.pending[id]; waiter != nil {
		delete(p.pending, id)
		waiter <- response
	}
}

func (p *RpcPeer) abandon(id uint64, err error) {
	p.mu.Lock()
	waiter := p.pending[id]
	delete(p.pending, id)
	p.mu.Unlock()
	if waiter == nil {
		return
	}
	if sendErr := p.connection.Send(struct {
		Kind string `json:"kind"`
		ID   uint64 `json:"id"`
	}{"cancel", id}); sendErr != nil {
		p.fail(sendErr)
	}
	waiter <- reply{err: err}
}

func (p *RpcPeer) OnEvent(handler func(service string, payload json.RawMessage, to *string)) {
	p.mu.Lock()
	p.eventHandlers = append(p.eventHandlers, handler)
	p.mu.Unlock()
}

func (p *RpcPeer) On(token Token, handler func(json.RawMessage)) {
	p.OnEvent(func(service string, payload json.RawMessage, _ *string) {
		if service == token.ServiceName() {
			handler(payload)
		}
	})
}

func On[API, Event any](peer *RpcPeer, token ServiceToken[API, Event], handler func(Event)) {
	peer.On(token, func(payload json.RawMessage) {
		var event Event
		if err := json.Unmarshal(payload, &event); err != nil {
			peer.fail(err)
			return
		}
		handler(event)
	})
}

func (p *RpcPeer) Emit(token Token, event any) error {
	return p.EmitRaw(token.ServiceName(), event, nil)
}
func (p *RpcPeer) EmitTo(token Token, event any, to string) error {
	return p.EmitRaw(token.ServiceName(), event, &to)
}
func (p *RpcPeer) EmitRaw(service string, payload any, to *string) error {
	return p.connection.Send(struct {
		Kind    string  `json:"kind"`
		Service string  `json:"service"`
		Payload any     `json:"payload"`
		To      *string `json:"to,omitempty"`
	}{"event", service, payload, to})
}

func (p *RpcPeer) onMessage(message json.RawMessage) {
	var f frame
	if err := json.Unmarshal(message, &f); err != nil {
		p.fail(err)
		return
	}
	p.mu.Lock()
	p.lastFrameAt = time.Now()
	p.mu.Unlock()
	switch f.Kind {
	case "call":
		p.answer(f)
	case "result":
		p.settle(f.ID, reply{value: f.Result})
	case "error":
		p.settle(f.ID, reply{err: errors.New(f.Error)})
	case "cancel":
		p.mu.Lock()
		if call := p.inflight[f.ID]; call != nil {
			call.cancel(errors.New("Cancelled by caller"))
			delete(p.inflight, f.ID)
		}
		p.mu.Unlock()
	case "announce":
		p.mu.Lock()
		p.announced = nil
		for _, name := range f.Services {
			if !slices.Contains(p.announced, name) {
				p.announced = append(p.announced, name)
			}
		}
		p.mu.Unlock()
	case "event":
		p.mu.Lock()
		handlers := slices.Clone(p.eventHandlers)
		p.mu.Unlock()
		for _, handler := range handlers {
			handler(f.Service, f.Payload, f.To)
		}
	case "ping":
	default:
		p.fail(fmt.Errorf("Unknown frame: %s", message))
	}
}

func (p *RpcPeer) answer(f frame) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	turn := &dispatchTurn{done: make(chan struct{})}
	ctx = context.WithValue(ctx, dispatchTurnKey{}, turn)
	call := &inflightCall{cancel: cancel}
	p.inflight[f.ID] = call
	// Add while holding the same lock as shutdown, so Wait cannot race new handlers after Close.
	p.wg.Go(func() {
		defer cancel(nil)
		defer turn.release()
		result, err := p.dispatch(ctx, f.Method, f.Args)
		turn.release()
		if err != nil {
			err = p.connection.Send(struct {
				Kind  string `json:"kind"`
				ID    uint64 `json:"id"`
				Error string `json:"error"`
			}{"error", f.ID, err.Error()})
		} else {
			err = p.connection.Send(struct {
				Kind   string `json:"kind"`
				ID     uint64 `json:"id"`
				Result any    `json:"result"`
			}{"result", f.ID, result})
		}
		if err != nil {
			p.fail(err)
		}
		p.mu.Lock()
		if p.inflight[f.ID] == call {
			delete(p.inflight, f.ID)
		}
		p.mu.Unlock()
	})
	p.mu.Unlock()
	// A TypeScript async handler runs through its first await before onMessage returns. Go handlers expose that boundary with Yield, without blocking replies or cancellation on the reader.
	<-turn.done
}

func (p *RpcPeer) dispatch(ctx context.Context, method string, args []json.RawMessage) (result any, err error) {
	// Synchronous throws and asynchronous rejections both become error replies.
	defer func() {
		if thrown := recover(); thrown != nil {
			result = nil
			err = fmt.Errorf("%v", thrown)
		}
	}()
	name, member, hasDot := strings.Cut(method, ".")
	p.mu.Lock()
	local, exists := p.services[name]
	var handler Handler
	if exists {
		handler = local[member]
	}
	p.mu.Unlock()
	if !hasDot || !exists {
		if p.options.Forward == nil {
			return nil, fmt.Errorf("No service provides %s", method)
		}
		return p.options.Forward(context.WithoutCancel(ctx), method, args)
	}
	if handler == nil {
		return nil, fmt.Errorf("Unknown method: %s", method)
	}
	return handler(ctx, args)
}

func (p *RpcPeer) liveness(deadMs float64) {
	interval := nodeTimerDelay(float64(int64(deadMs / 3)))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.mu.Lock()
			last := p.lastFrameAt
			p.mu.Unlock()
			if float64(time.Since(last))/float64(time.Millisecond) > deadMs {
				_ = p.Close()
				return
			}
			if err := p.connection.Send(struct {
				Kind string `json:"kind"`
			}{"ping"}); err != nil {
				p.fail(err)
				return
			}
		}
	}
}

// Node clamps invalid or out-of-range timer delays to one millisecond and truncates fractional milliseconds.
func nodeTimerDelay(ms float64) time.Duration {
	if !(ms >= 1 && ms <= 2147483647) {
		return time.Millisecond
	}
	return time.Duration(int64(ms)) * time.Millisecond
}
