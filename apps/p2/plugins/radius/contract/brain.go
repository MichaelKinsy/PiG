package contract

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/gorilla/websocket"
)

// Ports apps/p2/plugins/radius/brain.ts.
//
// p2 as a Radius brain
// ====================
//
// The brain keeps one outbound WebSocket to its relay in Radius (relay protocol v4,
// `radius/packages/gateway/src/servers/protocol.ts`). Radius multiplexes every browser connection over it as a channel and
// tells the brain who opened it. Each channel becomes a loopback request to this server, marked with the relay secret so the
// server treats it as the owner on the brain's cortex origin (see relay_access.go). No port is exposed and nothing listens
// publicly.
//
// The brain's Radius identity is separate from model providers: `radius.json` in the data directory holds the gateway, a
// stable server ID, and an OAuth credential from `p2 radius-login`.
//
// Who may open a channel. The owner reaches everything the owner reaches locally. Radius also admits people, in any
// organization, to the brain's UI when the brain shared a session with their email (ReportShares): Radius verifies the
// email, and the brain passes such a user on as a Radius user (not an owner), whom p2's sharing maps to the sessions shared
// with that email now. A user without an email, service accounts, and everyone but the owner on forward origins are refused.
// Browser cookies never reach the server through the brain's UI channels.
//
// Shares. Radius admits and lists a brain for the emails the brain reports with `PUT /v1/servers/<id>/shares`, which
// replaces the whole set. The brain reports whenever the set changes and after every connect, so a report lost while offline
// is repaired by the next connection. Radius learns nothing else: not which sessions, nor their titles.
//
// Backpressure. Nothing may buffer without bound between a loopback source and a slow browser; the relay itself cannot see a
// slow WebSocket reader.
//
//   - HTTP channels use the relay's credit flow control (`pi-server.host.v3`): response bodies stop at the window until the
//     relay returns credit as the browser reads, and request bodies earn the relay credit as the loopback request takes them.
//   - WebSocket channels rely on the application acknowledging end to end (the terminal does; chord links do too).
//   - Whatever the case, while more than congestionHighBytes are unsent on the socket to Radius, every loopback source on it
//     is paused, so a slow uplink slows the server down instead of growing this process's memory.
//
// Channel sockets (relay protocol v5). One socket to Radius is one TCP stream: a 500 KiB transcript snapshot in one tab
// delays the echo of a terminal in another by the time the snapshot takes to cross the uplink, and the pause above stops
// every channel at once. A v5 relay therefore has the brain open one socket per WebSocket channel
// (`/v1/servers/<id>/channels/<connection>`, authenticated like the host socket), and the brain pipes it to the loopback
// socket unchanged. Each direction pauses its source while its destination has more than pipeHighBytes unsent, so a slow
// reader slows only its own channel. HTTP channels stay on the host socket with credit flow control.
//
// Port forwards (relay protocol v4). A channel whose `connection_open` names a `forward` comes from that forward's own
// origin (`fwd--<label>--<forward>.rdtun.com`), not from the brain's UI. It goes to `localhost:<port>` of the forward, never
// to this server: it carries no relay secret, `Host` is the target's own, and `Origin` and redirects to `localhost` are
// translated between the two origins. A forward ID this server does not know answers 404 (HTTP) or closes the channel.
// `stream` channels, end-to-end forwards whose TLS terminates in the environment, are not supported yet and are closed at once.
//
// Reconnecting. A network loss rarely closes the socket: packets just stop, and TCP may take many minutes to notice (after a
// laptop sleeps, the socket can look open indefinitely). The brain therefore watches for anything from the relay. After
// heartbeatIdle of silence it sends a `ping`, which the relay answers with `pong`; after heartbeatTimeout of silence it
// terminates the socket and reconnects with backoff. The handshake has its own timeout, so a connection attempt into a dead
// network fails instead of hanging. The relay keeps a lost host until its own heartbeat expires (90 s by default) and refuses
// a second one meanwhile with 409. A brain that noticed the loss first would stay offline until then, up to 90 s, while
// browsers opened channels into the dead socket and waited for them to time out. The brain therefore connects with
// `?replace=1`: the relay fails the old host and its channels and takes the new socket at once. Relays that predate the
// parameter ignore it and answer 409, which the backoff retries until the old host expires.
//
// Two p2 processes with the same `radius.json` share one server ID, so they would replace each other in turn. The relay
// closes a replaced host with 1012 "Server host replaced", and that is always another process: this one gives up its own
// socket before it reconnects. The brain then yields: it reconnects without `replace`, gets 409 until the other process is
// gone, and replaces again once it is connected. So the most recently started process wins.
//
// Public forwards. Radius opens channels from a forward's public origin (`pub--<label>--<forward>`) without a principal:
// nobody signed in. Such a channel reaches only a forward the owner marked public; for any other forward, and for the
// brain's own UI, it is refused like an unknown forward.
//
// Client channels. A Pi client of the owner, the TUI launcher with `--radius`, connects to Radius with its own bearer
// credential (`pi-server.client.v2`). Radius then opens a `ws` channel with the principal but without a request or public
// origin: there is no browser request to describe. The client's first message is its request instead, JSON
// `{ type: "http" | "websocket", method, path, headers }`, and the brain makes it a loopback request marked like the owner's
// browser requests, except without a shell origin: no page frames a client. HTTP request bodies follow as binary messages
// and a text end marker. The brain answers with one text message `{ status, headers, origin }` (`origin`: what the server
// sees as this client's origin), then for HTTP the response body as binary messages and a close with 1000; for `websocket`,
// status 101 turns the channel into the piped loopback socket. Only relays with channel sockets (v5) carry them.
//
// Go differences. Node's `ws` and `http` become gorilla/websocket and net/http; the loopback client reads each response's raw
// head (headCapture) because Go's client folds header names into canonical case, so the names, their order and the
// transfer coding go to the relay as the server wrote them, as Pi's `rawHeaders` do. A message that makes Pi's handler throw
// (see validRequest) terminates the socket and reconnects, as in Pi.

const (
	// hostSubprotocols are offered newest first; v3 adds credit flow control for HTTP channels, v4 port forwards, v5 channel
	// sockets.
	channelHostSubprotocol = "pi-server.host.v5"
	channelSubprotocol     = "pi-server.channel.v1"
	// maxMessageBytes is the largest message a socket carries.
	maxMessageBytes = 16 * 1024 * 1024
	// defaultHTTPWindow is the relay's default window, for relays that do not state it in `connection_open`.
	defaultHTTPWindow        = 256 * 1024
	congestionHighBytes      = 1024 * 1024
	congestionLowBytes       = 256 * 1024
	protocolVersion          = 2
	dataHeaderBytes          = 18
	frameBinary         byte = 1
	frameText           byte = 2
	frameEnd            byte = 3
	bodyChunkBytes           = 256 * 1024
	retryInitial             = time.Second
	retryMax                 = 30 * time.Second
	// A client channel's origin while the brain's Radius origin is unknown (nobody opened it in a browser yet).
	clientOrigin = "https://radius.invalid"
	// The relay's close of a host socket another brain with this server ID replaced (relay protocol).
	replacedCode   = 1012
	replacedReason = "Server host replaced"
	// Silence from the relay after which the brain pings it; the relay pings an idle host itself only after 30 s.
	heartbeatIdle = 20 * time.Second
	// Silence from the relay after which the connection counts as lost and is replaced.
	heartbeatTimeout = 60 * time.Second
	handshakeTimeout = 30 * time.Second
)

var hostSubprotocols = []string{"pi-server.host.v5", "pi-server.host.v4", "pi-server.host.v3", "pi-server.host.v2"}

func flowControlSubprotocol(protocol string) bool {
	return protocol == "pi-server.host.v5" || protocol == "pi-server.host.v4" || protocol == "pi-server.host.v3"
}

// droppedHeaders are the request headers the brain sets itself on loopback requests.
var droppedHeaders = map[string]bool{
	"host": true, "connection": true, "upgrade": true, "content-length": true, "transfer-encoding": true,
	"x-radius-shell-origin": true,
	RelaySecretHeader:       true, RelayOriginHeader: true, RelayShellHeader: true, RelayUserHeader: true,
}

// StatusKind is where the brain's relay connection stands.
type StatusKind string

const (
	// StatusSignedOut: no radius.json, so this p2 is local only. Checked again periodically, so a later login connects.
	StatusSignedOut  StatusKind = "signed_out"
	StatusConnecting StatusKind = "connecting"
	StatusConnected  StatusKind = "connected"
	StatusRetrying   StatusKind = "retrying"
)

// Status is the brain's connection status.
type Status struct {
	Kind           StatusKind
	ServerID       string // connected
	OrganizationID string // connected
	Error          string // retrying
}

// Timing overrides the heartbeat and handshake times (tests).
type Timing struct{ HeartbeatIdle, HeartbeatTimeout, HandshakeTimeout time.Duration }

// ForwardTarget is a port forward as the brain relays to it.
type ForwardTarget struct {
	Port   int
	Public bool
}

// Forwards are the port forwards the brain relays to, and the brain's Radius origin they are addressed by.
type Forwards interface {
	Lookup(id string) (ForwardTarget, bool)
	// RadiusOrigin is the brain's Radius origin; "" while unknown.
	RadiusOrigin() string
	SetRadiusOrigin(origin string)
}

// BrainOptions is what NewBrain takes.
type BrainOptions struct {
	Directory string
	Port      func() int
	Relay     *RelayAccess
	// Forwards are the port forwards; without them, forward channels answer as unknown forwards.
	Forwards Forwards
	OnStatus func(Status)
	Timing   Timing
}

// principal is who opened a channel; Email (normalized, verified) is set for people other than the owner.
type principal struct {
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Email          string `json:"email"`
}

type requestHead struct {
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Headers [][2]string `json:"headers"`
}

type control struct {
	Type         string       `json:"type"`
	ConnectionID string       `json:"connection_id"`
	Kind         string       `json:"kind"`
	Principal    *principal   `json:"principal"`
	PublicOrigin string       `json:"public_origin"`
	Request      *requestHead `json:"request"`
	Window       *float64     `json:"window"`
	Forward      *string      `json:"forward"`
	Bytes        float64      `json:"bytes"`
}

// target is where a channel's loopback connection goes.
type target struct {
	host string
	port int
	// location rewrites a response's `Location`; forwards map `http://localhost:<port>` to their public origin.
	location func(value string) string
	// unavailable is what forwards answer with instead of closing when nothing listens on the port.
	unavailable string
}

// channel is the brain's end of one multiplexed connection. Its methods run on the host socket's reading goroutine and
// never block it.
type channel struct {
	data func(payload []byte, kind byte)
	// close ends the channel because the relay did, or the brain is closing.
	close func()
	// credit is the relay's credit for response body bytes (HTTP channels with flow control).
	credit func(bytes int64)
	// congested stops or restarts reading the loopback source while the socket to Radius is congested.
	congested func(paused bool)
}

// Brain maintains the brain's relay connection until closed.
type Brain struct {
	directory string
	port      func() int
	relay     *RelayAccess
	forwards  Forwards
	onStatus  func(Status)

	heartbeatIdle, heartbeatTimeout, handshakeTimeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	http   *http.Transport

	mu       sync.Mutex
	session  *session
	config   *Config
	retry    time.Duration
	replace  bool
	closed   bool
	shell    string
	shares   []string
	haveShar bool
	reported bool

	refreshMu sync.Mutex
	kick      chan struct{}
}

// NewBrain makes a brain; Start connects it.
func NewBrain(options BrainOptions) *Brain {
	ctx, cancel := context.WithCancel(context.Background())
	pick := func(value, fallback time.Duration) time.Duration {
		if value > 0 {
			return value
		}
		return fallback
	}
	onStatus := options.OnStatus
	if onStatus == nil {
		onStatus = func(Status) {}
	}
	return &Brain{
		directory: options.Directory, port: options.Port, relay: options.Relay, forwards: options.Forwards, onStatus: onStatus,
		heartbeatIdle:    pick(options.Timing.HeartbeatIdle, heartbeatIdle),
		heartbeatTimeout: pick(options.Timing.HeartbeatTimeout, heartbeatTimeout),
		handshakeTimeout: pick(options.Timing.HandshakeTimeout, handshakeTimeout),
		ctx:              ctx, cancel: cancel,
		http:    newLoopbackTransport(),
		retry:   retryInitial,
		replace: true,
		kick:    make(chan struct{}, 1),
	}
}

func (b *Brain) spawn(run func()) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		run()
	}()
}

// Start connects to the relay, and reconnects until Close.
func (b *Brain) Start() {
	b.spawn(b.run)
	b.spawn(b.reporter)
}

// ReportShares reports the emails sessions are shared with now if connected, else after the next connect (see "Shares").
func (b *Brain) ReportShares(emails []string) {
	b.mu.Lock()
	b.shares, b.haveShar, b.reported = append([]string{}, emails...), true, false
	b.mu.Unlock()
	b.wake()
}

func (b *Brain) wake() {
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

// ShellPage is this brain's page in the Radius shell, once known (see Config.ShellPage); "" before.
func (b *Brain) ShellPage() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.shell
}

// Close stops the brain: its sockets and channels, and waits for what it started.
func (b *Brain) Close() {
	b.mu.Lock()
	b.closed = true
	current := b.session
	b.mu.Unlock()
	b.cancel()
	if current != nil {
		current.ws.closeWith(1000, "")
		current.closeChannels()
	}
	b.wg.Wait()
	b.http.CloseIdleConnections()
}

func (b *Brain) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// sleep waits for d, false if the brain closed first.
func (b *Brain) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-b.ctx.Done():
		return false
	}
}

func (b *Brain) run() {
	for !b.isClosed() {
		if !b.sleep(b.attempt()) {
			return
		}
	}
}

// attempt connects once and serves the socket until it ends; it returns how long to wait before the next attempt.
func (b *Brain) attempt() time.Duration {
	current, err := ReadConfig(b.directory)
	if err != nil {
		return b.retryAfter(err)
	}
	if current == nil {
		b.onStatus(Status{Kind: StatusSignedOut})
		return retryMax
	}
	b.onStatus(Status{Kind: StatusConnecting})
	config, err := b.freshConfig()
	if err != nil {
		return b.retryAfter(err)
	}
	owner, err := b.owner(config)
	if err != nil {
		return b.retryAfter(err)
	}
	if b.forwards != nil && b.forwards.RadiusOrigin() == "" && config.PublicOrigin != "" {
		b.forwards.SetRadiusOrigin(config.PublicOrigin)
	}
	b.mu.Lock()
	if b.shell == "" {
		b.shell = config.ShellPage
	}
	replace := b.replace
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return 0
	}
	b.mu.Lock()
	b.config = config
	b.mu.Unlock()
	failure, ok := b.connect(config, owner, replace)
	if !ok {
		return 0
	}
	return b.retryAfter(failure)
}

// retryAfter reports the failure and returns the backoff before the next attempt.
func (b *Brain) retryAfter(failure error) time.Duration {
	if b.isClosed() {
		return 0
	}
	b.onStatus(Status{Kind: StatusRetrying, Error: failure.Error()})
	b.mu.Lock()
	defer b.mu.Unlock()
	delay := b.retry
	b.retry = min(b.retry*2, retryMax)
	return delay
}

// freshConfig reads radius.json again for every attempt and refreshes the access token when it is about to expire.
func (b *Brain) freshConfig() (*Config, error) {
	config, err := ReadConfig(b.directory)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, errors.New("Not signed in to Radius; run p2 radius-login")
	}
	if !expiresSoon(config.Credential) {
		return config, nil
	}
	ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
	defer cancel()
	credential, err := radiusOAuth(config.Gateway).RefreshTokenContext(ctx, config.Credential)
	if err != nil {
		return nil, err
	}
	// Read again: the public origin may have been saved while the refresh ran.
	refreshed, err := ReadConfig(b.directory)
	if err != nil {
		return nil, err
	}
	if refreshed == nil {
		refreshed = config
	}
	next := *refreshed
	next.Credential = credential
	if err := WriteConfig(b.directory, next); err != nil {
		return nil, err
	}
	return &next, nil
}

// currentConfig is the current configuration, its credential refreshed when about to expire; one refresh at a time.
func (b *Brain) currentConfig() (*Config, error) {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	b.mu.Lock()
	config := b.config
	b.mu.Unlock()
	if config != nil && !expiresSoon(config.Credential) {
		return config, nil
	}
	fresh, err := b.freshConfig()
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.config = fresh
	b.mu.Unlock()
	return fresh, nil
}

// owner is the principal the credential authenticates: the only principal this brain serves.
func (b *Brain) owner(config *Config) (principal, error) {
	ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(config.Gateway, "/")+"/v1/context", nil)
	if err != nil {
		return principal{}, err
	}
	request.Header.Set("authorization", "Bearer "+config.Credential.Access)
	request.Header.Set("accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return principal{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return principal{}, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return principal{}, fmt.Errorf("Radius context: %d %s", response.StatusCode, truncate(string(body), 200))
	}
	var context struct {
		Actor struct {
			PrincipalID   string `json:"principal_id"`
			PrincipalKind string `json:"principal_kind"`
		} `json:"actor"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	if err := json.Unmarshal(body, &context); err != nil {
		return principal{}, err
	}
	return principal{Kind: context.Actor.PrincipalKind, ID: context.Actor.PrincipalID, OrganizationID: context.Organization.ID}, nil
}

// truncate is `text.slice(0, n)` for the ASCII-or-not text of a response: n UTF-16 units, here n runes.
func truncate(text string, n int) string {
	if runes := []rune(text); len(runes) > n {
		return string(runes[:n])
	}
	return text
}

func websocketURL(gateway, path string) string {
	base, err := url.Parse(gateway)
	if err != nil {
		return gateway + path
	}
	scheme := "ws"
	if base.Scheme == "https" {
		scheme = "wss"
	}
	return scheme + "://" + base.Host + path
}

func (b *Brain) dialer(subprotocols []string) *websocket.Dialer {
	return &websocket.Dialer{HandshakeTimeout: b.handshakeTimeout, Subprotocols: subprotocols}
}

// dialFailure is why a handshake failed: the relay's status and body, else the dial's own error.
func dialFailure(err error, response *http.Response) error {
	if response == nil {
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	_ = response.Body.Close()
	return fmt.Errorf("%d: %s", response.StatusCode, truncate(string(body), 200))
}

// connect opens the host socket and serves it until it ends. ok is false when the brain closed meanwhile; failure is why the
// socket ended.
func (b *Brain) connect(config *Config, owner principal, replace bool) (failure error, ok bool) {
	address := websocketURL(config.Gateway, "/v1/servers/"+config.ServerID+"/connect")
	if replace {
		address += "?replace=1"
	}
	conn, response, err := b.dialer(hostSubprotocols).DialContext(b.ctx, address, http.Header{"Authorization": {"Bearer " + config.Credential.Access}})
	if err != nil {
		if b.isClosed() {
			return nil, false
		}
		return dialFailure(err, response), true
	}
	conn.SetReadLimit(maxMessageBytes + dataHeaderBytes)
	s := &session{brain: b, ws: newWsConn(conn), channels: map[string]*channel{}, serverID: config.ServerID}
	protocol := conn.Subprotocol()
	s.flow = flowControlSubprotocol(protocol)
	s.channelSockets = protocol == channelHostSubprotocol
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		s.ws.terminate()
		return nil, false
	}
	b.session = s
	b.retry = retryInitial
	b.replace = true
	b.mu.Unlock()
	var failed atomic.Pointer[error]
	fail := func(err error) {
		failed.CompareAndSwap(nil, &err)
	}
	// Wall-clock time on purpose: after a sleep the elapsed time includes it, so a stale socket is replaced at once.
	var lastReceived, lastPing atomic.Int64
	lastReceived.Store(time.Now().UnixMilli())
	stopBeat := make(chan struct{})
	b.spawn(func() {
		tick := max(10*time.Millisecond, min(b.heartbeatIdle, b.heartbeatTimeout)/4)
		timer := time.NewTicker(tick)
		defer timer.Stop()
		for {
			select {
			case <-stopBeat:
				return
			case <-timer.C:
			}
			now := time.Now().UnixMilli()
			// A backward clock step (NTP, manual change) would otherwise stop pings and the timeout until it caught up.
			if now < max(lastReceived.Load(), lastPing.Load()) {
				lastReceived.Store(now)
				lastPing.Store(now)
			}
			quiet := now - lastReceived.Load()
			if quiet >= b.heartbeatTimeout.Milliseconds() {
				fail(fmt.Errorf("no message from Radius for %d s", int64(math.Round(float64(quiet)/1000))))
				s.ws.terminate()
				return
			} else if now-max(lastReceived.Load(), lastPing.Load()) >= b.heartbeatIdle.Milliseconds() {
				lastPing.Store(now)
				s.sendControl(map[string]any{"version": protocolVersion, "type": "ping"})
			}
		}
	})
	b.onStatus(Status{Kind: StatusConnected, ServerID: config.ServerID, OrganizationID: owner.OrganizationID})
	b.mu.Lock()
	b.reported = false
	b.mu.Unlock()
	b.wake()
	closeCode, closeReason := 0, ""
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			closeCode, closeReason = closeInfo(err)
			var closed *websocket.CloseError
			if !errors.As(err, &closed) {
				fail(err)
			}
			break
		}
		lastReceived.Store(time.Now().UnixMilli())
		if kind == websocket.BinaryMessage {
			s.onData(data)
			continue
		}
		var message control
		// `null` parses in Node and fails on its first property.
		if err := json.Unmarshal(data, &message); err != nil || string(bytes.TrimSpace(data)) == "null" {
			fail(errInvalidRelayMessage)
			s.ws.terminate()
			continue
		}
		if err := b.onControl(s, owner, message); err != nil {
			fail(err)
			s.ws.terminate()
		}
	}
	close(stopBeat)
	_ = conn.Close()
	s.ws.terminate()
	b.mu.Lock()
	if b.session == s {
		b.session = nil
	}
	b.mu.Unlock()
	s.closeChannels()
	if failed.Load() == nil && closeCode == replacedCode && closeReason == replacedReason {
		b.mu.Lock()
		b.replace = false
		b.mu.Unlock()
		fail(errors.New("another p2 with this server ID took over; waiting for it to disconnect"))
	}
	if pointer := failed.Load(); pointer != nil {
		return *pointer, true
	}
	text := "relay closed (" + strconv.Itoa(closeCode)
	if closeReason != "" {
		text += " " + closeReason
	}
	return errors.New(text + ")"), true
}

// session is one host socket and the channels on it.
type session struct {
	brain    *Brain
	ws       *wsConn
	serverID string
	// flow is whether the relay accepted `pi-server.host.v3` or later; channelSockets whether it accepted v5.
	flow, channelSockets bool

	mu        sync.Mutex
	channels  map[string]*channel
	congested bool
}

func (s *session) get(id string) *channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channels[id]
}

func (s *session) has(id string) bool { return s.get(id) != nil }

func (s *session) put(id string, c *channel) {
	s.mu.Lock()
	s.channels[id] = c
	s.mu.Unlock()
}

// remove forgets a channel; whether it was there.
func (s *session) remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.channels[id]
	delete(s.channels, id)
	return ok
}

func (s *session) snapshot() []*channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := make([]*channel, 0, len(s.channels))
	for _, c := range s.channels {
		all = append(all, c)
	}
	return all
}

// closeChannels closes every channel and forgets them.
func (s *session) closeChannels() {
	s.mu.Lock()
	all := make([]*channel, 0, len(s.channels))
	for _, c := range s.channels {
		all = append(all, c)
	}
	s.channels = map[string]*channel{}
	s.mu.Unlock()
	for _, c := range all {
		c.close()
	}
}

func (s *session) sendControl(message any) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return
	}
	s.ws.send(websocket.TextMessage, encoded)
}

func (s *session) closeChannel(id string, code int) {
	message := map[string]any{"version": protocolVersion, "type": "connection_close", "connection_id": id}
	if code != 0 {
		message["code"] = code
	}
	s.sendControl(message)
}

func (s *session) sendData(id string, payload []byte, kind byte) {
	frame := make([]byte, dataHeaderBytes+len(payload))
	frame[0], frame[1] = protocolVersion, kind
	if raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", "")); err == nil {
		copy(frame[2:dataHeaderBytes], raw)
	}
	copy(frame[dataHeaderBytes:], payload)
	if !s.ws.send(websocket.BinaryMessage, frame) {
		return
	}
	s.mu.Lock()
	start := !s.congested && s.ws.buffered() > congestionHighBytes
	if start {
		s.congested = true
	}
	s.mu.Unlock()
	if start {
		s.congest()
	}
}

// congest pauses every loopback source on the host socket until it has drained below congestionLowBytes.
func (s *session) congest() {
	s.brain.spawn(func() {
		// Off the sender's goroutine: a channel that is flushing holds its own lock, which pausing it takes.
		for _, c := range s.snapshot() {
			c.congested(true)
		}
		timer := time.NewTicker(pollInterval)
		defer timer.Stop()
		for {
			select {
			case <-s.brain.ctx.Done():
				return
			case <-timer.C:
			}
			if s.ws.open() && s.ws.buffered() > congestionLowBytes {
				continue
			}
			s.mu.Lock()
			s.congested = false
			s.mu.Unlock()
			for _, c := range s.snapshot() {
				c.congested(false)
			}
			return
		}
	})
}

// respond is a short plain-text response the brain makes up itself; well within any flow-control window.
func (s *session) respond(id string, status int, text string) {
	s.sendControl(map[string]any{
		"version": protocolVersion, "type": "response", "connection_id": id, "status": status,
		"headers": [][2]string{{"content-type", "text/plain; charset=utf-8"}},
	})
	s.sendData(id, []byte(text), frameBinary)
	s.sendData(id, nil, frameEnd)
}

func (s *session) onData(frame []byte) {
	if len(frame) < dataHeaderBytes || frame[0] != protocolVersion {
		return
	}
	raw := hex.EncodeToString(frame[2:dataHeaderBytes])
	id := raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
	kind := frameBinary
	switch frame[1] {
	case frameText:
		kind = frameText
	case frameEnd:
		kind = frameEnd
	}
	if c := s.get(id); c != nil {
		c.data(frame[dataHeaderBytes:], kind)
	}
}

// errInvalidRelayMessage is what Pi's try/catch around its message handler records before it terminates the socket: a message
// that makes the handler throw (a request without headers, a header or path Node's http refuses, an origin `new URL` refuses).
var errInvalidRelayMessage = errors.New("Invalid Radius relay message")

func (b *Brain) onControl(s *session, owner principal, message control) error {
	switch message.Type {
	case "ping":
		s.sendControl(map[string]any{"version": protocolVersion, "type": "pong"})
		return nil
	case "pong":
		return nil
	case "connection_close":
		// The entry goes first, so the channel's own end does not tell the relay what it just said.
		if c := s.get(message.ConnectionID); c != nil {
			s.remove(message.ConnectionID)
			c.close()
		}
		return nil
	case "credit":
		if c := s.get(message.ConnectionID); c != nil {
			c.credit(int64(message.Bytes))
		}
		return nil
	}
	id := message.ConnectionID
	who := message.Principal
	isOwner := who != nil && who.Kind == owner.Kind && who.ID == owner.ID && who.OrganizationID == owner.OrganizationID
	if isOwner && message.Kind == "ws" && message.Request == nil && message.Forward == nil {
		b.openClientChannel(s, id)
		return nil
	}
	// Someone a session is shared with, on the brain's UI only: sharing decides what they reach.
	member := !isOwner && who != nil && who.Kind == "user" && who.Email != "" && message.Forward == nil
	// Only a public forward's origin connects without a principal; openForward checks the forward is public.
	anonymous := who == nil && message.Forward != nil
	if message.Kind == "stream" || !(isOwner || member || anonymous) || message.Request == nil || message.PublicOrigin == "" {
		s.closeChannel(id, 1008)
		return nil
	}
	head := *message.Request
	if head.Headers == nil {
		return errInvalidRelayMessage
	}
	window := int64(math.MaxInt64)
	if s.flow {
		window = defaultHTTPWindow
		if message.Window != nil {
			window = int64(*message.Window)
		}
	}
	headers := map[string]string{}
	shellOrigin := ""
	for _, pair := range head.Headers {
		name := strings.ToLower(pair[0])
		if name == "x-radius-shell-origin" {
			shellOrigin = pair[1]
		}
		if !droppedHeaders[name] {
			headers[name] = pair[1]
		}
	}
	if message.Forward != nil {
		if err := b.openForward(s, id, message.Kind, *message.Forward, head, headers, message.PublicOrigin, window, anonymous); err != nil {
			return err
		}
	} else {
		b.remember(message.PublicOrigin, shellOrigin, owner)
		// The brain's UI authenticates by Radius alone: no browser cookie may make a member the owner.
		delete(headers, "cookie")
		headers[RelaySecretHeader] = b.relay.Secret()
		headers[RelayOriginHeader] = message.PublicOrigin
		headers[RelayShellHeader] = shellOrigin
		if member && who.Email != "" {
			headers[RelayUserHeader] = EncodeRelayUser(p2runtime.RelayUser{ID: who.ID, Email: who.Email})
		}
		to := target{host: "127.0.0.1", port: b.port()}
		if err := validRequest(message.Kind, head, headers); err != nil {
			return err
		}
		if message.Kind == "http" {
			b.openHTTP(s, id, head, headers, window, to)
		} else {
			b.openSocket(s, id, head, headers, to)
		}
	}
	// A channel opened while congested starts paused.
	s.mu.Lock()
	congested := s.congested
	s.mu.Unlock()
	if congested {
		if c := s.get(id); c != nil {
			c.congested(true)
		}
	}
	return nil
}

// openForward is a channel from a forward's origin, relayed to the forward's port as if the browser had opened
// `http://localhost:<port>`: dev servers check `Host` and `Origin` against their own address. The `X-Forwarded-*` headers
// tell frameworks the public address, and make this server refuse the request should a forward ever point at it.
func (b *Brain) openForward(s *session, id, kind, forwardID string, head requestHead, headers map[string]string, publicOrigin string, window int64, anonymous bool) error {
	var forward ForwardTarget
	found := false
	if b.forwards != nil {
		forward, found = b.forwards.Lookup(forwardID)
	}
	// An anonymous visitor learns nothing about forwards that are not public: they look unknown.
	if !found || forward.Port == b.port() || (anonymous && !forward.Public) {
		if kind == "http" {
			s.respond(id, 404, "This port forward does not exist (any more).\n")
		} else {
			s.closeChannel(id, 1008)
		}
		return nil
	}
	local := "localhost:" + strconv.Itoa(forward.Port)
	parsed, err := url.Parse(publicOrigin)
	if err != nil {
		return errInvalidRelayMessage
	}
	delete(headers, "x-radius-shell-origin")
	if headers["origin"] == publicOrigin {
		headers["origin"] = "http://" + local
	}
	headers["x-forwarded-host"] = parsed.Host
	headers["x-forwarded-proto"] = parsed.Scheme
	prefixes := []string{"http://" + local, "http://127.0.0.1:" + strconv.Itoa(forward.Port)}
	to := target{
		host: "localhost",
		port: forward.Port,
		location: func(value string) string {
			for _, prefix := range prefixes {
				if value == prefix || strings.HasPrefix(value, prefix+"/") || strings.HasPrefix(value, prefix+"?") {
					return publicOrigin + value[len(prefix):]
				}
			}
			return value
		},
		unavailable: "Nothing is listening on port " + strconv.Itoa(forward.Port) + " of this device.\n",
	}
	if err := validRequest(kind, head, headers); err != nil {
		return err
	}
	if kind == "http" {
		b.openHTTP(s, id, head, headers, window, to)
	} else {
		b.openSocket(s, id, head, headers, to)
	}
	return nil
}

// validRequest is what Node's `http.request` checks synchronously (and throws for), which Pi's message handler turns into a
// terminated socket: the method and the path of an HTTP request, and every header name and value (a WebSocket's too).
func validRequest(kind string, head requestHead, headers map[string]string) error {
	if kind == "http" {
		if head.Method != "" && !validToken(head.Method) {
			return errInvalidRelayMessage
		}
		for _, r := range head.Path {
			if r < 0x21 || r > 0xff {
				return errInvalidRelayMessage
			}
		}
	}
	for name, value := range headers {
		if !validToken(name) {
			return errInvalidRelayMessage
		}
		for _, r := range value {
			if r != '\t' && (r < 0x20 || r == 0x7f || r > 0xff) {
				return errInvalidRelayMessage
			}
		}
	}
	return nil
}

// validToken is an HTTP token (Node's `checkIsHttpToken`).
func validToken(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

// remember keeps the brain's Radius addresses, in memory and in radius.json: its origin for forward links, and its page in the
// shell that framed the request for share links.
func (b *Brain) remember(origin, shellOrigin string, owner principal) {
	b.mu.Lock()
	shellPage := b.shell
	if originPattern.MatchString(shellOrigin) && b.config != nil {
		shellPage = shellOrigin + "/pi/brains/" + url.PathEscape(owner.OrganizationID) + "/" + url.PathEscape(b.config.ServerID)
	}
	newOrigin := b.forwards != nil && b.forwards.RadiusOrigin() != origin
	if !newOrigin && shellPage == b.shell {
		b.mu.Unlock()
		return
	}
	if b.forwards != nil {
		b.forwards.SetRadiusOrigin(origin)
	}
	b.shell = shellPage
	b.mu.Unlock()
	b.spawn(func() {
		config, err := ReadConfig(b.directory)
		if err != nil || config == nil || (config.PublicOrigin == origin && config.ShellPage == shellPage) {
			return
		}
		config.PublicOrigin = origin
		if shellPage != "" {
			config.ShellPage = shellPage
		}
		_ = WriteConfig(b.directory, *config)
	})
}

// reporter reports the shares, one report at a time, so an older set never lands after a newer one.
func (b *Brain) reporter() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.kick:
		}
		for b.reportOnce() {
		}
	}
}

// reportOnce reports the current shares if they need it and the socket is open; true when another look is due (the shares
// changed while it ran).
func (b *Brain) reportOnce() bool {
	b.mu.Lock()
	shares, current, done := b.shares, b.session, b.reported
	have := b.haveShar
	b.mu.Unlock()
	if !have || done || current == nil || !current.ws.open() {
		return false
	}
	config, err := b.currentConfig()
	if err == nil {
		err = b.putShares(config, shares)
	}
	if err != nil {
		// The next change or connect reports again.
		if !b.isClosed() {
			fmt.Fprintf(os.Stderr, "Radius: reporting shares failed (%v)\n", err)
		}
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if sameStrings(b.shares, shares) && b.haveShar {
		b.reported = true
		return false
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for at := range a {
		if a[at] != b[at] {
			return false
		}
	}
	return true
}

func (b *Brain) putShares(config *Config, shares []string) error {
	body, err := json.Marshal(map[string]any{"emails": shares})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(config.Gateway, "/")+"/v1/servers/"+config.ServerID+"/shares", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("authorization", "Bearer "+config.Credential.Access)
	request.Header.Set("content-type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	text, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("%d: %s", response.StatusCode, truncate(string(text), 200))
	}
	return nil
}
