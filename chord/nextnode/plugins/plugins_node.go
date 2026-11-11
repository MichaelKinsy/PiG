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

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
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
// SocketOwnerEnv names the environment variable that tells a process who started it, for the directories its NodeHosts make:
// the process that starts a worker sets it to a NewSocketOwner and removes what the worker left (RemoveSocketDirsOf); the worker
// takes it out of its environment at start (AdoptSocketOwner).
const SocketOwnerEnv = "PIG_CHORD_SOCKET_OWNER"

const defaultReadyTimeout = 30 * time.Second

// defaultStopGrace is NodeOptions.StopGrace's default: Pi's workers' GRACE (workers.ts), 5 s.
const defaultStopGrace = 5 * time.Second

// defaultCloseTimeout is a Pi host's default `closeTimeout` (host README: 30 s), how long NodeHost.Close lets a host that is
// running its close take when RuntimeLoad.CloseTimeout is not set.
const defaultCloseTimeout = 30 * time.Second

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

// NodeOptions is how Node subprocesses are started. The Node they run on is found by discoverNode (additive record D221): PIG_NODE,
// then `node` on PATH, then the install locations of version managers and installers (see discoverNode for the order and
// the list), the first that is Node 22.19.0 or newer.
type NodeOptions struct {
	// ExecArgv are Node's own flags for the subprocess (`--max-old-space-size=...`), after the one this component passes
	// (`--disable-warning=ExperimentalWarning`).
	ExecArgv []string
	// CacheDir is where the embedded runtime is extracted (once per content): `pig` under os.UserCacheDir() by default.
	CacheDir string
	// ReadyTimeout is how long finding Node (each `--version` probe also stops at 10 s) and, after it, starting the process may
	// each take, the second until it is connected and said it started; 30 s by default.
	ReadyTimeout *time.Duration
	// OnOutput gets what the process writes. Without it, the process's stdout and stderr go to this process's own.
	OnOutput func(stream NodeStream, chunk []byte)
	// OnActivity gets the keep-alive counts of the process's host (`host.activity`): its objects' references, calls, jobs and
	// lingering objects, once at the start and at each change. It runs on the goroutine that reads the channel and must not
	// block. A supervisor that stops idle workers reads them here: a Go host that routes to a NodeHost counts none of its
	// objects' work.
	OnActivity func(host.ActivityCounts)
	// OnError gets what the process's host reports in the background (a failed activation, a protocol error, a close past its
	// timeout, ...): upstream's `onError`, with every field of the kind (NodeHostError).
	OnError func(NodeHostError)
	// Command makes the command that starts a program (Node, to find its version and to run the subprocess): os/exec's
	// CommandContext by default. A platform that cannot execute a file in the app's data directory starts it through its own
	// loader here; the chord packages know no platform.
	Command func(ctx context.Context, name string, args ...string) *exec.Cmd
	// pig additive (D223): the stop grace and the spare process (KeepWarm) are the embedding program's settings.
	// StopGrace is how long a process whose channel ended, or that was told to close, gets to exit before it is killed (Pi's
	// workers wait GRACE = 5 s: workers.ts); 5 s by default. A process stalled in a loop cannot read its channel, so this is how
	// long the Go side waits after it closed the link (a heartbeat that found the host silent, for one) before it kills it. A
	// process that answers a Close with `closing` is running its host's close: Pi ends every object at once, then drains and
	// closes them one by one, each step bounded by `closeTimeout`, and the app's store closes after it. Close then waits one
	// closeTimeout (RuntimeLoad.CloseTimeout or 30 s) plus this grace for the exit, not one per object.
	StopGrace *time.Duration
	// KeepWarm makes the PluginRuntime start a spare process in the background each time a Load has taken its process, so
	// the next Load finds one up (PluginRuntime.Warm). Close ends it.
	KeepWarm bool
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

// pig additive (D223): how the subprocess ended; Pi's in-process plugins end the app instead.
// NodeExit is why a subprocess ended before it was asked to: its exit, what it reported as fatal, and the last of its
// stderr. Pi's plugins run in the app's process, where an uncaught exception ends the app with code 1, `process.exit(n)` with
// n and a loop stalls it; here they end or stall only the subprocess, and the NodeExit is what Node's `exit` event
// (`code`, `signal`) gives for it.
type NodeExit struct {
	// Err is the process's exit error (an *exec.ExitError), nil for an exit with code 0.
	Err error
	// Fatal is the uncaught exception or rejection the process reported before it ended, nil if it reported none.
	Fatal *NodeError
	// Stderr is the last of the process's stderr, which includes what it printed for a Fatal.
	Stderr string
	// Code is the process's exit code: 1 after an uncaught exception, n after `process.exit(n)`, -1 when a signal ended it.
	Code int
	// Signal is Node's name of the signal that ended the process (`SIGKILL`), empty when it exited.
	Signal string
	// Reason is why the Go side ended the process, when it did: its link closed (a heartbeat that found the host silent, a
	// closed link) and it did not exit within the stop grace. Empty when the process ended by itself.
	Reason string
}

func (exit *NodeExit) Error() string {
	text := "node subprocess exited"
	if exit.Err != nil {
		text += ": " + exit.Err.Error()
	}
	if exit.Fatal != nil {
		text += ": " + exit.Fatal.Message
	}
	if exit.Reason != "" {
		text += ": " + exit.Reason
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

// write sends one frame. A frame past maxFrame ends the channel at the reader, and its length would wrap the four bytes it is
// written in from 4 GiB on, so it is refused before any byte is sent.
func (p *framedPort) write(kind byte, payload []byte) error {
	if len(payload) >= maxFrame {
		return fmt.Errorf("a frame of %d bytes is longer than the channel's limit of %d", 1+len(payload), maxFrame)
	}
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
	link *nodeLink
	// kind is the facet kind the host was loaded with; owner is the component that loaded it (nil for a PluginRuntime's).
	kind  string
	owner *Plugins

	// life is the context of the work the process asked of this side (the bridged proxies' requests, NodeMember.Value): it
	// ends once the process ended and its pending requests settled. It carries the values of the context that started the
	// host, not its cancellation.
	life    context.Context
	endLife context.CancelFunc

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
	activity host.ActivityCounts
	// closeTimeout is how long the host's own close may take once it began (RuntimeLoad.CloseTimeout); closeBegun is closed
	// when the process reported that it began it (`closing`).
	closeTimeout time.Duration
	closeBegun   chan struct{}
	closeOnce    sync.Once
	// startAfter starts the timers of Close's kill deadlines: it returns the channel a deadline passes on and the timer's stop.
	// It is afterTimer, which wall time runs; a test replaces it to order a deadline against the process's answer, since how
	// long a process takes to answer a Close is not the test's to choose.
	startAfter func(d time.Duration) (<-chan time.Time, func())
	// killReason is why the Go side killed the process, if it did (NodeExit.Reason).
	killReason string
	// killed is whether Process.Kill ended it: Windows has no signal in an exit status, and Node reports a process its
	// parent killed as ended by a signal (processExit).
	killed   atomic.Bool
	stderr   tailWriter
	startOne sync.Once
	// cb is what the process calls on the Go side: functions, synchronous callbacks and telemetry (plugins_node_callbacks.go).
	cb nodeCallbacks
	// bridge is what the app holds of the plugins' host through the reference RPC (plugins_node_reference.go).
	bridge nodeBridge
}

// Exited is closed when the process ended.
func (h *NodeHost) Exited() <-chan struct{} { return h.exited }

// Link is the link to the process's host, the other end of the one that host connects to its parent (Pi's worker `connect`s a
// link to its server). The app attaches it to a Go host as the server of a worker does: `Route` of the host's kind, whose
// Resolve returns it (with the principal the app stamps the host with), or `Accept` with LinkOptions{Principal, Trusted:
// true}. It is one link, made when the process connected. It is attached lazily: the first use of it by the Go host
// (OnMessage or Send) makes the process's host connect its end and start its heartbeat (RuntimeLoad.Heartbeat), which closes
// a link silent for its window and ends the process with it. A NodeHost whose link nothing uses has no link and no
// heartbeat: it stays alive until Close, and the link can be attached at any later time. A closed link, from either end,
// ends the process's channel and the process.
func (h *NodeHost) Link() link.Link { return h.link }

// Activity is the keep-alive counts of the process's host as last reported (NodeOptions.OnActivity gives each change).
func (h *NodeHost) Activity() host.ActivityCounts {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.activity
}

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
	timeout := defaultReadyTimeout
	if options.ReadyTimeout != nil {
		timeout = *options.ReadyTimeout
	}
	finding, stopFinding := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("finding a Node.js executable did not finish within %s", timeout))
	nodePath, err := discoverNode(finding, options.Command)
	stopFinding()
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
	// The listener outlives a successful start: the process's worker thread connects to it when the host needs synchronous
	// callbacks (plugins_node_callbacks.go), so NodeHost owns it from then on.
	kept := false
	defer func() {
		if !kept {
			_ = listener.Close()
			cleanup()
		}
	}()

	life, endLife := context.WithCancel(context.WithoutCancel(ctx))
	h := &NodeHost{options: options, life: life, endLife: endLife, started: make(chan struct{}), reaped: make(chan struct{}), exited: make(chan struct{}), closeBegun: make(chan struct{}), closeTimeout: defaultCloseTimeout, startAfter: afterTimer, pending: map[int]*nodeCall{}}
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
	command := options.Command
	if command == nil {
		command = exec.CommandContext
	}
	h.cmd = command(context.WithoutCancel(ctx), nodePath, args...)
	h.cmd.Stdout = forwardWriter(func(chunk []byte) { output(NodeStdout, chunk) })
	h.stderr.forward = func(chunk []byte) { output(NodeStderr, chunk) }
	h.cmd.Stderr = &h.stderr
	if err := h.cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting node: %w", err)
	}
	//portlint:allow golifetime ends with the process: wait returns once cmd.Wait does, which the process's exit, Close and stop bring about
	go h.wait()

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
	h.link = newNodeLink(h, link.IpcLink(port))
	//portlint:allow golifetime ends with the process's channel: read returns when the socket closes, which the process's exit, Close and stop bring about
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
	h.keepListener(listener, cleanup)
	kept = true
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
// request. The channel is read to its end first (the process closed it when it ended; bounded by the stop grace), so a `fatal`
// the process sent before it exited is in the NodeExit.
func (h *NodeHost) wait() {
	err := h.cmd.Wait()
	close(h.reaped)
	if port := h.port.Load(); port != nil {
		timer := time.NewTimer(h.stopGrace())
		select {
		case <-port.done:
		case <-timer.C:
		}
		timer.Stop()
	}
	h.mu.Lock()
	if !h.closing || err != nil {
		code, signal := processExit(h.cmd.ProcessState, h.killed.Load())
		h.exit = &NodeExit{Err: err, Fatal: h.fatal, Stderr: h.stderr.text(), Code: code, Signal: signal, Reason: h.killReason}
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
	h.endLife()
}

// channelEnded is the reader's end: the channel is gone, so the process has nothing to serve; it exits by itself, or is
// killed after the grace. A process that began the close it was asked for (`closing`: its host ends the channel while it
// drains and closes its natives, then closes the app's store) has its closeTimeout first. Its exit settles the pending
// requests (wait), so they fail with how it ended.
func (h *NodeHost) channelEnded() {
	wait := h.stopGrace()
	select {
	case <-h.closeBegun:
		wait += h.closeTimeout
	default:
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-h.exited:
	case <-timer.C:
		h.mu.Lock()
		h.killReason = fmt.Sprintf("its link closed and it did not exit within %s", wait)
		h.mu.Unlock()
		h.kill()
	}
}

// stopGrace is how long the process gets to exit after its channel ended or it was told to close.
func (h *NodeHost) stopGrace() time.Duration {
	if h.options.StopGrace != nil {
		return *h.options.StopGrace
	}
	return defaultStopGrace
}

func (h *NodeHost) kill() {
	if h.cmd.Process != nil && h.cmd.Process.Kill() == nil {
		h.killed.Store(true)
	}
}

// stop kills the process and waits until it ended (a process that was killed ends at once; the stop grace bounds the wait for what
// holds its pipes).
func (h *NodeHost) stop() {
	h.kill()
	timer := time.NewTimer(h.stopGrace())
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
	timer := time.NewTimer(2 * h.stopGrace())
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
	if err := h.refuseSyncCallback(ctx); err != nil {
		h.drop(call)
		return nodeReply{}, err
	}
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
	// A reply has an id and no event; the requests the Node host's proxies make of the Go host are events with an id.
	if message.ID != nil && message.Event == "" {
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
	case "call", "cancel", "telemetry":
		h.callbackEvent(message.Event, payload)
	case "started":
		h.startOne.Do(func() { close(h.started) })
	case "closing":
		h.closeOnce.Do(func() { close(h.closeBegun) })
	case "activity":
		var event struct {
			Counts struct {
				References int `json:"references"`
				Calls      int `json:"calls"`
				Jobs       int `json:"jobs"`
				Lingering  int `json:"lingering"`
			} `json:"counts"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			return
		}
		counts := host.ActivityCounts(event.Counts)
		h.mu.Lock()
		h.activity = counts
		h.mu.Unlock()
		if h.options.OnActivity != nil {
			h.options.OnActivity(counts)
		}
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
	default:
		h.bridgeEvent(message.Event, payload)
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

// afterTimer is NodeHost.startAfter's: a timer of wall time.
func afterTimer(d time.Duration) (<-chan time.Time, func()) {
	timer := time.NewTimer(d)
	return timer.C, func() { timer.Stop() }
}

// Close asks the process to close its host and exit, and waits for it. A process that does not answer within the stop grace
// is killed, as is one still running its close after its closeTimeout and the grace, or when ctx ends. It returns the exit
// that was a crash, if the process ended that way before. The grace runs from the `close` the process was sent: an answer that
// comes later than that, because the process was stalled or starved, is no answer within it.
func (h *NodeHost) Close(ctx context.Context) error {
	if err := h.refuseSyncCallback(ctx); err != nil {
		return err
	}
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
	grace := h.stopGrace()
	deadline, stop := h.startAfter(grace)
	defer func() { stop() }()
	begun := h.closeBegun
	for {
		select {
		case <-h.exited:
			return h.Err()
		case <-begun:
			// The host is running its close, which ends with the app's store: it has its closeTimeout, then the grace.
			begun = nil
			stop()
			deadline, stop = h.startAfter(h.closeTimeout + grace)
		case <-deadline:
			h.kill()
			<-h.exited
			return h.Err()
		case <-ctx.Done():
			h.kill()
			<-h.exited
			return context.Cause(ctx)
		}
	}
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
