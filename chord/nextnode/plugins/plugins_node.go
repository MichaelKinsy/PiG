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
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// The Node subprocess of the plugins component (docs/plan/chord-plugins-node-evaluator.md). Pi evaluates a built script on
// Node's `vm` in the process of the host that loads it; Go has no JavaScript runtime in process (AGENTS.md), so the host
// that loads the plugins is Pi's own chord host in a Node subprocess, the child of a Go host over the chord link wire.
//
// No Pi file corresponds to this one: it is the Go side of what Pi's workers (next-node/workers) do with a forked child: the
// child connects a link to its parent (workers/main.ts), and the server routes the worker's kind to the other end of it
// (workers.ts). Here the other end is NodeHost.Link, and the app routes or accepts it.
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
	// OnError gets what the process's host reports in the background (a failed activation, a protocol error, a close past its
	// timeout, ...): upstream's `onError`, with every field of the kind (NodeHostError).
	OnError func(NodeHostError)
}

// NodeHostError is what the host of a subprocess reported in the background (upstream's `HostError`, which Go's
// host.HostError mirrors): the Kind and the fields that kind lists, as typed values; the others are zero. Err is the error
// of the kinds that carry a plain one (a *NodeError, its Cause chain included), Failure the cause of the kinds that carry a
// failure (activation_failed, object_failed). Kind is the host's: the kinds of host.HostError, of the batch
// (listener_error, delivery_failed) and of the registries (registry_conflict, registry_failed, contribution_failed,
// contribution_refused).
type NodeHostError struct {
	Kind string
	// Object is the object's type, with its params key when it has params.
	Object  string
	Failure data.FailureCause
	Err     error
	// Native and Routine are where a reported part sits on its object (close_failed, close_timeout: Native;
	// routine_failed: Routine).
	Native  host.Position
	Routine host.Position
	// Address is a failed route's address (resolve_failed).
	Address string
	// Member is the exposed state member of a derive_failed, if it is one.
	Member string
	// Job and ID name a job's run (job_failed, job_finish_failed, job_interrupted, job_dropped); Restarts is a
	// job_interrupted's count.
	Job      string
	ID       string
	Restarts int
	// WaitingFor, State and Since are a parked_long's: its first blocker, that blocker's state ("loading" or "failed") and
	// since when (Date.now() time).
	WaitingFor string
	State      string
	Since      int64
	// Registry names the registry of a registry error; Message and Plugins are a registry_conflict's, Plugin, Type (empty
	// for a facet) and Version a contribution's.
	Registry string
	Message  string
	Plugins  []string
	Plugin   string
	Type     string
	Version  int
}

// NodeError is an error thrown in a subprocess, as data: its message, its name, and its stack with the positions of plugin
// scripts mapped through their source maps (`notes/helper.ts:3:8`), as `mapStack` gives it. Cause is its `cause` (the
// chain goes on through the causes' own; a cause that is no Error is an error of its string). Chord is set when it is a
// ChordError (`aborted` after a reload's commit carries `details.committed`). Unwrap gives both, so errors.As finds a
// *data.ChordError anywhere in the chain.
type NodeError struct {
	Name    string
	Message string
	Stack   string
	Cause   *NodeError
	Chord   *data.ChordError
}

func (err *NodeError) Error() string { return err.Message }

// Unwrap returns the ChordError the subprocess threw, if it threw one, and the error's cause, if it has one.
func (err *NodeError) Unwrap() []error {
	var errs []error
	if err.Chord != nil {
		errs = append(errs, err.Chord)
	}
	if err.Cause != nil {
		errs = append(errs, err.Cause)
	}
	return errs
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

// completeMarker is the file extractNode writes last into a tree: its content is the hash the tree's directory is named for.
const completeMarker = ".complete"

// extractNode extracts the embedded tree under cacheDir (once per content hash) and returns its directory. A new tree is
// extracted aside and renamed into place, so a process that lost the race finds the winner's. A directory is trusted when
// its marker (written last) names this content and each of its files has the embedded size; one that is not (a cache that
// was partly deleted, or made by an older version) is repaired in place, file by file, the marker last.
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
	hash := hex.EncodeToString(sum.Sum(nil))
	dir := filepath.Join(cacheDir, "chord-plugins-node-"+hash[:16])
	if nodeTreeComplete(dir, hash) {
		return dir, nil
	}
	if _, err := os.Stat(dir); err == nil {
		if err := repairNodeTree(dir, hash); err != nil {
			return "", err
		}
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
	if err := writeNodeTree(stage, hash, func(string) bool { return true }, os.WriteFile); err != nil {
		return "", err
	}
	if err := os.Rename(stage, dir); err != nil {
		// A process that was first published its tree; one that is partial is repaired.
		if _, statErr := os.Stat(dir); statErr != nil {
			return "", err
		}
		if !nodeTreeComplete(dir, hash) {
			if err := repairNodeTree(dir, hash); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}

// nodeTreeComplete reports whether dir holds the whole embedded tree: its marker names hash and every file has its embedded
// size.
func nodeTreeComplete(dir, hash string) bool {
	marker, err := os.ReadFile(filepath.Join(dir, completeMarker))
	if err != nil || string(marker) != hash {
		return false
	}
	return nodeTreeMissing(dir) == nil
}

// nodeTreeMissing is the embedded files dir lacks, or holds with another size.
func nodeTreeMissing(dir string) map[string]bool {
	var missing map[string]bool
	_ = fs.WalkDir(nodeFiles, "node", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative := nodeRelative(path)
		if stat, err := os.Stat(filepath.Join(dir, relative)); err != nil || stat.Size() != info.Size() {
			if missing == nil {
				missing = map[string]bool{}
			}
			missing[relative] = true
		}
		return nil
	})
	return missing
}

func nodeRelative(path string) string {
	return filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(path, "node"), "/"))
}

// repairNodeTree writes what dir lacks, each file by a rename of a complete one (a process starting from dir meanwhile reads
// whole files, and two repairs write the same bytes), then the marker.
func repairNodeTree(dir, hash string) error {
	missing := nodeTreeMissing(dir)
	return writeNodeTree(dir, hash, func(relative string) bool { return missing[relative] }, writeFileAtomic)
}

// writeNodeTree writes, with write, the embedded files of root that wanted selects (paths relative to it), then the marker.
func writeNodeTree(root, hash string, wanted func(relative string) bool, write func(path string, content []byte, perm os.FileMode) error) error {
	err := fs.WalkDir(nodeFiles, "node", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative := nodeRelative(path)
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(root, relative), 0o755)
		}
		if !wanted(relative) {
			return nil
		}
		content, err := nodeFiles.ReadFile(path)
		if err != nil {
			return err
		}
		return write(filepath.Join(root, relative), content, 0o644)
	})
	if err != nil {
		return err
	}
	return write(filepath.Join(root, completeMarker), []byte(hash), 0o644)
}

// writeFileAtomic writes content to a temporary file next to path and renames it over path.
func writeFileAtomic(path string, content []byte, perm os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(file.Name(), perm)
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		_ = os.Remove(file.Name())
	}
	return err
}

// framedPort is the IPC channel of a subprocess (link.IpcPort): frames of a length, a kind byte and a payload over one
// connection. Link frames go to the port's listeners, control frames to onControl; both run on the reader's goroutine.
type framedPort struct {
	conn      net.Conn
	onControl func(payload []byte)
	// done is closed when read returned: every frame the connection delivered was handled.
	done chan struct{}

	writeMu sync.Mutex

	mu          sync.Mutex
	connected   bool
	messages    []func(any)
	disconnects []func()
}

func newFramedPort(conn net.Conn, onControl func(payload []byte)) *framedPort {
	return &framedPort{conn: conn, onControl: onControl, done: make(chan struct{}), connected: true}
}

// read reads frames until the connection ends; run on its own goroutine.
func (p *framedPort) read() {
	defer close(p.done)
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
	// link is the link over port, made when the channel was: the messages the process's host sends queue in it until the app
	// attaches it to a Go host.
	link link.Link
	// kind is the facet kind the host was loaded with; owner is the component that loaded it (nil for a PluginRuntime's).
	kind  string
	owner *Plugins

	started chan struct{}
	// reaped is closed when the process was waited for; exited, once its channel was read to the end too.
	reaped chan struct{}
	exited chan struct{}

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

// Link is the link to the process's host, the other end of the one that host connected to its parent (Pi's worker `connect`s
// a link to its server). The app attaches it to a Go host as the server of a worker does: `Route` of the host's kind, whose
// Resolve returns it (with the principal the app stamps the host with), or `Accept` with LinkOptions{Principal, Trusted:
// true}. It is one link, made when the process connected: the messages the process sent before are queued in it. The
// process's host closes a link silent for its heartbeat (30 s) and ends with it, so attach it in time. A closed link, from
// either end, ends the process's channel and the process.
func (h *NodeHost) Link() link.Link { return h.link }

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

	h := &NodeHost{options: options, started: make(chan struct{}), reaped: make(chan struct{}), exited: make(chan struct{}), pending: map[int]*nodeCall{}}
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
			h.stop()
			return nil, fmt.Errorf("accepting the node subprocess: %w", got.err)
		}
		conn = got.conn
	case <-h.exited:
		return nil, h.startFailure("the node subprocess ended before it connected")
	case <-deadline.C:
		err := h.startFailure(fmt.Sprintf("the node subprocess did not connect within %s", timeout))
		h.stop()
		return nil, err
	case <-ctx.Done():
		h.stop()
		return nil, context.Cause(ctx)
	}
	port := newFramedPort(conn, h.control)
	h.port.Store(port)
	// Made before the first frame is read: none is lost before the app attaches the link.
	h.link = link.IpcLink(port)
	go func() {
		port.read()
		h.channelEnded()
	}()
	select {
	case <-h.started:
	case <-h.exited:
		return nil, h.startFailure("the node subprocess ended before it started")
	case <-deadline.C:
		err := h.startFailure(fmt.Sprintf("the node subprocess did not start within %s", timeout))
		h.stop()
		return nil, err
	case <-ctx.Done():
		h.stop()
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
// request. The channel is read to its end first (the process closed it when it ended; bounded by stopGrace), so a `fatal`
// the process sent before it exited is in the NodeExit.
func (h *NodeHost) wait() {
	err := h.cmd.Wait()
	close(h.reaped)
	if port := h.port.Load(); port != nil {
		timer := time.NewTimer(stopGrace)
		select {
		case <-port.done:
		case <-timer.C:
		}
		timer.Stop()
	}
	h.mu.Lock()
	if !h.closing || err != nil {
		h.exit = &NodeExit{Err: err, Fatal: h.fatal, Stderr: h.stderr.text()}
	}
	exit := h.exit
	pending := h.pending
	h.pending = map[int]*nodeCall{}
	// Closed under the lock: a request that sees the process alive is in the map the line above emptied or the next one.
	close(h.exited)
	h.mu.Unlock()
	if exit == nil {
		exit = errors.New("the node subprocess ended")
	}
	for _, call := range pending {
		call.settle(nodeReply{}, exit)
	}
	if port := h.port.Load(); port != nil {
		port.ended()
	}
}

// channelEnded is the reader's end: the channel is gone, so the process has nothing to serve; it exits by itself, or is
// killed after the grace. Its exit settles the pending requests (wait), so they fail with how it ended.
func (h *NodeHost) channelEnded() {
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

// stop kills the process and waits until it ended (a process that was killed ends at once; stopGrace bounds the wait for what
// holds its pipes).
func (h *NodeHost) stop() {
	h.kill()
	timer := time.NewTimer(stopGrace)
	defer timer.Stop()
	select {
	case <-h.exited:
	case <-timer.C:
	}
}

// exitError is why the process ended, as a request that fails because of it reports: its NodeExit, or that it ended when it
// was asked to. Call it once h.exited is closed.
func (h *NodeHost) exitError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.exit == nil {
		return errors.New("the node subprocess ended")
	}
	return h.exit
}

// afterChannelEnded is the error of a send that found the channel ended (cause): the process's exit once it is known (it
// exits by itself, or channelEnded kills it after the grace), else cause.
func (h *NodeHost) afterChannelEnded(cause error) error {
	timer := time.NewTimer(2 * stopGrace)
	defer timer.Stop()
	select {
	case <-h.exited:
		return h.exitError()
	case <-timer.C:
		return cause
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
		h.mu.Unlock()
		return nil, h.exitError()
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
		return nil, h.afterChannelEnded(err)
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
			Error hostErrorWire `json:"error"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			return
		}
		h.options.OnError(event.Error.decode())
	}
}

// hostErrorWire is a HostError as host.mjs sends it: every field of the JavaScript value, the errors as wire errors.
type hostErrorWire struct {
	Kind    string `json:"kind"`
	Object  string `json:"object"`
	Failure *struct {
		Kind  data.FailureKind `json:"kind"`
		Error json.RawMessage  `json:"error"`
	} `json:"failure"`
	Error      *wireError    `json:"error"`
	Native     *positionWire `json:"native"`
	Routine    *positionWire `json:"routine"`
	Address    string        `json:"address"`
	Member     string        `json:"member"`
	Job        string        `json:"job"`
	ID         string        `json:"id"`
	Restarts   int           `json:"restarts"`
	WaitingFor string        `json:"waitingFor"`
	State      string        `json:"state"`
	Since      int64         `json:"since"`
	Registry   string        `json:"registry"`
	Message    string        `json:"message"`
	Plugins    []string      `json:"plugins"`
	Plugin     string        `json:"plugin"`
	Type       string        `json:"type"`
	Version    int           `json:"version"`
}

// positionWire is a host.Position as host.mjs sends it.
type positionWire struct {
	Kind     host.PositionKind `json:"kind"`
	Position int               `json:"position"`
}

func (w positionWire) position() host.Position {
	return host.Position{Kind: w.Kind, Position: w.Position}
}

func (w *hostErrorWire) decode() NodeHostError {
	out := NodeHostError{
		Kind: w.Kind, Object: w.Object, Address: w.Address, Member: w.Member, Job: w.Job, ID: w.ID, Restarts: w.Restarts,
		WaitingFor: w.WaitingFor, State: w.State, Since: w.Since, Registry: w.Registry, Message: w.Message,
		Plugins: w.Plugins, Plugin: w.Plugin, Type: w.Type, Version: w.Version,
	}
	if w.Error != nil {
		out.Err = w.Error.err()
	}
	if w.Native != nil {
		out.Native = w.Native.position()
	}
	if w.Routine != nil {
		out.Routine = w.Routine.position()
	}
	if w.Failure != nil {
		if chord, ok := chordErrorOf(w.Failure.Error); ok {
			out.Failure = data.FailureCause{Kind: w.Failure.Kind, Error: chord}
		}
	}
	return out
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

// wireError is an error as host.mjs sends it: Cause is its `cause`, itself a wire error.
type wireError struct {
	Name    string          `json:"name"`
	Message string          `json:"message"`
	Stack   string          `json:"stack"`
	Chord   json.RawMessage `json:"chord"`
	Cause   *wireError      `json:"cause"`
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
	if w.Cause != nil {
		out.Cause = w.Cause.err()
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
