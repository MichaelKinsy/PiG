package experimental

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
)

func TestRadiusHostQueuedControlsSharePendingByteLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
		release := make(chan struct{})
		socket.onSend = func() { <-release }
		var accepted *RelayServerByteConnection
		var failures []string
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{
			ServerID: testServerID, Auth: explicitAuth(t, "http://localhost"),
			Accept: func(c *RelayServerByteConnection) RelayByteConnectionHandler {
				accepted = c
				return RelayByteConnectionHandler{OnError: func(err error) { failures = append(failures, err.Error()) }}
			},
			WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) { return socket, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		defer host.Close()
		defer func() {
			if release != nil {
				close(release)
			}
		}()
		socket.control("connection_open", testConnectionID)
		synctest.Wait()
		result := make(chan error, 1)
		go func() {
			result <- accepted.Send(make([]byte, maxPendingBytes-relayDataHeaderBytes-len(`{"version":1,"type":"pong"}`)))
		}()
		synctest.Wait()
		socket.control("ping", "")
		synctest.Wait()
		writer := accepted.session.writer
		writer.mu.Lock()
		pending := writer.pendingBytes
		writer.mu.Unlock()
		if pending != maxPendingBytes {
			t.Fatalf("exact boundary reserved %d bytes, want %d", pending, maxPendingBytes)
		}
		select {
		case closed := <-socket.closing:
			t.Fatalf("rejected exact byte boundary: %+v", closed)
		default:
		}
		socket.control("ping", "")
		synctest.Wait()
		select {
		case closed := <-socket.closing:
			if closed.code != 4001 || closed.reason != "Radius relay send failed" {
				t.Fatalf("overflow close = %+v", closed)
			}
		default:
			t.Fatal("reader did not reject overflowing control while a predecessor write was blocked")
		}
		if !slices.Equal(failures, []string{"Radius relay exceeded its pending byte limit"}) || !accepted.Closed() {
			t.Fatalf("overflow terminal state: closed=%t failures=%v", accepted.Closed(), failures)
		}
		close(release)
		synctest.Wait()
		release = nil
		host.Close()
		if err := <-result; err == nil {
			t.Fatal("active write did not observe closed socket")
		}
		if writer.pendingBytes != 0 {
			t.Fatalf("retained %d bytes after shutdown", writer.pendingBytes)
		}
	})
}

func TestRadiusHostQueuedControlFailureAndShutdown(t *testing.T) {
	for _, terminal := range []string{"write-error", "close", "cancel"} {
		t.Run(terminal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				socket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
				release := make(chan struct{})
				socket.onSend = func() { <-release }
				socket.sendErr = errors.New("queued pong failed")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var accepted *RelayServerByteConnection
				var events []string
				host, err := NewRadiusRelayHost(RadiusRelayHostOptions{
					ServerID: testServerID, Auth: explicitAuth(t, "http://localhost"),
					Accept: func(c *RelayServerByteConnection) RelayByteConnectionHandler {
						accepted = c
						return RelayByteConnectionHandler{
							OnClose: func() { events = append(events, "close") },
							OnError: func(err error) { events = append(events, "error:"+err.Error()) },
						}
					},
					WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) { return socket, nil },
				})
				if err != nil {
					t.Fatal(err)
				}
				host.Start(ctx)
				defer host.Close()
				defer func() {
					if release != nil {
						close(release)
					}
				}()
				socket.control("connection_open", testConnectionID)
				socket.control("ping", "")
				socket.control("ping", "")
				synctest.Wait()
				joined := make(chan struct{})
				if terminal != "write-error" {
					if terminal == "cancel" {
						cancel()
					}
					go func() { host.Close(); close(joined) }()
					synctest.Wait()
					select {
					case <-joined:
						t.Error("host shutdown returned while the control writer still owned the socket")
					default:
					}
				}
				close(release)
				synctest.Wait()
				release = nil
				host.Close()
				wantEvents, wantCode := []string{"close"}, 1000
				if terminal == "write-error" {
					wantEvents, wantCode = []string{"error:queued pong failed"}, 4001
				} else {
					<-joined
				}
				if !slices.Equal(events, wantEvents) || !accepted.Closed() {
					t.Fatalf("terminal state: events=%v closed=%t, want %v", events, accepted.Closed(), wantEvents)
				}
				if got := <-socket.closing; got.code != wantCode {
					t.Fatalf("close = %+v, want code %d", got, wantCode)
				}
				if pending := accepted.session.writer.pendingBytes; pending != 0 {
					t.Fatalf("retained %d queued bytes", pending)
				}
				if len(socket.sent) != 0 {
					t.Fatal("queued writes escaped shutdown")
				}
			})
		})
	}
}
