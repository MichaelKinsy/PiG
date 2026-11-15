package tui

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
