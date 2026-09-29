package routing

// Ports packages/server/src/transports/unix/listener.ts
// Ports packages/server/src/transports/unix/preset.ts
// Ports packages/server/src/transports/unix/address.ts
// Ports packages/server/src/transports/unix/types.ts

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

type UnixListenerOptions struct {
	Path                   string
	Mode                   *float64
	MaxPendingBytes        *float64
	GracefulCloseTimeoutMs *float64
	MaxFrameLength         *float64
	OnError                func(error)
}
type UnixServerOptions struct {
	Path                     string
	ServerId                 string
	Mode                     *float64
	MaxPendingBytes          *float64
	GracefulCloseTimeoutMs   *float64
	MaxFrameLength           *float64
	HandshakeTimeoutMs       *float64
	OnConnectionCountChanged func(int)
	OnError                  func(error)
}

// UnixSocket is the native socket boundary consumed by UnixByteConnection. Half-close preserves delivery of a final frame before the peer closes or the grace expires.
type UnixSocket interface {
	net.Conn
	CloseWrite() error
}

// UnixByteConnection owns FIFO writes, pending-byte admission, and one joined graceful close.
type UnixByteConnection struct {
	socket               UnixSocket
	gracefulCloseTimeout time.Duration
	maxPendingBytes      int
	mu                   sync.Mutex
	pendingBytes         int
	closed, closing      bool
	tail                 chan struct{}
	closedDone           chan struct{}
	closeStarted         bool
	work                 sync.WaitGroup
}

func NewUnixByteConnection(socket UnixSocket, gracefulCloseTimeout time.Duration, maxPendingBytes int) *UnixByteConnection {
	tail := make(chan struct{})
	close(tail)
	return &UnixByteConnection{socket: socket, gracefulCloseTimeout: gracefulCloseTimeout, maxPendingBytes: maxPendingBytes, tail: tail, closedDone: make(chan struct{})}
}
func (c *UnixByteConnection) Closed() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.closed }
func (c *UnixByteConnection) Send(chunk []byte) error {
	c.mu.Lock()
	if c.closed || c.closing {
		c.mu.Unlock()
		return errors.New("Unix connection is closed")
	}
	if len(chunk) > c.maxPendingBytes-c.pendingBytes {
		c.mu.Unlock()
		return errors.New("Unix connection exceeded its pending byte limit")
	}
	c.pendingBytes += len(chunk)
	bytes := slices.Clone(chunk)
	previous, done := c.tail, make(chan struct{})
	c.tail = done
	var result error
	c.work.Go(func() {
		<-previous
		c.mu.Lock()
		stopped := c.closed || c.closing
		c.mu.Unlock()
		if stopped {
			result = errors.New("Unix connection is closed")
		} else {
			n, err := c.socket.Write(bytes)
			result = err
			if err == nil && n != len(bytes) {
				result = io.ErrShortWrite
			}
		}
		c.mu.Lock()
		c.pendingBytes -= len(bytes)
		c.mu.Unlock()
		close(done)
	})
	c.mu.Unlock()
	<-done
	return result
}
func (c *UnixByteConnection) Close(finalChunk []byte) error {
	c.mu.Lock()
	if !c.closeStarted && !c.closed {
		c.closeStarted, c.closing = true, true
		final, tail := slices.Clone(finalChunk), c.tail
		c.work.Go(func() {
			var timerDone sync.Once
			c.work.Add(1)
			finishTimer := func() { timerDone.Do(c.work.Done) }
			timer := time.AfterFunc(c.gracefulCloseTimeout, func() { defer finishTimer(); _ = c.socket.Close(); c.MarkClosed() })
			defer func() {
				if timer.Stop() {
					finishTimer()
				}
			}()
			<-tail
			if !c.Closed() {
				var err error
				if final != nil {
					var n int
					n, err = c.socket.Write(final)
					if err == nil && n != len(final) {
						err = io.ErrShortWrite
					}
				}
				if err == nil {
					err = c.socket.CloseWrite()
				}
				if err != nil {
					_ = c.socket.Close()
					c.MarkClosed()
				}
			}
			<-c.closedDone
		})
	}
	done := c.closedDone
	c.mu.Unlock()
	<-done
	c.work.Wait()
	return nil
}

// MarkClosed records the underlying socket's close event; it does not manufacture a transport close.
func (c *UnixByteConnection) MarkClosed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed, c.closing = true, true
		close(c.closedDone)
	}
}

// UnixListener publishes an owned socket inode by hard link and removes only that inode at shutdown.
type UnixListener struct {
	options           UnixListenerOptions
	mode              os.FileMode
	maxPendingBytes   int
	closeTimeout      time.Duration
	mu                sync.Mutex
	server            *net.UnixListener
	accept            ByteConnectionAcceptor
	connections       map[*UnixByteConnection]bool
	connectionOrder   []*UnixByteConnection
	identity          os.FileInfo
	ownedBindPath     string
	closing, starting bool
	closeDone         chan struct{}
	closeError        error
	work              sync.WaitGroup
}

func CreateUnixListener(options UnixListenerOptions) (*UnixListener, error) {
	if options.Path == "" {
		return nil, errors.New("Server Unix socket path must not be empty")
	}
	mode := float64(0o600)
	if options.Mode != nil {
		mode = *options.Mode
	}
	if !serverInteger(mode, 0, 0o777) {
		return nil, errors.New("Server Unix socket mode must be an integer between 0 and 0o777")
	}
	frame := float64(protocol.DefaultMaxFrameLength)
	if options.MaxFrameLength != nil {
		frame = *options.MaxFrameLength
	}
	if !serverInteger(frame, 1, math.MaxUint32) {
		return nil, errors.New("Server maxFrameLength must be an integer between 1 and 4294967295")
	}
	pending := frame * 4
	if options.MaxPendingBytes != nil {
		pending = *options.MaxPendingBytes
	}
	if !serverInteger(pending, frame+4, 9_007_199_254_740_991) {
		return nil, errors.New("Server maxPendingBytes must be a safe integer at least maxFrameLength + 4")
	}
	timeout := float64(5_000)
	if options.GracefulCloseTimeoutMs != nil {
		timeout = *options.GracefulCloseTimeoutMs
	}
	if !serverInteger(timeout, 1, math.MaxInt32) {
		return nil, errors.New("Server gracefulCloseTimeoutMs must be an integer between 1 and 2147483647")
	}
	return &UnixListener{options: options, mode: os.FileMode(mode), maxPendingBytes: int(pending), closeTimeout: time.Duration(timeout) * time.Millisecond, connections: make(map[*UnixByteConnection]bool)}, nil
}
func (l *UnixListener) Start(accept ByteConnectionAcceptor) error {
	l.mu.Lock()
	if l.server != nil || l.starting {
		l.mu.Unlock()
		return errors.New("Unix listener is already started")
	}
	if l.closing {
		l.mu.Unlock()
		return errors.New("Unix listener is closing or closed")
	}
	l.starting = true
	l.accept = accept
	l.mu.Unlock()
	defer func() { l.mu.Lock(); l.starting = false; l.mu.Unlock() }()
	owned := ownedBindPath(l.options.Path)
	if err := os.MkdirAll(filepath.Dir(l.options.Path), 0o700); err != nil {
		return err
	}
	if err := removeStaleUnixSocket(l.options.Path); err != nil {
		return err
	}
	if err := removeStaleUnixSocket(owned); err != nil {
		return err
	}
	l.mu.Lock()
	l.ownedBindPath = owned
	l.mu.Unlock()
	server, err := net.ListenUnix("unix", &net.UnixAddr{Name: owned, Net: "unix"})
	if err != nil {
		return errors.Join(err, l.cleanupPaths())
	}
	server.SetUnlinkOnClose(false)
	l.mu.Lock()
	l.server = server
	l.mu.Unlock()
	setup := func() error {
		info, err := os.Lstat(owned)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("Unix listener path is not a socket after binding: %s", owned)
		}
		l.mu.Lock()
		l.identity = info
		l.mu.Unlock()
		if err := os.Link(owned, l.options.Path); err != nil {
			return err
		}
		if err := setUnixSocketMode(l.options.Path, l.mode); err != nil {
			return err
		}
		if err := removeUnixPath(owned); err != nil {
			return err
		}
		l.mu.Lock()
		l.ownedBindPath = ""
		l.mu.Unlock()
		return nil
	}
	if err := setup(); err != nil {
		if closeErr := server.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			l.reportError(closeErr)
		}
		cleanupError := l.cleanupPaths()
		l.mu.Lock()
		l.server = nil
		l.mu.Unlock()
		if cleanupError != nil {
			return cleanupError
		}
		return err
	}
	l.work.Go(func() { l.acceptLoop(server) })
	return nil
}
func (l *UnixListener) acceptLoop(server *net.UnixListener) {
	for {
		socket, err := server.AcceptUnix()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				l.reportError(err)
			}
			return
		}
		l.mu.Lock()
		if l.closing {
			l.mu.Unlock()
			_ = socket.Close()
			continue
		}
		connection := NewUnixByteConnection(socket, l.closeTimeout, l.maxPendingBytes)
		l.connections[connection] = true
		l.connectionOrder = append(l.connectionOrder, connection)
		accept := l.accept
		l.work.Go(func() { l.serveSocket(socket, connection, accept) })
		l.mu.Unlock()
	}
}
func (l *UnixListener) serveSocket(socket *net.UnixConn, connection *UnixByteConnection, accept ByteConnectionAcceptor) {
	handler := accept(connection)
	defer func() {
		_ = socket.Close()
		connection.MarkClosed()
		l.mu.Lock()
		delete(l.connections, connection)
		l.connectionOrder = slices.DeleteFunc(l.connectionOrder, func(c *UnixByteConnection) bool { return c == connection })
		l.mu.Unlock()
		handler.OnClose()
	}()
	buffer := make([]byte, 32*1024)
	for {
		n, err := socket.Read(buffer)
		if n != 0 {
			handler.OnData(buffer[:n])
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				handler.OnError(err)
			}
			return
		}
	}
}
func (l *UnixListener) Close() error {
	l.mu.Lock()
	if l.closeDone == nil {
		l.closing = true
		l.closeDone = make(chan struct{})
		go func() { err := l.closeInternal(); l.mu.Lock(); l.closeError = err; close(l.closeDone); l.mu.Unlock() }()
	}
	done := l.closeDone
	l.mu.Unlock()
	<-done
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closeError
}
func (l *UnixListener) closeInternal() error {
	l.mu.Lock()
	server := l.server
	connections := slices.Clone(l.connectionOrder)
	l.mu.Unlock()
	if server != nil {
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			l.reportError(err)
		}
	}
	var group sync.WaitGroup
	for _, connection := range connections {
		group.Go(func() { _ = connection.Close(nil) })
	}
	group.Wait()
	l.work.Wait()
	err := l.cleanupPaths()
	l.mu.Lock()
	clear(l.connections)
	l.connectionOrder = nil
	l.server = nil
	l.mu.Unlock()
	return err
}
func (l *UnixListener) cleanupPaths() error {
	l.mu.Lock()
	bind := l.ownedBindPath
	l.ownedBindPath = ""
	l.mu.Unlock()
	if bind != "" {
		if err := removeUnixPath(bind); err != nil {
			return err
		}
	}
	return l.cleanupOwnedSocket()
}
func (l *UnixListener) cleanupOwnedSocket() error {
	l.mu.Lock()
	identity := l.identity
	l.identity = nil
	l.mu.Unlock()
	if identity == nil {
		return nil
	}
	current, err := os.Lstat(l.options.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSocket == 0 || !os.SameFile(identity, current) {
		return nil
	}
	preserved := filepath.Join(filepath.Dir(l.options.Path), "cleanup-"+uuid.NewString()[:6])
	if err := os.Rename(l.options.Path, preserved); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	moved, err := os.Lstat(preserved)
	if err != nil {
		return err
	}
	if moved.Mode()&os.ModeSocket != 0 && os.SameFile(identity, moved) {
		return removeUnixPath(preserved)
	}
	if _, err := os.Lstat(l.options.Path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(preserved, l.options.Path); err != nil {
			return err
		}
	}
	return fmt.Errorf("Unix listener path changed during cleanup; preserved replacement at %s", preserved)
}
func ownedBindPath(path string) string {
	hash := sha256.Sum256([]byte(path))
	return filepath.Join(filepath.Dir(path), fmt.Sprintf("bind-%x", hash[:4]))
}
func removeStaleUnixSocket(path string) error {
	original, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if original.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("Refusing to remove non-socket Unix listener path: %s", path)
	}
	live, err := isUnixSocketLive(path)
	if err != nil {
		return err
	}
	if live {
		return fmt.Errorf("Unix listener is already running: %s", path)
	}
	preserved := filepath.Join(filepath.Dir(path), "stale-"+uuid.NewString()[:6])
	if err := os.Rename(path, preserved); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	current, err := os.Lstat(preserved)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSocket == 0 || !os.SameFile(current, original) {
		if _, err := os.Lstat(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.Rename(preserved, path); err != nil {
				return err
			}
		}
		return fmt.Errorf("Unix listener path changed while checking for a stale socket: %s", path)
	}
	return removeUnixPath(preserved)
}
func isUnixSocketLive(path string) (bool, error) {
	// upstream: packages/server/src/transports/unix/listener.ts:SOCKET_PROBE_TIMEOUT_MS
	socket, err := net.DialTimeout("unix", path, 1_000*time.Millisecond)
	if err == nil {
		_ = socket.Close()
		return true, nil
	}
	if unixSocketStaleError(err) {
		return false, nil
	}
	if network, ok := errors.AsType[net.Error](err); ok && network.Timeout() {
		return true, nil
	}
	return false, err
}
func removeUnixPath(path string) error {
	err := syscall.Unlink(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func setUnixSocketMode(path string, mode os.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	err := os.Chmod(path, mode)
	if errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}
func (l *UnixListener) reportError(err error) {
	// upstream: packages/server/src/transports/unix/listener.ts:reportError
	defer func() { _ = recover() }()
	if l.options.OnError != nil {
		l.options.OnError(err)
	}
}
func GetUnixSocketPath(serverID, directory string) (string, error) {
	if !protocol.IsServerId(serverID) {
		return "", errors.New("Unix serverId must be a canonical lowercase UUIDv4")
	}
	return filepath.Join(directory, serverID+".sock"), nil
}
func CreateUnixServer(host ServerHost, options UnixServerOptions) (*Server, error) {
	listener, err := CreateUnixListener(UnixListenerOptions{Path: options.Path, Mode: options.Mode, MaxPendingBytes: options.MaxPendingBytes, GracefulCloseTimeoutMs: options.GracefulCloseTimeoutMs, MaxFrameLength: options.MaxFrameLength, OnError: options.OnError})
	if err != nil {
		return nil, err
	}
	return NewServer(host, ServerOptions{Listeners: []ServerListener{listener}, ServerId: options.ServerId, MaxFrameLength: options.MaxFrameLength, HandshakeTimeoutMs: options.HandshakeTimeoutMs, OnConnectionCountChanged: options.OnConnectionCountChanged, OnError: options.OnError})
}
