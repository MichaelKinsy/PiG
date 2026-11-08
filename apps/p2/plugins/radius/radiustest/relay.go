// Package radiustest is a local stand-in for Radius (apps/p2/test/p2/radius-fixture.ts) and the TUI launcher's transport
// through it (apps/p2/src/hosts/tui-radius.ts, which a Pi client uses; Go has no launcher yet, so the transport is only the
// tests' client). It never contacts Radius.
package radiustest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Owner is the Radius user the fake relay admits as the brain's owner.
var Owner = Principal{Kind: "user", ID: "usr_1", OrganizationID: "org_1"}

// PublicOrigin is the brain's browser-facing origin through the fake relay.
const PublicOrigin = "https://0123456789abcdef0123456789abcdef.rdtun.com"

const clientProtocol = "pi-server.client.v2"

// Principal is who opened a channel.
type Principal struct {
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Email          string `json:"email,omitempty"`
}

// Frame is one binary frame the brain sent on the host socket.
type Frame struct {
	ID      string
	Type    byte
	Payload []byte
}

// Options select what the relay speaks.
type Options struct {
	// Channels makes it relay protocol v5: the brain's channel sockets (`/v1/servers/<id>/channels/<connection>`) are
	// accepted unless RejectChannels, and Channel(id) returns the relay's end of one. Pi clients (`pi-server.client.v2`) get
	// a `ws` channel without a request, as the owner for `Bearer fixture-token` and as another user otherwise; their upgrade
	// fails with 403 when the brain refuses the channel.
	Channels       bool
	RejectChannels bool
}

// Relay is a fake Radius: the context endpoint, the shares endpoint and relay v4 or v5.
type Relay struct {
	t       testing.TB
	version string
	options Options
	server  *http.Server
	// Gateway is the URL the brain connects to.
	Gateway string

	mu             sync.Mutex
	cond           *sync.Cond
	shares         [][]string
	controls       []map[string]any
	frames         []Frame
	host           *websocket.Conn
	hostWrite      sync.Mutex
	paused         bool
	conns          []*websocket.Conn
	channels       map[string]*websocket.Conn
	channelWaiters map[string]chan *websocket.Conn
	closeWaiters   map[string]chan struct{}
	hostReady      chan struct{}
	readyOnce      sync.Once
}

// Start starts a fake relay for the test.
func Start(t testing.TB, options Options) *Relay {
	r := &Relay{t: t, options: options, version: "pi-server.host.v4", channels: map[string]*websocket.Conn{},
		channelWaiters: map[string]chan *websocket.Conn{}, closeWaiters: map[string]chan struct{}{}, hostReady: make(chan struct{})}
	if options.Channels {
		r.version = "pi-server.host.v5"
	}
	r.cond = sync.NewCond(&r.mu)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r.Gateway = "http://" + listener.Addr().String()
	r.server = &http.Server{Handler: http.HandlerFunc(r.serve)}
	go func() { _ = r.server.Serve(listener) }()
	t.Cleanup(r.Close)
	return r
}

// Close terminates every socket and stops the server.
func (r *Relay) Close() {
	r.mu.Lock()
	conns := r.conns
	r.conns = nil
	r.paused = false
	r.mu.Unlock()
	r.cond.Broadcast()
	for _, conn := range conns {
		_ = conn.Close()
	}
	_ = r.server.Close()
}

var channelPath = regexp.MustCompile(`^/v1/servers/[^/]+/channels/([^/]+)$`)
var sharesPath = regexp.MustCompile(`^/v1/servers/[^/]+/shares$`)

func (r *Relay) upgrade(w http.ResponseWriter, request *http.Request) *websocket.Conn {
	offered := map[string]bool{}
	for _, header := range request.Header.Values("Sec-Websocket-Protocol") {
		for _, protocol := range strings.Split(header, ",") {
			offered[strings.TrimSpace(protocol)] = true
		}
	}
	header := http.Header{}
	for _, protocol := range []string{r.version, "pi-server.channel.v1", clientProtocol} {
		if offered[protocol] {
			header.Set("Sec-Websocket-Protocol", protocol)
			break
		}
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, request, header)
	if err != nil {
		return nil
	}
	r.mu.Lock()
	r.conns = append(r.conns, conn)
	r.mu.Unlock()
	return conn
}

func (r *Relay) serve(w http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPut && sharesPath.MatchString(request.URL.Path) {
		var body struct {
			Emails []string `json:"emails"`
		}
		raw, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(raw, &body)
		r.mu.Lock()
		r.shares = append(r.shares, append([]string{}, body.Emails...))
		r.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if websocket.IsWebSocketUpgrade(request) {
		r.serveSocket(w, request)
		return
	}
	if request.URL.Path != "/v1/context" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"actor":        map[string]any{"principal_id": Owner.ID, "principal_kind": Owner.Kind},
		"organization": map[string]any{"id": Owner.OrganizationID},
	})
}

func (r *Relay) serveSocket(w http.ResponseWriter, request *http.Request) {
	if strings.Contains(request.Header.Get("Sec-Websocket-Protocol"), clientProtocol) {
		r.connectClient(w, request)
		return
	}
	if match := channelPath.FindStringSubmatch(request.URL.Path); match != nil {
		if got := request.Header.Get("Authorization"); got != "Bearer fixture-token" {
			r.t.Errorf("channel socket authorization = %q", got)
		}
		if r.options.RejectChannels {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		conn := r.upgrade(w, request)
		if conn == nil {
			return
		}
		r.mu.Lock()
		r.channels[match[1]] = conn
		waiter := r.channelWaiters[match[1]]
		r.mu.Unlock()
		if waiter != nil {
			waiter <- conn
		}
		return
	}
	conn := r.upgrade(w, request)
	if conn == nil {
		return
	}
	r.mu.Lock()
	r.host = conn
	r.mu.Unlock()
	r.readyOnce.Do(func() { close(r.hostReady) })
	go r.readHost(conn)
}

func (r *Relay) readHost(conn *websocket.Conn) {
	for {
		r.mu.Lock()
		for r.paused {
			r.cond.Wait()
		}
		r.mu.Unlock()
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		r.mu.Lock()
		if kind == websocket.TextMessage {
			var control map[string]any
			_ = json.Unmarshal(data, &control)
			r.controls = append(r.controls, control)
			if control["type"] == "connection_close" {
				if id, _ := control["connection_id"].(string); r.closeWaiters[id] != nil {
					close(r.closeWaiters[id])
					delete(r.closeWaiters, id)
				}
			}
		} else if len(data) >= 18 {
			raw := hex.EncodeToString(data[2:18])
			r.frames = append(r.frames, Frame{
				ID:      raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:],
				Type:    data[1],
				Payload: data[18:],
			})
		}
		r.mu.Unlock()
	}
}

func NewID() string {
	random := make([]byte, 16)
	_, _ = rand.Read(random)
	random[6] = random[6]&0x0f | 0x40
	random[8] = random[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", random[0:4], random[4:6], random[6:8], random[8:10], random[10:])
}

func sendableCode(code int) int {
	switch code {
	case 1005:
		return 1000
	case 1006:
		return 1011
	}
	return code
}

// connectClient is a Pi client: a channel the brain opens its own socket for, then piped to the client as Radius does.
func (r *Relay) connectClient(w http.ResponseWriter, request *http.Request) {
	id := NewID()
	principal := Owner
	if request.Header.Get("Authorization") != "Bearer fixture-token" {
		principal.ID = "usr_2"
	}
	refused := make(chan struct{})
	waiter := make(chan *websocket.Conn, 1)
	r.mu.Lock()
	r.closeWaiters[id] = refused
	r.channelWaiters[id] = waiter
	existing := r.channels[id]
	r.mu.Unlock()
	r.Send(map[string]any{"version": 2, "type": "connection_open", "connection_id": id, "kind": "ws", "principal": principal})
	var channel *websocket.Conn
	if existing != nil {
		channel = existing
	} else {
		select {
		case channel = <-waiter:
		case <-refused:
		case <-time.After(30 * time.Second):
		}
	}
	if channel == nil {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	client := r.upgrade(w, request)
	if client == nil {
		return
	}
	var send sync.Mutex
	pipe := func(from, to *websocket.Conn) {
		for {
			kind, data, err := from.ReadMessage()
			if err != nil {
				code := websocket.CloseAbnormalClosure
				if closed, ok := err.(*websocket.CloseError); ok {
					code = closed.Code
				}
				send.Lock()
				_ = to.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(sendableCode(code), ""), time.Now().Add(5*time.Second))
				send.Unlock()
				return
			}
			send.Lock()
			err = to.WriteMessage(kind, data)
			send.Unlock()
			if err != nil {
				return
			}
		}
	}
	go pipe(client, channel)
	go pipe(channel, client)
}

// Ready is closed once the brain's host socket is connected.
func (r *Relay) Ready() <-chan struct{} { return r.hostReady }

// Shares is every set of emails the brain reported, in order.
func (r *Relay) Shares() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.shares...)
}

// Controls is every control message the brain sent, in order.
func (r *Relay) Controls() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.controls...)
}

// Frames is every binary frame the brain sent, in order.
func (r *Relay) Frames() []Frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Frame(nil), r.frames...)
}

// Closed is whether the brain sent `connection_close` for id (with code, when code is not 0).
func (r *Relay) Closed(id string, code int) bool {
	for _, control := range r.Controls() {
		if control["type"] == "connection_close" && control["connection_id"] == id {
			if value, has := control["code"].(float64); code == 0 || (has && int(value) == code) {
				return true
			}
		}
	}
	return false
}

// Send sends a control message to the brain.
func (r *Relay) Send(message any) {
	encoded, err := json.Marshal(message)
	if err != nil {
		r.t.Fatal(err)
	}
	r.write(websocket.TextMessage, encoded)
}

func (r *Relay) write(kind int, data []byte) {
	r.mu.Lock()
	host := r.host
	r.mu.Unlock()
	if host == nil {
		return
	}
	r.hostWrite.Lock()
	defer r.hostWrite.Unlock()
	_ = host.WriteMessage(kind, data)
}

// SendData sends a frame of channel id: binary 1, text 2, end 3.
func (r *Relay) SendData(id string, payload []byte, kind byte) {
	frame := make([]byte, 18+len(payload))
	frame[0], frame[1] = 2, kind
	raw, _ := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	copy(frame[2:], raw)
	copy(frame[18:], payload)
	r.write(websocket.BinaryMessage, frame)
}

// Channel is the relay's end of a channel's own socket (v5), once the brain connected it.
func (r *Relay) Channel(id string) *websocket.Conn {
	r.mu.Lock()
	if conn := r.channels[id]; conn != nil {
		r.mu.Unlock()
		return conn
	}
	waiter := make(chan *websocket.Conn, 1)
	r.channelWaiters[id] = waiter
	r.mu.Unlock()
	select {
	case conn := <-waiter:
		return conn
	case <-time.After(20 * time.Second):
		r.t.Fatalf("channel %s: no socket", id)
		return nil
	}
}

// Reading stops or resumes reading the host socket.
func (r *Relay) Reading(on bool) {
	r.mu.Lock()
	r.paused = !on
	r.mu.Unlock()
	r.cond.Broadcast()
}

// Wait polls until check holds.
func Wait(t testing.TB, what string, timeout time.Duration, check func() bool) {
	t.Helper()
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Response is the `response` message of channel id.
func (r *Relay) Response(id string) map[string]any {
	r.t.Helper()
	var found map[string]any
	Wait(r.t, "the response of "+id, 20*time.Second, func() bool {
		for _, control := range r.Controls() {
			if control["type"] == "response" && control["connection_id"] == id {
				found = control
				return true
			}
		}
		return false
	})
	return found
}

// Header is the first header of a response named name, case-insensitively.
func Header(response map[string]any, name string) (string, bool) {
	pairs, _ := response["headers"].([]any)
	for _, each := range pairs {
		if pair, _ := each.([]any); len(pair) == 2 && strings.EqualFold(fmt.Sprint(pair[0]), name) {
			return fmt.Sprint(pair[1]), true
		}
	}
	return "", false
}

// Body is every payload of channel id once its end frame is there.
func (r *Relay) Body(id string) []byte {
	r.t.Helper()
	var body []byte
	Wait(r.t, "the body of "+id, 40*time.Second, func() bool {
		var out []byte
		ended := false
		for _, frame := range r.Frames() {
			if frame.ID == id {
				out = append(out, frame.Payload...)
				ended = ended || frame.Type == 3
			}
		}
		body = out
		return ended
	})
	return body
}

// Received is how many payload bytes of channel id arrived.
func (r *Relay) Received(id string) int {
	total := 0
	for _, frame := range r.Frames() {
		if frame.ID == id {
			total += len(frame.Payload)
		}
	}
	return total
}
