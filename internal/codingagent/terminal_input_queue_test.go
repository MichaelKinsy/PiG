package codingagent

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

// remoteInputExtension stands in for a subprocess extension on the far side of
// a real subprocess.Conn. It answers heartbeats itself and hands every
// terminal_input request to the test, which decides when and how to answer.
type remoteInputExtension struct {
	peer     net.Conn
	writeMu  sync.Mutex
	requests chan remoteInputRequest
	cancels  chan string
}

type remoteInputRequest struct {
	id   string
	data string
}

// attachRemoteInputExtension subscribes a fake subprocess extension to m's
// terminal input through the production bridge path: the UIBridge's
// ui.onTerminalInput call handler registering on m's ExtUIContext.
func attachRemoteInputExtension(t *testing.T, m *InteractiveMode, name string) *remoteInputExtension {
	t.Helper()
	hostEnd, extEnd := net.Pipe()
	conn := subprocess.NewConn(name, hostEnd)
	ctx, cancel := context.WithCancel(context.Background())
	conn.Start(ctx)
	ext := &remoteInputExtension{
		peer:     extEnd,
		requests: make(chan remoteInputRequest, 64),
		cancels:  make(chan string, 64),
	}
	go ext.serve()
	t.Cleanup(func() {
		cancel()
		_ = extEnd.Close()
		_ = hostEnd.Close()
	})

	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: m})
	bridge.RegisterExtConn(name, conn)
	result, err := bridge.HandleCallFrom(name, conn, &subprocess.CallPayload{Method: "ui.onTerminalInput"})
	if err != nil || result == nil || result.Error != nil {
		t.Fatalf("ui.onTerminalInput = %+v, %v", result, err)
	}
	return ext
}

// close drops the extension's end of the socket, as a crashed process does.
func (e *remoteInputExtension) close() { _ = e.peer.Close() }

func (e *remoteInputExtension) serve() {
	for {
		env, err := readInputQueueFrame(e.peer)
		if err != nil {
			return
		}
		switch {
		case env.Type == subprocess.MsgPing && env.Ping != nil:
			e.write(&subprocess.Envelope{Type: subprocess.MsgPong, Pong: &subprocess.PongPayload{Nonce: env.Ping.Nonce}})
		case env.Type == subprocess.MsgCancel && env.Cancel != nil:
			e.cancels <- env.Cancel.RequestID
		case env.Type == subprocess.MsgRequest && env.Request != nil && env.Request.Method == "terminal_input":
			var args struct {
				Data string `json:"data"`
			}
			_ = json.Unmarshal(env.Request.Args, &args)
			e.requests <- remoteInputRequest{id: env.ID, data: args.Data}
		}
	}
}

// next returns the next terminal_input request the host sent.
func (e *remoteInputExtension) next(t *testing.T, want string) remoteInputRequest {
	t.Helper()
	select {
	case req := <-e.requests:
		if req.data != want {
			t.Fatalf("extension was asked about %q, want %q", req.data, want)
		}
		return req
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("extension was never asked about %q", want)
		return remoteInputRequest{}
	}
}

// answer sends the extension's verdict for req.
func (e *remoteInputExtension) answer(t *testing.T, req remoteInputRequest, verdict map[string]any) {
	t.Helper()
	result, err := json.Marshal(verdict)
	if err != nil {
		t.Fatal(err)
	}
	e.write(&subprocess.Envelope{Type: subprocess.MsgResponse, ID: req.id, Response: &subprocess.ResponsePayload{Result: result}})
}

func (e *remoteInputExtension) write(env *subprocess.Envelope) {
	payload, err := json.Marshal(env)
	if err != nil {
		return
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	if _, err := e.peer.Write(size[:]); err != nil {
		return
	}
	_, _ = e.peer.Write(payload)
}

func readInputQueueFrame(r io.Reader) (*subprocess.Envelope, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	payload := make([]byte, binary.BigEndian.Uint32(size[:]))
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	var env subprocess.Envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// inputQueueMode is an InteractiveMode whose production input loop reads from
// a pipe the test writes.
type inputQueueMode struct {
	*InteractiveMode
	stdin *io.PipeWriter
	stop  context.CancelFunc
	// done closes when the input loop returns.
	done chan struct{}
}

func newInputQueueMode(t *testing.T) *inputQueueMode {
	t.Helper()
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(nil, "", nil)
	m.editor = tui.NewEditor()
	m.keybindings = DefaultKeybindingsManager()
	m.slashRegistry = NewSlashRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	m.backgroundCtx, m.backgroundCancel = context.WithCancel(ctx)
	return &inputQueueMode{InteractiveMode: m, stop: cancel, done: make(chan struct{})}
}

// start runs the production input loop. Stopping it cancels the mode, as Run
// does, and waits until every background task has drained.
func (q *inputQueueMode) start(t *testing.T) {
	t.Helper()
	source, stdin := io.Pipe()
	q.stdin = stdin
	go func() {
		defer close(q.done)
		_ = q.inputLoop(q.runCtx, source)
	}()
	t.Cleanup(func() {
		q.stop()
		_ = stdin.Close()
		select {
		case <-q.done:
		case <-time.After(testbudget.Wait(t)):
			t.Error("input loop did not stop")
			return
		}
		q.backgroundCancel()
		drained := make(chan struct{})
		go func() {
			q.backgroundTasks.Wait()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(testbudget.Wait(t)):
			t.Error("background input work did not drain")
		}
	})
}

// typeKeys writes keys to the terminal. It fails when the input pump stops
// reading the terminal, which a pump parked behind a pending verdict does.
func (q *inputQueueMode) typeKeys(t *testing.T, keys string) {
	t.Helper()
	written := make(chan error, 1)
	go func() {
		_, err := q.stdin.Write([]byte(keys))
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("write %q: %v", keys, err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("the terminal was not read: %q is still unread", keys)
	}
}

// runOnQueueLoop runs fn on the owner loop and returns its result. It fails when the
// loop cannot run a posted task, which is what a loop blocked on extension IPC
// looks like.
func runOnQueueLoop[T any](t *testing.T, m *InteractiveMode, fn func() T) T {
	t.Helper()
	result := make(chan T, 1)
	if !m.postUITask(func() { result <- fn() }) {
		t.Fatal("owner loop task queue is full")
	}
	select {
	case v := <-result:
		return v
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("owner loop did not run a posted task while a terminal-input verdict was pending")
		var zero T
		return zero
	}
}

// watchTerminalInput registers an in-process listener after the remote one
// and reports every chunk that reaches it, on the owner loop.
func watchTerminalInput(m *InteractiveMode) <-chan string {
	seen := make(chan string, 64)
	(&ExtUIContext{m: m}).OnTerminalInput(func(data string) extension.TerminalInputResult {
		seen <- data
		return extension.TerminalInputResult{}
	})
	return seen
}

func expectSeen(t *testing.T, seen <-chan string, want string) {
	t.Helper()
	select {
	case got := <-seen:
		if got != want {
			t.Fatalf("next listener saw %q, want %q", got, want)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("next listener never saw %q", want)
	}
}

// Upstream tui.ts runs terminal-input listeners synchronously, in order, before
// normal handling: a listener's data replaces the chunk and input after it
// waits. A subprocess listener's verdict crosses a socket, so the owner loop
// must not block on it: rendering, agent events, and posted UI work keep
// running while it is pending, and input typed after the chunk still waits, so
// verdicts, rewrites, and handling keep upstream's order.
func TestSubprocessTerminalInputVerdictLeavesOwnerLoopLive(t *testing.T) {
	q := newInputQueueMode(t)
	ext := attachRemoteInputExtension(t, q.InteractiveMode, "remapper")
	seen := watchTerminalInput(q.InteractiveMode)
	q.start(t)

	q.typeKeys(t, "a")
	ext.answer(t, ext.next(t, "a"), map[string]any{})
	expectSeen(t, seen, "a")

	q.typeKeys(t, "j")
	pending := ext.next(t, "j")
	q.typeKeys(t, "b")
	if got := runOnQueueLoop(t, q.InteractiveMode, func() string { return q.editor.Text() }); got != "a" {
		t.Fatalf("editor = %q while the verdict for j is pending, want %q: input after j must wait", got, "a")
	}
	select {
	case req := <-ext.requests:
		t.Fatalf("extension was asked about %q before it answered j", req.data)
	default:
	}

	ext.answer(t, pending, map[string]any{"data": "x"})
	expectSeen(t, seen, "x")
	ext.answer(t, ext.next(t, "b"), map[string]any{"consume": false})
	expectSeen(t, seen, "b")
	if got := runOnQueueLoop(t, q.InteractiveMode, func() string { return q.editor.Text() }); got != "axb" {
		t.Fatalf("editor = %q, want %q", got, "axb")
	}
}

// Mode shutdown cancels a pending verdict request: the extension receives a
// cancel for it and the input worker drains, instead of the loop waiting on an
// extension that may never answer.
func TestInputLoopShutdownCancelsPendingTerminalInputVerdict(t *testing.T) {
	q := newInputQueueMode(t)
	ext := attachRemoteInputExtension(t, q.InteractiveMode, "slow")
	q.start(t)

	q.typeKeys(t, "j")
	pending := ext.next(t, "j")
	q.stop()
	select {
	case <-q.done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("input loop did not stop while a terminal-input verdict was pending")
	}
	q.backgroundCancel()
	select {
	case id := <-ext.cancels:
		if id != pending.id {
			t.Fatalf("cancelled request %q, want %q", id, pending.id)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the pending terminal_input request was never cancelled")
	}
}

// A verdict that fails, here because the extension's connection closes, leaves
// the chunk unchanged, and input queued behind it follows in order.
func TestFailedTerminalInputVerdictDeliversInputInOrder(t *testing.T) {
	q := newInputQueueMode(t)
	ext := attachRemoteInputExtension(t, q.InteractiveMode, "crashing")
	seen := watchTerminalInput(q.InteractiveMode)
	q.start(t)

	q.typeKeys(t, "j")
	ext.next(t, "j")
	q.typeKeys(t, "b")
	ext.close()
	expectSeen(t, seen, "j")
	expectSeen(t, seen, "b")
	if got := runOnQueueLoop(t, q.InteractiveMode, func() string { return q.editor.Text() }); got != "jb" {
		t.Fatalf("editor = %q, want %q", got, "jb")
	}
}

// The complete path with a real extension: Pi's TypeScript API on the Node
// runtime, the subprocess host and bridge, and the production input loop. The
// extension remaps j to x and consumes q, as an upstream listener does in
// process, and every keystroke reaches the editor in the order it was typed.
func TestNodeTerminalInputListenerRewritesKeysInOrder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "pig-home"))
	source := filepath.Join(root, "remap")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"type":"module","main":"main.js"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	const extensionSource = `export default function register(pi) {
  pi.registerCommand("remap", {
    description: "Remap j to x and consume q",
    handler: async (_args, ctx) => {
      ctx.ui.onTerminalInput((data) => {
        if (data === "j") return { data: "x" };
        if (data === "q") return { consume: true };
        return undefined;
      });
    },
  });
}
`
	if err := os.WriteFile(filepath.Join(source, "main.js"), []byte(extensionSource), 0o644); err != nil {
		t.Fatal(err)
	}

	q := newInputQueueMode(t)
	host := subprocess.NewHost(root)
	host.SetMode("tui")
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: q.InteractiveMode})
	host.SetUIBridge(bridge)
	ctx := testbudget.Context(t)
	ext, err := host.Load(ctx, subprocess.ExtConfig{Name: "remap", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Shutdown("test complete")
	if err := ext.Commands["remap"].Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// The command returns once the extension sent ui.onTerminalInput, which
	// the host handles on its own goroutine.
	waitTerminalInputListeners(t, q.InteractiveMode, 1)
	seen := watchTerminalInput(q.InteractiveMode)
	q.start(t)

	q.typeKeys(t, "ajbqc")
	for _, want := range []string{"a", "x", "b", "c"} {
		expectSeen(t, seen, want)
	}
	if got := runOnQueueLoop(t, q.InteractiveMode, func() string { return q.editor.Text() }); got != "axbc" {
		t.Fatalf("editor = %q, want %q", got, "axbc")
	}
}

func waitTerminalInputListeners(t *testing.T, m *InteractiveMode, want int) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		m.terminalInputMu.Lock()
		got := len(m.terminalInputListeners)
		m.terminalInputMu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d terminal-input listeners registered, want %d", got, want)
		}
		runtime.Gosched()
	}
}
