package tui

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// linkServer is a terminal server for the launcher's tests: the base bundle is script, and its link endpoint accepts every socket and
// reports when the launcher closed one.
func linkServer(t *testing.T, script string) (port int, closedByLauncher chan struct{}) {
	t.Helper()
	closedByLauncher = make(chan struct{})
	var closeOnce sync.Once
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_chord/tui/start":
			_, _ = io.WriteString(w, `{"base":"h","script":"/_chord/tui/script","combined":"/_chord/tui/combined"}`)
		case "/_chord/tui/script":
			_, _ = io.WriteString(w, script)
		case "/chord":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					closeOnce.Do(func() { close(closedByLauncher) })
					return
				}
			}
		default:
			_, _ = io.WriteString(w, ``)
		}
	}))
	t.Cleanup(server.Close)
	return server.Listener.Addr().(*net.TCPAddr).Port, closedByLauncher
}

// The context StartTerminal takes ends the client as Stop does (its documentation: "Cancelling ctx ends the client too (as Stop does),
// whenever it happens"): the sandbox's process is ended and its sockets are closed. The loop used to stop on the cancelled context and
// nothing else: the sandbox's process and the link sockets kept running, and Stop found no loop to ask for them. Pi has no such
// cancellation (launcher.ts:105-111 makes an AbortController only the pairing wait reads; stop is `launcher.finish(0)`, :117), so the
// contract is the Go documentation's.
func TestCancellingTheContextEndsTheClientItsSandboxProcessAndItsSockets(t *testing.T) {
	port, closedByLauncher := linkServer(t, strings.Join([]string{
		`const bridge = globalThis.chordBridge;`,
		`bridge.onMessage((message) => { if (message.kind === "socket_opened") bridge.send({ kind: "write", data: "link open" }); });`,
		`bridge.send({ kind: "socket_open", socket: 1 });`,
	}, "\n"))
	l := launch(t, port, TerminalCredentials{Kind: CredentialsToken, Token: "t"})
	// The sandbox is running and its link socket open: the sandbox was told so, and wrote.
	l.waitFor(`link open`)
	launcher := l.running.launcher
	var sandbox *Sandbox
	launcher.sync(func() { sandbox = launcher.sandbox })
	l.cancel()
	select {
	case <-launcher.loopDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not stop when its context was cancelled")
	}
	l.running.Stop()
	select {
	case <-sandbox.exited:
	default:
		t.Fatal("the sandbox's process is still running after the client ended")
	}
	if n := len(launcher.sockets); n != 0 {
		t.Fatalf("%d link sockets are still known to the launcher after the client ended", n)
	}
	select {
	case <-closedByLauncher:
	case <-time.After(10 * time.Second):
		t.Fatal("the link socket's connection is still open after the client ended")
	}
	if code := l.exited(); code != 0 {
		t.Fatalf("exit code = %d, want 0 (as Stop)", code)
	}
}

// quietSandbox is a sandbox with no process: what is posted to it waits in its queue.
func quietSandbox() *Sandbox {
	gone := make(chan struct{})
	close(gone)
	s := &Sandbox{exited: gone}
	s.ready = sync.NewCond(&s.mu)
	return s
}

// posted is whether message was posted to sandbox.
func posted(sandbox *Sandbox, message LauncherMessage) bool {
	frame, err := LauncherFrame(message)
	if err != nil {
		panic(err)
	}
	sandbox.mu.Lock()
	defer sandbox.mu.Unlock()
	return slices.ContainsFunc(sandbox.queue, func(queued []byte) bool { return bytes.Equal(queued, frame) })
}

// startingLauncher is a launcher on a server that serves the terminal's files, whose sandboxes come from spawn. Its loop is not
// running: the test plays it (play), so an event that panics fails the test instead of ending the process.
func startingLauncher(t *testing.T, terminal Terminal, spawn func(node, source string, start BridgeStart, onMessage func(SandboxMessage), onEnd func(reason string)) (*Sandbox, error)) *Launcher {
	t.Helper()
	port, _ := linkServer(t, ``)
	base, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	l := newLauncher(context.Background(), base, "token",
		TerminalOptions{Client: http.DefaultClient, Colors: "256", Mode: "dark", Locales: []string{"en"}, spawn: spawn}, terminal)
	t.Cleanup(func() {
		l.closeSockets()
		l.cancel()
		// Frees a starter still waiting for the loop.
		close(l.loopDone)
	})
	return l
}

// runEvent runs one event of the launcher on the test's goroutine, as the loop does. An event that panics fails the test with the
// panic's text.
func runEvent(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("an event on the loop panicked: %v", r)
		}
	}()
	fn()
}

// play runs the launcher's events on the test's goroutine until stop reports true (asked before each event).
func play(t *testing.T, l *Launcher, stop func() bool) {
	t.Helper()
	for !stop() {
		select {
		case fn := <-l.events:
			runEvent(t, fn)
		case <-time.After(10 * time.Second):
			t.Fatal("nothing was posted to the loop")
		}
	}
}

// startOn starts the launcher's sandbox on a goroutine of its own and plays the loop until the start returned and done reports true.
func startOn(t *testing.T, l *Launcher, done func() bool) {
	t.Helper()
	started := make(chan error, 1)
	go func() { started <- l.startSandbox(l.ctx) }()
	returned := false
	deadline := time.After(10 * time.Second)
	for !returned || !done() {
		select {
		case fn := <-l.events:
			runEvent(t, fn)
		case err := <-started:
			if err != nil {
				t.Fatal(err)
			}
			returned = true
		case <-deadline:
			t.Fatal("the sandbox's start did not complete")
		}
	}
}

// A sandbox's first message can reach the loop before the starter installed the sandbox: its first `socket_open` made the launcher
// open a socket for the sandbox nil, and the dial's end then posted to it: a nil pointer panic that ended the process (and, before a
// socket, a request was dropped unanswered). Pi's launcher assigns the sandbox in the continuation of `Sandbox.start`, which runs
// before any stdout 'data' event (launcher.ts:240, sandbox.ts:183), so every message is handled with the sandbox set.
func TestASandboxsFirstMessageThatComesBeforeItIsInstalledIsHandledWithItInstalled(t *testing.T) {
	fake := quietSandbox()
	terminal := &orderTerminal{}
	var deliver func(SandboxMessage)
	l := startingLauncher(t, terminal, func(_, _ string, _ BridgeStart, onMessage func(SandboxMessage), _ func(string)) (*Sandbox, error) {
		deliver = onMessage
		onMessage(SandboxMessage{Kind: "socket_open", Socket: 1})
		return fake, nil
	})
	startOn(t, l, func() bool { return true })
	if l.sandbox != fake {
		t.Fatal("the sandbox is not installed")
	}
	// Its first message was handled when it was installed (the socket is registered by then), with the sandbox set.
	if l.sockets[1] == nil {
		t.Fatal("the socket the sandbox asked for before it was installed was not opened for it")
	}
	play(t, l, func() bool { return posted(fake, LauncherMessage{Kind: "socket_opened", Socket: 1}) })
	// Once installed, what it sends is handled as it comes.
	deliver(SandboxMessage{Kind: "write", Data: "after"})
	play(t, l, func() bool { return strings.Contains(terminal.written(), "after") })
}

// What a sandbox sent before it was installed is held, in order, as many messages as the loop's queue held before; the rest are dropped.
func TestAtMostAQueuesWorthOfMessagesAreHeldForASandboxThatIsNotInstalledYet(t *testing.T) {
	l := newLauncher(context.Background(), nil, "token", TerminalOptions{}, nullTerminal{})
	t.Cleanup(l.cancel)
	l.generation, l.starting = 1, true
	for i := range maxEarly + 5 {
		l.message(1, SandboxMessage{Kind: "write", Data: strconv.Itoa(i)})
	}
	if len(l.early) != maxEarly || l.early[0].Data != "0" || l.early[maxEarly-1].Data != strconv.Itoa(maxEarly-1) {
		t.Fatalf("%d messages are held, want the first %d", len(l.early), maxEarly)
	}
}

// What a sandbox sent before a restart replaced it is never handled for the next sandbox.
func TestWhatASandboxSentBeforeARestartReplacedItIsNotHandledForTheNext(t *testing.T) {
	terminal := &orderTerminal{}
	l := startingLauncher(t, terminal, func(_, _ string, _ BridgeStart, _ func(SandboxMessage), _ func(string)) (*Sandbox, error) {
		return quietSandbox(), nil
	})
	// The first sandbox's message is held; its start never completed.
	l.generation, l.starting = 1, true
	l.message(1, SandboxMessage{Kind: "write", Data: "stale"})
	startOn(t, l, func() bool { return true })
	if got := terminal.written(); strings.Contains(got, "stale") {
		t.Fatalf("the terminal was written %q: a message of a sandbox that was replaced was handled", got)
	}
}
