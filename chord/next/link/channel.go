package link

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/link/channel.ts. A host's end of a link (§3, §22, 2b): the handshake, the link's codec,
// the checks of every message (skipped on a trusted link), the heartbeat, and messages handled in order while one waits
// for an asynchronous inflate; a protocol error closes the link.
//
// Go forms: the `void | Promise<void>` a handler returns is a *Handling (nil: handled), settled by whoever finishes
// the work; a throw is a returned error. Like Pi's single event loop the channel handles one message at a time:
// link deliveries arrive one at a time on a link's goroutine, and the continuation of a Handling runs on the
// goroutine that settles it; the held queue is guarded so those two meet only at its edge.

// Handling is the Go form of the Promise a message handler returns while the message waits (for an asynchronous
// inflate): settled once, by Resolve or Reject, which runs the continuations registered with Then.
type Handling struct {
	mu        sync.Mutex
	settled   bool
	err       error
	callbacks []func(err error)
}

// NewHandling returns a pending Handling.
func NewHandling() *Handling { return &Handling{} }

// Resolve settles h successfully; later settlements are ignored.
func (h *Handling) Resolve() { h.settle(nil) }

// Reject settles h with err; later settlements are ignored.
func (h *Handling) Reject(err error) {
	if err == nil {
		err = errors.New("handling rejected")
	}
	h.settle(err)
}

func (h *Handling) settle(err error) {
	h.mu.Lock()
	if h.settled {
		h.mu.Unlock()
		return
	}
	h.settled, h.err = true, err
	callbacks := h.callbacks
	h.callbacks = nil
	h.mu.Unlock()
	for _, callback := range callbacks {
		callback(err)
	}
}

// then runs callback with h's outcome: now when settled already, else when it settles.
func (h *Handling) then(callback func(err error)) {
	h.mu.Lock()
	if !h.settled {
		h.callbacks = append(h.callbacks, callback)
		h.mu.Unlock()
		return
	}
	err := h.err
	h.mu.Unlock()
	callback(err)
}

// Then is `promise.then(() => run())`: a Handling that settles when h does, after run on success; a rejection passes
// through without running it.
func (h *Handling) Then(run func()) *Handling {
	next := NewHandling()
	h.then(func(err error) {
		if err != nil {
			next.Reject(err)
			return
		}
		run()
		next.Resolve()
	})
	return next
}

// ChannelHandler is what the owner of a channel handles.
type ChannelHandler interface {
	// Opened: the peer's `hello` arrived: the link is open.
	Opened()
	// Message handles a checked message after the handshake (never `hello`, `ping` or `pong`). An error is a protocol
	// error. A non-nil Handling (the message waits for an asynchronous inflate) holds every later message until it
	// settles; a rejection is a protocol error too.
	Message(message Message) (*Handling, error)
	// Closed: the link closed, from either end; called once.
	Closed()
}

// ChannelOptions is what a channel is made with besides its link and handler.
type ChannelOptions struct {
	Codec Codec
	// Incarnation is this host's incarnation, sent in `hello`.
	Incarnation string
	// Calls is how many concurrent calls this side accepts, sent in `hello`.
	Calls int
	// Trusted skips the message schema check (framing, the codec's decode and the owners' checks still run).
	Trusted bool
	// Report is where protocol errors go.
	Report func(err error)
	// Now is the clock of the heartbeat in milliseconds (`Date.now()`); nil reads the wall clock. Tests set it.
	Now func() int64
}

type channelKind int

const (
	channelGreeting channelKind = iota
	channelOpen
	channelClosed
)

// Channel is one end of a link. It sends `hello { protocol, incarnation, calls }` first and nothing else until the
// peer's arrived (its own messages wait). Every message is decoded by the link's codec and, unless the link is
// trusted, checked against the message schema; a first message that is no `hello` of this codec, a second `hello`,
// another protocol version, or a message failing a check closes the link and is reported. The heartbeat: every
// received message stamps `lastReceived`; the host's timer calls Tick, which pings a silent open link and closes a link
// silent for too long (the handshake included); a `ping` is answered with `pong` at once. A message the handler
// handles asynchronously holds the later ones, `ping` included, until it settles; they are handled in order then, and
// dropped when the link closes meanwhile.
type Channel struct {
	link    Link
	codec   Codec
	handler ChannelHandler
	report  func(err error)
	trusted bool
	ctx     context.Context
	now     func() int64
	hello   []byte

	mu           sync.Mutex
	state        channelKind
	queued       [][]byte
	peer         string
	peerCalls    int
	lastReceived int64
	// pinged is whether a `ping` went out since the last received message.
	pinged bool
	// held is the messages received while one is handled asynchronously, in order, and the index of the next to handle;
	// nil while none waits.
	held *heldMessages
	// waiting is whether a message is being handled asynchronously: the held ones wait.
	waiting bool
}

type heldMessages struct {
	queue [][]byte
	next  int
}

// NewChannel returns a channel over link; Start starts it. The only error is a `hello` the codec cannot write.
func NewChannel(link Link, handler ChannelHandler, options ChannelOptions, ctx context.Context) (*Channel, error) {
	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	hello, err := options.Codec.Encode(&HelloMessage{Protocol: data.ProtocolVersion, Incarnation: options.Incarnation, Calls: options.Calls})
	if err != nil {
		return nil, err
	}
	return &Channel{
		link: link, codec: options.Codec, handler: handler, report: options.Report, trusted: options.Trusted, ctx: ctx, now: now,
		hello: hello, lastReceived: now(),
	}, nil
}

// Start sends `hello` and starts listening: messages that arrived already are handled now, and a link closed already
// calls `Closed` now; so the handler is complete before it starts.
func (c *Channel) Start() {
	c.link.Send(c.hello)
	c.link.OnMessage(c.receive)
	c.link.OnClose(c.linkClosed)
}

// Peer is the peer's incarnation, from its `hello`; empty before it arrived (nothing else can arrive before it).
func (c *Channel) Peer() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == channelOpen {
		return c.peer
	}
	return ""
}

// PeerCalls is how many concurrent calls the peer accepts, from its `hello`; 0 before it arrived.
func (c *Channel) PeerCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == channelOpen {
		return c.peerCalls
	}
	return 0
}

// Opened reports whether the peer's `hello` arrived.
func (c *Channel) Opened() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == channelOpen
}

// Closed reports whether the channel closed.
func (c *Channel) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == channelClosed
}

// Send sends one message; before the handshake it waits, after the close it is dropped. It returns an error for a
// message the codec cannot write.
func (c *Channel) Send(message Message) error {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	if state == channelClosed {
		return nil
	}
	bytes, err := c.codec.Encode(message)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.state == channelGreeting {
		c.queued = append(c.queued, bytes)
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	c.link.Send(bytes)
	return nil
}

// Close closes the link.
func (c *Channel) Close() { c.link.Close() }

// Tick is the heartbeat, at now (milliseconds): a link silent for close ms (the handshake included) closes; an open
// one silent for ping ms sends one `ping`.
func (c *Channel) Tick(now, ping, close int64) {
	c.mu.Lock()
	silent := now - c.lastReceived
	sendPing := false
	if silent >= close {
		c.mu.Unlock()
		c.Close()
		return
	}
	if silent >= ping && !c.pinged && c.state == channelOpen {
		c.pinged = true
		sendPing = true
	}
	c.mu.Unlock()
	if sendPing {
		if bytes, err := c.codec.Encode(&PingMessage{}); err == nil {
			c.link.Send(bytes)
		}
	}
}

func (c *Channel) receive(bytes []byte) {
	c.mu.Lock()
	if c.state == channelClosed {
		c.mu.Unlock()
		return
	}
	c.lastReceived = c.now()
	c.pinged = false
	if c.held != nil {
		c.held.queue = append(c.held.queue, bytes)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.handle(bytes)
}

// handle handles one received message; one the handler handles asynchronously holds the next until it settles.
func (c *Channel) handle(bytes []byte) {
	if err := c.handleBytes(bytes); err != nil {
		c.failed(err)
	}
}

func (c *Channel) handleBytes(bytes []byte) error {
	c.mu.Lock()
	state, queued := c.state, c.queued
	c.mu.Unlock()
	if state == channelGreeting {
		return c.greet(bytes, queued)
	}
	message, err := c.decode(bytes)
	if err != nil {
		return err
	}
	switch message.(type) {
	case *HelloMessage:
		return errors.New("handshake: a second hello")
	case *PingMessage:
		// Control of the link itself, never queued behind the window.
		pong, err := c.codec.Encode(&PongMessage{})
		if err != nil {
			return err
		}
		c.link.Send(pong)
	case *PongMessage:
	default:
		handling, err := c.handler.Message(message)
		if err != nil {
			return err
		}
		if handling != nil {
			c.hold(handling)
		}
	}
	return nil
}

// hold holds later messages until handling settles, then handles them in order (each may hold again).
func (c *Channel) hold(handling *Handling) {
	c.mu.Lock()
	if c.held == nil {
		c.held = &heldMessages{}
	}
	c.waiting = true
	c.mu.Unlock()
	handling.then(func(err error) {
		if err != nil {
			c.failed(err)
			return
		}
		c.mu.Lock()
		c.waiting = false
		c.mu.Unlock()
		c.release()
	})
}

// release: the held message was handled: the ones behind it are handled in order, until one holds again (the handled
// ones are dropped from the queue then).
func (c *Channel) release() {
	for {
		c.mu.Lock()
		held := c.held
		if held == nil {
			c.mu.Unlock()
			return
		}
		if held.next >= len(held.queue) {
			c.held = nil
			c.mu.Unlock()
			return
		}
		if c.state != channelOpen {
			c.mu.Unlock()
			return
		}
		bytes := held.queue[held.next]
		held.queue[held.next] = nil
		held.next++
		c.mu.Unlock()
		c.handle(bytes)
		c.mu.Lock()
		if c.waiting && c.held != nil {
			c.held.queue = c.held.queue[c.held.next:]
			c.held.next = 0
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
}

// failed is a protocol error: reported, and the link closes.
func (c *Channel) failed(err error) {
	if c.Closed() {
		return
	}
	c.report(err)
	c.Close()
}

// decode is a message from its bytes, checked against the message schema unless the link is trusted.
func (c *Channel) decode(bytes []byte) (Message, error) {
	message, err := c.codec.Decode(bytes)
	if err != nil || c.trusted {
		return message, err
	}
	return CheckMessage(message, c.ctx)
}

// greet handles the first message: the peer's `hello` in this link's codec and protocol version.
func (c *Channel) greet(bytes []byte, queued [][]byte) error {
	message, err := c.decode(bytes)
	if err != nil {
		return &causedError{message: fmt.Sprintf("handshake: the first message is no %s message", c.codec.Name()), cause: err}
	}
	hello, isHello := message.(*HelloMessage)
	if !isHello {
		return fmt.Errorf("handshake: %s before hello", message.Kind())
	}
	if hello.Protocol != data.ProtocolVersion {
		return fmt.Errorf("handshake: protocol %d, this host speaks %d", hello.Protocol, data.ProtocolVersion)
	}
	c.mu.Lock()
	c.state, c.peer, c.peerCalls, c.queued = channelOpen, hello.Incarnation, hello.Calls, nil
	c.mu.Unlock()
	for _, bytes := range queued {
		c.link.Send(bytes)
	}
	c.handler.Opened()
	return nil
}

func (c *Channel) linkClosed() {
	c.mu.Lock()
	if c.state == channelClosed {
		c.mu.Unlock()
		return
	}
	c.state = channelClosed
	c.held = nil
	c.queued = nil
	c.mu.Unlock()
	c.handler.Closed()
}
