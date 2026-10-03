package routingtest

// Ports packages/server/src/testing/client.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// WireChannel carries a ProtocolTestClient's bytes to a server. Send and SendFragmented return after the transport accepts the bytes; Close returns after the transport has closed.
type WireChannel interface {
	Send(chunk []byte) error
	SendFragmented(chunk []byte, splitAt int) error
	Close() error
}

type messageWaiter struct {
	predicate func(protocol.ServerMessage) bool
	settled   chan struct{}
	message   protocol.ServerMessage
	err       error
}

// ProtocolTestClient decodes server frames into an ordered message log and lets a test wait for messages by predicate. A transport delivers received bytes with Receive, its close with MarkClosed, and its errors with Fail. Wait predicates run under the client's lock, so a predicate must not call the client.
type ProtocolTestClient struct {
	channel        WireChannel
	closedDeferred *Deferred[struct{}]

	mu              sync.Mutex
	decoder         *protocol.ServerMessageDecoder
	messages        []protocol.ServerMessage
	waiters         []*messageWaiter
	requestSequence int
	attachment      *protocol.SessionTarget
	closedValue     bool
}

// NewProtocolTestClient returns a client that sends through channel and decodes with the default frame limit.
func NewProtocolTestClient(channel WireChannel) *ProtocolTestClient {
	decoder, err := protocol.NewServerMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		// The default decoder options are valid by construction.
		panic(err)
	}
	return &ProtocolTestClient{channel: channel, closedDeferred: NewDeferred[struct{}](), decoder: decoder}
}

// Messages returns a copy of every decoded server message in arrival order.
func (c *ProtocolTestClient) Messages() []protocol.ServerMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.messages)
}

// Closed reports whether the transport has closed.
func (c *ProtocolTestClient) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closedValue
}

// Hello sends a hello for version, or for protocol.ProtocolVersion when version is nil, and returns the first hello or hello_error message.
func (c *ProtocolTestClient) Hello(ctx context.Context, version *float64) (protocol.ServerMessage, error) {
	requested := float64(protocol.ProtocolVersion)
	if version != nil {
		requested = *version
	}
	waiter := c.register(0, func(message protocol.ServerMessage) bool {
		switch message.(type) {
		case protocol.ServerHello, protocol.ServerHelloError:
			return true
		}
		return false
	})
	if err := c.SendMessage(protocol.ClientHello{Version: requested}); err != nil {
		c.unregister(waiter)
		return nil, err
	}
	return c.wait(ctx, waiter)
}

// RequestService sends one request and returns its response. A nil id selects the next request-<n> ID.
func (c *ProtocolTestClient) RequestService(ctx context.Context, target protocol.RpcTarget, call chord.ServiceCall, id *string) (protocol.ResponseEnvelope, error) {
	c.mu.Lock()
	var requestID string
	if id != nil {
		requestID = *id
	} else {
		c.requestSequence++
		requestID = fmt.Sprintf("request-%d", c.requestSequence)
	}
	c.mu.Unlock()
	waiter := c.register(0, func(message protocol.ServerMessage) bool {
		response, ok := message.(protocol.ResponseEnvelope)
		return ok && response.Id == requestID
	})
	value, err := serviceCallValue(call)
	if err == nil {
		err = c.SendMessage(protocol.RequestEnvelope{Id: requestID, Target: target, Call: value})
	}
	if err != nil {
		c.unregister(waiter)
		return protocol.ResponseEnvelope{}, err
	}
	message, err := c.wait(ctx, waiter)
	if err != nil {
		return protocol.ResponseEnvelope{}, err
	}
	return message.(protocol.ResponseEnvelope), nil
}

// serviceCallValue converts a service call to its protocol value without validating it, so a test can send an invalid call.
func serviceCallValue(call chord.ServiceCall) (any, error) {
	data, err := json.Marshal(call)
	if err != nil {
		return nil, err
	}
	return protocol.FromJSON(data)
}

// Attach requests pi.session-management attach for sessionID on serverID.
func (c *ProtocolTestClient) Attach(ctx context.Context, serverID, sessionID string) (protocol.ResponseEnvelope, error) {
	argument, err := json.Marshal(sessionID)
	if err != nil {
		return protocol.ResponseEnvelope{}, err
	}
	return c.RequestService(ctx, protocol.ServerTarget{ServerId: serverID}, chord.ServiceCall{ServiceId: "pi.session-management", Member: "attach", Args: []json.RawMessage{argument}}, nil)
}

// RequestSessionService sends call to the client's current attachment for sessionID. Without that attachment it targets the attachment ID "missing-attachment".
func (c *ProtocolTestClient) RequestSessionService(ctx context.Context, serverID, sessionID string, call chord.ServiceCall, id *string) (protocol.ResponseEnvelope, error) {
	c.mu.Lock()
	target := protocol.SessionTarget{ServerId: serverID, SessionId: sessionID, AttachmentId: "missing-attachment"}
	if attachment := c.attachment; attachment != nil && attachment.SessionId == sessionID {
		target = protocol.SessionTarget{ServerId: serverID, SessionId: attachment.SessionId, AttachmentId: attachment.AttachmentId}
	}
	c.mu.Unlock()
	return c.RequestService(ctx, target, call, id)
}

// SendMessage encodes message as one frame and sends it.
func (c *ProtocolTestClient) SendMessage(message protocol.ClientMessage) error {
	frame, err := protocol.EncodeClientMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		return err
	}
	return c.channel.Send(frame)
}

// SendBytes sends raw bytes.
func (c *ProtocolTestClient) SendBytes(chunk []byte) error {
	return c.channel.Send(chunk)
}

// SendFragmentedMessage encodes message as one frame and sends it as two chunks split at splitAt.
func (c *ProtocolTestClient) SendFragmentedMessage(message protocol.ClientMessage, splitAt int) error {
	frame, err := protocol.EncodeClientMessage(message, protocol.FrameDecoderOptions{})
	if err != nil {
		return err
	}
	return c.channel.SendFragmented(frame, splitAt)
}

// Next returns the first message, received or still to come, that predicate accepts.
func (c *ProtocolTestClient) Next(ctx context.Context, predicate func(protocol.ServerMessage) bool) (protocol.ServerMessage, error) {
	return c.NextFrom(ctx, 0, predicate)
}

// NextFrom returns the first message at or after index that predicate accepts. A negative index counts from the end of the log. The wait fails when the client is closed, when Fail reports an error, or when ctx ends.
func (c *ProtocolTestClient) NextFrom(ctx context.Context, index int, predicate func(protocol.ServerMessage) bool) (protocol.ServerMessage, error) {
	return c.wait(ctx, c.register(index, predicate))
}

// WaitForClose returns after the transport closes, or with the context's error.
func (c *ProtocolTestClient) WaitForClose(ctx context.Context) error {
	select {
	case <-c.closedDeferred.Promise():
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Close closes the channel.
func (c *ProtocolTestClient) Close() error {
	return c.channel.Close()
}

// Receive decodes chunk, records each message and the current Session attachment, and settles the waiters each message satisfies. A decode failure fails the pending waiters.
func (c *ProtocolTestClient) Receive(chunk []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	messages, err := c.decoder.Push(chunk)
	if err != nil {
		c.failLocked(err)
		return
	}
	for _, message := range messages {
		if envelope, ok := message.(protocol.AttachmentEnvelope); ok {
			c.attachment = nil
			if envelope.Attachment != nil {
				c.attachment = &protocol.SessionTarget{SessionId: envelope.Attachment.SessionId, AttachmentId: envelope.Attachment.AttachmentId}
			}
		}
		c.messages = append(c.messages, message)
		c.waiters = slices.DeleteFunc(c.waiters, func(waiter *messageWaiter) bool {
			if !waiter.predicate(message) {
				return false
			}
			waiter.settle(message, nil)
			return true
		})
	}
}

// MarkClosed records the transport close once and fails the pending waiters.
func (c *ProtocolTestClient) MarkClosed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closedValue {
		return
	}
	c.closedValue = true
	c.closedDeferred.Resolve(struct{}{})
	c.failLocked(errors.New("Wire connection closed"))
}

// Fail fails every pending waiter with err. Later waits are unaffected.
func (c *ProtocolTestClient) Fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failLocked(err)
}

func (c *ProtocolTestClient) failLocked(err error) {
	for _, waiter := range c.waiters {
		waiter.settle(nil, err)
	}
	c.waiters = nil
}

// register returns a waiter already settled by a logged message or a closed client, or a pending waiter added to the client.
func (c *ProtocolTestClient) register(index int, predicate func(protocol.ServerMessage) bool) *messageWaiter {
	waiter := &messageWaiter{predicate: predicate, settled: make(chan struct{})}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, message := range c.messages[sliceStart(index, len(c.messages)):] {
		if predicate(message) {
			waiter.settle(message, nil)
			return waiter
		}
	}
	if c.closedValue {
		waiter.settle(nil, errors.New("Wire client is closed"))
		return waiter
	}
	c.waiters = append(c.waiters, waiter)
	return waiter
}

func (c *ProtocolTestClient) unregister(waiter *messageWaiter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waiters = slices.DeleteFunc(c.waiters, func(candidate *messageWaiter) bool { return candidate == waiter })
}

func (c *ProtocolTestClient) wait(ctx context.Context, waiter *messageWaiter) (protocol.ServerMessage, error) {
	select {
	case <-waiter.settled:
	case <-ctx.Done():
		c.unregister(waiter)
		select {
		case <-waiter.settled:
		default:
			return nil, context.Cause(ctx)
		}
	}
	return waiter.message, waiter.err
}

func (waiter *messageWaiter) settle(message protocol.ServerMessage, err error) {
	waiter.message, waiter.err = message, err
	close(waiter.settled)
}

// sliceStart applies Array.prototype.slice start semantics: a negative index counts from the end, and the result is clamped to [0, length].
func sliceStart(index, length int) int {
	if index < 0 {
		return max(length+index, 0)
	}
	return min(index, length)
}

// ConnectUnixTestClient connects a ProtocolTestClient to the Unix socket at path. Received bytes, socket errors, and the socket close reach the client; closing the client closes the socket and waits for its close.
func ConnectUnixTestClient(ctx context.Context, path string) (*ProtocolTestClient, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	channel := &unixWireChannel{conn: conn, closed: make(chan struct{})}
	client := NewProtocolTestClient(channel)
	go channel.read(client)
	return client, nil
}

type unixWireChannel struct {
	conn      net.Conn
	destroyed atomic.Bool
	closed    chan struct{}
}

// read delivers socket bytes until the socket ends. An error the client did not cause by closing reaches Fail before the close reaches MarkClosed, as a socket's error event precedes its close event.
func (channel *unixWireChannel) read(client *ProtocolTestClient) {
	defer close(channel.closed)
	buffer := make([]byte, 64*1024)
	for {
		n, err := channel.conn.Read(buffer)
		if n > 0 {
			client.Receive(bytes.Clone(buffer[:n]))
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) && !channel.destroyed.Load() {
			client.Fail(err)
		}
		channel.destroyed.Store(true)
		_ = channel.conn.Close()
		client.MarkClosed()
		return
	}
}

func (channel *unixWireChannel) Send(chunk []byte) error {
	_, err := channel.conn.Write(chunk)
	return err
}

func (channel *unixWireChannel) SendFragmented(chunk []byte, splitAt int) error {
	split := sliceStart(splitAt, len(chunk))
	if err := channel.Send(chunk[:split]); err != nil {
		return err
	}
	return channel.Send(chunk[split:])
}

// Close destroys the socket unless it is already destroyed and waits for its close.
func (channel *unixWireChannel) Close() error {
	if channel.destroyed.Swap(true) {
		<-channel.closed
		return nil
	}
	_ = channel.conn.Close()
	<-channel.closed
	return nil
}
