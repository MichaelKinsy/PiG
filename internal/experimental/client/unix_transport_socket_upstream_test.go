//go:build !windows

package client

// Ports packages/client/test/unix-transport.test.ts (the Unix socket cases).

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// listenUnix serves each accepted connection with serve and closes every socket at cleanup, as upstream's listen and afterEach do.
func listenUnix(t *testing.T, path string, serve func(net.Conn)) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	var mu sync.Mutex
	var open []net.Conn
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for _, conn := range open {
			_ = conn.Close()
		}
		mu.Unlock()
		group.Wait()
	})
	group.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			open = append(open, conn)
			mu.Unlock()
			group.Go(func() { serve(conn) })
		}
	})
}

// serveClientMessages decodes client frames from conn and hands each message to handle until the connection ends.
func serveClientMessages(conn net.Conn, handle func(protocol.ClientMessage) bool) {
	decoder, err := protocol.NewClientMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		return
	}
	buffer := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			messages, decodeErr := decoder.Push(buffer[:n])
			if decodeErr != nil {
				return
			}
			for _, message := range messages {
				if !handle(message) {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// TestUnixTransportSocketsUpstream holds the cases upstream gates with describe.runIf(process.platform !== "win32").
func TestUnixTransportSocketsUpstream(t *testing.T) {
	t.Parallel()
	t.Run("createUnixTransportFactory › carries a complete Client handshake and request over a real Unix socket", func(t *testing.T) {
		// upstream: packages/client/test/unix-transport.test.ts:55
		t.Parallel()
		path := filepath.Join(shortDirectory(t, "pi-client-transport-"), "pi.sock")
		var mu sync.Mutex
		var receivedMembers []string
		listenUnix(t, path, func(conn net.Conn) {
			serveClientMessages(conn, func(message protocol.ClientMessage) bool {
				switch message := message.(type) {
				case protocol.ClientHello:
					for _, b := range encodeServer(protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: testServerId}) {
						if _, err := conn.Write([]byte{b}); err != nil {
							return false
						}
					}
				case protocol.RequestEnvelope:
					raw, err := protocol.ToJSON(message.Call)
					if err != nil {
						return false
					}
					call, err := chord.ParseServiceCall(raw)
					if err != nil {
						return false
					}
					mu.Lock()
					receivedMembers = append(receivedMembers, call.ServiceId+"."+call.Member)
					mu.Unlock()
					frame := encodeServer(protocol.ResponseEnvelope{Id: message.Id, Ok: true, HasResult: true, Result: []any{}})
					split := len(frame) / 2
					if _, err := conn.Write(frame[:split]); err != nil {
						return false
					}
					if _, err := conn.Write(frame[split:]); err != nil {
						return false
					}
				}
				return true
			})
		})
		factory, err := CreateUnixTransportFactory(UnixTransportOptions{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: factory})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Dispose(); _ = client.WaitClosed(context.Background()) })

		hello, err := client.Connect(t.Context())
		if err != nil || hello.ServerId != testServerId {
			t.Fatalf("connect = %+v, %v", hello, err)
		}
		result, err := client.Request(t.Context(), protocol.ServerTarget{ServerId: testServerId}, chord.ServiceCall{ServiceId: "test.server", Member: "list", Args: []json.RawMessage{}})
		if err != nil || string(result) != `[]` {
			t.Fatalf("request = %s, %v; want []", result, err)
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(receivedMembers, []string{"test.server.list"}) {
			t.Fatalf("received members = %v", receivedMembers)
		}
	})
	t.Run("createUnixTransportFactory › reports truncated final frames through Client", func(t *testing.T) {
		// upstream: packages/client/test/unix-transport.test.ts:100
		t.Parallel()
		path := filepath.Join(shortDirectory(t, "pi-client-transport-"), "pi.sock")
		listenUnix(t, path, func(conn net.Conn) {
			serveClientMessages(conn, func(message protocol.ClientMessage) bool {
				if _, ok := message.(protocol.ClientHello); ok {
					_, err := conn.Write(encodeServer(protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: testServerId}))
					return err == nil
				}
				// socket.end(chunk): write the final bytes, then half-close.
				if _, err := conn.Write([]byte{0, 0, 0, 2, 1}); err != nil {
					return false
				}
				_ = conn.(*net.UnixConn).CloseWrite()
				return true
			})
		})
		factory, err := CreateUnixTransportFactory(UnixTransportOptions{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		client, err := NewClient(ClientOptions{ServerId: testServerId, TransportFactory: factory})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Dispose(); _ = client.WaitClosed(context.Background()) })

		if _, err := client.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		_, err = client.Request(t.Context(), protocol.ServerTarget{ServerId: testServerId}, chord.ServiceCall{ServiceId: "test.server", Member: "list", Args: []json.RawMessage{}})
		var validation *protocol.ProtocolValidationError
		if !errors.As(err, &validation) || !regexp.MustCompile(`(?i)truncated`).MatchString(validation.Message) {
			t.Fatalf("request error = %v; want a truncated ProtocolValidationError", err)
		}
		if state := client.ConnectionState(); state != Disconnected {
			t.Fatalf("connection state = %s", state)
		}
	})
	t.Run("createUnixTransportFactory › rejects connection attempts to missing sockets", func(t *testing.T) {
		// upstream: packages/client/test/unix-transport.test.ts:137
		t.Parallel()
		path := filepath.Join(shortDirectory(t, "pi-client-transport-"), "pi.sock")
		factory, err := CreateUnixTransportFactory(UnixTransportOptions{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		transport, err := factory(t.Context(), ByteTransportHandlers{OnData: func([]byte) {}, OnClose: func() {}, OnError: func(error) {}})
		if transport != nil {
			transport.Close()
		}
		completed := make(chan error, 1)
		completed <- err
		if err := <-completed; !errors.Is(err, syscall.ENOENT) {
			t.Fatalf("factory error = %v; want ENOENT", err)
		}
	})
}
