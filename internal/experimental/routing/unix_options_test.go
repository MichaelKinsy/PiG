//go:build unix

package routing_test

// pi: packages/server/src/transports/unix/types.ts

// pi: packages/server/src/transports/unix/preset.ts

// pi: packages/server/src/transports/unix/address.ts

import (
	"math"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// Pi: packages/server/src/transports/unix/listener.ts and types.ts option validation, defaults and messages.
// Pi source: packages/server/src/transports/unix/listener.ts:392-396 (resolveUnixListenerOptions).
// mutation-checked: negating the condition `!serverInteger(timeout, 1, math.MaxInt32)` at unix.go:213 fails it.
func TestUnixListenerOptionsAreValidatedLikePi(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDirectory(t, "pso-"), "s.sock")
	create := func(mutate func(*routing.UnixListenerOptions)) error {
		options := routing.UnixListenerOptions{Path: path}
		mutate(&options)
		_, err := routing.CreateUnixListener(options)
		return err
	}
	exactMessage(t, create(func(o *routing.UnixListenerOptions) { o.Path = "" }), "Server Unix socket path must not be empty")
	const mode = "Server Unix socket mode must be an integer between 0 and 0o777"
	for _, value := range []float64{-1, 0o1000, 1.5, math.NaN()} {
		exactMessage(t, create(func(o *routing.UnixListenerOptions) { o.Mode = &value }), mode)
	}
	for _, value := range []float64{0, 0o777} {
		if err := create(func(o *routing.UnixListenerOptions) { o.Mode = &value }); err != nil {
			t.Fatalf("mode %o rejected: %v", int(value), err)
		}
	}
	const frame = "Server maxFrameLength must be an integer between 1 and 4294967295"
	for _, value := range []float64{0, 1.5, 4294967296} {
		exactMessage(t, create(func(o *routing.UnixListenerOptions) { o.MaxFrameLength = &value }), frame)
	}
	small := 10.0
	const pending = "Server maxPendingBytes must be a safe integer at least maxFrameLength + 4"
	for _, value := range []float64{13, 1.5, 9007199254740992} {
		exactMessage(t, create(func(o *routing.UnixListenerOptions) { o.MaxFrameLength = &small; o.MaxPendingBytes = &value }), pending)
	}
	for _, value := range []float64{14, 9007199254740991} {
		if err := create(func(o *routing.UnixListenerOptions) { o.MaxFrameLength = &small; o.MaxPendingBytes = &value }); err != nil {
			t.Fatalf("maxPendingBytes %v rejected: %v", value, err)
		}
	}
	const timeout = "Server gracefulCloseTimeoutMs must be an integer between 1 and 2147483647"
	for _, value := range []float64{0, 1.5, 2147483648} {
		exactMessage(t, create(func(o *routing.UnixListenerOptions) { o.GracefulCloseTimeoutMs = &value }), timeout)
	}
	for _, value := range []float64{1, 2147483647} {
		if err := create(func(o *routing.UnixListenerOptions) { o.GracefulCloseTimeoutMs = &value }); err != nil {
			t.Fatalf("gracefulCloseTimeoutMs %v rejected: %v", value, err)
		}
	}
}

// Pi: listener.ts mode option and parent directory: the socket takes the configured mode and a created parent is 0700.
// Pi source: packages/server/src/transports/unix/listener.ts:53 (mkdir mode 0o700) and :78 (setSocketMode).
// mutation-checked: negating the condition `runtime.GOOS == "windows"` at unix.go:503 fails it.
func TestUnixListenerAppliesTheConfiguredModeAndCreatesPrivateParents(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDirectory(t, "psm-"), "p", "s.sock")
	mode := 0o660
	modeValue := float64(mode)
	server, err := routing.CreateUnixServer(newTestServerHost(), routing.UnixServerOptions{Path: path, ServerId: testServerID, Mode: &modeValue})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if err := startError(server); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != os.FileMode(mode) {
		t.Fatalf("socket mode=%v, %v; want %o", info, err, mode)
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || parent.Mode().Perm() != 0o700 {
		t.Fatalf("parent mode=%v, %v; want 700", parent, err)
	}
}

// Pi: listener.ts start() states and messages.
// Pi source: packages/server/src/transports/unix/listener.ts:48-49 (start states).
// mutation-checked: the mutant "a second Start succeeds" fails it.
func TestUnixListenerStartStatesReportPiMessages(t *testing.T) {
	t.Parallel()
	listener, err := routing.CreateUnixListener(routing.UnixListenerOptions{Path: filepath.Join(shortDirectory(t, "pss-"), "s.sock")})
	if err != nil {
		t.Fatal(err)
	}
	accept := func(routing.ByteConnection) routing.ByteConnectionHandler { return routing.ByteConnectionHandler{} }
	if err := listener.Start(accept); err != nil {
		t.Fatal(err)
	}
	exactMessage(t, listener.Start(accept), "Unix listener is already started")
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	exactMessage(t, listener.Start(accept), "Unix listener is closing or closed")
}

// Pi: packages/server/src/transports/unix/address.ts getUnixSocketPath.
func TestGetUnixSocketPathRequiresACanonicalServerID(t *testing.T) {
	t.Parallel()
	got, err := routing.GetUnixSocketPath(testServerID, "/run/pi")
	if err != nil || got != "/run/pi/"+testServerID+".sock" {
		t.Fatalf("path=%q err=%v", got, err)
	}
	for _, id := range []string{"", "nope", "00000000-0000-4000-8000-00000000000G"} {
		_, err := routing.GetUnixSocketPath(id, "/run/pi")
		exactMessage(t, err, "Unix serverId must be a canonical lowercase UUIDv4")
	}
}

// Pi: packages/server/src/transports/unix/listener.ts UnixByteConnection.send: admission is bounded by maxPendingBytes, equal to the bound is accepted, and a closed connection rejects.
func TestUnixByteConnectionPendingLimitAndClosedSend(t *testing.T) {
	t.Parallel()
	socket := newControlledSocket()
	connection := routing.NewUnixByteConnection(socket, time.Second, 10)
	first := make(chan error, 1)
	go func() { first <- connection.Send(make([]byte, 10)) }() // exactly the bound: accepted, held by the pending write
	<-socket.entered
	exactMessage(t, connection.Send([]byte{1}), "Unix connection exceeded its pending byte limit")
	close(socket.release)
	if err := <-first; err != nil {
		t.Fatalf("send at the bound: %v", err)
	}
	if err := connection.Send(make([]byte, 11)); err == nil || err.Error() != "Unix connection exceeded its pending byte limit" {
		t.Fatalf("oversized send=%v", err)
	}
	if err := connection.Close(nil); err != nil {
		t.Fatal(err)
	}
	_ = socket.Close()
	connection.MarkClosed()
	exactMessage(t, connection.Send([]byte{1}), "Unix connection is closed")
}

// Pi: listener.ts maxPendingBytes defaults to four maximum frames: an accepted connection admits that many bytes and refuses one more.
// Pi source: packages/server/src/transports/unix/listener.ts:216-217 (maxPendingBytes admission).
// mutation-checked: negating the condition `err != nil` at unix.go:255 fails it.
func TestUnixListenerDefaultPendingBytesAreFourFrames(t *testing.T) {
	t.Parallel()
	path := filepath.Join(shortDirectory(t, "psd-"), "s.sock")
	frame := 10.0
	listener, err := routing.CreateUnixListener(routing.UnixListenerOptions{Path: path, MaxFrameLength: &frame})
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan routing.ByteConnection, 1)
	if err := listener.Start(func(connection routing.ByteConnection) routing.ByteConnectionHandler {
		accepted <- connection
		return routing.ByteConnectionHandler{OnData: func([]byte) {}, OnClose: func() {}, OnError: func(error) {}}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	peer, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	connection := <-accepted
	exactMessage(t, connection.Send(make([]byte, 41)), "Unix connection exceeded its pending byte limit")
	if err := connection.Send(make([]byte, 40)); err != nil {
		t.Fatalf("send of four frames: %v", err)
	}
}
