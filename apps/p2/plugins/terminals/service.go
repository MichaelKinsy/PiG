package terminals

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/terminals/service.ts.

// TerminalPingInterval is how often a viewer's socket gets `{"type":"ping"}`. An idle terminal sends nothing, so without it
// a dead connection looks open to the viewer and idle-timeouts on the way may close it. Not part of protocol.go: that
// file is hashed into the terminal host's version (version.go), and the host never sees viewer pings.
const TerminalPingInterval = 15 * time.Second

// ViewSocket is a viewer's WebSocket as a TerminalView uses it (the `ws` WebSocket's surface).
type ViewSocket interface {
	// Open is whether the socket is open (`readyState === OPEN`).
	Open() bool
	// BufferedAmount is the bytes sent and not yet written to the network.
	BufferedAmount() int
	SendText(text string)
	SendBinary(bytes []byte)
	// Terminate destroys the socket at once.
	Terminate()
	// Close starts the closing handshake.
	Close(code int, reason string)
}

// TerminalView is one server attachment. Closing it never stops the host or its programs.
type TerminalView struct {
	Session string
	version string

	mu         sync.Mutex
	attachment *Attachment
	socket     ViewSocket
	binding    *ViewBinding
	queue      []queuedFrame
	queued     int
	list       chan listResult
	closed     bool

	// serial runs the view's commands one at a time, in order.
	serial sync.Mutex
	// directory is where the terminal host of this data directory is.
	directory string
}

type queuedFrame struct {
	text  string
	bytes []byte
	raw   bool
}

type listResult struct {
	terminals []TerminalInfo
	err       error
}

// NewTerminalView is a view of session over the host of directory; version is the host code version it expects.
func NewTerminalView(directory, session, version string) *TerminalView {
	return &TerminalView{directory: directory, Session: session, version: version}
}

// Ready attaches to the host (starting it when none runs) and waits for its hello.
func (v *TerminalView) Ready(ctx context.Context) error {
	v.mu.Lock()
	if v.attachment == nil {
		var attachment *Attachment
		// The callbacks wait for this lock: they never see the attachment unassigned.
		attachment = AttachTerminalHost(v.directory, v.frame, func() {
			v.mu.Lock()
			if v.attachment != attachment {
				v.mu.Unlock()
				return
			}
			v.attachment = nil
			socket := v.socket
			v.mu.Unlock()
			if socket != nil {
				socket.Close(1011, "Terminal host disconnected")
			}
		})
		v.attachment = attachment
	}
	attachment := v.attachment
	v.mu.Unlock()
	return attachment.Ready(ctx)
}

// detach detaches from the host (it and its programs live on).
func (v *TerminalView) detach() {
	v.mu.Lock()
	attachment := v.attachment
	v.attachment = nil
	v.mu.Unlock()
	if attachment != nil {
		attachment.Detach()
	}
}

func (v *TerminalView) write(message any) error {
	v.mu.Lock()
	attachment := v.attachment
	v.mu.Unlock()
	if attachment == nil {
		return errors.New("Terminal host is disconnected")
	}
	return attachment.Write(message)
}

// frame is a frame from the host.
func (v *TerminalView) frame(frame Frame) {
	if frame.Raw {
		v.forward(queuedFrame{bytes: frame.Bytes, raw: true})
		return
	}
	event, err := ParseEvent(frame.JSON)
	if err != nil {
		return
	}
	switch {
	case event.Type == EventHello:
		if event.Version != v.version {
			text, _ := chordjson.Marshal(TerminalEvent{Type: EventOutdated, Running: event.Running, HasRunning: true}.JSON())
			v.forward(queuedFrame{text: string(text)})
		}
		return
	case event.Type == EventList:
		v.mu.Lock()
		list := v.list
		v.list = nil
		v.mu.Unlock()
		if list != nil {
			list <- listResult{terminals: event.Terminals}
			return
		}
	}
	text, _ := chordjson.Marshal(frame.JSON)
	v.forward(queuedFrame{text: string(text)})
}

func (v *TerminalView) forward(frame queuedFrame) {
	size := len(frame.text)
	if frame.raw {
		size = len(frame.bytes)
	}
	v.mu.Lock()
	socket := v.socket
	if socket != nil && socket.Open() {
		v.mu.Unlock()
		if socket.BufferedAmount()+size > MaxBacklog {
			socket.Terminate()
			return
		}
		if frame.raw {
			socket.SendBinary(frame.bytes)
		} else {
			socket.SendText(frame.text)
		}
		return
	}
	v.queued += size
	if v.queued > MaxBacklog {
		v.queue = nil
		v.queued = 0
		v.mu.Unlock()
		v.detach()
		return
	}
	v.queue = append(v.queue, frame)
	v.mu.Unlock()
}

// ViewBinding is a socket bound to a view: its messages and its close go through it.
type ViewBinding struct {
	view   *TerminalView
	socket ViewSocket
	stop   chan struct{}
	once   sync.Once
}

// Bind makes socket the view's viewer: frames queued so far go to it, it is pinged while open, and its messages are
// input for the host.
func (v *TerminalView) Bind(socket ViewSocket) *ViewBinding {
	binding := &ViewBinding{view: v, socket: socket, stop: make(chan struct{})}
	v.mu.Lock()
	previous := v.socket
	v.socket = socket
	v.binding = binding
	queue := v.queue
	v.queue = nil
	v.queued = 0
	v.mu.Unlock()
	if previous != nil {
		previous.Terminate()
	}
	for _, frame := range queue {
		v.forward(frame)
	}
	// Keepalive; see TerminalPingInterval.
	//portlint:allow golifetime ends when the socket closes (Closed)
	go func() {
		ticker := time.NewTicker(TerminalPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if socket.Open() {
					socket.SendText(`{"type":"ping"}`)
				}
			case <-binding.stop:
				return
			}
		}
	}()
	return binding
}

// Message is a message of the viewer's socket: the terminal channel accepts bytes only.
func (b *ViewBinding) Message(binary bool, data []byte) {
	if !binary {
		b.socket.Close(1003, "Terminal channel accepts bytes only")
		return
	}
	if err := b.view.write(data); err != nil {
		b.socket.Terminate()
	}
}

// Closed is the end of the viewer's socket (an error ends it too).
func (b *ViewBinding) Closed() {
	b.once.Do(func() { close(b.stop) })
	v := b.view
	v.mu.Lock()
	if v.socket != b.socket {
		v.mu.Unlock()
		return
	}
	v.socket = nil
	v.binding = nil
	v.queue = nil
	v.queued = 0
	v.mu.Unlock()
	v.detach()
}

// Command sends one request of the host's protocol for this view; commands run one at a time, in order.
func (v *TerminalView) Command(ctx context.Context, request TerminalRequest) error {
	v.serial.Lock()
	defer v.serial.Unlock()
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed {
		return errors.New("Terminal view is closed")
	}
	if err := v.Ready(ctx); err != nil {
		return err
	}
	if request.Type == RequestAttach {
		list := make(chan listResult, 1)
		v.mu.Lock()
		v.list = list
		v.mu.Unlock()
		if err := v.write(TerminalRequest{Type: RequestList, SessionID: v.Session}.JSON()); err != nil {
			return err
		}
		timer := time.NewTimer(10 * time.Second)
		var result listResult
		select {
		case result = <-list:
		case <-timer.C:
			result.err = errors.New("Terminal list timed out")
		case <-ctx.Done():
			result.err = context.Cause(ctx)
		}
		timer.Stop()
		v.mu.Lock()
		if v.list == list {
			v.list = nil
		}
		v.mu.Unlock()
		if result.err != nil {
			return result.err
		}
		found := false
		for _, terminal := range result.terminals {
			found = found || terminal.ID == request.TerminalID
		}
		if !found {
			return p2runtime.Forbidden("This terminal does not belong to this session")
		}
	}
	return v.write(request.JSON())
}

// Close ends the view: its socket, its attachment, and a command waiting for the host's list.
func (v *TerminalView) Close() {
	v.mu.Lock()
	v.closed = true
	list := v.list
	v.list = nil
	socket := v.socket
	v.socket = nil
	v.mu.Unlock()
	if list != nil {
		list <- listResult{err: errors.New("Terminal view closed")}
	}
	if socket != nil {
		socket.Terminate()
	}
	v.detach()
}

// OutdatedHost is what a call to a terminal host running older code fails with: it does not know the calls.
const OutdatedHost = "The terminal host runs older code: restart it from the terminal panel (this ends its terminals)"

// HostCalls is the server's connection for calls with replies (the agent's terminal tool, archiving): one attachment, made
// when the first call needs it and again after the host went away. Cancelling a call tells the host, which ends its wait.
type HostCalls struct {
	directory string
	version   string

	mu         sync.Mutex
	attachment *Attachment
	// hostVersion is the attached host's version, from its hello.
	hostVersion string
	next        int
	pending     map[int]chan callReply
	closed      bool
}

type callReply struct {
	result HostResult
	err    error
}

// NewHostCalls makes the calls connection to the host of directory, which must run code `version`.
func NewHostCalls(directory, version string) *HostCalls {
	return &HostCalls{directory: directory, version: version, next: 1, pending: map[int]chan callReply{}}
}

// Call is one call to the host (starting it when none runs); ctx cancels it.
func (c *HostCalls) Call(ctx context.Context, call HostCall) (HostResult, error) {
	if err := ctx.Err(); err != nil {
		return HostResult{}, context.Cause(ctx)
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return HostResult{}, errors.New("The terminal service is closed")
	}
	attachment := c.attach()
	c.mu.Unlock()
	if err := attachment.Ready(ctx); err != nil {
		return HostResult{}, err
	}
	c.mu.Lock()
	if c.hostVersion != c.version {
		c.mu.Unlock()
		return HostResult{}, errors.New(OutdatedHost)
	}
	id := c.next
	c.next++
	reply := make(chan callReply, 1)
	c.pending[id] = reply
	c.mu.Unlock()
	forget := func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		_, present := c.pending[id]
		delete(c.pending, id)
		return present
	}
	if err := attachment.Write(TerminalRequest{Type: RequestCall, ID: id, Call: &call}.JSON()); err != nil {
		forget()
		return HostResult{}, err
	}
	select {
	case out := <-reply:
		return out.result, out.err
	case <-ctx.Done():
		if !forget() {
			// The reply won the race.
			out := <-reply
			return out.result, out.err
		}
		_ = attachment.Write(TerminalRequest{Type: RequestCancel, ID: id}.JSON())
		return HostResult{}, context.Cause(ctx)
	}
}

// attach is the current attachment, made when there is none; c.mu is held.
func (c *HostCalls) attach() *Attachment {
	if c.attachment != nil {
		return c.attachment
	}
	var attachment *Attachment
	attachment = AttachTerminalHost(c.directory, c.frame, func() {
		c.mu.Lock()
		if c.attachment != attachment {
			c.mu.Unlock()
			return
		}
		c.attachment = nil
		c.mu.Unlock()
		c.fail(errors.New("Terminal host disconnected"))
	})
	c.attachment = attachment
	c.hostVersion = ""
	return attachment
}

func (c *HostCalls) frame(frame Frame) {
	if frame.Raw {
		return
	}
	event, err := ParseEvent(frame.JSON)
	if err != nil {
		return
	}
	if event.Type == EventHello {
		c.mu.Lock()
		c.hostVersion = event.Version
		c.mu.Unlock()
	}
	if event.Type != EventReply {
		return
	}
	c.mu.Lock()
	pending := c.pending[event.ID]
	delete(c.pending, event.ID)
	c.mu.Unlock()
	if pending == nil {
		return
	}
	if event.Err != nil {
		pending <- callReply{err: errors.New(*event.Err)}
	} else if event.Result != nil {
		pending <- callReply{result: *event.Result}
	} else {
		pending <- callReply{}
	}
}

func (c *HostCalls) fail(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[int]chan callReply{}
	c.mu.Unlock()
	for _, reply := range pending {
		reply <- callReply{err: err}
	}
}

// Close ends the connection; calls waiting fail.
func (c *HostCalls) Close() {
	c.mu.Lock()
	c.closed = true
	attachment := c.attachment
	c.attachment = nil
	c.mu.Unlock()
	if attachment != nil {
		attachment.Detach()
	}
	c.fail(errors.New("The terminal service is closed"))
}

// TerminalService is the server's terminal service: viewers' sockets, their one-use channel tokens, and the calls.
type TerminalService struct {
	Directory string
	upgrader  websocket.Upgrader

	mu     sync.Mutex
	views  map[*TerminalView]struct{}
	tokens map[string]tokenEntry
	calls  *HostCalls
	closed bool
}

type tokenEntry struct {
	view    *TerminalView
	expires time.Time
}

// maxViewerPayload is the most a viewer's frame carries.
const maxViewerPayload = 1024 * 1024

// tokenLife is how long a channel token may wait for its upgrade.
const tokenLife = 30 * time.Second

// NewTerminalService is the service of the terminal host of directory.
func NewTerminalService(directory string) *TerminalService {
	return &TerminalService{
		Directory: directory, views: map[*TerminalView]struct{}{}, tokens: map[string]tokenEntry{},
		// The server checked the request's origin and principal before the upgrade.
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, ReadBufferSize: 32 * 1024, WriteBufferSize: 32 * 1024},
	}
}

// Call is one call to the terminal host (starting it when none runs); ctx cancels it.
func (s *TerminalService) Call(ctx context.Context, call HostCall) (HostResult, error) {
	s.mu.Lock()
	if s.calls == nil {
		version, err := HostVersion()
		if err != nil {
			s.mu.Unlock()
			return HostResult{}, err
		}
		s.calls = NewHostCalls(s.Directory, version)
	}
	calls := s.calls
	s.mu.Unlock()
	return calls.Call(ctx, call)
}

// closeViewerTimeout is how long closing a session's terminals on a host running older code may take.
const closeViewerTimeout = 10 * time.Second

// CloseSession closes the session's terminals; without a running host there are none, and none is started. A host running
// older code does not know `closeSession`: it closes them as the panel does, attaching to each and closing it.
func (s *TerminalService) CloseSession(ctx context.Context, sessionID string) error {
	conn := ConnectRunningTerminalHost(s.Directory)
	if conn == nil {
		return nil
	}
	older, err := func() (bool, error) {
		defer conn.Close()
		events := newHostEvents(conn)
		hello, err := events.next(func(event TerminalEvent) bool { return event.Type == EventHello })
		if err != nil {
			return false, err
		}
		version, err := HostVersion()
		if err != nil {
			return false, err
		}
		if hello.Version != version {
			return true, closeAsViewer(conn, events, sessionID)
		}
		return false, nil
	}()
	if err != nil || older {
		return err
	}
	_, err = s.Call(ctx, HostCall{Op: OpCloseSession, SessionID: sessionID})
	return err
}

// Open is a view of session over the host.
func (s *TerminalService) Open(ctx context.Context, session string) (*TerminalView, error) {
	version, err := HostVersion()
	if err != nil {
		return nil, err
	}
	view := NewTerminalView(s.Directory, session, version)
	s.mu.Lock()
	s.views[view] = struct{}{}
	s.mu.Unlock()
	if err := view.Ready(ctx); err != nil {
		s.Release(view)
		return nil, err
	}
	return view, nil
}

// Channel is a one-use token for the view's byte channel (`/terminal?token=`).
func (s *TerminalService) Channel(ctx context.Context, view *TerminalView) (string, error) {
	if err := view.Ready(ctx); err != nil {
		return "", err
	}
	var bytes [24]byte
	_, _ = rand.Read(bytes[:])
	token := base64.RawURLEncoding.EncodeToString(bytes[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for each, entry := range s.tokens {
		if entry.view == view || entry.expires.Before(now) {
			delete(s.tokens, each)
		}
	}
	s.tokens[token] = tokenEntry{view: view, expires: now.Add(tokenLife)}
	return token, nil
}

// Upgrade takes the viewer's WebSocket: the token of the URL names the view, once.
func (s *TerminalService) Upgrade(response http.ResponseWriter, request *http.Request) {
	token := request.URL.Query().Get("token")
	s.mu.Lock()
	entry, found := s.tokens[token]
	delete(s.tokens, token)
	closed := s.closed
	s.mu.Unlock()
	if !found || entry.expires.Before(time.Now()) || closed {
		response.Header().Set("Connection", "close")
		response.WriteHeader(http.StatusForbidden)
		return
	}
	conn, err := s.upgrader.Upgrade(response, request, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(maxViewerPayload)
	socket := newWebSocketView(conn)
	binding := entry.view.Bind(socket)
	//portlint:allow golifetime ends when the viewer's socket closes
	go func() {
		defer binding.Closed()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				socket.Terminate()
				return
			}
			binding.Message(kind == websocket.BinaryMessage, data)
		}
	}()
}

// Release ends a view.
func (s *TerminalService) Release(view *TerminalView) {
	s.mu.Lock()
	delete(s.views, view)
	for token, entry := range s.tokens {
		if entry.view == view {
			delete(s.tokens, token)
		}
	}
	s.mu.Unlock()
	view.Close()
}

// Close ends the service: its calls and its views.
func (s *TerminalService) Close() {
	s.mu.Lock()
	s.closed = true
	calls := s.calls
	views := make([]*TerminalView, 0, len(s.views))
	for view := range s.views {
		views = append(views, view)
	}
	s.mu.Unlock()
	if calls != nil {
		calls.Close()
	}
	for _, view := range views {
		s.Release(view)
	}
}

// hostEvents are a host connection's events, in order: next waits for the first one after the last taken that wanted
// accepts.
type hostEvents struct {
	mu    sync.Mutex
	queue []TerminalEvent
	ended bool
	wake  chan struct{}
}

func newHostEvents(conn interface{ Read([]byte) (int, error) }) *hostEvents {
	events := &hostEvents{wake: make(chan struct{}, 1)}
	//portlint:allow golifetime ends when the connection closes
	go func() {
		_ = ReadFrames(conn, func(frame Frame) {
			if frame.Raw {
				return
			}
			if event, err := ParseEvent(frame.JSON); err == nil {
				events.mu.Lock()
				events.queue = append(events.queue, event)
				events.mu.Unlock()
				events.signal()
			}
		})
		events.mu.Lock()
		events.ended = true
		events.mu.Unlock()
		events.signal()
	}()
	return events
}

func (e *hostEvents) signal() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *hostEvents) next(wanted func(TerminalEvent) bool) (TerminalEvent, error) {
	timer := time.NewTimer(closeViewerTimeout)
	defer timer.Stop()
	for {
		e.mu.Lock()
		for len(e.queue) > 0 {
			event := e.queue[0]
			e.queue = e.queue[1:]
			if wanted(event) {
				e.mu.Unlock()
				return event, nil
			}
		}
		ended := e.ended
		e.mu.Unlock()
		if ended {
			return TerminalEvent{}, errors.New("Terminal host disconnected")
		}
		//portlint:allow golifetime bounded by the timer (the wait for the host's answer)
		select {
		case <-e.wake:
		case <-timer.C:
			return TerminalEvent{}, errors.New("The terminal host did not answer")
		}
	}
}

// closeAsViewer closes the session's terminals with the viewer protocol every host version speaks: list them, then attach
// to each and force its close; it returns once the host reported each closed.
func closeAsViewer(conn interface{ Write([]byte) (int, error) }, events *hostEvents, sessionID string) error {
	write := func(request TerminalRequest) error {
		frame, err := EncodeFrame(request.JSON())
		if err != nil {
			return err
		}
		_, err = conn.Write(frame)
		return err
	}
	if err := write(TerminalRequest{Type: RequestList, SessionID: sessionID}); err != nil {
		return err
	}
	listed, err := events.next(func(event TerminalEvent) bool { return event.Type == EventList })
	if err != nil {
		return err
	}
	open := map[string]bool{}
	var order []string
	for _, terminal := range listed.Terminals {
		if !open[terminal.ID] {
			order = append(order, terminal.ID)
		}
		open[terminal.ID] = true
	}
	force := true
	for _, id := range order {
		if err := write(TerminalRequest{Type: RequestAttach, TerminalID: id}); err != nil {
			return err
		}
		if err := write(TerminalRequest{Type: RequestClose, Force: &force}); err != nil {
			return err
		}
	}
	for len(open) > 0 {
		closed, err := events.next(func(event TerminalEvent) bool { return event.Type == EventClosed })
		if err != nil {
			return err
		}
		delete(open, closed.TerminalID)
	}
	return nil
}

// webSocketView is a gorilla WebSocket as a ViewSocket: messages are written in order from a goroutine of their own, and
// the bytes not written yet are its BufferedAmount.
type webSocketView struct {
	conn     *websocket.Conn
	mu       sync.Mutex
	queue    []wsMessage
	buffered int
	wake     chan struct{}
	closed   bool
}

type wsMessage struct {
	kind    int
	data    []byte
	closing bool
}

func newWebSocketView(conn *websocket.Conn) *webSocketView {
	socket := &webSocketView{conn: conn, wake: make(chan struct{}, 1)}
	//portlint:allow golifetime ends when the socket is terminated or closed
	go socket.run()
	return socket
}

func (s *webSocketView) run() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closed {
			s.mu.Unlock()
			<-s.wake
			s.mu.Lock()
		}
		if len(s.queue) == 0 {
			s.mu.Unlock()
			return
		}
		message := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		var err error
		if message.closing {
			err = s.conn.WriteControl(websocket.CloseMessage, message.data, time.Now().Add(time.Second))
		} else {
			err = s.conn.WriteMessage(message.kind, message.data)
		}
		s.mu.Lock()
		s.buffered -= len(message.data)
		s.mu.Unlock()
		if err != nil {
			s.Terminate()
			return
		}
	}
}

func (s *webSocketView) push(message wsMessage) {
	s.mu.Lock()
	if s.closed && !message.closing {
		s.mu.Unlock()
		return
	}
	s.queue = append(s.queue, message)
	s.buffered += len(message.data)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *webSocketView) Open() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

func (s *webSocketView) BufferedAmount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffered
}

func (s *webSocketView) SendText(text string) {
	s.push(wsMessage{kind: websocket.TextMessage, data: []byte(text)})
}
func (s *webSocketView) SendBinary(data []byte) {
	s.push(wsMessage{kind: websocket.BinaryMessage, data: data})
}

func (s *webSocketView) Terminate() {
	s.mu.Lock()
	s.closed = true
	s.queue = nil
	s.mu.Unlock()
	_ = s.conn.Close()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *webSocketView) Close(code int, reason string) {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.mu.Unlock()
	if already {
		return
	}
	s.push(wsMessage{closing: true, data: websocket.FormatCloseMessage(code, reason)})
}

var _ = fmt.Sprint
