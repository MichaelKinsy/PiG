package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"sync"

	"github.com/google/uuid"
)

// CoordinatorConnectionEvent carries peer membership changes or an opaque routed payload.
// A nil Payload means absent; json.RawMessage("null") is an explicit JSON null.
type CoordinatorConnectionEvent struct {
	Type    string
	PeerID  string
	From    string
	Payload json.RawMessage
}

// CoordinatorConnectionOptions identifies this server generation and its private forwarding endpoint.
type CoordinatorConnectionOptions struct {
	ControlPath        string
	Endpoint           string
	ServerConnectionID *string
}

// CoordinatorConnectionListener is a callback with stable reference identity.
// Go functions cannot be compared; OnEvent identifies listeners by this pointer instead.
type CoordinatorConnectionListener struct {
	listen func(CoordinatorConnectionEvent)
}

// NewCoordinatorConnectionListener creates a listener reference. Reuse it to register the same callback again.
func NewCoordinatorConnectionListener(listener func(CoordinatorConnectionEvent)) *CoordinatorConnectionListener {
	return &CoordinatorConnectionListener{listen: listener}
}

type coordinatorListener struct {
	id       uint64
	listener *CoordinatorConnectionListener
}

// CoordinatorConnection is the server-side endpoint of the coordinator's opaque message router.
// Event callbacks run in receive order, outside the state lock. They may send, close or unsubscribe.
type CoordinatorConnection struct {
	mu                 sync.Mutex
	writeMu            sync.Mutex
	controlPath        string
	endpoint           string
	serverConnectionID string
	socket             net.Conn
	connecting         bool
	registered         bool
	closed             bool
	wasReplaced        bool
	replaced           chan struct{}
	peerIDs            []string
	listeners          []coordinatorListener
	nextListenerID     uint64
	registrationDone   chan struct{}
	registrationErr    error
	registrationEnded  bool
}

// NewCoordinatorConnection creates an unconnected server generation with a random ID when none is supplied.
func NewCoordinatorConnection(options CoordinatorConnectionOptions) *CoordinatorConnection {
	var id string
	if options.ServerConnectionID == nil {
		id = uuid.NewString()
	} else {
		id = *options.ServerConnectionID
	}
	return &CoordinatorConnection{
		controlPath: options.ControlPath, endpoint: options.Endpoint, serverConnectionID: id,
		replaced: make(chan struct{}),
	}
}

func (c *CoordinatorConnection) ControlPath() string        { return c.controlPath }
func (c *CoordinatorConnection) ServerConnectionID() string { return c.serverConnectionID }

// Replaced closes once on replacement or an unexpected disconnect, but not on an explicit Close.
func (c *CoordinatorConnection) Replaced() <-chan struct{} { return c.replaced }

func (c *CoordinatorConnection) WasReplaced() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wasReplaced
}

// PeerIDs returns a detached snapshot in the coordinator's insertion order.
func (c *CoordinatorConnection) PeerIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.peerIDs...)
}

// OnEvent adds a listener reference once, in insertion order.
// Each cleanup removes that reference, including any later re-registration.
func (c *CoordinatorConnection) OnEvent(listener *CoordinatorConnectionListener) func() {
	c.mu.Lock()
	if !slices.ContainsFunc(c.listeners, func(item coordinatorListener) bool { return item.listener == listener }) {
		id := c.nextListenerID
		c.nextListenerID++
		c.listeners = append(c.listeners, coordinatorListener{id, listener})
	}
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.listeners = slices.DeleteFunc(c.listeners, func(item coordinatorListener) bool { return item.listener == listener })
		c.mu.Unlock()
	}
}

// Connect waits for a validated registration. Cancellation closes the pending connection.
func (c *CoordinatorConnection) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.socket != nil || c.connecting {
		c.mu.Unlock()
		return errors.New("Coordinator server is already connected")
	}
	c.connecting = true
	c.mu.Unlock()
	socket, err := (&net.Dialer{}).DialContext(ctx, "unix", c.controlPath)
	c.mu.Lock()
	c.connecting = false
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if c.closed {
		c.mu.Unlock()
		_ = socket.Close()
		return errors.New("Coordinator server closed")
	}
	c.socket = socket
	c.registrationDone = make(chan struct{})
	c.registrationErr = nil
	c.registrationEnded = false
	registrationDone := c.registrationDone
	c.mu.Unlock()
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		err := readControlLines(socket, c.handleMessage)
		if errors.Is(err, io.EOF) {
			err = errors.New("Coordinator connection closed")
		}
		_ = socket.Close()
		c.disconnected(socket, err)
	}()
	stop := context.AfterFunc(ctx, func() { _ = socket.Close() })
	defer stop()
	c.writeMu.Lock()
	err = writeControlLine(socket, struct {
		Type               string `json:"type"`
		Protocol           int    `json:"protocol"`
		ServerConnectionID string `json:"serverConnectionId"`
		Endpoint           string `json:"endpoint"`
	}{"register_server", CoordinatorProtocolVersion, c.serverConnectionID, c.endpoint})
	c.writeMu.Unlock()
	if err != nil {
		_ = socket.Close()
		<-readDone
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	select {
	case <-registrationDone:
		c.mu.Lock()
		err = c.registrationErr
		c.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		_ = socket.Close()
		<-readDone
		return ctx.Err()
	}
}

// Send writes a payload to a named peer after registration and waits for the socket write.
func (c *CoordinatorConnection) Send(peerID string, payload any) error {
	return c.write(struct {
		Type    string `json:"type"`
		To      string `json:"to"`
		Payload any    `json:"payload"`
	}{"send", peerID, payload})
}

// Broadcast writes a payload to every connected peer after registration.
func (c *CoordinatorConnection) Broadcast(payload any) error {
	return c.write(struct {
		Type    string `json:"type"`
		Payload any    `json:"payload"`
	}{"broadcast", payload})
}

func (c *CoordinatorConnection) write(message any) error {
	c.mu.Lock()
	socket := c.socket
	if !c.registered || socket == nil || c.closed {
		c.mu.Unlock()
		return errors.New("Coordinator server is not connected")
	}
	c.mu.Unlock()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeControlLine(socket, message)
}

// Close destroys the socket and clears membership without marking an intentional close as replacement.
func (c *CoordinatorConnection) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.socket != nil {
		_ = c.socket.Close()
		c.socket = nil
	}
	c.peerIDs = nil
	c.completeRegistration(errors.New("Coordinator server closed"))
}

func (c *CoordinatorConnection) completeRegistration(err error) {
	if c.registrationDone != nil && !c.registrationEnded {
		c.registrationErr = err
		c.registrationEnded = true
		close(c.registrationDone)
	}
}

func (c *CoordinatorConnection) markReplaced() {
	if !c.wasReplaced {
		c.wasReplaced = true
		close(c.replaced)
	}
}

func (c *CoordinatorConnection) disconnected(socket net.Conn, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.socket == socket {
		c.socket = nil
	}
	if c.closed {
		return
	}
	c.completeRegistration(err)
	c.markReplaced()
}

func (c *CoordinatorConnection) handleMessage(line json.RawMessage) error {
	message, err := decodeControlMessage(line)
	if err != nil {
		return errors.New("Coordinator sent an invalid message")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(line, &fields); err != nil {
		return errors.New("Coordinator sent an invalid message")
	}
	stringField := func(name string) bool {
		value := fields[name]
		return len(value) > 0 && value[0] == '"'
	}
	c.mu.Lock()
	var event CoordinatorConnectionEvent
	switch message.Type {
	case "server_registered":
		var peers []json.RawMessage
		if !stringField("serverConnectionId") || len(fields["peers"]) == 0 || fields["peers"][0] != '[' || json.Unmarshal(fields["peers"], &peers) != nil {
			c.mu.Unlock()
			return errors.New("Coordinator sent an invalid message")
		}
		for _, peer := range peers {
			if len(peer) == 0 || peer[0] != '"' {
				c.mu.Unlock()
				return errors.New("Coordinator sent an invalid message")
			}
		}
		if message.ServerConnectionID != c.serverConnectionID {
			c.mu.Unlock()
			return errors.New("Coordinator returned an invalid server registration")
		}
		for _, peer := range peers {
			var id string
			if err := json.Unmarshal(peer, &id); err != nil {
				c.mu.Unlock()
				return errors.New("Coordinator sent an invalid message")
			}
			if !slices.Contains(c.peerIDs, id) {
				c.peerIDs = append(c.peerIDs, id)
			}
		}
		c.registered = true
		c.completeRegistration(nil)
		c.mu.Unlock()
		return nil
	case "server_replaced":
		c.markReplaced()
		c.mu.Unlock()
		return nil
	case "peer_connected", "peer_disconnected":
		if !stringField("peerId") {
			c.mu.Unlock()
			return errors.New("Coordinator sent an invalid message")
		}
		if message.Type == "peer_connected" && !slices.Contains(c.peerIDs, message.PeerID) {
			c.peerIDs = append(c.peerIDs, message.PeerID)
		} else if message.Type == "peer_disconnected" {
			c.peerIDs = slices.DeleteFunc(c.peerIDs, func(id string) bool { return id == message.PeerID })
		}
		event = CoordinatorConnectionEvent{Type: message.Type, PeerID: message.PeerID}
	case "message":
		if !stringField("from") {
			c.mu.Unlock()
			return errors.New("Coordinator sent an invalid message")
		}
		event = CoordinatorConnectionEvent{Type: "message", From: message.From, Payload: slices.Clone(message.Payload)}
	default:
		c.mu.Unlock()
		return errors.New("Coordinator sent an invalid message")
	}
	c.mu.Unlock()
	c.emit(event)
	return nil
}

func (c *CoordinatorConnection) emit(event CoordinatorConnectionEvent) {
	var nextID uint64
	for {
		c.mu.Lock()
		index := slices.IndexFunc(c.listeners, func(listener coordinatorListener) bool { return listener.id >= nextID })
		if index == -1 {
			c.mu.Unlock()
			return
		}
		listener := c.listeners[index]
		nextID = listener.id + 1
		c.mu.Unlock()
		listener.listener.listen(event)
	}
}
