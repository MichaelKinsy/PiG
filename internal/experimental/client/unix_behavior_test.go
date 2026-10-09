//go:build !windows

package client

// pi: packages/client/src/unix.ts

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// Pi: packages/client/src/unix.ts:97-112 validateUnixTransportOptions.
func TestUnixTransportOptionsAreValidatedLikePi(t *testing.T) {
	t.Parallel()
	_, err := CreateUnixTransportFactory(UnixTransportOptions{})
	if err == nil || err.Error() != "Unix transport path must not be empty" {
		t.Fatalf("empty path error=%v", err)
	}
	const message = "Unix transport maxPendingBytes must be a positive safe integer"
	for _, limit := range []float64{0, -1, 1.5, math.NaN(), math.Inf(1), 1 << 53} {
		_, err := CreateUnixTransportFactory(UnixTransportOptions{Path: "/tmp/x.sock", MaxPendingBytes: &limit})
		if err == nil || err.Error() != message {
			t.Fatalf("limit %v error=%v", limit, err)
		}
	}
	for _, limit := range []float64{1, 1<<53 - 1} {
		if _, err := CreateUnixTransportFactory(UnixTransportOptions{Path: "/tmp/x.sock", MaxPendingBytes: &limit}); err != nil {
			t.Fatalf("limit %v rejected: %v", limit, err)
		}
	}
}

// Pi: unix.ts:42-45 (timeoutMs validation, MAX_TIMER_DELAY_MS) and :47-52 (a missing directory has no routes).
func TestDiscoverUnixServersValidatesTheTimeout(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(shortDirectory(t, "pcd-"), "absent")
	const message = "Unix discovery timeoutMs must be an integer between 1 and 2147483647"
	for _, value := range []float64{0, -1, 1.5, math.NaN(), math.Inf(1), 2147483648} {
		_, err := DiscoverUnixServers(t.Context(), DiscoverUnixServersOptions{Directory: missing, TimeoutMs: &value})
		if err == nil || err.Error() != message {
			t.Fatalf("timeout %v error=%v", value, err)
		}
	}
	for _, value := range []float64{1, 2147483647} {
		if routes, err := DiscoverUnixServers(t.Context(), DiscoverUnixServersOptions{Directory: missing, TimeoutMs: &value}); err != nil || len(routes) != 0 {
			t.Fatalf("timeout %v: %v %v", value, routes, err)
		}
	}
}

// Pi: unix.ts:17,65-67 only <canonical server id>.sock names are probed; DEFAULT_DISCOVERY_TIMEOUT_MS is 1,000.
func TestDiscoverUnixServersProbesOnlyCanonicalNamesWithTheDefaultTimeout(t *testing.T) {
	t.Parallel()
	directory := shortDirectory(t, "pcn-")
	// Sockets whose names are not canonical server ids are never dialled, even when something listens on them.
	for _, name := range []string{"not-a-server.sock", strings.ToUpper(discoveryServerId(7)) + ".sock", discoveryServerId(8) + ".sck"} {
		startSilentSocket(t, filepath.Join(directory, name), nil)
	}
	if routes := discover(t, DiscoverUnixServersOptions{Directory: directory}); len(routes) != 0 {
		t.Fatalf("routes=%v", routes)
	}
	// A canonical silent socket is abandoned after the default 1,000 ms, not earlier and not much later.
	startSilentSocket(t, filepath.Join(directory, discoveryServerId(1)+".sock"), nil)
	started := time.Now()
	if routes := discover(t, DiscoverUnixServersOptions{Directory: directory}); len(routes) != 0 {
		t.Fatalf("routes=%v", routes)
	}
	if elapsed := time.Since(started); elapsed < 950*time.Millisecond || elapsed > 8*time.Second {
		t.Fatalf("default discovery timeout took %v, want about 1s", elapsed)
	}
}

// Pi: unix.ts:158-173 probeUnixServer omits endpoints that are missing, refused, version-mismatched or closed without a cause, and propagates anything else.
func TestProbeUnixServerOmitsOnlyTheEndpointsPiOmits(t *testing.T) {
	t.Parallel()
	directory := shortDirectory(t, "pcp-")
	probe := func(route UnixServerRoute) (bool, error) {
		return probeUnixServer(t.Context(), route, 2*time.Second)
	}
	route := UnixServerRoute{ServerId: discoveryServerId(1), Path: filepath.Join(directory, discoveryServerId(1)+".sock")}
	if found, err := probe(route); found || err != nil { // ENOENT: the socket vanished after readdir
		t.Fatalf("missing socket: %v %v", found, err)
	}
	// ECONNREFUSED: a socket file nobody listens on.
	stale := filepath.Join(directory, discoveryServerId(2)+".sock")
	listener, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	if unix, ok := listener.(*net.UnixListener); ok {
		unix.SetUnlinkOnClose(false)
	}
	_ = listener.Close()
	if found, err := probe(UnixServerRoute{ServerId: discoveryServerId(2), Path: stale}); found || err != nil {
		t.Fatalf("refused socket: %v %v", found, err)
	}
	// A server that answers with a different identity is not the advertised server.
	startDiscoveryServer(t, directory, discoveryServerId(3), discoveryServerId(4))
	if found, err := probe(UnixServerRoute{ServerId: discoveryServerId(3), Path: filepath.Join(directory, discoveryServerId(3)+".sock")}); found || err != nil {
		t.Fatalf("identity mismatch: %v %v", found, err)
	}
	// A reachable server is found.
	startDiscoveryServer(t, directory, discoveryServerId(5), "")
	if found, err := probe(UnixServerRoute{ServerId: discoveryServerId(5), Path: filepath.Join(directory, discoveryServerId(5)+".sock")}); !found || err != nil {
		t.Fatalf("reachable server: %v %v", found, err)
	}
	// unix.ts:266-267: a hello_error with code "version" and a close without a cause are omitted; any other hello_error propagates as the ServerError.
	for index, test := range []struct {
		name  string
		reply []byte
		omit  bool
	}{
		{"version mismatch", helloErrorFrame(t, "version", "Unsupported protocol version 8; expected 9"), true},
		{"close after the client hello", nil, true},
		{"another hello_error", helloErrorFrame(t, "internal_error", "Internal server error"), false},
	} {
		id := discoveryServerId(10 + index)
		path := filepath.Join(directory, id+".sock")
		startReplyingSocket(t, path, test.reply)
		found, err := probe(UnixServerRoute{ServerId: id, Path: path})
		if test.omit {
			if found || err != nil {
				t.Fatalf("%s: %v %v, want omitted", test.name, found, err)
			}
			continue
		}
		if failure, ok := errors.AsType[*ServerError](err); found || !ok || failure.Code != "internal_error" || failure.Message != "Internal server error" {
			t.Fatalf("%s: %v %v, want the ServerError", test.name, found, err)
		}
	}
	// A dial failure that Pi does not list (ENOTDIR: a path component is a regular file) propagates.
	file := filepath.Join(directory, "plainfile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := probe(UnixServerRoute{ServerId: discoveryServerId(6), Path: filepath.Join(file, "s.sock")}); found || !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("unlisted dial failure: %v %v, want ENOTDIR", found, err)
	}
}

func helloErrorFrame(t *testing.T, code, message string) []byte {
	t.Helper()
	frame, err := protocol.EncodeServerMessage(protocol.ServerHelloError{Error: protocol.ProtocolError{Code: code, Message: message}}, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

// startReplyingSocket accepts one connection, reads the complete client hello frame, writes reply (if any) and closes. Reading the whole hello first makes the close an orderly end of stream rather than a reset.
func startReplyingSocket(t *testing.T, path string, reply []byte) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { _ = listener.Close(); <-done })
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		if _, err := io.ReadFull(conn, make([]byte, binary.BigEndian.Uint32(header[:]))); err != nil {
			return
		}
		if reply != nil {
			_, _ = conn.Write(reply)
		}
	}()
}

// Pi: unix.ts:180-185 and :126-130 send(): a write over maxPendingBytes is rejected; a close rejects later writes with "Unix transport is closed".
// The transport is driven through the ByteTransport contract the Connection uses (transport.ts:1-5 send, close).
func TestUnixTransportPendingLimitAndClosedSend(t *testing.T) {
	t.Parallel()
	server, clientSide := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	unix := newUnixByteTransport(clientSide, 10, ByteTransportHandlers{OnData: func([]byte) {}, OnClose: func() {}, OnError: func(error) {}})
	go unix.read()
	var transport callbackByteTransport = unix
	t.Cleanup(transport.Close)
	result := make(chan error, 1)
	transport.Submit(make([]byte, 11), func(err error) { result <- err })
	if err := <-result; err == nil || err.Error() != "Unix transport exceeded its pending byte limit" {
		t.Fatalf("oversized send error=%v", err)
	}
	transport.Close()
	transport.Submit([]byte{1}, func(err error) { result <- err })
	if err := <-result; err == nil || err.Error() != "Unix transport is closed" {
		t.Fatalf("send after close error=%v", err)
	}
}

// packages/client/src/transport.ts:1-4: ByteTransport.send(chunk) sends one byte chunk, and calls are delivered in invocation order; the completion reports the send's outcome.
func TestByteTransportSendDeliversChunksInInvocationOrder(t *testing.T) {
	t.Parallel()
	server, clientSide := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	var transport callbackByteTransport = newUnixByteTransport(clientSide, 1<<20, ByteTransportHandlers{OnData: func([]byte) {}, OnClose: func() {}, OnError: func(error) {}})
	t.Cleanup(transport.Close)
	received := make(chan []byte, 1)
	go func() {
		data := make([]byte, 6)
		if _, err := io.ReadFull(server, data); err != nil {
			data = nil
		}
		received <- data
	}()
	done := make(chan error, 3)
	for _, chunk := range [][]byte{{1, 2, 3}, {4}, {5, 6}} {
		transport.Submit(chunk, func(err error) { done <- err })
	}
	for range 3 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("send completion error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a send never completed")
		}
	}
	if got := <-received; !slices.Equal(got, []byte{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("server read %v, want the chunks in invocation order", got)
	}
}

// Pi: connection.ts #openTransport awaits transportFactory(handlers) and only then installs the transport, and Node delivers a socket's 'data' after
// that continuation: a server that writes before the client hello reaches onData only once the Connection owns the transport. The factory path
// the Connection uses starts the unix transport's reads after the Connection has taken it.
// mutation-checked: starting the reader before the completion callback (adaptFactory) delivers the server's frame while the Connection is still taking the transport.
func TestUnixTransportDeliversDataOnlyAfterTheConnectionOwnsIt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "s.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_, _ = connection.Write([]byte{1, 2, 3}) // before any client hello
		_, _ = io.Copy(io.Discard, connection)
	}()
	factory, err := CreateUnixTransportFactory(UnixTransportOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	owned := false
	var ownedAtData []bool
	delivered := make(chan struct{}, 1)
	handlers := ByteTransportHandlers{
		OnData: func([]byte) {
			mu.Lock()
			ownedAtData = append(ownedAtData, owned)
			mu.Unlock()
			select {
			case delivered <- struct{}{}:
			default:
			}
		},
		OnClose: func() {}, OnError: func(error) {},
	}
	taken := make(chan callbackByteTransport, 1)
	adaptFactory(factory)(t.Context(), handlers, func(transport callbackByteTransport, err error) {
		if err != nil {
			t.Error(err)
		}
		// The Connection takes the transport here; a frame that reached onData during this window would see owned == false.
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		owned = true
		mu.Unlock()
		taken <- transport
	})
	transport := <-taken
	t.Cleanup(transport.Close)
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("the server's frame never reached onData")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ownedAtData) == 0 || slices.Contains(ownedAtData, false) {
		t.Fatalf("onData ran while the Connection was still taking the transport: %v", ownedAtData)
	}
}
