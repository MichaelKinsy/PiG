package radiustest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/gorilla/websocket"
)

// Ports apps/p2/src/hosts/tui-radius.ts `radiusTransport`: requests to a server through Radius, one client connection each. A
// request is one text message (`{type, method, path, headers}`), the body as binary messages and a text end marker. The
// server answers with one text message (`{status, headers, origin}`): for HTTP the response body follows as binary messages
// and a close with 1000 ends it; for the chord WebSocket, status 101 makes the connection the link itself.

// Client is a signed-in Radius user: the gateway and a current access token.
type Client struct {
	Gateway string
	Access  func() (string, error)
}

// Transport is a transport through Radius, and the server's origin as its last answer named it.
type Transport struct {
	client   Client
	serverID string

	mu     sync.Mutex
	origin string
}

// NewTransport is a transport to serverID through client.
func NewTransport(client Client, serverID string) *Transport {
	return &Transport{client: client, serverID: serverID}
}

// Origin is the server's origin as its last answer named it ("" before one did).
func (t *Transport) Origin() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.origin
}

type head struct {
	Status  int         `json:"status"`
	Headers [][2]string `json:"headers"`
	Origin  *string     `json:"origin"`
}

func (t *Transport) connect(ctx context.Context) (*websocket.Conn, *http.Response, error) {
	token, err := t.client.Access()
	if err != nil {
		return nil, nil, err
	}
	base, err := url.Parse(t.client.Gateway)
	if err != nil {
		return nil, nil, err
	}
	scheme := "ws"
	if base.Scheme == "https" {
		scheme = "wss"
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Subprotocols: []string{clientProtocol}}
	conn, response, err := dialer.DialContext(ctx, scheme+"://"+base.Host+"/v1/servers/"+url.PathEscape(t.serverID)+"/connect", http.Header{"Authorization": {"Bearer " + token}})
	if conn != nil {
		conn.SetReadLimit(16 * 1024 * 1024)
	}
	return conn, response, err
}

func (t *Transport) parse(data []byte) (head, error) {
	var value head
	if err := json.Unmarshal(data, &value); err != nil || value.Origin == nil || value.Headers == nil {
		return head{}, errors.New("Invalid answer through Radius")
	}
	t.mu.Lock()
	t.origin = *value.Origin
	t.mu.Unlock()
	return value, nil
}

// Response is the answer to a request.
type Response struct {
	Status int
	// Body is the response body; a status of 204, 205 or 304 has none.
	Body io.ReadCloser
}

// Bytes reads the whole body.
func (r *Response) Bytes() ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

// Request is one HTTP request over its own connection. A 401 or 403 from Radius, which answers so when the server refused
// the connection (or the sign-in is invalid), is a Response like any server's.
func (t *Transport) Request(ctx context.Context, method, path string, body io.Reader) (*Response, error) {
	conn, refused, err := t.connect(ctx)
	if err != nil {
		if refused != nil && (refused.StatusCode == 401 || refused.StatusCode == 403) {
			_ = refused.Body.Close()
			return &Response{Status: refused.StatusCode, Body: io.NopCloser(emptyReader{})}, nil
		}
		if refused != nil {
			_ = refused.Body.Close()
			return nil, fmt.Errorf("Radius refused the connection (%d)", refused.StatusCode)
		}
		return nil, err
	}
	request, _ := chordjson.Marshal(delta.JsonObjectOf("type", "http", "method", method, "path", path, "headers", delta.NewJsonObject(0)))
	if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if body != nil {
		buffer := make([]byte, 256*1024)
		for {
			n, readErr := body.Read(buffer)
			if n > 0 {
				if err := conn.WriteMessage(websocket.BinaryMessage, append([]byte(nil), buffer[:n]...)); err != nil {
					_ = conn.Close()
					return nil, err
				}
			}
			if readErr != nil {
				break
			}
		}
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"end"}`)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_, first, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, errors.New("The server closed the connection without an answer")
	}
	answer, err := t.parse(first)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader, writer := io.Pipe()
	//portlint:allow golifetime ends when the connection closes (its read fails), which closes it
	go func() {
		defer conn.Close()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				var closed *websocket.CloseError
				if errors.As(err, &closed) && closed.Code == websocket.CloseNormalClosure {
					_ = writer.Close()
				} else {
					_ = writer.CloseWithError(fmt.Errorf("The connection through Radius was lost (%v)", err))
				}
				return
			}
			if kind != websocket.BinaryMessage {
				_ = writer.CloseWithError(errors.New("Invalid answer through Radius"))
				return
			}
			if _, err := writer.Write(data); err != nil {
				return
			}
		}
	}()
	if answer.Status == 204 || answer.Status == 205 || answer.Status == 304 {
		_ = reader.Close()
		return &Response{Status: answer.Status, Body: io.NopCloser(emptyReader{})}, nil
	}
	return &Response{Status: answer.Status, Body: reader}, nil
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

// Socket opens the chord WebSocket over its own connection: the connection once the server answers 101 (the link's frames
// follow unchanged), or the status it refused with.
func (t *Transport) Socket(ctx context.Context, path string) (conn *websocket.Conn, refused int, err error) {
	conn, response, err := t.connect(ctx)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
			return nil, response.StatusCode, nil
		}
		return nil, 0, err
	}
	request, _ := chordjson.Marshal(delta.JsonObjectOf("type", "websocket", "path", path, "headers", delta.NewJsonObject(0)))
	if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
		_ = conn.Close()
		return nil, 0, err
	}
	kind, data, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, 0, err
	}
	answer, parseErr := t.parse(data)
	if kind != websocket.TextMessage || parseErr != nil {
		_ = conn.Close()
		return nil, 0, errors.New("Invalid answer through Radius")
	}
	if answer.Status != 101 {
		_ = conn.Close()
		return nil, answer.Status, nil
	}
	return conn, 0, nil
}
