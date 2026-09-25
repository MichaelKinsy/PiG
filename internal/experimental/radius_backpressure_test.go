package experimental

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
)

// Upstream radius-relay.ts leaves pong, unknown-connection close, and rejected-open close writes unawaited in #handleHostMessage/#openConnection.
func TestRadiusHostControlsDoNotBlockIncomingTraffic(t *testing.T) {
	for _, control := range []string{"ping", "unknown", "rejected"} {
		for _, predecessor := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/predecessor=%t", control, predecessor), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					socket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
					release := make(chan struct{})
					socket.onSend = func() { <-release }
					otherID := "00000000-0000-4000-8000-000000000003"
					var events []string
					var accepted []*RelayServerByteConnection
					host, err := NewRadiusRelayHost(RadiusRelayHostOptions{
						ServerID: testServerID, Auth: explicitAuth(t, "http://localhost"),
						Accept: func(c *RelayServerByteConnection) RelayByteConnectionHandler {
							if c.id == testServerID {
								if err := c.Close(nil); err != nil {
									t.Error(err)
								}
								return RelayByteConnectionHandler{}
							}
							accepted = append(accepted, c)
							events = append(events, "open:"+c.id)
							return RelayByteConnectionHandler{
								OnData:  func(data []byte) { events = append(events, "data:"+string(data)) },
								OnClose: func() { events = append(events, "close:"+c.id) },
								OnError: func(err error) { t.Errorf("unexpected terminal error: %v", err) },
							}
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
					results := make(chan error, 2)
					var want []socketMessage
					if predecessor {
						frame, err := EncodeRelayDataFrame(testConnectionID, []byte("before"))
						if err != nil {
							t.Fatal(err)
						}
						want = append(want, socketMessage{binary: true, data: frame})
						go func() { results <- accepted[0].Send([]byte("before")) }()
						synctest.Wait()
					}
					switch control {
					case "ping":
						socket.control("ping", "")
						want = append(want, socketMessage{data: []byte(`{"version":1,"type":"pong"}`)})
					case "unknown":
						frame, err := EncodeRelayDataFrame(testServerID, []byte("unknown"))
						if err != nil {
							t.Fatal(err)
						}
						socket.incoming <- socketMessage{binary: true, data: frame}
						want = append(want, socketMessage{data: []byte(`{"version":1,"type":"connection_close","connection_id":"` + testServerID + `","code":1000}`)})
					case "rejected":
						socket.control("connection_open", testServerID)
						want = append(want, socketMessage{data: []byte(`{"version":1,"type":"connection_close","connection_id":"` + testServerID + `","code":1012}`)})
					}
					synctest.Wait()
					frame, err := EncodeRelayDataFrame(testConnectionID, []byte("inbound"))
					if err != nil {
						t.Fatal(err)
					}
					socket.incoming <- socketMessage{binary: true, data: frame}
					socket.control("connection_close", testConnectionID)
					socket.control("connection_open", otherID)
					synctest.Wait()
					wantEvents := []string{"open:" + testConnectionID, "data:inbound", "close:" + testConnectionID, "open:" + otherID}
					if !slices.Equal(events, wantEvents) {
						t.Fatalf("incoming traffic blocked behind control write: got %v, want %v before drain", events, wantEvents)
					}
					if !accepted[0].Closed() || accepted[1].Closed() {
						t.Fatal("remote close/open did not update connection state before drain")
					}
					go func() { results <- accepted[1].Send([]byte("after")) }()
					synctest.Wait()
					if len(socket.sent) != 0 {
						t.Fatal("write escaped held drain")
					}
					close(release)
					synctest.Wait()
					release = nil
					if err := <-results; err != nil {
						t.Fatal(err)
					}
					if predecessor {
						if err := <-results; err != nil {
							t.Fatal(err)
						}
					}
					frame, err = EncodeRelayDataFrame(otherID, []byte("after"))
					if err != nil {
						t.Fatal(err)
					}
					want = append(want, socketMessage{binary: true, data: frame})
					for i, expected := range want {
						select {
						case got := <-socket.sent:
							if got.binary != expected.binary || !slices.Equal(got.data, expected.data) {
								t.Fatalf("write %d = %+v, want %+v", i, got, expected)
							}
						default:
							t.Fatalf("missing write %d", i)
						}
					}
					if len(socket.sent) != 0 {
						t.Fatal("unexpected additional writes")
					}
				})
			})
		}
	}
}
