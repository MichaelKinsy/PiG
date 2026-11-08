package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The launcher's loop is one goroutine; the tests below play it (they call what the loop runs, one at a time, and run the
// functions posted to it themselves). Nothing the sandbox sends may end the launcher (Opus E3).

// nextEvent is the next function posted to the loop.
func nextEvent(t *testing.T, l *Launcher) func() {
	t.Helper()
	select {
	case fn := <-l.events:
		return fn
	case <-time.After(10 * time.Second):
		t.Fatal("nothing was posted to the loop")
		return nil
	}
}

// socketLauncher is a launcher with a link server whose handshake waits for release and whose connections report when the
// server's side sees them closed.
func socketLauncher(t *testing.T) (l *Launcher, release func(), upgraded, closedByLauncher chan struct{}) {
	t.Helper()
	releaseCh := make(chan struct{})
	upgraded = make(chan struct{})
	closedByLauncher = make(chan struct{})
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-releaseCh:
		case <-r.Context().Done():
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		close(upgraded)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				close(closedByLauncher)
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l = &Launcher{base: base, ctx: ctx, cancel: cancel, events: make(chan func(), eventQueue), loopDone: make(chan struct{}),
		sockets: map[int]*linkSocket{}, sandbox: &Sandbox{ended: true}}
	var once sync.Once
	return l, func() { once.Do(func() { close(releaseCh) }) }, upgraded, closedByLauncher
}

// A socket the sandbox closes while its dial is under way (Pi: `ws.close()` aborts a CONNECTING socket) is never opened by the
// dial finishing, and a send on it is dropped: the send used to panic on the closed channel and end the launcher.
func TestALinkSocketClosedDuringItsDialStaysClosedAndASendOnItIsDropped(t *testing.T) {
	l, release, _, _ := socketLauncher(t)
	l.openSocket(1)
	socket := l.sockets[1]
	if err := l.handle(SandboxMessage{Kind: "socket_close", Socket: 1}); err != nil {
		t.Fatal(err)
	}
	release()
	nextEvent(t, l)()
	if socket.open || !socket.closed || l.sockets[1] != nil {
		t.Fatalf("the socket is open %v, closed %v, known %v after its dial", socket.open, socket.closed, l.sockets[1] != nil)
	}
	l.send(1, []byte("hello"))
}

// The same when the dial has finished and only the loop has not taken its result yet: the connection is closed, not kept.
func TestALinkSocketClosedAfterItsDialFinishedButBeforeTheLoopTookItIsClosedAndNeverOpened(t *testing.T) {
	l, release, upgraded, closedByLauncher := socketLauncher(t)
	l.openSocket(1)
	socket := l.sockets[1]
	release()
	<-upgraded
	// The dial's result is posted to the loop; the sandbox's close comes first.
	event := nextEvent(t, l)
	if err := l.handle(SandboxMessage{Kind: "socket_close", Socket: 1}); err != nil {
		t.Fatal(err)
	}
	event()
	if socket.open || l.sockets[1] != nil {
		t.Fatalf("the socket is open %v, known %v", socket.open, l.sockets[1] != nil)
	}
	l.send(1, []byte("hello"))
	select {
	case <-closedByLauncher:
	case <-time.After(10 * time.Second):
		t.Fatal("the connection that arrived after the close was kept open")
	}
}

// A socket whose server drops the link is ended on the loop: a send racing that end is dropped, never sent on a closed channel.
func TestALinkSocketTheServerDropsIsEndedOnTheLoopAndASendOnItIsDropped(t *testing.T) {
	l, release, _, _ := socketLauncher(t)
	l.openSocket(1)
	socket := l.sockets[1]
	release()
	nextEvent(t, l)()
	if !socket.open {
		t.Fatal("the socket did not open")
	}
	_ = socket.conn.Close()
	nextEvent(t, l)()
	if !socket.closed || l.sockets[1] != nil {
		t.Fatalf("closed %v, known %v", socket.closed, l.sockets[1] != nil)
	}
	l.send(1, []byte("hello"))
}

// A sandbox that stops reading its stdin is ended when a post the loop makes goes over its bound; its end must not wait for the
// loop (the end's callback posts to a loop whose queue is full: the loop would wait for itself, and Stop and Ctrl-C would hang).
func TestASandboxEndingOnAPostTheLoopMakesWithTheQueueFullDoesNotBlockTheLoop(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
	base, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &Launcher{base: base, ctx: ctx, cancel: cancel, events: make(chan func(), eventQueue), loopDone: make(chan struct{}),
		exited: make(chan int, 1), sockets: map[int]*linkSocket{}, term: nullTerminal{}, output: io.Discard}
	generation := 1
	l.generation = generation
	// Terminate waits for the sandbox's exit: closed here, the child is the test's.
	gone := make(chan struct{})
	close(gone)
	sandbox := &Sandbox{cmd: cmd, exited: gone, queued: maxUnread + MaxPipeFrame, onMessage: func(SandboxMessage) {},
		onEnd: func(string) {
			//portlint:allow golifetime the post ends when the loop takes it or the loop ends (loopDone)
			go l.post(func() { l.sandboxEnded(generation) })
		}}
	sandbox.ready = sync.NewCond(&sandbox.mu)
	l.sandbox = sandbox
	l.confirming = true
	go l.loop()
	ran := make(chan struct{})
	l.post(func() {
		defer close(ran)
		for full := false; !full; {
			select {
			case l.events <- func() {}:
			default:
				full = true
			}
		}
		// A request while a prompt is open is refused: a post to the sandbox, past its bound.
		_ = l.handle(SandboxMessage{Kind: "request", ID: 1, Request: BridgeRequest{Kind: "copy"}})
	})
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop is blocked in the sandbox's end")
	}
	stopped := make(chan struct{})
	go func() { l.finish(0); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("finish (Stop) did not return: the loop is blocked")
	}
}

type nullTerminal struct{}

func (nullTerminal) Input() io.Reader       { return eofReader{} }
func (nullTerminal) Output() io.Writer      { return io.Discard }
func (nullTerminal) Columns() int           { return 80 }
func (nullTerminal) Rows() int              { return 24 }
func (nullTerminal) IsTTY() bool            { return false }
func (nullTerminal) SetRaw(bool) error      { return nil }
func (nullTerminal) OnResize(func()) func() { return func() {} }

type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

// slowTTY is a terminal whose first IsTTY is slow, widening the window between the sandbox starting and the launcher taking the
// terminal; it records the SetRaw calls.
type slowTTY struct {
	mu    sync.Mutex
	calls int
	raw   []bool
	in    *io.PipeReader
}

func (s *slowTTY) Input() io.Reader  { return s.in }
func (s *slowTTY) Output() io.Writer { return io.Discard }
func (s *slowTTY) Columns() int      { return 80 }
func (s *slowTTY) Rows() int         { return 24 }
func (s *slowTTY) IsTTY() bool {
	s.mu.Lock()
	s.calls++
	first := s.calls == 1
	s.mu.Unlock()
	if first {
		time.Sleep(3 * time.Second)
	}
	return true
}
func (s *slowTTY) SetRaw(raw bool) error {
	s.mu.Lock()
	s.raw = append(s.raw, raw)
	s.mu.Unlock()
	return nil
}
func (s *slowTTY) OnResize(func()) func() { return func() {} }

// A sandbox that ends while the launcher is still starting (its bundle fails at once) leaves the terminal as it found it: the
// raw mode is set on the loop, after the check that the client has not finished.
func TestATerminalIsNotLeftRawWhenTheSandboxEndsWhileTheLauncherStarts(t *testing.T) {
	node := tuiNode(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_chord/tui/start":
			_, _ = io.WriteString(w, `{"base":"h","script":"/_chord/tui/script","combined":"/_chord/tui/combined"}`)
		case "/_chord/tui/script":
			_, _ = io.WriteString(w, `throw new Error("the bundle fails at once");`)
		default:
			_, _ = io.WriteString(w, ``)
		}
	}))
	defer server.Close()
	in, inWriter := io.Pipe()
	defer func() { _ = inWriter.Close() }()
	terminal := &slowTTY{in: in}
	running, err := StartTerminal(context.Background(), TerminalOptions{URL: server.URL, Credentials: TerminalCredentials{Kind: CredentialsToken, Token: "t"},
		Terminal: terminal, Node: node, Locales: []string{"en"}})
	if err != nil {
		return
	}
	select {
	case <-running.Exited():
	case <-time.After(10 * time.Second):
	}
	running.Stop()
	time.Sleep(200 * time.Millisecond)
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	if len(terminal.raw) > 0 && terminal.raw[len(terminal.raw)-1] {
		t.Fatalf("the terminal is left in raw mode after the client ended: SetRaw calls %v", terminal.raw)
	}
}
