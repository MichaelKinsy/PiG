package client

// Ports packages/client/src/unix.ts.

import (
	"cmp"
	"context"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

const (
	defaultDiscoveryTimeoutMs    = 1_000
	maxConcurrentDiscoveryProbes = 16
	maxTimerDelayMs              = 2_147_483_647
	maxSafeInteger               = 1<<53 - 1
)

// UnixTransportOptions selects a socket and the maximum admitted, not-yet-completed send bytes.
type UnixTransportOptions struct {
	Path            string
	MaxPendingBytes *float64
}

// UnixServerRoute identifies a canonical server-addressed local socket.
type UnixServerRoute struct {
	ServerId string
	Path     string
}

// DiscoverUnixServersOptions bounds each connection and handshake attempt.
type DiscoverUnixServersOptions struct {
	Directory string
	TimeoutMs *float64
}

// CreateUnixTransportFactory validates options without connecting. Every invocation owns one cancellable dial and returns a fresh transport.
func CreateUnixTransportFactory(options UnixTransportOptions) (ByteTransportFactory, error) {
	if options.Path == "" {
		return nil, errors.New("Unix transport path must not be empty")
	}
	limit := float64(protocol.DefaultMaxFrameLength * 4)
	if options.MaxPendingBytes != nil {
		limit = *options.MaxPendingBytes
	}
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit <= 0 || limit > maxSafeInteger || math.Trunc(limit) != limit {
		return nil, errors.New("Unix transport maxPendingBytes must be a positive safe integer")
	}
	if runtime.GOOS == "windows" {
		return nil, errors.New("Unix transport is not supported on Windows")
	}
	return func(ctx context.Context, handlers ByteTransportHandlers) (ByteTransport, error) {
		// The attempt context cancels the dial; the established transport's lifetime passes to the Connection with the transport.
		connection, err := (&net.Dialer{}).DialContext(ctx, "unix", options.Path)
		if err != nil {
			return nil, err
		}
		return newUnixByteTransport(connection, uint64(limit), handlers), nil
	}, nil
}

type unixWrite struct {
	bytes    []byte
	complete func(error)
}
type unixByteTransport struct {
	connection      net.Conn
	maxPendingBytes uint64
	handlers        ByteTransportHandlers
	mu              sync.Mutex
	ready           *sync.Cond
	closed          bool
	pendingBytes    uint64
	writes          []*unixWrite
	parts           atomic.Int32
	done            chan struct{}
	// notified closes once a terminal close is visible to the handlers. Upstream's socket close listener calls onClose/onError before any pending write settles, so write completions caused by the close wait for it.
	notified chan struct{}
	// rejected holds Sends admitted after a remote close began but before its handlers returned; remoteClose settles them after the handlers.
	rejected []func(error)
}

func newUnixByteTransport(connection net.Conn, maxPendingBytes uint64, handlers ByteTransportHandlers) *unixByteTransport {
	transport := &unixByteTransport{connection: connection, maxPendingBytes: maxPendingBytes, handlers: handlers, done: make(chan struct{}), notified: make(chan struct{})}
	transport.ready = sync.NewCond(&transport.mu)
	transport.parts.Store(2)
	go transport.write()
	return transport
}
func (transport *unixByteTransport) Done() <-chan struct{} { return transport.done }

// startReading begins delivering the socket's data and close to the handlers.
func (transport *unixByteTransport) startReading() { go transport.read() }
func (transport *unixByteTransport) finished() {
	if transport.parts.Add(-1) == 0 {
		close(transport.done)
	}
}

// Send is upstream's send(): it admits the chunk behind the earlier ones and returns when the write settles. A cancelled ctx refuses a chunk that
// is not admitted yet and stops the wait for one that is.
func (transport *unixByteTransport) Send(ctx context.Context, chunk []byte) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	result := make(chan error, 1)
	transport.Submit(chunk, func(err error) { result <- err })
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Submit admits a send in invocation order and completes it exactly once, as upstream's send() settles its Promise.
func (transport *unixByteTransport) Submit(chunk []byte, complete func(error)) {
	transport.mu.Lock()
	if transport.closed {
		select {
		case <-transport.notified:
		default:
			// Upstream send() rejects asynchronously and its close handlers run synchronously in the socket listener, so the rejection never precedes them.
			transport.rejected = append(transport.rejected, complete)
			transport.mu.Unlock()
			return
		}
		transport.mu.Unlock()
		complete(errors.New("Unix transport is closed"))
		return
	}
	if uint64(len(chunk)) > transport.maxPendingBytes-transport.pendingBytes {
		transport.mu.Unlock()
		complete(errors.New("Unix transport exceeded its pending byte limit"))
		return
	}
	transport.pendingBytes += uint64(len(chunk))
	transport.writes = append(transport.writes, &unixWrite{bytes: slices.Clone(chunk), complete: complete})
	transport.ready.Signal()
	transport.mu.Unlock()
}
func (transport *unixByteTransport) Close() {
	transport.mu.Lock()
	if transport.closed {
		transport.mu.Unlock()
		return
	}
	transport.closed = true
	transport.ready.Broadcast()
	close(transport.notified)
	transport.mu.Unlock()
	// Upstream marks the local terminal state before destroying the socket; local close does not notify the remote-close handler.
	_ = transport.connection.Close()
}
func (transport *unixByteTransport) remoteClose(err error) {
	transport.mu.Lock()
	if transport.closed {
		transport.mu.Unlock()
		return
	}
	transport.closed = true
	transport.ready.Broadcast()
	transport.mu.Unlock()
	_ = transport.connection.Close()
	defer transport.settleRejected()
	if err == nil {
		transport.handlers.OnClose()
	} else {
		transport.handlers.OnError(err)
	}
}

// settleRejected publishes a remote close to waiting writes once its handlers returned, then rejects the Sends admitted meanwhile.
func (transport *unixByteTransport) settleRejected() {
	transport.mu.Lock()
	rejected := transport.rejected
	transport.rejected = nil
	close(transport.notified)
	transport.mu.Unlock()
	for _, complete := range rejected {
		complete(errors.New("Unix transport is closed"))
	}
}
func (transport *unixByteTransport) read() {
	defer transport.finished()
	// Chunk size is an implementation detail; framing accepts arbitrary fragmentation and returned chunks retain independent storage.
	buffer := make([]byte, 64*1024)
	for {
		n, err := transport.connection.Read(buffer)
		if n > 0 {
			transport.mu.Lock()
			closed := transport.closed
			transport.mu.Unlock()
			if !closed {
				transport.handlers.OnData(slices.Clone(buffer[:n]))
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				transport.remoteClose(nil)
			} else {
				transport.remoteClose(err)
			}
			return
		}
	}
}
func (transport *unixByteTransport) write() {
	defer transport.finished()
	for {
		transport.mu.Lock()
		for !transport.closed && len(transport.writes) == 0 {
			transport.ready.Wait()
		}
		if len(transport.writes) == 0 {
			transport.mu.Unlock()
			return
		}
		request := transport.writes[0]
		transport.writes[0] = nil
		transport.writes = transport.writes[1:]
		closed := transport.closed
		transport.mu.Unlock()
		var err error
		if closed {
			err = errors.New("Unix transport is closed")
		} else {
			remaining := request.bytes
			for {
				var n int
				n, err = transport.connection.Write(remaining)
				remaining = remaining[n:]
				if err != nil || len(remaining) == 0 {
					break
				}
				if n == 0 {
					err = io.ErrShortWrite
					break
				}
			}
		}
		transport.mu.Lock()
		transport.pendingBytes -= uint64(len(request.bytes))
		closedNow := transport.closed
		transport.mu.Unlock()
		if err != nil && closedNow {
			// A close during the write settles it as upstream's write close listener does, after the handlers saw the close.
			if !closed {
				err = errors.New("Unix transport closed during write")
			}
			<-transport.notified
		}
		request.complete(err)
		if err != nil && !closed {
			transport.remoteClose(err)
		}
	}
}

// DiscoverUnixServers probes only canonical server-addressed sockets, with at most sixteen joined workers. Stale/unresponsive endpoints remain on disk and are omitted; unexpected filesystem/transport errors propagate.
func DiscoverUnixServers(ctx context.Context, options DiscoverUnixServersOptions) ([]UnixServerRoute, error) {
	if runtime.GOOS == "windows" {
		return nil, errors.New("Unix transport is not supported on Windows")
	}
	timeout := float64(defaultDiscoveryTimeoutMs)
	if options.TimeoutMs != nil {
		timeout = *options.TimeoutMs
	}
	if math.IsNaN(timeout) || math.IsInf(timeout, 0) || timeout <= 0 || timeout > maxTimerDelayMs || math.Trunc(timeout) != timeout {
		return nil, errors.New("Unix discovery timeoutMs must be an integer between 1 and 2147483647")
	}
	names, err := os.ReadDir(options.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return []UnixServerRoute{}, nil
	}
	if err != nil {
		return nil, err
	}
	candidates := []UnixServerRoute{}
	for _, name := range names {
		id, ok := strings.CutSuffix(name.Name(), ".sock")
		if ok && protocol.IsServerId(id) {
			candidates = append(candidates, UnixServerRoute{ServerId: id, Path: filepath.Join(options.Directory, name.Name())})
		}
	}
	var mu sync.Mutex
	next := 0
	var failure error
	routes := []UnixServerRoute{}
	var group sync.WaitGroup
	for range min(maxConcurrentDiscoveryProbes, len(candidates)) {
		group.Go(func() {
			for {
				mu.Lock()
				if failure != nil || next >= len(candidates) {
					mu.Unlock()
					return
				}
				candidate := candidates[next]
				next++
				mu.Unlock()
				if ctx.Err() != nil {
					mu.Lock()
					if failure == nil {
						failure = context.Cause(ctx)
					}
					mu.Unlock()
					return
				}
				info, err := os.Lstat(candidate.Path)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err == nil && info.Mode()&os.ModeSocket == 0 {
					continue
				}
				found := false
				if err == nil {
					found, err = probeUnixServer(ctx, candidate, time.Duration(timeout)*time.Millisecond)
				}
				mu.Lock()
				if err != nil && failure == nil {
					failure = err
				}
				if found {
					routes = append(routes, candidate)
				}
				mu.Unlock()
			}
		})
	}
	group.Wait()
	if failure != nil {
		return nil, failure
	}
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	slices.SortFunc(routes, func(left, right UnixServerRoute) int { return cmp.Compare(left.ServerId, right.ServerId) })
	return routes, nil
}
func probeUnixServer(ctx context.Context, route UnixServerRoute, timeout time.Duration) (bool, error) {
	factory, err := CreateUnixTransportFactory(UnixTransportOptions{Path: route.Path})
	if err != nil {
		return false, err
	}
	client, err := NewClient(ClientOptions{ServerId: route.ServerId, TransportFactory: factory})
	if err != nil {
		return false, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() { _ = client.Dispose(); _ = client.WaitClosed(context.WithoutCancel(ctx)) }()
	_, err = client.Connect(probeCtx)
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, context.Cause(ctx)
	}
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return false, nil
	}
	if _, ok := errors.AsType[*protocol.ProtocolValidationError](err); ok {
		return false, nil
	}
	if failure, ok := errors.AsType[*DisconnectedError](err); ok && failure.Cause == nil {
		return false, nil
	}
	if failure, ok := errors.AsType[*ServerError](err); ok && failure.Code == "version" {
		return false, nil
	}
	for _, code := range []error{syscall.ENOENT, syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EPIPE, syscall.ETIMEDOUT} {
		if errors.Is(err, code) {
			return false, nil
		}
	}
	return false, err
}
