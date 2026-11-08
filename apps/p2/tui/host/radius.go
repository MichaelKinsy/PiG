package host

// The TUI launcher through Radius (`p2 tui --radius`): the launcher's own Radius sign-in, finding the server, and its requests as Radius client connections,
// which the server's Radius plugin answers (plugins/radius/contract brain, "Client channels").
//
// Temporary upstream: one Radius connection per request. Every connection counts against Radius's limit on new connections (120 a minute per user, shared
// with the server's own reconnects), so the launcher checks access only every RadiusPollInterval. The proper form carries all of a launcher's requests and its
// chord link over one connection; see docs/p2-notes.md "TUI through Radius".
//
// pi: apps/p2/src/hosts/tui-radius.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/gorilla/websocket"
)

// RadiusPollInterval is how often a launcher through Radius checks that the server still admits it (see the file comment).
const RadiusPollInterval = 10 * time.Second

const (
	clientSubprotocol  = "pi-server.client.v2"
	maxRadiusMessage   = 16 * 1024 * 1024
	bodyChunkBytes     = 256 * 1024
	radiusRefreshAhead = 5 * time.Minute
)

// RadiusClient is a signed-in Radius user: the gateway and a current access token.
type RadiusClient struct {
	Gateway string
	Access  func(ctx context.Context) (string, error)
}

type radiusLogin struct {
	Gateway    string         `json:"gateway"`
	Credential map[string]any `json:"credential"`
}

func readRadiusLogin(path string) (*radiusLogin, error) {
	text, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var login radiusLogin
	// A file on disk: checked before use.
	if json.Unmarshal(text, &login) != nil || login.Gateway == "" || login.Credential["type"] != "oauth" {
		return nil, fmt.Errorf("Invalid %s; delete it to sign in to Radius again", path)
	}
	return &login, nil
}

func saveRadiusLogin(directory, path string, login radiusLogin) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "\t")
	if err := encoder.Encode(login); err != nil {
		return err
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func credentialOf(login radiusLogin) ai.OAuthCredentials {
	encoded, _ := json.Marshal(login.Credential)
	var credential ai.OAuthCredentials
	_ = json.Unmarshal(encoded, &credential)
	return credential
}

func loginOf(gateway string, credential ai.OAuthCredentials) radiusLogin {
	encoded, _ := json.Marshal(credential)
	fields := map[string]any{}
	_ = json.Unmarshal(encoded, &fields)
	fields["type"] = "oauth"
	return radiusLogin{Gateway: gateway, Credential: fields}
}

// NewRadiusClient is the launcher's Radius sign-in, kept in `<config>/radius.json` (0600): read, or made once by device code (print shows the code), and
// refreshed before the token expires. Separate from a p2 server's own `radius.json`.
func NewRadiusClient(ctx context.Context, print func(text string), directory string) (*RadiusClient, error) {
	path := filepath.Join(directory, "radius.json")
	login, err := readRadiusLogin(path)
	if err != nil {
		return nil, err
	}
	if login == nil {
		gateway := ai.NormalizeRadiusGatewayURL(firstNonEmpty(os.Getenv("PI_RADIUS_GATEWAY"), ai.DefaultRadiusGateway))
		print("Signing in to Radius at " + gateway)
		credential, err := ai.CreateRadiusOAuth(ai.RadiusOAuthOptions{Gateway: gateway}).LoginContext(ctx, ai.OAuthLoginCallbacks{
			OnSelect: func(ai.OAuthSelectPrompt) (string, error) { return ai.RadiusLoginMethodDeviceCode, nil },
			OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
				print("Open " + info.VerificationURI + " and enter the code " + info.UserCode)
			},
		})
		if err != nil {
			return nil, err
		}
		next := loginOf(gateway, credential)
		if err := saveRadiusLogin(directory, path, next); err != nil {
			return nil, err
		}
		login = &next
	}
	var mu sync.Mutex
	current := *login
	return &RadiusClient{Gateway: current.Gateway, Access: func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		credential := credentialOf(current)
		if time.UnixMilli(credential.Expires).Sub(time.Now()) > radiusRefreshAhead {
			return credential.Access, nil
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		refreshed, err := ai.CreateRadiusOAuth(ai.RadiusOAuthOptions{Gateway: current.Gateway}).RefreshTokenContext(refreshCtx, credential)
		if err != nil {
			return "", err
		}
		next := loginOf(current.Gateway, refreshed)
		if err := saveRadiusLogin(directory, path, next); err != nil {
			return "", err
		}
		current = next
		return refreshed.Access, nil
	}}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// RadiusServer is the server to connect to: serverID, else this user's only server Radius reports online.
func RadiusServer(ctx context.Context, client *RadiusClient, serverID string) (string, error) {
	if serverID != "" {
		return serverID, nil
	}
	token, err := client.Access(ctx)
	if err != nil {
		return "", err
	}
	timed, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(timed, http.MethodGet, strings.TrimRight(client.Gateway, "/")+"/v1/servers", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", fmt.Errorf("Radius did not list your servers (%d)", response.StatusCode)
	}
	var listing struct {
		Servers []struct {
			ServerID string `json:"server_id"`
			Status   string `json:"status"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&listing); err != nil {
		return "", err
	}
	var online []string
	for _, server := range listing.Servers {
		if server.Status == "online" {
			online = append(online, server.ServerID)
		}
	}
	switch len(online) {
	case 1:
		return online[0], nil
	case 0:
		return "", errors.New("None of your p2 servers is connected to Radius")
	}
	return "", fmt.Errorf("Several of your servers are online; choose one with --radius <server-id>: %s", strings.Join(online, ", "))
}

// RadiusTransport is a transport through Radius, and the server's origin as its last answer named it.
type RadiusTransport struct {
	client   *RadiusClient
	serverID string
	mu       sync.Mutex
	origin   string
}

// NewRadiusTransport is a transport to serverID through client: requests to it, one client connection each. A request is one text message, the body as
// binary messages and a text end marker. The server answers with one text message (the head): for HTTP the response body follows as binary messages and a
// close with 1000 ends it; for the chord WebSocket, status 101 makes the connection the link itself.
func NewRadiusTransport(client *RadiusClient, serverID string) *RadiusTransport {
	return &RadiusTransport{client: client, serverID: serverID}
}

// Origin is the server's origin as its last answer named it ("" before one did).
func (t *RadiusTransport) Origin() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.origin
}

type tunnelHead struct {
	Status  int         `json:"status"`
	Headers [][2]string `json:"headers"`
	Origin  *string     `json:"origin"`
}

func (t *RadiusTransport) connect(ctx context.Context) (*websocket.Conn, *http.Response, error) {
	token, err := t.client.Access(ctx)
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
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Subprotocols: []string{clientSubprotocol}}
	conn, response, err := dialer.DialContext(ctx, scheme+"://"+base.Host+"/v1/servers/"+url.PathEscape(t.serverID)+"/connect", http.Header{"Authorization": {"Bearer " + token}})
	if conn != nil {
		conn.SetReadLimit(maxRadiusMessage)
	}
	return conn, response, err
}

// parse is the server's answer, from the wire: checked before use.
func (t *RadiusTransport) parse(data []byte) (tunnelHead, error) {
	var value tunnelHead
	if err := json.Unmarshal(data, &value); err != nil || value.Origin == nil || value.Headers == nil {
		return tunnelHead{}, errors.New("Invalid answer through Radius")
	}
	t.mu.Lock()
	t.origin = *value.Origin
	t.mu.Unlock()
	return value, nil
}

type emptyBody struct{}

func (emptyBody) Read([]byte) (int, error) { return 0, io.EOF }

// Request is one HTTP request over its own connection. A 401 or 403 from Radius, which answers so when the server refused the connection (or the sign-in is
// invalid), is a response like any server's.
func (t *RadiusTransport) Request(ctx context.Context, path string, r Request) (*http.Response, error) {
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	conn, refused, err := t.connect(ctx)
	if err != nil {
		if refused != nil {
			_ = refused.Body.Close()
			if refused.StatusCode == 401 || refused.StatusCode == 403 {
				return &http.Response{StatusCode: refused.StatusCode, Header: http.Header{}, Body: io.NopCloser(emptyBody{})}, nil
			}
			return nil, fmt.Errorf("Radius refused the connection (%d)", refused.StatusCode)
		}
		return nil, err
	}
	headers := r.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	request, _ := json.Marshal(map[string]any{"type": "http", "method": method, "path": path, "headers": headers})
	abort := context.AfterFunc(ctx, func() { _ = conn.Close() })
	fail := func(err error) (*http.Response, error) {
		abort()
		_ = conn.Close()
		if ctx.Err() != nil {
			return nil, errors.New("Request aborted")
		}
		return nil, err
	}
	if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
		return fail(err)
	}
	if r.Body != nil {
		buffer := make([]byte, bodyChunkBytes)
		for {
			n, readErr := r.Body.Read(buffer)
			if n > 0 {
				if err := conn.WriteMessage(websocket.BinaryMessage, append([]byte(nil), buffer[:n]...)); err != nil {
					return fail(err)
				}
			}
			if readErr != nil {
				break
			}
		}
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"end"}`)); err != nil {
		return fail(err)
	}
	_, first, err := conn.ReadMessage()
	if err != nil {
		return fail(errors.New("The server closed the connection without an answer"))
	}
	answer, err := t.parse(first)
	if err != nil {
		return fail(err)
	}
	header := http.Header{}
	for _, pair := range answer.Headers {
		header.Add(pair[0], pair[1])
	}
	response := &http.Response{StatusCode: answer.Status, Header: header, ContentLength: -1}
	if answer.Status == 204 || answer.Status == 205 || answer.Status == 304 {
		abort()
		_ = conn.Close()
		response.Body = io.NopCloser(emptyBody{})
		return response, nil
	}
	reader, writer := io.Pipe()
	go func() {
		defer abort()
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
	response.Body = reader
	return response, nil
}

type radiusSocket struct {
	mu         sync.Mutex
	conn       *websocket.Conn
	open       atomic.Bool
	terminated bool
	writeM     sync.Mutex
}

func (s *radiusSocket) Open() bool { return s.open.Load() }

func (s *radiusSocket) Send(data []byte) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return io.ErrClosedPipe
	}
	s.writeM.Lock()
	defer s.writeM.Unlock()
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *radiusSocket) Terminate() {
	s.mu.Lock()
	s.terminated = true
	conn := s.conn
	s.mu.Unlock()
	s.open.Store(false)
	if conn != nil {
		_ = conn.Close()
	}
}

// Socket is the chord WebSocket over its own connection: open once the server answers 101, then the link's frames unchanged.
func (t *RadiusTransport) Socket(path string, events SocketEvents) Socket {
	socket := &radiusSocket{}
	go func() {
		closeOnce := sync.OnceFunc(func() {
			socket.open.Store(false)
			events.Close()
		})
		defer closeOnce()
		conn, response, err := t.connect(context.Background())
		if err != nil {
			if response != nil {
				_ = response.Body.Close()
				if events.Refused != nil {
					events.Refused(response.StatusCode)
				}
			}
			return
		}
		socket.mu.Lock()
		socket.conn = conn
		terminated := socket.terminated
		socket.mu.Unlock()
		defer conn.Close()
		if terminated {
			return
		}
		request, _ := json.Marshal(map[string]any{"type": "websocket", "path": path, "headers": map[string]string{}})
		if conn.WriteMessage(websocket.TextMessage, request) != nil {
			return
		}
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if socket.open.Load() {
				events.Message(data, kind == websocket.BinaryMessage)
				continue
			}
			status := 0
			if kind == websocket.TextMessage {
				if answer, err := t.parse(data); err == nil {
					status = answer.Status
				}
			}
			if status == 101 {
				socket.open.Store(true)
				events.Open()
				continue
			}
			if status != 0 && events.Refused != nil {
				events.Refused(status)
			}
			return
		}
	}()
	return socket
}

// RadiusConnection is a launcher signed in through Radius: the server's origin and the transport to it.
type RadiusConnection struct {
	URL       *url.URL
	Transport *RadiusTransport
}

// ConnectRadius signs in to Radius, finds the server, and checks that it admits this user as its owner. The server's answer names its origin, which the
// launcher shows and builds browser links from.
func ConnectRadius(ctx context.Context, serverID string, out io.Writer) (*RadiusConnection, error) {
	client, err := NewRadiusClient(ctx, func(text string) { fmt.Fprintln(out, text) }, ConfigDirectory())
	if err != nil {
		return nil, err
	}
	id, err := RadiusServer(ctx, client, serverID)
	if err != nil {
		return nil, err
	}
	transport := NewRadiusTransport(client, id)
	timed, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := transport.Request(timed, "/owner/check", Request{})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var value struct {
		Owner bool `json:"owner"`
	}
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		_ = json.NewDecoder(response.Body).Decode(&value)
	}
	origin := transport.Origin()
	if !value.Owner || origin == "" {
		return nil, fmt.Errorf("This server does not admit you as its owner through Radius (%d)", response.StatusCode)
	}
	target, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	return &RadiusConnection{URL: target, Transport: transport}, nil
}
