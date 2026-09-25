package experimental

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gorilla/websocket"
)

type socketMessage struct {
	binary bool
	data   []byte
	err    error
}
type socketClose struct {
	code   int
	reason string
}
type fakeRelaySocket struct {
	protocol string
	incoming chan socketMessage
	sent     chan socketMessage
	closed   chan struct{}
	closing  chan socketClose
	once     sync.Once
	onSend   func()
	sendErr  error
}

func newFakeRelaySocket(protocol string) *fakeRelaySocket {
	return &fakeRelaySocket{protocol: protocol, incoming: make(chan socketMessage, 32), sent: make(chan socketMessage, 32), closed: make(chan struct{}), closing: make(chan socketClose, 1)}
}
func (s *fakeRelaySocket) Protocol() string { return s.protocol }
func (s *fakeRelaySocket) Read() (bool, []byte, error) {
	select {
	case event := <-s.incoming:
		return event.binary, event.data, event.err
	case <-s.closed:
		return false, nil, &websocket.CloseError{Code: 1000}
	}
}
func (s *fakeRelaySocket) Send(binary bool, data []byte) error {
	if s.onSend != nil {
		s.onSend()
	}
	if s.sendErr != nil {
		return s.sendErr
	}
	select {
	case <-s.closed:
		return errors.New("closed")
	default:
	}
	s.sent <- socketMessage{binary: binary, data: bytes.Clone(data)}
	return nil
}
func (s *fakeRelaySocket) Close(code int, reason string) error {
	s.once.Do(func() { s.closing <- socketClose{code, reason}; close(s.closed) })
	return nil
}
func (s *fakeRelaySocket) control(kind, id string) {
	data, err := json.Marshal(hostControlMessage{Version: 1, Type: kind, ConnectionID: id})
	if err != nil {
		panic(err)
	}
	s.incoming <- socketMessage{data: data}
}
func explicitAuth(t *testing.T, gateway string) *RadiusRelayAuthResolver {
	t.Helper()
	auth, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Input: &AuthInput{Type: "token", Token: " secret "}, Gateway: gateway})
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func TestRadiusHostMultiplexingAndFinalChunk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
		accepted := make(chan *RelayServerByteConnection, 2)
		received := make(chan string, 2)
		closed := make(chan string, 2)
		statuses := make(chan RadiusRelayHostStatus, 8)
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{ServerID: testServerID, Auth: explicitAuth(t, "https://localhost/base?old=yes"), OnStatus: func(s RadiusRelayHostStatus) { statuses <- s }, Accept: func(c *RelayServerByteConnection) RelayByteConnectionHandler {
			accepted <- c
			return RelayByteConnectionHandler{OnData: func(data []byte) { received <- c.id + ":" + string(data) }, OnClose: func() { closed <- c.id }, OnError: func(err error) { closed <- err.Error() }}
		}, WebSocketFactory: func(_ context.Context, options RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
			if options.URL != "wss://localhost/v1/session-relays/"+testServerID+"/connect" || options.Authorization != "Bearer secret" || options.Protocol != RadiusRelayHostSubprotocol {
				t.Errorf("opening = %+v", options)
			}
			return socket, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		host.Start(t.Context())
		defer host.Close()
		synctest.Wait()
		if first, second := <-statuses, <-statuses; first.Status != "connecting" || second.Status != "connected" {
			t.Fatalf("statuses = %+v %+v", first, second)
		}
		otherID := "00000000-0000-4000-8000-000000000003"
		socket.control("connection_open", testConnectionID)
		socket.control("connection_open", otherID)
		synctest.Wait()
		first, second := <-accepted, <-accepted
		for _, connection := range []*RelayServerByteConnection{first, second} {
			frame, err := EncodeRelayDataFrame(connection.id, []byte(connection.id))
			if err != nil {
				t.Fatal(err)
			}
			socket.incoming <- socketMessage{binary: true, data: frame}
		}
		synctest.Wait()
		if got := <-received; got != testConnectionID+":"+testConnectionID {
			t.Fatal(got)
		}
		if got := <-received; got != otherID+":"+otherID {
			t.Fatal(got)
		}
		if err := first.Send([]byte{4, 5, 6}); err != nil {
			t.Fatal(err)
		}
		sent := <-socket.sent
		parsed, ok := ParseRelayDataFrame(sent.data)
		if !sent.binary || !ok || parsed.ConnectionID != testConnectionID || !bytes.Equal(parsed.Payload, []byte{4, 5, 6}) {
			t.Fatalf("sent = %+v", sent)
		}
		if err := first.Close([]byte("final")); err != nil {
			t.Fatal(err)
		}
		final, control := <-socket.sent, <-socket.sent
		parsed, ok = ParseRelayDataFrame(final.data)
		if !ok || string(parsed.Payload) != "final" || !final.binary {
			t.Fatalf("final = %+v", final)
		}
		if string(control.data) != `{"version":1,"type":"connection_close","connection_id":"`+testConnectionID+`","code":1000}` {
			t.Fatalf("control = %s", control.data)
		}
		if err := first.Close(nil); err != nil {
			t.Fatal(err)
		}
		if err := first.Send(nil); err == nil {
			t.Fatal("send after close succeeded")
		}
		socket.control("connection_close", otherID)
		socket.control("connection_close", otherID)
		synctest.Wait()
		if got := <-closed; got != otherID || !second.Closed() || len(closed) != 0 {
			t.Fatalf("close = %s", got)
		}
		socket.control("ping", "")
		synctest.Wait()
		if got := <-socket.sent; got.binary || string(got.data) != `{"version":1,"type":"pong"}` {
			t.Fatalf("pong = %+v", got)
		}
		host.Close()
		if got := <-socket.closing; got.code != 1000 {
			t.Fatalf("local close = %+v", got)
		}
	})
}

func TestRadiusHostRetryRefreshAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sockets := make(chan *fakeRelaySocket, 4)
		statuses := make(chan RadiusRelayHostStatus, 8)
		var attempts atomic.Int32
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{ServerID: testServerID, Auth: explicitAuth(t, "http://localhost"), Accept: func(*RelayServerByteConnection) RelayByteConnectionHandler { return RelayByteConnectionHandler{} }, OnStatus: func(s RadiusRelayHostStatus) { statuses <- s }, WebSocketFactory: func(ctx context.Context, options RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
			attempt := attempts.Add(1)
			if attempt == 2 {
				return nil, errors.New("temporary")
			}
			if attempt == 3 {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			socket := newFakeRelaySocket(options.Protocol)
			sockets <- socket
			return socket, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		synctest.Wait()
		socket := <-sockets
		socket.incoming <- socketMessage{err: &websocket.CloseError{Code: 1006, Text: "lost"}}
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if attempts.Load() != 2 {
			t.Fatalf("attempts after 1s = %d", attempts.Load())
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if attempts.Load() != 3 {
			t.Fatalf("attempts after 3s = %d", attempts.Load())
		}
		host.Close()
		var got []string
		for len(statuses) > 0 {
			status := <-statuses
			got = append(got, status.Status+":"+status.Error)
		}
		want := "connecting:|connected:|retrying:Radius relay host closed (1006: lost)|connecting:|retrying:temporary|connecting:"
		if strings.Join(got, "|") != want {
			t.Fatalf("statuses = %v", got)
		}
	})
}

func TestRadiusHostProtocolRejectionAndDropOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
		var errorsSeen []string
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{ServerID: testServerID, Auth: explicitAuth(t, "http://localhost"), Accept: func(c *RelayServerByteConnection) RelayByteConnectionHandler {
			return RelayByteConnectionHandler{OnError: func(err error) {
				if !c.Closed() {
					t.Error("terminal callback before closed")
				}
				errorsSeen = append(errorsSeen, c.id+":"+err.Error())
			}}
		}, WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) { return socket, nil }})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		defer host.Close()
		synctest.Wait()
		socket.control("connection_open", testConnectionID)
		socket.control("connection_open", testServerID)
		socket.control("connection_open", testConnectionID)
		synctest.Wait()
		if got := <-socket.closing; got.code != 4000 || got.reason != "Radius relay protocol error" {
			t.Fatalf("close = %+v", got)
		}
		want := testConnectionID + ":Radius relay reused a connection ID|" + testServerID + ":Radius relay reused a connection ID"
		if strings.Join(errorsSeen, "|") != want {
			t.Fatalf("drop = %v", errorsSeen)
		}
	})
}

func TestRadiusClientRawBinaryAndTerminalEvents(t *testing.T) {
	for _, terminal := range []string{"remote-close", "error", "text", "local-close", "cancel"} {
		t.Run(terminal, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				socket := newFakeRelaySocket(RadiusRelayClientSubprotocol)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var received []byte
				closes, failures := 0, 0
				factory := CreateRadiusClientTransportFactory(RadiusClientTransportOptions{ServerID: testServerID, Auth: explicitAuth(t, "http://localhost"), WebSocketFactory: func(_ context.Context, options RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
					if options.Authorization != "Bearer secret" || options.Protocol != RadiusRelayClientSubprotocol {
						t.Errorf("opening = %+v", options)
					}
					return socket, nil
				}})
				transport, err := factory(ctx, RelayByteConnectionHandler{OnData: func(data []byte) { received = data }, OnClose: func() { closes++ }, OnError: func(error) { failures++ }})
				if err != nil {
					t.Fatal(err)
				}
				if err := transport.Send([]byte{1, 2, 3}); err != nil {
					t.Fatal(err)
				}
				sent := <-socket.sent
				if !sent.binary || !bytes.Equal(sent.data, []byte{1, 2, 3}) {
					t.Fatalf("sent = %+v", sent)
				}
				socket.incoming <- socketMessage{binary: true, data: []byte{4, 5, 6}}
				synctest.Wait()
				if !bytes.Equal(received, []byte{4, 5, 6}) {
					t.Fatal(received)
				}
				switch terminal {
				case "remote-close":
					socket.incoming <- socketMessage{err: &websocket.CloseError{Code: 1006}}
				case "error":
					socket.incoming <- socketMessage{err: errors.New("network lost")}
				case "text":
					socket.incoming <- socketMessage{data: []byte("not binary")}
				case "local-close":
					transport.Close()
				case "cancel":
					cancel()
				}
				<-transport.Done()
				wantClose, wantError := 0, 0
				if terminal == "remote-close" {
					wantClose = 1
				}
				if terminal == "error" || terminal == "text" {
					wantError = 1
				}
				if closes != wantClose || failures != wantError {
					t.Fatalf("close/error = %d/%d", closes, failures)
				}
				if err := transport.Send(nil); err == nil {
					t.Fatal("send after terminal succeeded")
				}
				transport.Close()
			})
		})
	}
}

func TestRadiusWriterOrderedCopiedBoundedAndDrained(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := newFakeRelaySocket(RadiusRelayClientSubprotocol)
		release := make(chan struct{})
		socket.onSend = func() { <-release }
		writer := newOrderedWebSocketWriter(socket)
		first, second := []byte("first"), []byte("second")
		results := make(chan error, 2)
		go func() { results <- writer.send(true, first) }()
		synctest.Wait()
		go func() { results <- writer.send(true, second) }()
		synctest.Wait()
		first[0], second[0] = 'X', 'Y'
		if err := writer.send(true, make([]byte, maxPendingBytes)); err == nil || !strings.Contains(err.Error(), "pending byte limit") {
			t.Fatalf("limit = %v", err)
		}
		close(release)
		if err := <-results; err != nil {
			t.Fatal(err)
		}
		if err := <-results; err != nil {
			t.Fatal(err)
		}
		if got := <-socket.sent; string(got.data) != "first" {
			t.Fatal(string(got.data))
		}
		if got := <-socket.sent; string(got.data) != "second" {
			t.Fatal(string(got.data))
		}
		writer.close()
		writer.wait()
		if writer.pendingBytes != 0 {
			t.Fatalf("pending = %d", writer.pendingBytes)
		}
		if err := writer.send(true, nil); err == nil {
			t.Fatal("send after close")
		}
	})
}

func TestRadiusControlValidation(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `{`, `{}`, `{"version":"1","type":"ping"}`, `{"version":2,"type":"ping"}`, `{"version":1,"type":"invalid"}`, `{"version":1,"type":"connection_open","connection_id":"bad"}`} {
		if _, err := parseHostControlMessage([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	for _, code := range []string{`null`, `true`, `"1000"`, `999`, `5000`, `1000.5`} {
		input := fmt.Sprintf(`{"version":1,"type":"connection_close","connection_id":%q,"code":%s}`, testConnectionID, code)
		if _, err := parseHostControlMessage([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	for _, code := range []string{`1000`, `4999`, `1e3`, `1000.0`} {
		input := fmt.Sprintf(`{"version":1.0,"type":"connection_close","connection_id":%q,"code":%s}`, testConnectionID, code)
		if _, err := parseHostControlMessage([]byte(input)); err != nil {
			t.Fatalf("rejected %s: %v", input, err)
		}
	}
}

func TestRadiusRealFakeGateway(t *testing.T) {
	headers := make(chan http.Header, 1)
	paths := make(chan string, 1)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		paths <- r.URL.Path
		upgrader := websocket.Upgrader{Subprotocols: []string{RadiusRelayClientSubprotocol}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := conn.WriteMessage(kind, data); err != nil {
			return
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer gateway.Close()
	data := make(chan []byte, 1)
	transport, err := CreateRadiusClientTransportFactory(RadiusClientTransportOptions{ServerID: testServerID, Auth: explicitAuth(t, gateway.URL)})(t.Context(), RelayByteConnectionHandler{OnData: func(chunk []byte) { data <- chunk }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { transport.Close(); <-transport.Done() }()
	if err := transport.Send([]byte("local echo")); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-data:
		if string(received) != "local echo" {
			t.Fatal(string(received))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("echo timed out")
	}
	if got := <-headers; got.Get("Authorization") != "Bearer secret" || got.Get("Sec-WebSocket-Protocol") != RadiusRelayClientSubprotocol {
		t.Fatal(got)
	}
	if got := <-paths; got != "/v1/session-relays/"+testServerID+"/connect" {
		t.Fatal(got)
	}
}

func TestRadiusHandshakeFailures(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol string
		err      error
		want     string
	}{
		{"protocol", "wrong", nil, `Radius relay selected unexpected WebSocket protocol "wrong"`},
		{"failure", "", io.ErrUnexpectedEOF, "unexpected EOF"},
		{"empty failure", "", errors.New(""), "Radius WebSocket connection failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket := newFakeRelaySocket(test.protocol)
			_, err := openRadiusRelayWebSocket(t.Context(), &RadiusRelayAuth{Gateway: "http://localhost", Token: "token"}, testServerID, RadiusRelayClientSubprotocol, func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
				if test.err != nil {
					return nil, test.err
				}
				return socket, nil
			})
			if err == nil || err.Error() != test.want {
				t.Fatalf("failure = %v", err)
			}
		})
	}
}
