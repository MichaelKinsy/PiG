package terminals

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/ghostty"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/hostenv"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports apps/p2/plugins/terminals/main.ts: the terminal host. It owns the pi-env connections and the programs running in
// terminals, independent of the server and the session workers, so terminals survive their restarts (detached.go). A
// headless libghostty terminal follows each one, so a browser that attaches later gets the current screen. It exits when
// it has had no terminals and no connections for a while. One per data directory. It is its own program, started by path
// (`p2 terminals-host <data directory>`, the p2 executable like the session worker, D473).
//
// The agent's terminal tool reaches the same terminals through calls with replies (`call`, see HostCall): each terminal
// keeps its last MiB of output and a read cursor for it here, so reads continue where the last one ended, also across
// server and worker restarts.
//
// Forced Go differences: Node's one thread is the host's loop here, one goroutine that owns every terminal, client and
// ghostty instance (libghostty's wasm instance is not thread-safe); sockets, pi-env connections and timers post to it.
// Calls with replies run on a goroutine each and enter the loop for each step, as the `await`s of main.ts.

const (
	idleExit  = 10 * time.Minute
	scrollbackLines = 10_000
	// ringBytes is the output each terminal keeps for the agent's reads.
	ringBytes = 1024 * 1024
	// maxAgentTerminals are the running terminals the agent may have opened in one session.
	maxAgentTerminals = 8
	// rememberedCalls are the results of calls with a key the host remembers, so a repeated call (a replayed tool call)
	// is not done twice.
	rememberedCalls = 1024
	// matchWindow: a `match` wait tests the plain text of at most this much of the latest output.
	matchWindow = 64 * 1024
	// defaultQuietMs is how long without output a `quiet` wait waits by default.
	defaultQuietMs = 500
	// maxCaptureScrollback is the lines of scrollback `capture` returns at most.
	maxCaptureScrollback = 5000
	idleCheckEvery       = 30 * time.Second
)

// defaultTheme is the colors programs see until a viewer reports its theme: the browser's dark theme.
var defaultTheme = TerminalTheme{Foreground: 0xe6e6e6, Background: 0x141414, Scheme: "dark"}

// Window of one terminal: what the host keeps of it.
type hosted struct {
	info *TerminalInfo
	vt   *ghostty.VtTerminal
	pty  *env.RemoteTerminal
	// theme holds the colors of its latest viewer, which answer the program's color queries.
	theme TerminalTheme
	// pending is input that arrived before the program started.
	pending      [][]byte
	pendingBytes int
	clients      map[*hostClient]struct{}
	connection   string
	// ring holds the last MiB of output, for the agent's reads.
	ring *OutputRing
	// cursor is where the agent's next read starts (an offset into ring).
	cursor int64
	// waiters are called on each output, the exit and the close.
	waiters map[*waiter]struct{}
}

type waiter struct{ wake func() }

// hostClient is one connection to the host. Output flow control per viewer: sent and acked count output bytes (snapshots
// included) since it attached. A viewer with more than TerminalOutputWindow unacknowledged output after its latest
// snapshot (see lag) is behind: it gets no more output, only the headless terminal follows the program. When its
// acknowledgements catch up it gets a fresh snapshot instead of the bytes it missed, so a flood (`find /`) costs a slow
// viewer one screen, not the whole stream, and nothing buffers without bound.
//
// The snapshot itself never counts against the window, as chord lets one large frame exceed its window: a screen with
// colored scrollback can be larger than the window, and counting it would mark the viewer behind as soon as it was sent,
// so every catch-up would send it again, forever, without any new output.
type hostClient struct {
	*frameWriter
	attached *hosted
	theme    TerminalTheme
	sent     int
	acked    int
	// snapshotEnd is `sent` right after the latest snapshot.
	snapshotEnd int
	behind      bool
	// session is the session it listed last: it hears about that session's terminals changing (`terminals`).
	session    string
	hasSession bool
	// calls are the cancels of its calls still running, by call id.
	calls map[int]func()

}

// connEntry is a pi-env connection by environment ("" for this machine), with the configuration it was made for.
type connEntry struct {
	revision string
	done     chan struct{}
	conn     *env.Connection
	err      error
	shellEnv map[string]string
}

// Host is the terminal host of one data directory.
type Host struct {
	directory string
	version   string
	address   string
	ghostty   *ghostty.Ghostty
	listener  net.Listener

	tasks chan func()
	quit  chan struct{}
	stop  sync.Once
	done  chan struct{}

	// Owned by the loop.
	terminals   map[string]*hosted
	order       []*hosted
	clients     map[*hostClient]struct{}
	connections map[string]*connEntry
	idleSince   time.Time
	remembered  map[string]*rememberedCall
	rememberedOrder []string
}

type rememberedCall struct {
	done   chan struct{}
	result HostResult
	err    error
}

func hostLog(message string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), message)
}

// post runs fn on the host's loop; false once the host is gone.
func (h *Host) post(fn func()) bool {
	select {
	case h.tasks <- fn:
		return true
	case <-h.done:
		return false
	}
}

// do runs fn on the loop and returns its value (the zero value once the host is gone).
func do[T any](h *Host, fn func() T) T {
	result := make(chan T, 1)
	if !h.post(func() { result <- fn() }) {
		var zero T
		return zero
	}
	select {
	case value := <-result:
		return value
	case <-h.done:
		var zero T
		return zero
	}
}

func (h *Host) loop() {
	defer close(h.done)
	for {
		select {
		case fn := <-h.tasks:
			fn()
		case <-h.quit:
			return
		}
	}
}

// RunHost runs the terminal host of directory until it is asked to shut down or has been idle; a host that finds another
// serving the directory returns nil at once.
func RunHost(ctx context.Context, directory string) error {
	address, err := HostAddress(directory)
	if err != nil {
		return err
	}
	release, err := p2runtime.TakeProcessLock("p2:terminals:"+address, 0)
	if err != nil {
		// Another host serves this data directory.
		return nil
	}
	defer func() { _ = release() }()
	// We hold the lock: a socket file left behind is a killed host's.
	if !strings.HasPrefix(address, "@") && !strings.HasPrefix(address, `\\`) {
		_ = os.Remove(address)
	}
	version, err := HostVersion()
	if err != nil {
		return err
	}
	engine, err := ghostty.Load(ctx, ghosttyWasm)
	if err != nil {
		return err
	}
	defer func() { _ = engine.Close(context.WithoutCancel(ctx)) }()
	listener, err := listen(address)
	if err != nil {
		return err
	}
	h := &Host{
		directory: directory, version: version, address: address, ghostty: engine, listener: listener,
		tasks: make(chan func(), 256), quit: make(chan struct{}), done: make(chan struct{}),
		terminals: map[string]*hosted{}, clients: map[*hostClient]struct{}{}, connections: map[string]*connEntry{},
		idleSince: time.Now(), remembered: map[string]*rememberedCall{},
	}
	go h.loop()
	var accepting sync.WaitGroup
	accepting.Add(1)
	//portlint:allow golifetime ends when the listener closes (the host is stopping)
	go func() {
		defer accepting.Done()
		h.accept()
	}()
	hostLog(fmt.Sprintf("terminal host %d (%s) listening", os.Getpid(), version))
	stopping := ctx.Done()
	ticker := time.NewTicker(idleCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-h.done:
			_ = listener.Close()
			accepting.Wait()
			return nil
		case <-stopping:
			stopping = nil
			h.shutdown()
		case <-ticker.C:
			h.post(func() {
				running := false
				for _, terminal := range h.terminals {
					if terminal.info.Exited == nil {
						running = true
					}
				}
				if running || len(h.clients) > 0 {
					h.idleSince = time.Now()
				} else if time.Since(h.idleSince) > idleExit {
					hostLog("idle, exiting")
					h.exit()
				}
			})
		}
	}
}

// exit stops the loop; the process ends with RunHost.
func (h *Host) exit() { h.stop.Do(func() { close(h.quit) }) }

func (h *Host) shutdown() {
	h.post(func() {
		for _, terminal := range slices.Clone(h.order) {
			h.close(terminal)
		}
		for _, entry := range h.connections {
			go closeEntry(entry)
		}
		// Let the daemons see their input end before this process goes.
		time.AfterFunc(100*time.Millisecond, h.exit)
	})
}

func closeEntry(entry *connEntry) {
	<-entry.done
	if entry.conn != nil {
		entry.conn.Close()
	}
}

func listen(address string) (net.Listener, error) {
	return net.Listen("unix", address)
}

func (h *Host) accept() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			return
		}
		client := &hostClient{frameWriter: newFrameWriter(conn), theme: defaultTheme, calls: map[int]func(){}}
		//portlint:allow golifetime the writer ends when the client is destroyed
		go client.run()
		if !h.post(func() { h.connect(client) }) {
			client.destroy()
			return
		}
	}
}

func (h *Host) connect(client *hostClient) {
	h.clients[client] = struct{}{}
	running := 0
	for _, terminal := range h.terminals {
		if terminal.info.Exited == nil {
			running++
		}
	}
	h.sendEvent(client, TerminalEvent{Type: EventHello, Version: h.version, Pid: os.Getpid(), Running: running})
	//portlint:allow golifetime the reader ends when the socket closes
	go func() {
		_ = ReadFrames(client.conn, func(frame Frame) {
			h.post(func() { h.receive(client, frame) })
		})
		client.destroy()
		h.post(func() {
			delete(h.clients, client)
			if client.attached != nil {
				delete(client.attached.clients, client)
			}
			for _, cancel := range client.calls {
				cancel()
			}
			h.idleSince = time.Now()
		})
	}()
}

// sendFrame queues an encoded frame for the client; a client more than MaxBacklog behind is disconnected.
func (h *Host) sendFrame(client *hostClient, frame []byte) { client.send(frame) }

func (h *Host) sendEvent(client *hostClient, event TerminalEvent) {
	frame, err := EncodeFrame(event.JSON())
	if err == nil {
		h.sendFrame(client, frame)
	}
}

func (h *Host) sendBytes(client *hostClient, bytes []byte) {
	frame, _ := EncodeFrame(bytes)
	h.sendFrame(client, frame)
}

// lag is the unacknowledged output after the latest snapshot.
func (client *hostClient) lag() int { return client.sent - max(client.acked, client.snapshotEnd) }

// output sends program output to a viewer, which is no longer sent any once it is behind.
func (h *Host) output(client *hostClient, bytes []byte, frame []byte) {
	if client.behind {
		return
	}
	h.sendFrame(client, frame)
	client.sent += len(bytes)
	if client.lag() > TerminalOutputWindow {
		client.behind = true
	}
}

// resync replays the current screen to a viewer as on attach, which also resets its terminal.
func (h *Host) resync(client *hostClient, terminal *hosted) {
	client.behind = false
	h.sendEvent(client, TerminalEvent{Type: EventAttached, Terminal: terminal.info.Clone()})
	snapshot := terminal.vt.Snapshot()
	h.sendBytes(client, snapshot)
	client.sent += len(snapshot)
	client.snapshotEnd = client.sent
}

func (h *Host) changed(terminal *hosted) {
	for client := range terminal.clients {
		h.sendEvent(client, TerminalEvent{Type: EventChanged, Terminal: terminal.info.Clone()})
	}
	h.listed(terminal.info.SessionID)
}

// sessionTerminals are the session's terminals, oldest first.
func (h *Host) sessionTerminals(sessionID string) []*hosted {
	var out []*hosted
	for _, terminal := range h.order {
		if terminal.info.SessionID == sessionID {
			out = append(out, terminal)
		}
	}
	return out
}

func infosOf(terminals []*hosted) []TerminalInfo {
	infos := make([]TerminalInfo, len(terminals))
	for index, terminal := range terminals {
		infos[index] = terminal.info.Clone()
	}
	return infos
}

// listed tells every connection that listed the session (its viewers) its terminals now.
func (h *Host) listed(sessionID string) {
	list := infosOf(h.sessionTerminals(sessionID))
	for client := range h.clients {
		if client.hasSession && client.session == sessionID {
			h.sendEvent(client, TerminalEvent{Type: EventTerminals, Terminals: list})
		}
	}
}

// connection is the pi-env connection for env (nil: this machine), opened at the first use. It runs off the loop.
func (h *Host) connection(ctx context.Context, name *string) (*env.Connection, map[string]string, error) {
	if name == nil {
		entry := do(h, func() *connEntry {
			if existing := h.connections[""]; existing != nil {
				return existing
			}
			entry := &connEntry{done: make(chan struct{}), shellEnv: map[string]string{}}
			h.connections[""] = entry
			go h.start(entry, "", func() (*env.Connection, error) {
				return hostenv.Local(ctx, func(text string) { hostLog("pi-env: " + text) })
			})
			return entry
		})
		if entry == nil {
			return nil, nil, errors.New("The terminal host is closing")
		}
		<-entry.done
		return entry.conn, entry.shellEnv, entry.err
	}
	remote, err := hostenv.Resolve(ctx, h.directory, *name)
	if err != nil {
		return nil, nil, err
	}
	entry := do(h, func() *connEntry {
		if existing := h.connections[*name]; existing != nil && existing.revision == remote.Revision {
			return existing
		}
		entry := &connEntry{revision: remote.Revision, done: make(chan struct{}), shellEnv: remote.ShellEnv}
		h.connections[*name] = entry
		go h.start(entry, *name, func() (*env.Connection, error) {
			return remote.Connect(ctx, func(text string) { hostLog("pi-env " + *name + ": " + text) })
		})
		return entry
	})
	if entry == nil {
		return nil, nil, errors.New("The terminal host is closing")
	}
	<-entry.done
	return entry.conn, entry.shellEnv, entry.err
}

// start makes the connection of entry; a failure is forgotten, so the next use tries again.
func (h *Host) start(entry *connEntry, key string, connect func() (*env.Connection, error)) {
	entry.conn, entry.err = connect()
	close(entry.done)
	if entry.err != nil {
		h.post(func() {
			if h.connections[key] == entry {
				delete(h.connections, key)
			}
		})
	}
}

// release stops a connection's daemon once none of its terminals runs.
func (h *Host) release(key string) {
	for _, terminal := range h.order {
		if terminal.connection == key && terminal.info.Exited == nil {
			return
		}
	}
	entry := h.connections[key]
	delete(h.connections, key)
	if entry != nil {
		go closeEntry(entry)
	}
}

// applyTheme shows the terminal in a viewer's colors; programs that asked to hear about light/dark changes (mode 2031) do.
func (h *Host) applyTheme(terminal *hosted, theme TerminalTheme) {
	previous := terminal.theme
	terminal.theme = theme
	terminal.vt.SetColors(ghostty.Colors{Foreground: theme.Foreground, Background: theme.Background})
	if previous.Scheme != theme.Scheme && terminal.vt.Mode(2031, false) && terminal.pty != nil {
		code := "2"
		if theme.Scheme == "dark" {
			code = "1"
		}
		terminal.pty.Write([]byte("\x1b[?997;" + code + "n"))
	}
}

func (h *Host) attach(client *hostClient, terminal *hosted) {
	if client.attached != nil {
		delete(client.attached.clients, client)
	}
	client.attached = terminal
	terminal.clients[client] = struct{}{}
	h.applyTheme(terminal, client.theme)
	client.sent = 0
	client.acked = 0
	h.resync(client, terminal)
}

// opening is a terminal to create.
type opening struct {
	sessionID  string
	cwd        string
	env        *string
	cols, rows int
	name       string
	agent      bool
}

func randomID() string {
	var bytes [6]byte
	_, _ = rand.Read(bytes[:])
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

// create makes a new terminal, listed at once; started closes once its program started or failed to.
func (h *Host) create(request opening, theme TerminalTheme) (*hosted, <-chan struct{}, error) {
	id := randomID()
	taken := map[int]bool{}
	for _, each := range h.sessionTerminals(request.sessionID) {
		taken[each.info.Index] = true
	}
	index := 1
	for taken[index] {
		index++
	}
	info := &TerminalInfo{
		ID: id, SessionID: request.sessionID, Index: index, Name: request.name, Agent: request.agent, Cwd: request.cwd,
		Env: request.env, CreatedAt: time.Now().UnixMilli(), Cols: request.cols, Rows: request.rows,
	}
	terminal := &hosted{
		info: info, theme: theme, clients: map[*hostClient]struct{}{}, connection: derefString(request.env),
		ring: NewOutputRing(ringBytes), waiters: map[*waiter]struct{}{},
	}
	vt, err := h.ghostty.Terminal(ghostty.Options{
		Cols: request.cols, Rows: request.rows, Scrollback: scrollbackLines,
		Colors:      &ghostty.Colors{Foreground: theme.Foreground, Background: theme.Background},
		ColorScheme: func() string { return terminal.theme.Scheme },
		// Answers to queries (cursor position, device attributes) come from here, never from the browsers.
		OnReply: func(bytes []byte) {
			if h.terminals[id] != nil && terminal.pty != nil {
				terminal.pty.Write(bytes)
			}
		},
	})
	if err != nil {
		return nil, nil, err
	}
	started := make(chan struct{})
	terminal.vt = vt
	h.terminals[id] = terminal
	h.order = append(h.order, terminal)
	h.listed(request.sessionID)
	//portlint:allow golifetime ends once the program started or failed to
	go h.launch(terminal, request, theme, started)
	return terminal, started, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// end records how a terminal's program ended.
func (h *Host) end(terminal *hosted, exited Exited) {
	if h.terminals[terminal.info.ID] != terminal {
		return
	}
	terminal.info.Exited = &exited
	h.changed(terminal)
	h.poke(terminal)
	h.release(terminal.connection)
}

func errorExited(err error) Exited {
	message := err.Error()
	return Exited{Error: &message}
}

// launch starts the terminal's program (off the loop); started closes when it has, or it is gone with the error.
func (h *Host) launch(terminal *hosted, request opening, theme TerminalTheme, started chan<- struct{}) {
	defer close(started)
	info := terminal.info
	fail := func(err error) { h.post(func() { h.end(terminal, errorExited(err)) }) }
	connection, shellEnv, err := h.connection(context.Background(), request.env)
	if err != nil {
		fail(err)
		return
	}
	environment := map[string]string{}
	for key, value := range shellEnv {
		environment[key] = value
	}
	environment["TERM"] = "xterm-256color"
	environment["COLORTERM"] = "truecolor"
	// COLORFGBG: light or dark for programs that only look at the environment.
	if theme.Scheme == "dark" {
		environment["COLORFGBG"] = "15;0"
	} else {
		environment["COLORFGBG"] = "0;15"
	}
	pty, err := env.OpenTerminal(context.Background(), connection, env.TerminalOptions{
		Cwd: request.cwd, Env: environment, Cols: request.cols, Rows: request.rows,
		OnData: func(bytes []byte) {
			h.post(func() { h.data(terminal, bytes) })
		},
	})
	if err != nil {
		fail(err)
		return
	}
	attached := do(h, func() bool {
		terminal.pty = pty
		// Closed meanwhile: the program must not outlive its terminal.
		if h.terminals[info.ID] != terminal {
			pty.Close()
		}
		if info.Cols != request.cols || info.Rows != request.rows {
			pty.Resize(info.Cols, info.Rows)
		}
		for _, bytes := range terminal.pending {
			pty.Write(bytes)
		}
		terminal.pending = nil
		terminal.pendingBytes = 0
		return true
	})
	if !attached {
		return
	}
	//portlint:allow golifetime ends when the program exits or the connection is lost
	go func() {
		code, err := pty.Exited()
		if err != nil {
			h.post(func() { h.end(terminal, errorExited(err)) })
			return
		}
		h.post(func() { h.end(terminal, Exited{Code: &code}) })
	}()
}

// data is the program's output.
func (h *Host) data(terminal *hosted, bytes []byte) {
	// Output racing a close: the headless terminal is already freed.
	if h.terminals[terminal.info.ID] != terminal {
		return
	}
	terminal.vt.Write(bytes)
	terminal.ring.Append(bytes)
	if len(terminal.clients) > 0 {
		frame, _ := EncodeFrame(bytes)
		for viewer := range terminal.clients {
			h.output(viewer, bytes, frame)
		}
	}
	if title := terminal.vt.Title(); title != terminal.info.Title {
		terminal.info.Title = title
		h.changed(terminal)
	}
	h.poke(terminal)
}

// poke wakes the terminal's waits.
func (h *Host) poke(terminal *hosted) {
	for each := range terminal.waiters {
		each.wake()
	}
}

// input is input for the program, held until it started. False when too much is held already.
func (h *Host) input(terminal *hosted, bytes []byte) bool {
	if terminal.pty != nil {
		terminal.pty.Write(bytes)
		return true
	}
	if terminal.pendingBytes+len(bytes) > MaxBacklog {
		return false
	}
	terminal.pending = append(terminal.pending, bytes)
	terminal.pendingBytes += len(bytes)
	return true
}

func (h *Host) close(terminal *hosted) {
	if h.terminals[terminal.info.ID] != terminal {
		return
	}
	delete(h.terminals, terminal.info.ID)
	h.order = slices.DeleteFunc(h.order, func(each *hosted) bool { return each == terminal })
	if terminal.pty != nil {
		terminal.pty.Close()
	}
	for client := range terminal.clients {
		h.sendEvent(client, TerminalEvent{Type: EventClosed, TerminalID: terminal.info.ID})
		client.attached = nil
	}
	clear(terminal.clients)
	terminal.vt.Free()
	h.release(terminal.connection)
	h.poke(terminal)
	h.listed(terminal.info.SessionID)
}

// receive handles one frame from a client.
func (h *Host) receive(client *hostClient, frame Frame) {
	defer func() {
		if recovered := recover(); recovered != nil {
			h.sendEvent(client, TerminalEvent{Type: EventError, Message: fmt.Sprint(recovered)})
		}
	}()
	if frame.Raw {
		terminal := client.attached
		if terminal == nil || terminal.info.Exited != nil {
			return
		}
		if !h.input(terminal, frame.Bytes) {
			client.destroy()
		}
		return
	}
	request, err := ParseRequest(frame.JSON)
	if err != nil {
		h.sendEvent(client, TerminalEvent{Type: EventError, Message: err.Error()})
		return
	}
	h.handle(client, request)
}

func (h *Host) handle(client *hostClient, request TerminalRequest) {
	switch request.Type {
	case RequestList:
		client.session, client.hasSession = request.SessionID, true
		h.sendEvent(client, TerminalEvent{Type: EventList, Terminals: infosOf(h.sessionTerminals(request.SessionID))})
	case RequestOpen:
		terminal, _, err := h.create(opening{sessionID: request.SessionID, cwd: request.Cwd, env: request.Env, cols: request.Cols, rows: request.Rows}, client.theme)
		if err != nil {
			h.sendEvent(client, TerminalEvent{Type: EventError, Message: err.Error()})
			return
		}
		h.attach(client, terminal)
	case RequestCall:
		h.call(client, request.ID, *request.Call)
	case RequestCancel:
		if cancel := client.calls[request.ID]; cancel != nil {
			cancel()
		}
	case RequestAttach:
		if terminal := h.terminals[request.TerminalID]; terminal != nil {
			h.attach(client, terminal)
		} else {
			h.sendEvent(client, TerminalEvent{Type: EventError, Message: "This terminal no longer exists"})
		}
	case RequestAck:
		client.acked = min(client.sent, client.acked+request.Bytes)
		if terminal := client.attached; terminal != nil && client.behind && client.lag() <= TerminalOutputWindow/4 {
			h.resync(client, terminal)
		}
	case RequestTheme:
		client.theme = request.Theme
		if client.attached != nil {
			h.applyTheme(client.attached, client.theme)
		}
	case RequestResize:
		terminal := client.attached
		if terminal == nil || (terminal.info.Cols == request.Cols && terminal.info.Rows == request.Rows) {
			return
		}
		terminal.info.Cols, terminal.info.Rows = request.Cols, request.Rows
		terminal.vt.Resize(request.Cols, request.Rows)
		if terminal.pty != nil {
			terminal.pty.Resize(request.Cols, request.Rows)
		}
		h.changed(terminal)
	case RequestClose:
		terminal := client.attached
		if terminal == nil {
			return
		}
		if (request.Force != nil && *request.Force) || terminal.pty == nil || terminal.info.Exited != nil {
			h.close(terminal)
			return
		}
		// A job in front of the shell (an editor, a build): the viewer asks before killing it. Background jobs do not
		// count. When the daemon cannot tell (it predates `ptyForeground`, or the terminal just ended), close.
		pty := terminal.pty
		//portlint:allow golifetime ends with the daemon's answer
		go func() {
			job, err := pty.Foreground(context.Background())
			if err != nil {
				job = nil
			}
			h.post(func() {
				if h.terminals[terminal.info.ID] != terminal {
					return
				}
				if job != nil && terminal.info.Exited == nil {
					h.sendEvent(client, TerminalEvent{Type: EventBusy, TerminalID: terminal.info.ID, Program: job.Name})
				} else {
					h.close(terminal)
				}
			})
		}()
	case RequestShutdown:
		hostLog("shutting down on request")
		h.shutdown()
	default:
		h.sendEvent(client, TerminalEvent{Type: EventError, Message: "Unknown request"})
	}
}

var errCancelled = errors.New("cancelled")

// call runs a call and replies; the caller may cancel it while it waits.
func (h *Host) call(client *hostClient, id int, request HostCall) {
	cancelled := make(chan struct{})
	var once sync.Once
	client.calls[id] = func() { once.Do(func() { close(cancelled) }) }
	//portlint:allow golifetime ends with the call, which a cancel or the client's close ends
	go func() {
		type outcome struct {
			result HostResult
			err    error
		}
		finished := make(chan outcome, 1)
		go func() {
			var out outcome
			if request.HasKey() {
				out.result, out.err = h.once(request)
			} else {
				out.result, out.err = h.perform(request, cancelled)
			}
			finished <- out
		}()
		var event TerminalEvent
		select {
		case out := <-finished:
			event = replyEvent(id, out.result, out.err)
		case <-cancelled:
			event = replyEvent(id, HostResult{}, errCancelled)
		}
		h.post(func() {
			delete(client.calls, id)
			frame, err := EncodeFrame(event.JSON())
			if err == nil {
				h.sendFrame(client, frame)
			}
		})
	}()
}

func replyEvent(id int, result HostResult, err error) TerminalEvent {
	if err != nil {
		message := err.Error()
		return TerminalEvent{Type: EventReply, ID: id, Err: &message}
	}
	return TerminalEvent{Type: EventReply, ID: id, Result: &result}
}

// once does a call with a key once: a call with a key the host has seen gets the first one's result.
func (h *Host) once(request HostCall) (HostResult, error) {
	key := request.SessionID + "\x00" + request.Op + "\x00" + request.Key
	known := do(h, func() *rememberedCall {
		if known := h.remembered[key]; known != nil {
			return known
		}
		// Calls with a key never wait, so nothing cancels them.
		first := &rememberedCall{done: make(chan struct{})}
		h.remembered[key] = first
		h.rememberedOrder = append(h.rememberedOrder, key)
		for len(h.remembered) > rememberedCalls && len(h.rememberedOrder) > 0 {
			oldest := h.rememberedOrder[0]
			h.rememberedOrder = h.rememberedOrder[1:]
			delete(h.remembered, oldest)
		}
		go func() {
			first.result, first.err = h.perform(request, nil)
			close(first.done)
			if first.err != nil {
				// A failed call may be tried again.
				h.post(func() {
					if h.remembered[key] == first {
						delete(h.remembered, key)
					}
				})
			}
		}()
		return first
	})
	if known == nil {
		return HostResult{}, errors.New("The terminal host is closing")
	}
	<-known.done
	return known.result, known.err
}

// perform is one call; cancelled closes when the caller gives up (a wait then ends).
func (h *Host) perform(request HostCall, cancelled <-chan struct{}) (HostResult, error) {
	switch request.Op {
	case OpList:
		terminals := do(h, func() []*hosted { return h.sessionTerminals(request.SessionID) })
		windows := make([]Window, len(terminals))
		var group sync.WaitGroup
		for index, terminal := range terminals {
			group.Add(1)
			go func() {
				defer group.Done()
				job := h.foreground(terminal)
				windows[index] = Window{Terminal: do(h, func() TerminalInfo { return terminal.info.Clone() }), Foreground: job}
			}()
		}
		group.Wait()
		return HostResult{Kind: KindList, Windows: windows}, nil
	case OpOpen:
		info, err := h.openForAgent(request)
		if err != nil {
			return HostResult{}, err
		}
		return HostResult{Kind: KindTerminal, Terminal: info}, nil
	case OpSend:
		type sent struct {
			info TerminalInfo
			err  error
		}
		out := do(h, func() sent {
			terminal, err := h.target(request.SessionID, request.Target)
			if err != nil {
				return sent{err: err}
			}
			if terminal.info.Exited != nil {
				return sent{err: fmt.Errorf("window %d has exited", terminal.info.Index)}
			}
			var bytes []byte
			for _, key := range request.Keys {
				var part []byte
				var ok bool
				if press, named := TmuxKey(key); !request.Literal && named {
					part, ok = terminal.vt.EncodeKey(press)
				} else {
					part, ok = []byte(key), true
				}
				if !ok {
					return sent{err: fmt.Errorf("unknown key: %s", key)}
				}
				bytes = append(bytes, part...)
			}
			if !h.input(terminal, bytes) {
				return sent{err: errors.New("too much input before the program started")}
			}
			return sent{info: terminal.info.Clone()}
		})
		if out.err != nil {
			return HostResult{}, out.err
		}
		return HostResult{Kind: KindTerminal, Terminal: out.info}, nil
	case OpCursor:
		type cursor struct {
			info   TerminalInfo
			offset int64
			err    error
		}
		out := do(h, func() cursor {
			terminal, err := h.target(request.SessionID, request.Target)
			if err != nil {
				return cursor{err: err}
			}
			return cursor{info: terminal.info.Clone(), offset: terminal.cursor}
		})
		if out.err != nil {
			return HostResult{}, out.err
		}
		return HostResult{Kind: KindCursor, Terminal: out.info, Offset: out.offset}, nil
	case OpRead:
		read, err := h.read(request, cancelled)
		if err != nil {
			return HostResult{}, err
		}
		return HostResult{Kind: KindRead, Read: read}, nil
	case OpCapture:
		type captured struct {
			info TerminalInfo
			text string
			err  error
		}
		out := do(h, func() captured {
			terminal, err := h.target(request.SessionID, request.Target)
			if err != nil {
				return captured{err: err}
			}
			return captured{info: terminal.info.Clone(), text: capture(terminal.vt, request.Scrollback, request.Escapes)}
		})
		if out.err != nil {
			return HostResult{}, out.err
		}
		return HostResult{Kind: KindCapture, Terminal: out.info, Text: out.text}, nil
	case OpClose:
		type closed struct {
			id  string
			err error
		}
		out := do(h, func() closed {
			terminal, err := h.target(request.SessionID, request.Target)
			if err != nil {
				return closed{err: err}
			}
			h.close(terminal)
			return closed{id: terminal.info.ID}
		})
		if out.err != nil {
			return HostResult{}, out.err
		}
		return HostResult{Kind: KindClosed, TerminalID: out.id}, nil
	case OpCloseSession:
		do(h, func() struct{} {
			for _, terminal := range h.sessionTerminals(request.SessionID) {
				h.close(terminal)
			}
			return struct{}{}
		})
		return HostResult{Kind: KindDone}, nil
	}
	return HostResult{}, fmt.Errorf("Unknown call %s", request.Op)
}

// target is the session's terminal `name`: its id, its number (`2` or tmux's `:2`), or its name.
func (h *Host) target(sessionID, name string) (*hosted, error) {
	own := h.sessionTerminals(sessionID)
	bare := strings.TrimPrefix(name, ":")
	for _, each := range own {
		if each.info.ID == name {
			return each, nil
		}
	}
	for _, each := range own {
		if strconv.Itoa(each.info.Index) == bare {
			return each, nil
		}
	}
	for _, each := range own {
		if each.info.Name != "" && each.info.Name == bare {
			return each, nil
		}
	}
	return nil, fmt.Errorf("can't find window: %s", name)
}

// openForAgent opens a terminal for the agent; its program has started when this returns, or it is gone with the error.
func (h *Host) openForAgent(request HostCall) (TerminalInfo, error) {
	type opened struct {
		terminal *hosted
		started  <-chan struct{}
		err      error
	}
	out := do(h, func() opened {
		running := 0
		for _, each := range h.sessionTerminals(request.SessionID) {
			if each.info.Agent && each.info.Exited == nil {
				running++
			}
		}
		if running >= maxAgentTerminals {
			return opened{err: fmt.Errorf("the agent has %d terminals running in this session already: close one with kill-window", maxAgentTerminals)}
		}
		terminal, started, err := h.create(opening{
			sessionID: request.SessionID, cwd: request.Cwd, env: request.Env, cols: request.Cols, rows: request.Rows,
			agent: true, name: request.Name,
		}, defaultTheme)
		if err != nil {
			return opened{err: err}
		}
		// The shell reads it once it is ready, as typed ahead.
		if request.Command != "" {
			h.input(terminal, []byte(request.Command+"\r"))
		}
		return opened{terminal: terminal, started: started}
	})
	if out.err != nil {
		return TerminalInfo{}, out.err
	}
	if out.terminal == nil {
		return TerminalInfo{}, errors.New("The terminal host is closing")
	}
	<-out.started
	type result struct {
		info   TerminalInfo
		failed *string
	}
	state := do(h, func() result {
		info := out.terminal.info.Clone()
		var failed *string
		if info.Exited != nil && info.Exited.Error != nil && *info.Exited.Error != "" {
			failed = info.Exited.Error
			h.close(out.terminal)
		}
		return result{info: info, failed: failed}
	})
	if state.failed != nil {
		return TerminalInfo{}, errors.New(*state.failed)
	}
	return state.info, nil
}

// read is the output since `from` (default: the read cursor) as plain text, after the wait; it moves the read cursor
// past it.
func (h *Host) read(request HostCall, cancelled <-chan struct{}) (TerminalRead, error) {
	type start struct {
		terminal *hosted
		from     int64
		err      error
	}
	begun := do(h, func() start {
		terminal, err := h.target(request.SessionID, request.Target)
		if err != nil {
			return start{err: err}
		}
		from := terminal.cursor
		if request.From != nil {
			from = *request.From
		}
		return start{terminal: terminal, from: from}
	})
	if begun.err != nil {
		return TerminalRead{}, begun.err
	}
	if begun.terminal == nil {
		return TerminalRead{}, errors.New("The terminal host is closing")
	}
	terminal, from := begun.terminal, begun.from
	waited := ""
	if request.Wait != nil {
		var err error
		if waited, err = h.waitFor(terminal, from, *request.Wait, cancelled); err != nil {
			return TerminalRead{}, err
		}
	}
	read := do(h, func() TerminalRead {
		bytes, skipped := terminal.ring.Since(from)
		to := terminal.ring.End()
		terminal.cursor = max(terminal.cursor, to)
		plain := PlainText(decodeOutput(bytes))
		length := jsstring.Length(plain)
		truncated := max(0, length-ReadTextLimit)
		open := h.terminals[terminal.info.ID] == terminal
		var screen *string
		if open && terminal.vt.AlternateScreen() {
			text := capture(terminal.vt, 0, false)
			screen = &text
		}
		text := plain
		if truncated > 0 {
			text = jsstring.Slice(plain, truncated)
		}
		return TerminalRead{
			Terminal: terminal.info.Clone(), From: from, To: to, Text: text, Skipped: skipped, Truncated: truncated,
			Screen: screen, Waited: waited,
		}
	})
	read.Foreground = h.foreground(terminal)
	return read, nil
}

// foreground is the name of the job in front of the terminal's shell, while one runs.
func (h *Host) foreground(terminal *hosted) string {
	pty := do(h, func() *env.RemoteTerminal {
		if h.terminals[terminal.info.ID] != terminal || terminal.pty == nil || terminal.info.Exited != nil {
			return nil
		}
		return terminal.pty
	})
	if pty == nil {
		return ""
	}
	job, err := pty.Foreground(context.Background())
	if err != nil || job == nil {
		return ""
	}
	if job.Name != "" {
		return job.Name
	}
	return "process " + strconv.Itoa(job.Pid)
}

// waitFor waits for the terminal: until its output is quiet, matches, or it exits, whichever was asked, or the timeout.
// It also ends when the program exits or the terminal closes. cancelled ends it with an error.
func (h *Host) waitFor(terminal *hosted, from int64, wait TerminalWait, cancelled <-chan struct{}) (string, error) {
	// A bad pattern fails the read before it waits.
	var pattern *JSRegExp
	if wait.Until == WaitMatch {
		source := ""
		if wait.Pattern != nil {
			source = *wait.Pattern
		}
		var err error
		if pattern, err = CompileJSRegExp(source, wait.IgnoreCase); err != nil {
			return "", err
		}
	}
	finished := make(chan string, 1)
	var cleanup func()
	do(h, func() struct{} {
		var quiet, check, timeout *time.Timer
		over := false
		var self *waiter
		matches := func() bool {
			bytes, _ := terminal.ring.Since(max(from, terminal.ring.End()-matchWindow))
			return pattern != nil && pattern.Test(PlainText(decodeOutput(bytes)))
		}
		finish := func(why string) {
			if over {
				return
			}
			over = true
			for _, timer := range []*time.Timer{timeout, quiet, check} {
				if timer != nil {
					timer.Stop()
				}
			}
			delete(terminal.waiters, self)
			finished <- why
		}
		later := func(delay time.Duration, fn func()) *time.Timer {
			return time.AfterFunc(delay, func() {
				h.post(func() {
					if !over {
						fn()
					}
				})
			})
		}
		self = &waiter{wake: func() {
			switch {
			case over:
			case h.terminals[terminal.info.ID] != terminal:
				if pattern != nil && matches() {
					finish(WaitMatch)
				} else {
					finish("closed")
				}
			case terminal.info.Exited != nil:
				if pattern != nil && matches() {
					finish(WaitMatch)
				} else {
					finish(WaitExit)
				}
			case wait.Until == WaitQuiet:
				if quiet != nil {
					quiet.Stop()
				}
				quietMs := defaultQuietMs
				if wait.QuietMs != 0 {
					quietMs = wait.QuietMs
				}
				quiet = later(time.Duration(quietMs)*time.Millisecond, func() { finish(WaitQuiet) })
			case pattern != nil && check == nil:
				// Output comes in many small chunks: test once they settled a little.
				check = later(50*time.Millisecond, func() {
					check = nil
					if matches() {
						finish(WaitMatch)
					}
				})
			}
		}}
		cleanup = func() { finish("") }
		timeout = later(time.Duration(wait.TimeoutMs)*time.Millisecond, func() { finish("timeout") })
		terminal.waiters[self] = struct{}{}
		if pattern != nil && matches() {
			finish(WaitMatch)
		} else {
			self.wake()
		}
		return struct{}{}
	})
	select {
	case why := <-finished:
		return why, nil
	case <-h.done:
		return "", errors.New("The terminal host is closing")
	case <-cancelled:
		h.post(func() {
			if cleanup != nil {
				cleanup()
			}
		})
		return "", errCancelled
	}
}

// sgrFlags are the SGR parameters of cell flags, in Cell.Flags bit order.
var sgrFlags = [][2]int{
	{ghostty.BOLD, 1}, {ghostty.FAINT, 2}, {ghostty.ITALIC, 3}, {ghostty.UNDERLINE, 4}, {ghostty.BLINK, 5},
	{ghostty.INVERSE, 7}, {ghostty.INVISIBLE, 8}, {ghostty.STRIKETHROUGH, 9},
}

// capture is the screen as plain text, with up to `scrollback` lines before it; trailing blank lines left out. `escapes`
// keeps colors and attributes as SGR sequences (tmux's `capture-pane -e`).
func capture(vt *ghostty.VtTerminal, scrollback int, escapes bool) string {
	total, _, length := vt.Scrollbar()
	first := max(0, total-length-min(scrollback, maxCaptureScrollback))
	var lines []string
	// The viewport shows `length` rows at a time: page from `first` to the bottom, then scroll back down.
	vt.ScrollBottom()
	vt.Scroll(first - (total - length))
	next := first
	for {
		shownTotal, shownOffset, shownLength := vt.Scrollbar()
		frame := vt.Frame(true)
		rows := make([]string, len(frame.Changed))
		for index, row := range frame.Changed {
			if escapes {
				rows[index] = styledRow(row.Cells)
			} else {
				rows[index] = plainRow(row.Cells)
			}
		}
		skip := min(max(next-shownOffset, 0), len(rows))
		lines = append(lines, rows[skip:]...)
		next = shownOffset + len(rows)
		if shownOffset+shownLength >= shownTotal {
			break
		}
		vt.Scroll(shownLength)
	}
	vt.ScrollBottom()
	for len(lines) > 0 && PlainText(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func plainRow(cells []ghostty.Cell) string {
	var row strings.Builder
	for _, cell := range cells {
		if cell.Width == 0 {
			continue
		}
		if cell.Text == "" {
			row.WriteByte(' ')
		} else {
			row.WriteString(cell.Text)
		}
	}
	return jsstring.TrimEnd(row.String())
}

// styledRow is a row with its colors and attributes as SGR sequences, reset at its end.
func styledRow(cells []ghostty.Cell) string {
	color := func(base, value int) []string {
		if value < 0 {
			return nil
		}
		return []string{strconv.Itoa(base), "2", strconv.Itoa(value >> 16 & 0xff), strconv.Itoa(value >> 8 & 0xff), strconv.Itoa(value & 0xff)}
	}
	var row strings.Builder
	style := "0"
	for _, cell := range cells {
		if cell.Width == 0 {
			continue
		}
		params := []string{"0"}
		for _, flag := range sgrFlags {
			if cell.Flags&flag[0] != 0 {
				params = append(params, strconv.Itoa(flag[1]))
			}
		}
		params = append(params, color(38, cell.Fg)...)
		params = append(params, color(48, cell.Bg)...)
		if joined := strings.Join(params, ";"); joined != style {
			row.WriteString("\x1b[" + joined + "m")
			style = joined
		}
		if cell.Text == "" {
			row.WriteByte(' ')
		} else {
			row.WriteString(cell.Text)
		}
	}
	text := row.String()
	// Unstyled trailing blanks carry nothing.
	if style == "0" {
		return strings.TrimRight(text, " ")
	}
	return text + "\x1b[0m"
}
