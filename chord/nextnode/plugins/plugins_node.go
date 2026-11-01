package plugins

import (
	"bufio"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// The Node subprocess of the plugins component (docs/plan/chord-plugins-node-evaluator.md). Pi evaluates a built script on
// Node's `vm` in the process of the host that loads it; Go has no JavaScript runtime in process (AGENTS.md), so the host
// that loads the plugins is Pi's own chord host in a Node subprocess, linked to the Go host over the chord link wire.
//
// No Pi file corresponds to this one: it is the Go side of what Pi's workers (next-node/workers) do with a forked child.
// The vendored Pi sources (node/pi, with node/manifest.json) and the driver (node/host.mjs) are embedded and extracted once
// per content hash.

//go:embed all:node
var nodeFiles embed.FS

// minimumNode is the first Node that strips TypeScript types by default: Pi's `packages/chord` `engines.node`.
const minimumNode = "v22.19.0"

// defaultReadyTimeout is how long Load waits for the process to start and connect (chord/host's loader waits as long).
const defaultReadyTimeout = 30 * time.Second

// stopGrace is how long a process that was told to close, or whose channel ended, gets to exit before it is killed (Pi's
// workers wait as long: workers.ts).
const stopGrace = 3 * time.Second

// stderrTail is how much of the last of the process's stderr an error keeps.
const stderrTail = 8 << 10

// maxFrame bounds one frame of the channel: a frame longer ends the channel.
const maxFrame = 256 << 20

// Frame kinds of the channel (host.mjs): a chord link message, or a control message (one JSON object).
const (
	frameLink    = 0
	frameControl = 1
)

// NodeStream is the stream of a process's output a chunk came from.
type NodeStream string

const (
	// NodeStdout is the process's standard output.
	NodeStdout NodeStream = "stdout"
	// NodeStderr is the process's standard error.
	NodeStderr NodeStream = "stderr"
)

// NodeOptions is how Node subprocesses are started.
type NodeOptions struct {
	// ExecArgv are Node's own flags for the subprocess (`--max-old-space-size=...`), after the one this component passes
	// (`--disable-warning=ExperimentalWarning`).
	ExecArgv []string
	// CacheDir is where the embedded runtime is extracted (once per content): `pig` under os.UserCacheDir() by default.
	CacheDir string
	// ReadyTimeout is how long starting a process may take until it is connected; 30 s by default.
	ReadyTimeout *time.Duration
	// OnOutput gets what the process writes. Without it, the process's stdout and stderr go to this process's own.
	OnOutput func(stream NodeStream, chunk []byte)
	// OnError gets what the process's host reports in the background (a failed activation, a protocol error): upstream's
	// `onError`.
	OnError func(NodeHostError)
}

// NodeHostError is what the host of a subprocess reported in the background: its kind (upstream's `HostError.kind`), the
// object or address it names, and its error.
type NodeHostError struct {
	Kind    string
	Object  string
	Address string
	Err     error
}

// NodeError is an error thrown in a subprocess, as data: its message, its name, and its stack with the positions of plugin
// scripts mapped through their source maps (`notes/helper.ts:3:8`), as `mapStack` gives it. Chord is set when it is a
// ChordError (`aborted` after a reload's commit carries `details.committed`); Unwrap returns it.
type NodeError struct {
	Name    string
	Message string
	Stack   string
	Chord   *data.ChordError
}

func (err *NodeError) Error() string { return err.Message }

// Unwrap returns the ChordError the subprocess threw, if it threw one.
func (err *NodeError) Unwrap() error {
	if err.Chord == nil {
		return nil
	}
	return err.Chord
}

// NodeExit is why a subprocess ended before it was asked to: its exit, what it reported as fatal, and the last of its
// stderr.
type NodeExit struct {
	Err    error
	Fatal  *NodeError
	Stderr string
}

func (exit *NodeExit) Error() string {
	text := "node subprocess exited"
	if exit.Err != nil {
		text += ": " + exit.Err.Error()
	}
	if exit.Fatal != nil {
		text += ": " + exit.Fatal.Message
	}
	if tail := strings.TrimSpace(exit.Stderr); tail != "" {
		text += "\n" + tail
	}
	return text
}

// Unwrap returns the exit error and the fatal error.
func (exit *NodeExit) Unwrap() []error {
	var errs []error
	if exit.Err != nil {
		errs = append(errs, exit.Err)
	}
	if exit.Fatal != nil {
		errs = append(errs, exit.Fatal)
	}
	return errs
}

// discoverNode finds Node as extension runtimes do: `node` on PATH, at least minimumNode.
func discoverNode(ctx context.Context) (string, error) {
	requirement := fmt.Sprintf("evaluating plugins needs Node.js %s or newer", strings.TrimPrefix(minimumNode, "v"))
	path, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("%s; node was not found on PATH", requirement)
	}
	output, err := linkerexec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("%s; node --version failed: %s", requirement, detail)
	}
	found := strings.TrimSpace(string(output))
	if !semver.IsValid(found) {
		return "", fmt.Errorf("%s; node --version returned %q", requirement, found)
	}
	if semver.Compare(found, minimumNode) < 0 {
		return "", fmt.Errorf("%s; found %s", requirement, found)
	}
	return path, nil
}

// extractNode extracts the embedded tree under cacheDir (once per content hash) and returns its directory. The tree is
// extracted aside and renamed into place: a process that lost the race finds the winner's.
func extractNode(cacheDir string) (string, error) {
	if cacheDir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("the cache directory of the plugins' Node runtime: %w", err)
		}
		cacheDir = filepath.Join(base, "pig")
	}
	manifest, err := nodeFiles.ReadFile("node/manifest.json")
	if err != nil {
		return "", err
	}
	driver, err := nodeFiles.ReadFile("node/host.mjs")
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	sum.Write(manifest)
	sum.Write(driver)
	dir := filepath.Join(cacheDir, "chord-plugins-node-"+hex.EncodeToString(sum.Sum(nil))[:16])
	if _, err := os.Stat(filepath.Join(dir, "host.mjs")); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(cacheDir, ".extract-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	err = fs.WalkDir(nodeFiles, "node", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(stage, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(path, "node"), "/")))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := nodeFiles.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return "", err
	}
	if err := os.Rename(stage, dir); err != nil {
		if _, statErr := os.Stat(filepath.Join(dir, "host.mjs")); statErr == nil {
			return dir, nil
		}
		return "", err
	}
	return dir, nil
}

// framedPort is the IPC channel of a subprocess (link.IpcPort): frames of a length, a kind byte and a payload over one
// connection. Link frames go to the port's listeners, control frames to onControl; both run on the reader's goroutine.
type framedPort struct {
	conn      net.Conn
	onControl func(payload []byte)

	writeMu sync.Mutex

	mu          sync.Mutex
	connected   bool
	messages    []func(any)
	disconnects []func()
}

func newFramedPort(conn net.Conn, onControl func(payload []byte)) *framedPort {
	return &framedPort{conn: conn, onControl: onControl, connected: true}
}

// read reads frames until the connection ends; run on its own goroutine.
func (p *framedPort) read() {
	reader := bufio.NewReader(p.conn)
	for {
		var length [4]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			p.ended()
			return
		}
		size := binary.BigEndian.Uint32(length[:])
		if size == 0 || size > maxFrame {
			p.ended()
			return
		}
		frame := make([]byte, size)
		if _, err := io.ReadFull(reader, frame); err != nil {
			p.ended()
			return
		}
		switch frame[0] {
		case frameLink:
			p.mu.Lock()
			listeners := append([]func(any){}, p.messages...)
			p.mu.Unlock()
			for _, listener := range listeners {
				listener(frame[1:])
			}
		case frameControl:
			p.onControl(frame[1:])
		}
	}
}

// ended ends the channel once: the connection closes and the disconnect listeners run.
func (p *framedPort) ended() {
	p.mu.Lock()
	was := p.connected
	p.connected = false
	listeners := p.disconnects
	p.disconnects = nil
	p.mu.Unlock()
	_ = p.conn.Close()
	if !was {
		return
	}
	for _, listener := range listeners {
		listener()
	}
}

func (p *framedPort) write(kind byte, payload []byte) error {
	frame := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(1+len(payload)))
	frame[4] = kind
	frame = append(frame, payload...)
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err := p.conn.Write(frame)
	return err
}

// Send is link.IpcPort's: one link message.
func (p *framedPort) Send(message []byte, callback func(err error)) (bool, error) {
	err := p.write(frameLink, message)
	callback(err)
	return err == nil, nil
}

// control sends one control message.
func (p *framedPort) control(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return p.write(frameControl, payload)
}

// OnMessage is link.IpcPort's.
func (p *framedPort) OnMessage(listener func(message any)) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, listener)
	return func() {}
}

// OnDisconnect is link.IpcPort's.
func (p *framedPort) OnDisconnect(listener func()) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.disconnects = append(p.disconnects, listener)
	return func() {}
}

// Disconnect is link.IpcPort's.
func (p *framedPort) Disconnect() { p.ended() }

// Connected is link.IpcPort's.
func (p *framedPort) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected
}

var _ link.IpcPort = (*framedPort)(nil)

// NodeHost is a Node subprocess whose host loaded the plugins of one kind: Pi's chord host with Pi's plugin runtime, linked
// to a Go host. It is never restarted by itself: when the process ends, the link closes, every pending request fails with
// a *NodeExit and Exited closes.
type NodeHost struct {
	options NodeOptions
	cmd     *exec.Cmd
	port    atomic.Pointer[framedPort]
	// kind is the facet kind the host was loaded with; owner is the component that loaded it (nil for a PluginRuntime's).
	kind  string
	owner *Plugins

	started chan struct{}
	exited  chan struct{}

	mu       sync.Mutex
	nextID   int
	pending  map[int]*nodeCall
	closing  bool
	exit     error
	fatal    *NodeError
	stderr   tailWriter
	startOne sync.Once
}

// Exited is closed when the process ended.
func (h *NodeHost) Exited() <-chan struct{} { return h.exited }

// Err is why the process ended before Close asked it to (a *NodeExit), or nil.
func (h *NodeHost) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exit
}

// tailWriter keeps the last stderrTail bytes written to it, and forwards every chunk.
type tailWriter struct {
	mu      sync.Mutex
	tail    []byte
	forward func(chunk []byte)
}

func (w *tailWriter) Write(chunk []byte) (int, error) {
	w.mu.Lock()
	w.tail = append(w.tail, chunk...)
	if len(w.tail) > stderrTail {
		w.tail = w.tail[len(w.tail)-stderrTail:]
	}
	w.mu.Unlock()
	if w.forward != nil {
		w.forward(chunk)
	}
	return len(chunk), nil
}

func (w *tailWriter) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.tail)
}

// forwardWriter forwards every chunk written to it.
type forwardWriter func(chunk []byte)

func (f forwardWriter) Write(chunk []byte) (int, error) {
	f(chunk)
	return len(chunk), nil
}

// startNode starts the subprocess and returns once it is connected and said it started.
func startNode(options NodeOptions, ctx context.Context) (*NodeHost, error) {
	nodePath, err := discoverNode(ctx)
	if err != nil {
		return nil, err
	}
	dir, err := extractNode(options.CacheDir)
	if err != nil {
		return nil, err
	}
	listener, address, cleanup, err := listenNode()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	defer func() { _ = listener.Close() }()

	h := &NodeHost{options: options, started: make(chan struct{}), exited: make(chan struct{}), pending: map[int]*nodeCall{}}
	output := options.OnOutput
	if output == nil {
		output = func(stream NodeStream, chunk []byte) {
			if stream == NodeStderr {
				_, _ = os.Stderr.Write(chunk)
			} else {
				_, _ = os.Stdout.Write(chunk)
			}
		}
	}
	args := append([]string{"--disable-warning=ExperimentalWarning"}, options.ExecArgv...)
	args = append(args, filepath.Join(dir, "host.mjs"), address)
	// The process outlives the context that started it: it is stopped by Close, or killed when starting fails.
	h.cmd = linkerexec.CommandContext(context.WithoutCancel(ctx), nodePath, args...)
	h.cmd.Stdout = forwardWriter(func(chunk []byte) { output(NodeStdout, chunk) })
	h.stderr.forward = func(chunk []byte) { output(NodeStderr, chunk) }
	h.cmd.Stderr = &h.stderr
	if err := h.cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting node: %w", err)
	}
	go h.wait()

	timeout := defaultReadyTimeout
	if options.ReadyTimeout != nil {
		timeout = *options.ReadyTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	type accepted struct {
		conn net.Conn
		err  error
	}
	connections := make(chan accepted, 1)
	go func() {
		conn, err := listener.Accept()
		connections <- accepted{conn, err}
	}()
	var conn net.Conn
	select {
	case got := <-connections:
		if got.err != nil {
			h.kill()
			return nil, fmt.Errorf("accepting the node subprocess: %w", got.err)
		}
		conn = got.conn
	case <-h.exited:
		return nil, h.startFailure("the node subprocess ended before it connected")
	case <-deadline.C:
		h.kill()
		return nil, h.startFailure(fmt.Sprintf("the node subprocess did not connect within %s", timeout))
	case <-ctx.Done():
		h.kill()
		return nil, context.Cause(ctx)
	}
	port := newFramedPort(conn, h.control)
	h.port.Store(port)
	go func() {
		port.read()
		h.channelEnded()
	}()
	select {
	case <-h.started:
	case <-h.exited:
		return nil, h.startFailure("the node subprocess ended before it started")
	case <-deadline.C:
		h.kill()
		return nil, h.startFailure(fmt.Sprintf("the node subprocess did not start within %s", timeout))
	case <-ctx.Done():
		h.kill()
		return nil, context.Cause(ctx)
	}
	return h, nil
}

// startFailure is the error of a process that did not come up: what it says, and how it ended with its stderr.
func (h *NodeHost) startFailure(what string) error {
	select {
	case <-h.exited:
		return fmt.Errorf("%s: %w", what, h.Err())
	default:
	}
	return fmt.Errorf("%s\n%s", what, strings.TrimSpace(h.stderr.text()))
}

// wait is the goroutine that waits for the process: it records how it ended, closes Exited and fails every pending
// request.
func (h *NodeHost) wait() {
	err := h.cmd.Wait()
	h.mu.Lock()
	if !h.closing || err != nil {
		h.exit = &NodeExit{Err: err, Fatal: h.fatal, Stderr: h.stderr.text()}
	}
	exit := h.exit
	pending := h.pending
	h.pending = map[int]*nodeCall{}
	h.mu.Unlock()
	if exit == nil {
		exit = errors.New("the node subprocess ended")
	}
	close(h.exited)
	for _, call := range pending {
		call.settle(nodeReply{}, exit)
	}
	if port := h.port.Load(); port != nil {
		port.ended()
	}
}

// channelEnded is the reader's end: the channel is gone, so the process has nothing to serve; it exits by itself, or is
// killed after the grace.
func (h *NodeHost) channelEnded() {
	h.mu.Lock()
	pending := h.pending
	h.pending = map[int]*nodeCall{}
	h.mu.Unlock()
	for _, call := range pending {
		call.settle(nodeReply{}, errors.New("the channel to the node subprocess closed"))
	}
	timer := time.NewTimer(stopGrace)
	defer timer.Stop()
	select {
	case <-h.exited:
	case <-timer.C:
		h.kill()
	}
}

func (h *NodeHost) kill() {
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
}

// nodeReply is a control reply.
type nodeReply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *wireError      `json:"error"`
}

// nodeCall is a control request sent and not answered yet.
type nodeCall struct {
	id    int
	done  chan struct{}
	reply nodeReply
	err   error
	once  sync.Once
}

func (c *nodeCall) settle(reply nodeReply, err error) {
	c.once.Do(func() {
		c.reply, c.err = reply, err
		close(c.done)
	})
}

// envelope is what every control request starts with.
type envelope struct {
	ID int    `json:"id"`
	Op string `json:"op"`
}

func (e *envelope) stamp(id int, op string) { e.ID, e.Op = id, op }

// request is a control request: a struct that embeds an envelope.
type request interface{ stamp(id int, op string) }

// send sends the control request op and returns the pending call. Requests reach the process in the order of the calls.
func (h *NodeHost) send(op string, body request) (*nodeCall, error) {
	h.mu.Lock()
	select {
	case <-h.exited:
		err := h.exit
		h.mu.Unlock()
		if err == nil {
			err = errors.New("the node subprocess ended")
		}
		return nil, err
	default:
	}
	h.nextID++
	call := &nodeCall{id: h.nextID, done: make(chan struct{})}
	h.pending[call.id] = call
	h.mu.Unlock()
	body.stamp(call.id, op)
	payload, err := json.Marshal(body)
	if err != nil {
		h.drop(call)
		return nil, err
	}
	if err := h.port.Load().write(frameControl, payload); err != nil {
		h.drop(call)
		return nil, err
	}
	return call, nil
}

func (h *NodeHost) drop(call *nodeCall) {
	h.mu.Lock()
	delete(h.pending, call.id)
	h.mu.Unlock()
}

// await waits for the reply of call. An abort of ctx is sent to the process, which answers the request promptly (the host
// returns `aborted`, after its commit with what the commit applied): the wait goes on until the answer or the process's
// end.
func (h *NodeHost) await(call *nodeCall, ctx context.Context) (nodeReply, error) {
	stop := context.AfterFunc(ctx, func() {
		_ = h.port.Load().control(map[string]any{"op": "abort", "target": call.id})
	})
	defer stop()
	<-call.done
	h.drop(call)
	if call.err != nil {
		return nodeReply{}, call.err
	}
	if !call.reply.OK {
		return nodeReply{}, call.reply.Error.err()
	}
	return call.reply, nil
}

// control handles one control message of the process.
func (h *NodeHost) control(payload []byte) {
	var message struct {
		ID    *int       `json:"id"`
		Event string     `json:"event"`
		Error *wireError `json:"error"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return
	}
	if message.ID != nil {
		h.mu.Lock()
		call := h.pending[*message.ID]
		h.mu.Unlock()
		if call == nil {
			return
		}
		var reply nodeReply
		if err := json.Unmarshal(payload, &reply); err != nil {
			call.settle(nodeReply{}, err)
			return
		}
		call.settle(reply, nil)
		return
	}
	switch message.Event {
	case "started":
		h.startOne.Do(func() { close(h.started) })
	case "fatal":
		h.mu.Lock()
		h.fatal = message.Error.err()
		h.mu.Unlock()
	case "error":
		if h.options.OnError == nil {
			return
		}
		var event struct {
			Error struct {
				Kind    string     `json:"kind"`
				Object  string     `json:"object"`
				Address string     `json:"address"`
				Error   *wireError `json:"error"`
			} `json:"error"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			return
		}
		var err error
		if event.Error.Error != nil {
			err = event.Error.Error.err()
		}
		h.options.OnError(NodeHostError{Kind: event.Error.Kind, Object: event.Error.Object, Address: event.Error.Address, Err: err})
	}
}

// Close asks the process to close its host and exit, and waits for it (killing it after the grace, or when ctx ends). It
// returns the exit that was a crash, if the process ended that way before.
func (h *NodeHost) Close(ctx context.Context) error {
	h.mu.Lock()
	select {
	case <-h.exited:
		err := h.exit
		h.mu.Unlock()
		return err
	default:
	}
	h.closing = true
	h.mu.Unlock()
	_ = h.port.Load().control(map[string]any{"op": "close"})
	timer := time.NewTimer(stopGrace)
	defer timer.Stop()
	select {
	case <-h.exited:
	case <-timer.C:
		h.kill()
		<-h.exited
	case <-ctx.Done():
		h.kill()
		<-h.exited
		return context.Cause(ctx)
	}
	return h.Err()
}

// wireError is an error as host.mjs sends it.
type wireError struct {
	Name    string          `json:"name"`
	Message string          `json:"message"`
	Stack   string          `json:"stack"`
	Chord   json.RawMessage `json:"chord"`
}

// err is the *NodeError of w.
func (w *wireError) err() *NodeError {
	if w == nil {
		return &NodeError{Name: "Error", Message: "an unknown error"}
	}
	out := &NodeError{Name: w.Name, Message: w.Message, Stack: w.Stack}
	if len(w.Chord) > 0 {
		if chord, ok := chordErrorOf(w.Chord); ok {
			out.Chord = chord
		}
	}
	return out
}

// chordErrorOf decodes the wire form of a ChordError.
func chordErrorOf(raw json.RawMessage) (*data.ChordError, bool) {
	value, err := delta.DecodeJson(raw)
	if err != nil {
		return nil, false
	}
	chord, err := data.ChordErrorFromJson(value)
	return chord, err == nil
}
