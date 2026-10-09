// Package env is the remote execution environment of Pi Durable: an agent's tools run on another machine, usually over
// SSH, while the Durable worker, its storage and credentials stay local.
//
// Ports packages/env/src/index.ts
//
// Go mapping: upstream's Promise-returning methods are blocking calls that take a context.Context first, where
// upstream passes an AbortSignal. A request whose result is awaited later is a Call, started in call order (frames
// leave in the order the calls were started, which the daemon's write ordering needs) and awaited with Wait.
package env

// Ports packages/env/src/connection.ts

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Frame types (docs/protocol.md).
const (
	frameRequest byte = 1
	frameResult  byte = 2
	frameError   byte = 3
	frameEvent   byte = 4
	frameCancel  byte = 5
	framePing    byte = 6
)

const (
	maxFrame     = 16 * 1024 * 1024
	pingInterval = 5 * time.Second
	// silenceLimit: the daemon pings every five seconds; this much silence means the connection is gone.
	silenceLimit = 30 * time.Second
	// startTimeout is how long starting the daemon and its `hello` may take.
	startTimeout = 60 * time.Second
	// maxNoise is how much output before the sync line is kept while looking for it.
	maxNoise = 1024 * 1024
)

// Json is a JSON object.
type Json = map[string]any

// RemoteError is a failure reported by the daemon, with its Node-style code.
type RemoteError struct {
	Code    string
	Path    string
	Message string
	Fields  Json
}

// NewRemoteError returns the error of the daemon's error fields.
func NewRemoteError(fields Json) *RemoteError {
	message := "Remote operation failed"
	if text, ok := fields["message"].(string); ok {
		message = text
	}
	code := "unknown"
	if text, ok := fields["code"].(string); ok {
		code = text
	}
	path, _ := fields["path"].(string)
	e := &RemoteError{Code: code, Path: path, Message: message, Fields: fields}
	return e
}

func (e *RemoteError) Error() string { return e.Message }

// Name is the `name` property, "RemoteError".
func (e *RemoteError) Name() string { return "RemoteError" }

// RemoteInfo is what `hello` reports about the remote machine.
type RemoteInfo struct {
	Protocol int
	Version  string
	// OS is Rust's std::env::consts::OS spelling: linux, macos, android or windows.
	OS   string
	Arch string
	Home string
	// Tmpdir follows the remote Node's os.tmpdir() rules.
	Tmpdir string
	// Separator is the path separator: `/`, or `\` on Windows.
	Separator string
	// Cwd is the daemon's working directory, where Node's path.resolve would fall back to process.cwd().
	Cwd string
	// DriveCwds are Windows' per-drive working directories (`C:` to `C:\work`) of the `=C:` variables.
	DriveCwds map[string]string
	Pid       int
}

func parseInfo(fields Json) RemoteInfo {
	text := func(key string) string {
		value, _ := fields[key].(string)
		return value
	}
	number := func(key string) int {
		value, _ := fields[key].(float64)
		return int(value)
	}
	info := RemoteInfo{
		Protocol: number("protocol"), Version: text("version"), OS: text("os"), Arch: text("arch"), Home: text("home"),
		Tmpdir: text("tmpdir"), Separator: text("separator"), Cwd: text("cwd"), Pid: number("pid"),
		DriveCwds: map[string]string{},
	}
	if drives, ok := fields["driveCwds"].(Json); ok {
		for drive, directory := range drives {
			if value, ok := directory.(string); ok {
				info.DriveCwds[drive] = value
			}
		}
	}
	return info
}

// ConnectionOptions configure a Connection.
type ConnectionOptions struct {
	// Command starts the daemon, before its `serve --token <hex>` arguments, e.g. `ssh -T -- host
	// ~/.pi/mobile/tools/pi-env`.
	Command []string
	// CommandFunc computes the command at each start instead (detecting and deploying first, for example); its failure
	// fails that start, and the next request tries again. It takes precedence over Command.
	CommandFunc func(ctx context.Context) ([]string, error)
	// OnLog receives the daemon's and the transport's diagnostic output.
	OnLog func(text string)
}

// Reply is a successful reply.
type Reply struct {
	JSON    Json
	Payload []byte
	// Session is the daemon session that answered; handles from it are only valid while it lives.
	Session int
}

// RequestOptions configure one request.
type RequestOptions struct {
	Payload []byte
	// OnEvent receives progress events of the request, such as `exec` output, on the connection's reader.
	OnEvent func(json Json, payload []byte)
	// OnStart receives the request's id and session, for Kill, before the request is sent.
	OnStart func(id uint32, session int)
	// Session, when not 0, only runs the request in that daemon session, for requests on handles it opened. After the
	// connection was lost and started again, such requests fail instead of reaching a daemon that never opened the
	// handle.
	Session int
}

func lost(message string) *RemoteError {
	return NewRemoteError(Json{"code": "unknown", "message": message, "lost": true})
}

// IsConnectionLost reports whether err means the connection was lost, not that the daemon refused the request.
func IsConnectionLost(err error) bool {
	remote, ok := errors.AsType[*RemoteError](err)
	return ok && remote.Fields["lost"] == true
}

func closedError() *RemoteError {
	return NewRemoteError(Json{"code": "unknown", "message": "Connection closed"})
}

func encodeFrame(kind byte, id uint32, value Json, payload []byte) []byte {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	encoded := bytes.TrimSuffix(body.Bytes(), []byte("\n"))
	data := make([]byte, 13, 13+len(encoded)+len(payload))
	binary.BigEndian.PutUint32(data[0:4], uint32(9+len(encoded)+len(payload)))
	data[4] = kind
	binary.BigEndian.PutUint32(data[5:9], id)
	binary.BigEndian.PutUint32(data[9:13], uint32(len(encoded)))
	data = append(data, encoded...)
	return append(data, payload...)
}

type pendingCall struct {
	call    *Call
	onEvent func(json Json, payload []byte)
}

// Call is a request that was sent and has not been awaited.
type Call struct {
	done  chan struct{}
	reply Reply
	err   error

	mu       sync.Mutex
	stop     func() bool
	finished bool
}

func newCall() *Call { return &Call{done: make(chan struct{})} }

func failedCall(err error) *Call {
	call := newCall()
	call.finish(Reply{}, err)
	return call
}

func (c *Call) finish(reply Reply, err error) {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	stop := c.stop
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
	c.reply, c.err = reply, err
	close(c.done)
}

// onSettled runs stop once the call settles; if it already has, it runs now.
func (c *Call) onSettled(stop func() bool) {
	c.mu.Lock()
	if !c.finished {
		c.stop = stop
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	stop()
}

// Wait blocks until the request settles. Cancelling the context of the request sends `cancel`; the request still
// settles with the daemon's result or error.
func (c *Call) Wait() (Reply, error) {
	<-c.done
	return c.reply, c.err
}

// session is one running daemon.
type session struct {
	id    int
	cmd   *exec.Cmd
	stdin io.WriteCloser
	token string

	writeMu sync.Mutex

	mu       sync.Mutex
	pending  map[uint32]*pendingCall
	live     bool
	exited   chan struct{}
	exitCode int
	stopPing chan struct{}
	lastSeen atomic.Int64
}

// readyStart is the shared outcome of starting the daemon, so concurrent first requests wait for one start.
type readyStart struct {
	done    chan struct{}
	info    RemoteInfo
	session *session
	err     error
}

// Connection is one connection to a pi-env daemon: started lazily on the first request and started again after it is
// lost. Requests in flight when it is lost fail with code `unknown`; for mutations their outcome is then unknown.
type Connection struct {
	options ConnectionOptions

	mu       sync.Mutex
	session  *session
	ready    *readyStart
	sessions int
	nextID   uint32
	closed   bool
	// lifetime is cancelled by Close, which ends a start in progress.
	lifetime context.Context
	cancel   context.CancelFunc
	starts   sync.WaitGroup
}

// NewConnection returns a connection that starts the daemon at its first request.
func NewConnection(options ConnectionOptions) *Connection {
	lifetime, cancel := context.WithCancel(context.Background())
	return &Connection{options: options, nextID: 1, lifetime: lifetime, cancel: cancel}
}

// Info connects if needed and returns what the daemon reported about its machine.
func (c *Connection) Info(ctx context.Context) (RemoteInfo, error) {
	ready, err := c.connect(ctx)
	if err != nil {
		return RemoteInfo{}, err
	}
	return ready.info, nil
}

// Session connects if needed and returns the live session's id.
func (c *Connection) Session(ctx context.Context) (int, error) {
	ready, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}
	return ready.session.id, nil
}

// Request sends one request and waits for its reply.
func (c *Connection) Request(ctx context.Context, op string, request Json, options RequestOptions) (Reply, error) {
	return c.Start(ctx, op, request, options).Wait()
}

// Start sends one request and returns without waiting for its reply. Starting requests in a row sends them in that
// order.
func (c *Connection) Start(ctx context.Context, op string, request Json, options RequestOptions) *Call {
	if options.Session != 0 {
		c.mu.Lock()
		current := c.session
		c.mu.Unlock()
		if current == nil || current.id != options.Session || !current.isLive() {
			return failedCall(lost("pi-env connection lost"))
		}
		return c.send(ctx, current, op, request, options)
	}
	ready, err := c.connect(ctx)
	if err != nil {
		return failedCall(err)
	}
	return c.send(ctx, ready.session, op, request, options)
}

// Close stops the daemon; it kills everything it started.
func (c *Connection) Close() {
	c.mu.Lock()
	c.closed = true
	current := c.session
	c.mu.Unlock()
	c.cancel()
	if current != nil {
		c.teardown(current, closedError())
	}
	c.starts.Wait()
}

// Kill kills one `exec` without aborting it.
func (c *Connection) Kill(id uint32, sessionID int) {
	c.mu.Lock()
	current := c.session
	c.mu.Unlock()
	if current != nil && current.id == sessionID && current.isLive() {
		c.write(current, encodeFrame(frameCancel, id, Json{"mode": "kill"}, nil))
	}
}

func (s *session) isLive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live
}

func (c *Connection) connect(ctx context.Context) (*readyStart, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, closedError()
	}
	if c.ready == nil {
		ready := &readyStart{done: make(chan struct{})}
		c.ready = ready
		c.starts.Go(func() { c.runStart(ready) })
	}
	ready := c.ready
	c.mu.Unlock()
	select {
	case <-ready.done:
		if ready.err != nil {
			return nil, ready.err
		}
		return ready, nil
	case <-ctx.Done():
		// The start goes on for the next request; this one is aborted, as the daemon would answer a cancelled request.
		return nil, NewRemoteError(Json{"code": "aborted", "message": "aborted"})
	}
}

func (c *Connection) runStart(ready *readyStart) {
	info, started, err := c.start()
	ready.info, ready.session, ready.err = info, started, err
	if err != nil {
		// A failed start is not remembered: the next request tries again.
		c.mu.Lock()
		if c.ready == ready {
			c.ready = nil
		}
		c.mu.Unlock()
	}
	close(ready.done)
}

func (c *Connection) send(ctx context.Context, s *session, op string, request Json, options RequestOptions) *Call {
	call := newCall()
	if !s.isLive() {
		return failedCall(lost("pi-env connection lost"))
	}
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()
	s.mu.Lock()
	if !s.live {
		s.mu.Unlock()
		return failedCall(lost("pi-env connection lost"))
	}
	s.pending[id] = &pendingCall{call: call, onEvent: options.OnEvent}
	s.mu.Unlock()
	if options.OnStart != nil {
		options.OnStart(id, s.id)
	}
	body := make(Json, len(request)+1)
	maps.Copy(body, request)
	body["op"] = op
	c.write(s, encodeFrame(frameRequest, id, body, options.Payload))
	// Registered after the request is written, so a cancel never leaves before its request; a context that is already
	// done sends its cancel right away.
	call.onSettled(context.AfterFunc(ctx, func() { c.write(s, encodeFrame(frameCancel, id, Json{}, nil)) }))
	return call
}

func (c *Connection) write(s *session, data []byte) {
	if !s.isLive() {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.stdin.Write(data)
}

// start starts the daemon and waits for its `hello`.
func (c *Connection) start() (RemoteInfo, *session, error) {
	var command []string
	var commandErr error
	switch {
	case c.options.CommandFunc != nil:
		command, commandErr = c.options.CommandFunc(c.lifetime)
	default:
		command = c.options.Command
	}
	if commandErr != nil {
		return RemoteInfo{}, nil, NewRemoteError(Json{"code": "spawn_error", "message": commandErr.Error(), "lost": true})
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return RemoteInfo{}, nil, closedError()
	}
	if len(command) == 0 {
		return RemoteInfo{}, nil, NewRemoteError(Json{"code": "spawn_error", "message": "No daemon command"})
	}
	var tokenBytes [16]byte
	_, _ = rand.Read(tokenBytes[:])
	token := hex.EncodeToString(tokenBytes[:])
	cmd := exec.Command(command[0], append(append([]string{}, command[1:]...), "serve", "--token", token)...)
	stdin, stdinErr := cmd.StdinPipe()
	stdout, stdoutErr := cmd.StdoutPipe()
	stderr, stderrErr := cmd.StderrPipe()
	if err := errors.Join(stdinErr, stdoutErr, stderrErr); err != nil {
		return RemoteInfo{}, nil, NewRemoteError(Json{"code": "spawn_error", "message": err.Error(), "lost": true})
	}
	if err := cmd.Start(); err != nil {
		return RemoteInfo{}, nil, NewRemoteError(Json{"code": "spawn_error", "message": err.Error(), "lost": true})
	}
	c.mu.Lock()
	c.sessions++
	s := &session{
		id: c.sessions, cmd: cmd, stdin: stdin, token: token, pending: map[uint32]*pendingCall{}, live: true,
		exited: make(chan struct{}), stopPing: make(chan struct{}),
	}
	s.lastSeen.Store(time.Now().UnixMilli())
	if c.closed {
		c.mu.Unlock()
		c.teardown(s, closedError())
		return RemoteInfo{}, nil, closedError()
	}
	c.session = s
	c.mu.Unlock()

	// The hello is pending before any goroutine below can deliver output, an exit or a teardown, as upstream sends it in
	// the tick that spawns the daemon and Node delivers child events later. Whichever teardown comes first then decides
	// the hello's failure: the daemon's own exit before the hello reports itself, as upstream's `exit` listener rejects
	// the start first, and a corrupt frame or a timeout tears the session down before the killed daemon's exit can.
	hello := c.send(c.lifetime, s, "hello", Json{"protocol": 1}, RequestOptions{})
	var helloDone atomic.Bool
	go func() { // reaps the daemon and fails the session when it exits
		state, _ := cmd.Process.Wait()
		s.exitCode = state.ExitCode()
		close(s.exited)
		failure := lost("pi-env connection lost")
		if !helloDone.Load() {
			code := "null"
			if s.exitCode >= 0 {
				code = fmt.Sprint(s.exitCode)
			}
			failure = lost("pi-env exited with code " + code + " before it was ready")
		}
		c.teardown(s, failure)
	}()
	go func() {
		defer func() { _ = stderr.Close() }()
		chunk := make([]byte, 16*1024)
		for {
			read, err := stderr.Read(chunk)
			if read > 0 && c.options.OnLog != nil {
				c.options.OnLog(string(chunk[:read]))
			}
			if err != nil {
				return
			}
		}
	}()
	go c.readLoop(s, stdout)
	go c.pingLoop(s)

	timer := time.NewTimer(startTimeout)
	defer timer.Stop()
	type outcome struct {
		reply Reply
		err   error
	}
	answered := make(chan outcome, 1)
	go func() {
		reply, err := hello.Wait()
		answered <- outcome{reply, err}
	}()
	var reply Reply
	var err error
	select {
	case result := <-answered:
		reply, err = result.reply, result.err
	case <-timer.C:
		err = lost(fmt.Sprintf("pi-env did not answer within %d s", int(startTimeout/time.Second)))
	}
	if err == nil && reply.JSON["protocol"] != float64(1) {
		err = lost("Unsupported protocol " + templateString(reply.JSON, "protocol"))
	}
	if err != nil {
		c.teardown(s, err)
		return RemoteInfo{}, nil, err
	}
	helloDone.Store(true)
	return parseInfo(reply.JSON), s, nil
}

func (c *Connection) pingLoop(s *session) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopPing:
			return
		case <-ticker.C:
		}
		if time.Since(time.UnixMilli(s.lastSeen.Load())) > silenceLimit {
			c.teardown(s, lost("pi-env connection timed out"))
			return
		}
		c.write(s, encodeFrame(framePing, 0, Json{}, nil))
	}
}

func (c *Connection) teardown(s *session, err error) {
	s.mu.Lock()
	if !s.live {
		s.mu.Unlock()
		return
	}
	s.live = false
	pending := make([]*pendingCall, 0, len(s.pending))
	for _, call := range s.pending {
		pending = append(pending, call)
	}
	s.pending = map[uint32]*pendingCall{}
	s.mu.Unlock()
	close(s.stopPing)
	c.mu.Lock()
	if c.session == s {
		c.session = nil
		c.ready = nil
	}
	c.mu.Unlock()
	_ = s.stdin.Close()
	_ = s.cmd.Process.Kill()
	for _, call := range pending {
		call.call.finish(Reply{}, err)
	}
}

// seenReader records when bytes last arrived, so a large frame arriving slowly counts as a live daemon.
type seenReader struct {
	reader   io.Reader
	lastSeen *atomic.Int64
}

func (r seenReader) Read(buffer []byte) (int, error) {
	read, err := r.reader.Read(buffer)
	if read > 0 {
		r.lastSeen.Store(time.Now().UnixMilli())
	}
	return read, err
}

// readLoop reads the daemon's stdout: the sync line, then frames. The end of stdout ends nothing by itself: the
// daemon's exit, or its silence, tears the session down, as upstream reacts to `exit` and not to the end of stdout.
func (c *Connection) readLoop(s *session, stdout io.ReadCloser) {
	defer func() { _ = stdout.Close() }()
	synced, ok := c.syncStream(s, seenReader{stdout, &s.lastSeen})
	if !ok {
		return
	}
	reader := seenReader{synced, &s.lastSeen}
	for {
		var lengthBytes [4]byte
		if _, err := io.ReadFull(reader, lengthBytes[:]); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint32(lengthBytes[:]))
		if length < 9 || length > maxFrame {
			c.teardown(s, lost("Corrupt frame from pi-env"))
			return
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			return
		}
		jsonLength := int(binary.BigEndian.Uint32(body[5:9]))
		var value any
		if 9+jsonLength > length || json.Unmarshal(body[9:9+jsonLength], &value) != nil {
			c.teardown(s, lost("Corrupt frame from pi-env"))
			return
		}
		object, isObject := value.(Json)
		if !isObject {
			c.teardown(s, lost("Corrupt frame from pi-env"))
			return
		}
		c.dispatch(s, body[0], binary.BigEndian.Uint32(body[1:5]), object, body[9+jsonLength:])
	}
}

// syncStream skips everything before the daemon's sync line, which shell startup files may print before it runs.
func (c *Connection) syncStream(s *session, stdout io.Reader) (io.Reader, bool) {
	marker := []byte("PI-ENV " + s.token + "\n")
	var buffered []byte
	chunk := make([]byte, 64*1024)
	for {
		read, err := stdout.Read(chunk)
		if read > 0 {
			buffered = append(buffered, chunk[:read]...)
			if index := bytes.Index(buffered, marker); index >= 0 {
				if noise := jsstring.Trim(string(buffered[:index])); noise != "" && c.options.OnLog != nil {
					c.options.OnLog(noise)
				}
				return io.MultiReader(bytes.NewReader(buffered[index+len(marker):]), stdout), true
			}
			if len(buffered) > maxNoise {
				buffered = append([]byte(nil), buffered[len(buffered)-len(marker):]...)
			}
		}
		if err != nil {
			return nil, false
		}
	}
}

func (c *Connection) dispatch(s *session, kind byte, id uint32, value Json, payload []byte) {
	s.mu.Lock()
	pending := s.pending[id]
	if (kind == frameResult || kind == frameError) && pending != nil {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	if pending == nil {
		return
	}
	switch kind {
	case frameEvent:
		if pending.onEvent != nil {
			pending.onEvent(value, payload)
		}
	case frameResult:
		pending.call.finish(Reply{JSON: value, Payload: payload, Session: s.id}, nil)
	case frameError:
		pending.call.finish(Reply{}, NewRemoteError(value))
	}
}

// templateString is `${object[key]}` of a JSON object: undefined when the key is missing, as JavaScript prints it.
func templateString(object Json, key string) string {
	value, present := object[key]
	if !present {
		return "undefined"
	}
	return jsString(value)
}

// jsString is String(value) of a decoded JSON value.
func jsString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return jsnumber.String(typed)
	case []any:
		parts := make([]string, len(typed))
		for index, item := range typed {
			if item != nil {
				parts[index] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}
