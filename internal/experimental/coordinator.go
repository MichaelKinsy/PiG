package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// CoordinatorProtocolVersion is the exact upstream-owned coordinator wire version.
const CoordinatorProtocolVersion = 3

const (
	coordinatorStartTimeout = 10 * time.Second
	coordinatorRetry        = 10 * time.Millisecond
	emptyStartupGrace       = 30 * time.Second
	emptyShutdownGrace      = 250 * time.Millisecond
)

type serverPeer struct {
	serverConnectionID string
	endpoint           string
	socket             *routedSocket
}

type routedPeer struct {
	peerID string
	socket *routedSocket
}

type publicConnection struct {
	client   net.Conn
	upstream net.Conn
	cancel   context.CancelFunc
}

// coordinator owns both listening sockets and every accepted connection and forwarding goroutine.
// The mutex is the router's event-loop boundary; no socket I/O waits while it is held.
type coordinator struct {
	mu                 sync.Mutex
	wg                 sync.WaitGroup
	publicPath         string
	controlPath        string
	publicServer       *net.UnixListener
	controlServer      *net.UnixListener
	peers              map[string]*routedPeer
	peerOrder          []string
	controlConnections map[*routedSocket]struct{}
	publicConnections  map[*publicConnection]struct{}
	currentServer      *serverPeer
	shuttingDown       bool
	emptyTimer         *time.Timer
	done               chan struct{}
	err                error
}

var coordinatorRunning atomic.Bool

// RunCoordinatorProcess serves the stable endpoint until shutdown, cancellation or the empty grace period.
// Unlike Node's process-owned event loop, Go explicitly joins the owned socket work before returning.
func RunCoordinatorProcess(ctx context.Context, args []string) error {
	if coordinatorRunning.Load() {
		return errors.New("Coordinator process is already running")
	}
	if len(args) < 2 || args[0] == "" || args[1] == "" {
		return errors.New("Coordinator requires public and control socket paths")
	}
	if !coordinatorRunning.CompareAndSwap(false, true) {
		return errors.New("Coordinator process is already running")
	}
	c, err := startCoordinator(args[0], args[1])
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		c.shutdown()
	case <-c.done:
	}
	<-c.done
	return c.err
}

func startCoordinator(publicPath, controlPath string) (*coordinator, error) {
	if err := removeStaleSocket(controlPath); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(publicPath); err != nil {
		return nil, err
	}
	controlServer, err := listenCoordinatorSocket(controlPath)
	if err != nil {
		return nil, errors.Join(err, cleanupSocket(controlPath), cleanupSocket(publicPath))
	}
	publicServer, err := listenCoordinatorSocket(publicPath)
	if err != nil {
		_ = controlServer.Close()
		return nil, errors.Join(err, cleanupSocket(controlPath), cleanupSocket(publicPath))
	}
	c := &coordinator{
		publicPath: publicPath, controlPath: controlPath,
		publicServer: publicServer, controlServer: controlServer,
		peers: make(map[string]*routedPeer), controlConnections: make(map[*routedSocket]struct{}),
		publicConnections: make(map[*publicConnection]struct{}), done: make(chan struct{}),
	}
	c.mu.Lock()
	c.scheduleEmptyShutdown(emptyStartupGrace)
	c.mu.Unlock()
	c.wg.Go(func() { c.acceptControl() })
	c.wg.Go(func() { c.acceptPublic() })
	return c, nil
}

func (c *coordinator) acceptControl() {
	for {
		conn, err := c.controlServer.Accept()
		if err != nil {
			return
		}
		socket := newRoutedSocket(conn)
		c.mu.Lock()
		if c.shuttingDown {
			c.mu.Unlock()
			socket.close()
			return
		}
		c.controlConnections[socket] = struct{}{}
		c.wg.Go(socket.writeLoop)
		c.wg.Go(func() { c.readControl(socket) })
		c.mu.Unlock()
	}
}

func (c *coordinator) readControl(socket *routedSocket) {
	var server *serverPeer
	var peer *routedPeer
	_ = readControlLines(socket.Conn, func(line json.RawMessage) error {
		message, err := decodeControlMessage(line)
		if err != nil {
			return err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.shuttingDown {
			return net.ErrClosed
		}
		if server == nil && peer == nil {
			switch message.Type {
			case "register_server":
				server, err = c.registerServer(socket, message)
			case "register_peer":
				peer, err = c.registerPeer(socket, message)
			default:
				err = errors.New("Coordinator connection did not register a role")
			}
			return err
		}
		if server != nil {
			if server == c.currentServer {
				return c.handleRoutedMessage("server", message)
			}
			return nil
		}
		return c.handleRoutedMessage(peer.peerID, message)
	})
	socket.close()
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.controlConnections, socket)
	if server != nil && c.currentServer == server {
		c.currentServer = nil
		c.notifyPeers(controlMessage{Type: "server_disconnected", ServerConnectionID: server.serverConnectionID})
	}
	if peer != nil && c.peers[peer.peerID] == peer {
		delete(c.peers, peer.peerID)
		c.peerOrder = slices.DeleteFunc(c.peerOrder, func(id string) bool { return id == peer.peerID })
		if c.currentServer != nil {
			c.writeRoutedLine(c.currentServer.socket, controlMessage{Type: "peer_disconnected", PeerID: peer.peerID})
		}
	}
	c.checkEmpty()
}

func validCoordinatorProtocol(value json.RawMessage) bool {
	var protocol float64
	return json.Unmarshal(value, &protocol) == nil && protocol == CoordinatorProtocolVersion
}

func (c *coordinator) registerServer(socket *routedSocket, message controlMessage) (*serverPeer, error) {
	if !validCoordinatorProtocol(message.Protocol) {
		return nil, errors.New("Unsupported coordinator protocol")
	}
	if message.ServerConnectionID == "" {
		return nil, errors.New("Coordinator serverConnectionId must be a string")
	}
	if message.Endpoint == "" {
		return nil, errors.New("Coordinator endpoint must be a string")
	}
	server := &serverPeer{message.ServerConnectionID, message.Endpoint, socket}
	previous := c.currentServer
	c.currentServer = server
	c.cancelEmptyShutdown()
	c.writeRoutedLine(socket, struct {
		Type               string   `json:"type"`
		ServerConnectionID string   `json:"serverConnectionId"`
		Peers              []string `json:"peers"`
	}{"server_registered", server.serverConnectionID, append([]string{}, c.peerOrder...)})
	if previous != nil {
		c.closePublicConnections()
		c.notifyPeers(controlMessage{Type: "server_disconnected", ServerConnectionID: previous.serverConnectionID})
		c.writeRoutedLine(previous.socket, controlMessage{Type: "server_replaced"})
	}
	c.notifyPeers(controlMessage{Type: "server_connected", ServerConnectionID: server.serverConnectionID})
	return server, nil
}

func (c *coordinator) registerPeer(socket *routedSocket, message controlMessage) (*routedPeer, error) {
	if !validCoordinatorProtocol(message.Protocol) {
		return nil, errors.New("Unsupported coordinator protocol")
	}
	if message.PeerID == "" {
		return nil, errors.New("Coordinator peerId must be a string")
	}
	if message.PeerID == "server" || c.peers[message.PeerID] != nil {
		return nil, fmt.Errorf("Coordinator peer is already connected: %s", message.PeerID)
	}
	peer := &routedPeer{message.PeerID, socket}
	c.peers[peer.peerID] = peer
	c.peerOrder = append(c.peerOrder, peer.peerID)
	c.cancelEmptyShutdown()
	registered := controlMessage{Type: "peer_registered", PeerID: peer.peerID}
	if c.currentServer != nil {
		registered.ServerConnectionID = c.currentServer.serverConnectionID
	}
	c.writeRoutedLine(socket, registered)
	if c.currentServer != nil {
		c.writeRoutedLine(c.currentServer.socket, controlMessage{Type: "peer_connected", PeerID: peer.peerID})
	}
	return peer, nil
}

func (c *coordinator) writeRoutedLine(socket *routedSocket, message any) {
	if err := socket.enqueue(message); err != nil {
		socket.close()
	}
}

func (c *coordinator) notifyPeers(message any) {
	for _, id := range c.peerOrder {
		c.writeRoutedLine(c.peers[id].socket, message)
	}
}

func (c *coordinator) handleRoutedMessage(from string, message controlMessage) error {
	switch message.Type {
	case "send":
		if message.To == "" {
			return errors.New("Coordinator message target must be a string")
		}
		var target *routedSocket
		if message.To == "server" {
			if c.currentServer != nil {
				target = c.currentServer.socket
			}
		} else if peer := c.peers[message.To]; peer != nil {
			target = peer.socket
		}
		if target != nil {
			return target.enqueue(controlMessage{Type: "message", From: from, Payload: message.Payload})
		}
		return nil
	case "broadcast":
		if from != "server" {
			return errors.New("Only the current server may broadcast")
		}
		for _, id := range c.peerOrder {
			if err := c.peers[id].socket.enqueue(controlMessage{Type: "message", From: from, Payload: message.Payload}); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("Unknown coordinator routing message: %s", message.Type)
	}
}

func (c *coordinator) acceptPublic() {
	for {
		client, err := c.publicServer.Accept()
		if err != nil {
			return
		}
		c.mu.Lock()
		if c.shuttingDown || c.currentServer == nil {
			c.mu.Unlock()
			_ = client.Close()
			continue
		}
		c.cancelEmptyShutdown()
		ctx, cancel := context.WithCancel(context.Background())
		connection := &publicConnection{client: client, cancel: cancel}
		c.publicConnections[connection] = struct{}{}
		endpoint := c.currentServer.endpoint
		c.wg.Go(func() { c.forwardPublic(ctx, connection, endpoint) })
		c.mu.Unlock()
	}
}

func (c *coordinator) forwardPublic(ctx context.Context, connection *publicConnection, endpoint string) {
	defer func() {
		connection.cancel()
		_ = connection.client.Close()
		c.mu.Lock()
		delete(c.publicConnections, connection)
		c.checkEmpty()
		c.mu.Unlock()
	}()
	upstream, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	c.mu.Lock()
	if _, active := c.publicConnections[connection]; !active || c.shuttingDown {
		c.mu.Unlock()
		return
	}
	connection.upstream = upstream
	c.mu.Unlock()
	var copies sync.WaitGroup
	copies.Go(func() {
		_, _ = io.Copy(upstream, connection.client)
		_ = upstream.Close()
		_ = connection.client.Close()
	})
	_, _ = io.Copy(connection.client, upstream)
	_ = upstream.Close()
	_ = connection.client.Close()
	copies.Wait()
}

func (c *coordinator) closePublicConnections() {
	for connection := range c.publicConnections {
		connection.cancel()
		_ = connection.client.Close()
		if connection.upstream != nil {
			_ = connection.upstream.Close()
		}
	}
	clear(c.publicConnections)
}

func (c *coordinator) isEmpty() bool {
	return c.currentServer == nil && len(c.peers) == 0 && len(c.publicConnections) == 0 && len(c.controlConnections) == 0
}

func (c *coordinator) checkEmpty() {
	if c.shuttingDown || !c.isEmpty() {
		c.cancelEmptyShutdown()
		return
	}
	c.scheduleEmptyShutdown(emptyShutdownGrace)
}

func (c *coordinator) scheduleEmptyShutdown(delay time.Duration) {
	if c.emptyTimer != nil || c.shuttingDown {
		return
	}
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.emptyTimer != timer {
			return
		}
		c.emptyTimer = nil
		if c.isEmpty() {
			c.beginShutdown()
		}
	})
	c.emptyTimer = timer
}

func (c *coordinator) cancelEmptyShutdown() {
	if c.emptyTimer != nil {
		c.emptyTimer.Stop()
		c.emptyTimer = nil
	}
}

func (c *coordinator) shutdown() {
	c.mu.Lock()
	c.beginShutdown()
	c.mu.Unlock()
}

func (c *coordinator) beginShutdown() {
	if c.shuttingDown {
		return
	}
	c.shuttingDown = true
	c.cancelEmptyShutdown()
	c.closePublicConnections()
	for socket := range c.controlConnections {
		socket.close()
	}
	_ = c.publicServer.Close()
	_ = c.controlServer.Close()
	go func() {
		c.wg.Wait()
		c.err = errors.Join(cleanupSocket(c.publicPath), cleanupSocket(c.controlPath))
		close(c.done)
	}()
}

func listenCoordinatorSocket(path string) (*net.UnixListener, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	return listener, nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("Coordinator path is not a socket: %s", path)
	}
	live, err := tryConnect(context.Background(), path)
	if err != nil {
		return err
	}
	if live != nil {
		_ = live.Close()
		return fmt.Errorf("Coordinator socket is already active: %s", path)
	}
	return cleanupSocket(path)
}

// cleanupSocket removes a coordinator socket file. Upstream skips this on
// win32, where Node listens on named pipes that leave no file; PiG listens on
// AF_UNIX socket files on Windows too, and they persist until removed.
func cleanupSocket(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func tryConnect(ctx context.Context, path string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if errors.Is(err, os.ErrNotExist) || connectionRefused(err) {
		return nil, nil
	}
	return conn, err
}

// CoordinatorStartupLease keeps an unregistered control connection alive while a server starts.
type CoordinatorStartupLease struct {
	socket net.Conn
}

// Close releases startup demand. It does not stop a coordinator that has other demand.
func (lease *CoordinatorStartupLease) Close() { _ = lease.socket.Close() }

// EnsureCoordinator connects to an existing router or launches one and waits for its control endpoint.
// The optional native spawn options select an explicit opt-in entry executable, without changing Stock CLI dispatch.
func EnsureCoordinator(ctx context.Context, publicPath, controlPath string, options ...InternalProcessSpawnOptions) (*CoordinatorStartupLease, error) {
	existing, err := tryConnect(ctx, controlPath)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return &CoordinatorStartupLease{existing}, nil
	}
	var spawnOptions InternalProcessSpawnOptions
	if len(options) > 0 {
		spawnOptions = options[0]
	}
	child, err := SpawnInternalProcess("coordinator", []string{publicPath, controlPath}, spawnOptions)
	if err != nil {
		return nil, err
	}
	deadline := time.NewTimer(coordinatorStartTimeout)
	defer deadline.Stop()
	for {
		socket, err := tryConnect(ctx, controlPath)
		if ctx.Err() != nil {
			if socket != nil {
				_ = socket.Close()
			}
			return nil, errors.Join(ctx.Err(), TerminateInternalProcess(child))
		}
		if err != nil {
			return nil, err
		}
		if socket != nil {
			return &CoordinatorStartupLease{socket}, nil
		}
		select {
		case <-child.Done():
			return nil, errors.New("Coordinator exited during startup")
		case <-deadline.C:
			return nil, errors.New("Timed out waiting for coordinator startup")
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), TerminateInternalProcess(child))
		case <-time.After(coordinatorRetry):
		}
	}
}
