package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord/delta"
	next "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports packages/chord/src/node/tui/launcher.ts. `StartTerminal` (slice 11 §4, decision "Slice 11, P8"): the trusted launcher of
// a terminal client. It signs in with its token (given, from a file, or by pairing), downloads the terminal kind's base bundle and
// plugin scripts, and runs them in the sandbox, always. It keeps the credentials, the real terminal and the user's choices: the
// sandbox gets input and resizes, its frames pass the output filter, its Chord link is a socket the launcher opens and relays,
// its plugin script fetches go only to the terminal surface's files, and its actions on the machine (copy, open a URL, attach a
// file) are confirmed by the launcher's own prompt. A new base bundle on the server starts a new sandbox. Nothing the sandbox
// sends can end the launcher (fix round 2, Opus E3): its messages are checked (pipe), a send on a socket not yet open is dropped,
// every buffer and the requests in flight are bounded, a signal cannot open the launcher's inspector, and however the launcher's
// process ends the terminal is given back (fix round 3, Opus E8, E10, E11). During a prompt Ctrl-C ends the client: its keys are
// the launcher's; and three Ctrl-C within a second always end it (Opus R2).
//
// Forced Go differences (D602). Pi's launcher is one JavaScript thread; this one is a loop goroutine running the events (input,
// resizes, the sandbox's messages, the sockets') one at a time, so its state needs no locks; what blocks (downloads, prompts,
// actions) runs on goroutines of its own that return to the loop through `sync`. `exited` is a channel carrying the exit code.
// The terminal given back when the process exits without `finish` (Pi's `process.once("exit")`) has no Go form: a Go program
// that exits without finishing its launcher leaves the terminal as the sandbox left it; Run's caller defers Stop.

// TerminalOptions is what StartTerminal takes.
type TerminalOptions struct {
	// URL is the app's server (`http://127.0.0.1:8788`).
	URL         string
	Credentials TerminalCredentials
	// Terminal is the terminal's input and output: the process's by default (raw mode while running, when a TTY).
	Terminal Terminal
	// Colors is how many colours the terminal shows ("truecolor" or "256"): true colour when `COLORTERM` says so, else 256.
	Colors string
	// Mode is the terminal's background, choosing between the user's dark and light theme: "dark" by default.
	Mode string
	// Locales are the user's languages, most preferred first, until they choose one: the system's locale by default.
	Locales []string
	// Opener opens a URL the user confirmed: the platform's opener by default.
	Opener Opener
	// Node is the Node (26.5.1 or later) running the sandbox: `CHORD_TUI_NODE`, else `node`.
	Node string
	// Client is the HTTP client of the launcher's downloads and pairing: the default one by default.
	Client *http.Client
}

// RunningTerminal is a running terminal client.
type RunningTerminal struct {
	launcher *Launcher
}

// Exited carries the client's exit code once it ends.
func (r *RunningTerminal) Exited() <-chan int { return r.launcher.exited }

// Stop ends the client (exit code 0) and waits for it.
func (r *RunningTerminal) Stop() { r.launcher.finish(0) }

// terminalPrefix is the terminal surface's files (the server's `TUI_PREFIX`).
const terminalPrefix = "/_chord/tui/"

// terminalFilePattern is a terminal surface file's path, as the sandbox may ask for it.
var terminalFilePattern = regexp.MustCompile(`^/_chord/tui/(?:plugin/)?[\w.~@-]+$`)

// localeSuffix is what follows a locale name: its encoding or modifier (`de_DE.UTF-8@euro`).
var localeSuffix = regexp.MustCompile(`[.@].*$`)

// TerminalFile is path resolved on the server `server` when it names one of the terminal surface's files (dot segments and
// encodings resolved first: Opus M2, Fable L1); it fails otherwise: the sandbox reaches nothing else of the server through the
// launcher's token.
func TerminalFile(path string, server string) (string, error) {
	target, err := nodeurl.ResolveURL(path, server)
	base, baseErr := nodeurl.ParseURL(server)
	if err != nil || baseErr != nil || target.Origin() != base.Origin() || target.Query != "" || !terminalFilePattern.MatchString(target.Pathname) {
		return "", errors.New("terminal: no terminal file")
	}
	return target.Pathname, nil
}

const (
	// maxHeld is the most frames held while a prompt is open: 1 MiB (characters); later ones are dropped.
	maxHeld = 1024 * 1024
	// maxSockets is the most link sockets the sandbox may have open at once.
	maxSockets = 4
	// maxSocketBuffer is the most bytes a link socket may have waiting to go out; a sandbox sending more loses the socket.
	maxSocketBuffer = 16 * 1024 * 1024
	// promptGuard is how long after a prompt appears its keys are ignored: a key typed for the sandbox does not answer it (Opus E4).
	promptGuard = 300 * time.Millisecond
	// resetModes are the terminal modes the sandbox may have set, back to the shell's: sync, mouse, focus, paste, cursor, the
	// scroll region (the cursor kept where it was) and autowrap (Opus E11), screen, colours.
	resetModes = "\x1b[?2026l\x1b[?1000l\x1b[?1002l\x1b[?1006l\x1b[?1004l\x1b[?2004l\x1b[?25h\x1b7\x1b[r\x1b8\x1b[?7h\x1b[?1049l\x1b[0m"
	// maxRequests is the most requests of one sandbox the launcher answers at once; more are refused at once (Opus E10).
	maxRequests = 8
	// interrupt is Ctrl-C: during a prompt it ends the client, so a sandbox asking again and again can always be left (Opus E11);
	// and three within a second always end it, whatever the sandbox does with them (Opus R2).
	interrupt          = '\x03'
	interruptsToEnd    = 3
	interruptWindow    = time.Second
	maxSocketsPayload  = 100 * 1024 * 1024
	defaultColumns     = 80
	defaultRows        = 24
	eventQueue         = 256
	socketOpen         = 1
	socketClosedStatus = 2
)

// StartTerminal starts a terminal client; it fails when it cannot sign in or download, or Node is too old for the sandbox. Cancelling
// ctx ends the client too (as Stop does), whenever it happens.
func StartTerminal(ctx context.Context, options TerminalOptions) (*RunningTerminal, error) {
	if options.Node == "" {
		options.Node = os.Getenv("CHORD_TUI_NODE")
	}
	if options.Node == "" {
		options.Node = "node"
	}
	if options.Terminal == nil {
		options.Terminal = &ProcessTerminal{}
	}
	if options.Client == nil {
		options.Client = http.DefaultClient
	}
	if err := CheckSandboxNode(options.Node); err != nil {
		return nil, err
	}
	base, err := url.Parse(options.URL)
	if err != nil {
		return nil, err
	}
	output := options.Terminal.Output()
	// What the launcher prints of the server's (the pairing's code) passes the output filter too.
	say := func(line string) {
		if filtered, err := FilterOutput(line); err == nil {
			_, _ = io.WriteString(output, filtered+"\r\n")
		}
	}
	token, err := TokenOf(ctx, options.Credentials, base, say, options.Client)
	if err != nil {
		return nil, err
	}
	launcher := newLauncher(ctx, base, token, options)
	if err := launcher.start(ctx); err != nil {
		return nil, err
	}
	return &RunningTerminal{launcher: launcher}, nil
}

// prompt is the prompt open: its lines, its answer, and when its keys start to count. Keys go to it, not the sandbox.
type prompt struct {
	lines  []string
	answer chan bool
	ready  time.Time
}

// linkSocket is one link socket the launcher relays.
type linkSocket struct {
	conn  *websocket.Conn
	state atomic.Int32
	// buffered is the bytes waiting to go out.
	buffered atomic.Int64
	out      chan []byte
	once     sync.Once
}

// Launcher is one terminal client's launcher.
type Launcher struct {
	base    *url.URL
	token   string
	options TerminalOptions
	term    Terminal
	output  io.Writer
	client  *http.Client

	ctx    context.Context
	cancel context.CancelFunc
	events chan func()
	// loopDone is closed when the event loop ended.
	loopDone chan struct{}
	exited   chan int

	// The state below belongs to the loop.
	sandbox *Sandbox
	// generation numbers the sandboxes: a message of one that is not the current is dropped.
	generation int
	// base64 hash of the running base bundle: an `outdated` sandbox restarts only for another.
	baseHash string
	sockets  map[int]*linkSocket
	// confirming is whether an action is being confirmed (one at a time), from its request to its end: meanwhile the sandbox's
	// frames are held (held, at most maxHeld bytes), so nothing draws over the prompt, even before it appears.
	confirming bool
	// requests are the running sandbox's requests being answered (at most maxRequests).
	requests   int
	prompt     *prompt
	held       []string
	heldBytes  int
	interrupts []time.Time
	finished   bool
	stopResize func()
	restoreSig func()
}

func newLauncher(parent context.Context, base *url.URL, token string, options TerminalOptions) *Launcher {
	ctx, cancel := context.WithCancel(parent)
	return &Launcher{
		base: base, token: token, options: options, term: options.Terminal, output: options.Terminal.Output(), client: options.Client,
		ctx: ctx, cancel: cancel, events: make(chan func(), eventQueue), loopDone: make(chan struct{}), exited: make(chan int, 1),
		sockets: map[int]*linkSocket{},
	}
}

// post runs fn on the loop (dropped once the loop ended).
func (l *Launcher) post(fn func()) {
	select {
	case l.events <- fn:
	case <-l.loopDone:
	}
}

// sync runs fn on the loop and waits for it (it does not run once the loop ended).
func (l *Launcher) sync(fn func()) {
	done := make(chan struct{})
	l.post(func() { fn(); close(done) })
	select {
	case <-done:
	case <-l.loopDone:
	}
}

// loop runs the events one at a time.
func (l *Launcher) loop() {
	//portlint:allow doubleclose loop runs once per launcher (started once by start, which Launcher's constructor makes the only caller), and nothing else closes loopDone
	defer close(l.loopDone)
	for {
		select {
		case fn := <-l.events:
			fn()
			if l.finished {
				return
			}
		case <-l.ctx.Done():
			return
		}
	}
}

func (l *Launcher) columns() int {
	if columns := l.term.Columns(); columns > 0 {
		return columns
	}
	return defaultColumns
}

func (l *Launcher) rows() int {
	if rows := l.term.Rows(); rows > 0 {
		return rows
	}
	return defaultRows
}

// start downloads the base bundle and the plugins, starts the sandbox and takes the terminal.
func (l *Launcher) start(ctx context.Context) error {
	//portlint:allow golifetime the loop ends with the launcher: finish ends it (loopDone) and every post and sync gives up once it is closed
	go l.loop()
	if err := l.startSandbox(ctx); err != nil {
		l.cancel()
		return err
	}
	if l.term.IsTTY() {
		_ = l.term.SetRaw(true)
	}
	//portlint:allow golifetime ends when the terminal's input closes (a stopped launcher closes it) and its posts are dropped once the loop ended
	go l.readInput()
	// On the loop: a finish (the sandbox exited meanwhile) reads both, and must not see them half set, nor miss them.
	l.sync(func() {
		if l.finished {
			return
		}
		l.stopResize = l.term.OnResize(func() { l.post(l.onResize) })
		l.restoreSig = ignoreInspectorSignal()
	})
	return nil
}

// readInput posts the terminal's input to the loop until it ends.
func (l *Launcher) readInput() {
	buffer := make([]byte, 4096)
	input := l.term.Input()
	for {
		n, err := input.Read(buffer)
		if n > 0 {
			text := string(buffer[:n])
			l.post(func() { l.onInput(text) })
		}
		if err != nil {
			return
		}
	}
}

// onInput is what the user typed.
func (l *Launcher) onInput(text string) {
	if l.finished {
		return
	}
	if l.interrupted(text) {
		l.finishAsync(130)
		return
	}
	switch {
	case l.prompt == nil:
		if l.sandbox != nil {
			l.sandbox.Post(LauncherMessage{Kind: "input", Data: text})
		}
	case strings.ContainsRune(text, interrupt):
		l.finishAsync(130)
	case !time.Now().Before(l.prompt.ready):
		l.answered(text == "y" || text == "Y")
	}
}

// interrupted is whether text brings the third Ctrl-C within a second (each still goes to the sandbox otherwise).
func (l *Launcher) interrupted(text string) bool {
	now := time.Now()
	for _, character := range text {
		if character == interrupt {
			l.interrupts = append(l.interrupts, now)
		}
	}
	kept := l.interrupts[:0]
	for _, at := range l.interrupts {
		if now.Sub(at) < interruptWindow {
			kept = append(kept, at)
		}
	}
	l.interrupts = kept
	return len(l.interrupts) >= interruptsToEnd
}

func (l *Launcher) onResize() {
	if l.prompt != nil {
		l.drawPrompt()
	}
	if l.sandbox != nil {
		l.sandbox.Post(LauncherMessage{Kind: "resize", Columns: l.columns(), Rows: l.rows()})
	}
}

// finish ends the client with code: the terminal given back, the sockets closed, the sandbox ended.
// finishAsync ends the client from the loop's own goroutine: finish waits for the loop, so it cannot run on it.
func (l *Launcher) finishAsync(code int) {
	//portlint:allow golifetime ends with finish, which closes loopDone and the sandbox; it runs once (the `finished` flag) and every post it makes is dropped once the loop ended
	go l.finish(code)
}

// outdatedAsync restarts the sandbox for a newer base bundle off the loop (it downloads).
func (l *Launcher) outdatedAsync() {
	//portlint:allow golifetime ends with the launcher: its downloads take l.ctx, which finish cancels
	go l.outdated()
}

func (l *Launcher) finish(code int) {
	done := make(chan struct{})
	var sandbox *Sandbox
	l.post(func() {
		defer close(done)
		if l.finished {
			return
		}
		l.finished = true
		if l.stopResize != nil {
			l.stopResize()
		}
		if l.restoreSig != nil {
			l.restoreSig()
		}
		if l.term.IsTTY() {
			_ = l.term.SetRaw(false)
		}
		if l.prompt != nil {
			l.prompt.answer <- false
			l.prompt = nil
		}
		_, _ = io.WriteString(l.output, resetModes)
		l.closeSockets()
		sandbox = l.sandbox
		l.sandbox = nil
		l.generation++
	})
	select {
	case <-done:
	case <-l.loopDone:
	}
	l.cancel()
	if sandbox != nil {
		sandbox.Terminate()
	}
	<-l.loopDone
	select {
	case l.exited <- code:
	default:
	}
}

// startSandbox starts a new sandbox with what the server serves now. It runs off the loop (its downloads block) and takes the
// loop only to install the sandbox.
func (l *Launcher) startSandbox(ctx context.Context) error {
	text, err := l.get(ctx, terminalPrefix+"start")
	if err != nil {
		return err
	}
	var start struct {
		Base     string `json:"base"`
		Script   string `json:"script"`
		Combined string `json:"combined"`
	}
	if err := json.Unmarshal([]byte(text), &start); err != nil {
		return err
	}
	var source, combined string
	var sourceErr, combinedErr error
	var group sync.WaitGroup
	group.Go(func() { source, sourceErr = l.get(ctx, start.Script) })
	group.Go(func() { combined, combinedErr = l.get(ctx, start.Combined) })
	group.Wait()
	if err := errors.Join(sourceErr, combinedErr); err != nil {
		return err
	}
	colors := l.options.Colors
	if colors == "" {
		colors = "256"
		if colorterm := os.Getenv("COLORTERM"); strings.Contains(colorterm, "truecolor") || strings.Contains(colorterm, "24bit") {
			colors = "truecolor"
		}
	}
	mode := l.options.Mode
	if mode == "" {
		mode = "dark"
	}
	locales := l.options.Locales
	if locales == nil {
		locales = systemLocales()
	}
	bridge := BridgeStart{Base: start.Base, Combined: combined, Columns: l.columns(), Rows: l.rows(), Colors: colors, Mode: mode, Locales: locales}
	var generation int
	l.sync(func() {
		l.baseHash = start.Base
		l.requests = 0
		l.generation++
		generation = l.generation
	})
	sandbox, err := StartSandbox(l.options.Node, source, bridge,
		func(message SandboxMessage) { l.post(func() { l.message(generation, message) }) },
		func(string) { l.post(func() { l.sandboxEnded(generation) }) },
	)
	if err != nil {
		return err
	}
	l.sync(func() {
		if l.generation == generation {
			l.sandbox = sandbox
		}
	})
	if l.sandboxIs(sandbox) {
		return nil
	}
	sandbox.Terminate()
	return errors.New("terminal: the client ended while its sandbox started")
}

// sandboxIs is whether sandbox is the running one.
func (l *Launcher) sandboxIs(sandbox *Sandbox) bool {
	var same bool
	l.sync(func() { same = l.sandbox == sandbox })
	return same
}

// sandboxEnded is the sandbox's end (an exit, a crash, a malformed frame): the client ends with code 1.
func (l *Launcher) sandboxEnded(generation int) {
	if generation == l.generation && !l.finished {
		l.finishAsync(1)
	}
}

// message is one message of the sandbox; one the launcher fails on ends the client, never the launcher's process.
func (l *Launcher) message(generation int, message SandboxMessage) {
	if generation != l.generation || l.finished {
		return
	}
	if err := l.handle(message); err != nil {
		l.finishAsync(1)
	}
}

func (l *Launcher) handle(message SandboxMessage) error {
	switch message.Kind {
	case "write":
		l.write(message.Data)
	case "socket_open":
		l.openSocket(message.Socket)
	case "socket_send":
		l.send(message.Socket, message.Bytes)
	case "socket_close":
		if socket := l.sockets[message.Socket]; socket != nil {
			socket.close()
		}
	case "request":
		l.answer(message.ID, message.Request)
	case "outdated":
		l.outdatedAsync()
	case "failed":
		_, _ = io.WriteString(l.output, "terminal: the terminal client failed to start\n")
		l.finishAsync(1)
	case "exit":
		l.finishAsync(message.Code)
	}
	return nil
}

// write writes the sandbox's frame through the output filter (held while a prompt is open); one over 64 KiB is dropped.
func (l *Launcher) write(data string) {
	if l.confirming {
		if l.heldBytes+len(data) <= maxHeld {
			l.held = append(l.held, data)
			l.heldBytes += len(data)
		}
		return
	}
	// Over 64 KiB: dropped, as an escape it could hide would be.
	if filtered, err := FilterOutput(data); err == nil {
		_, _ = io.WriteString(l.output, filtered)
	}
}

// openSocket opens socket id to the links endpoint with the launcher's token, relaying it to the sandbox.
func (l *Launcher) openSocket(id int) {
	// A few at a time: the connection loop needs one; a runaway sandbox gets no more.
	if len(l.sockets) >= maxSockets {
		l.sandbox.Post(LauncherMessage{Kind: "socket_closed", Socket: id})
		return
	}
	target := *l.base.JoinPath("/chord")
	if target.Scheme == "https" {
		target.Scheme = "wss"
	} else {
		target.Scheme = "ws"
	}
	socket := &linkSocket{out: make(chan []byte, 1024)}
	l.sockets[id] = socket
	sandbox := l.sandbox
	headers := http.Header{"Authorization": {"Bearer " + l.token}}
	go func() {
		conn, _, err := websocket.DefaultDialer.DialContext(l.ctx, target.String(), headers) //nolint:bodyclose // gorilla/websocket hands back a handshake response whose body is already read and closed
		closed := func() {
			l.post(func() {
				if l.sockets[id] != socket {
					return
				}
				delete(l.sockets, id)
				sandbox.Post(LauncherMessage{Kind: "socket_closed", Socket: id})
			})
		}
		if err != nil {
			closed()
			return
		}
		conn.SetReadLimit(maxSocketsPayload)
		socket.conn = conn
		socket.state.Store(socketOpen)
		sandbox.Post(LauncherMessage{Kind: "socket_opened", Socket: id})
		go socket.writeLoop()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				break
			}
			sandbox.Post(LauncherMessage{Kind: "socket_message", Socket: id, Bytes: payload})
		}
		socket.close()
		closed()
	}()
}

// writeLoop sends the queued messages in order.
func (s *linkSocket) writeLoop() {
	for payload := range s.out {
		err := s.conn.WriteMessage(websocket.BinaryMessage, payload)
		s.buffered.Add(-int64(len(payload)))
		if err != nil {
			_ = s.conn.Close()
		}
	}
}

// close closes the socket (once).
func (s *linkSocket) close() {
	s.once.Do(func() {
		s.state.Store(socketClosedStatus)
		if s.conn != nil {
			_ = s.conn.Close()
		}
		close(s.out)
	})
}

// send sends bytes on socket id once it is open (before, the sandbox's link holds its messages; a send then is dropped: Opus E3); a
// socket with more than 16 MiB waiting is closed.
func (l *Launcher) send(id int, bytes []byte) {
	socket := l.sockets[id]
	if socket == nil || socket.state.Load() != socketOpen {
		return
	}
	if socket.buffered.Load()+int64(len(bytes)) > maxSocketBuffer {
		socket.close()
		return
	}
	socket.buffered.Add(int64(len(bytes)))
	select {
	case socket.out <- bytes:
	default:
		socket.close()
	}
}

func (l *Launcher) closeSockets() {
	for _, socket := range l.sockets {
		socket.close()
	}
	clear(l.sockets)
}

// answer answers the sandbox's request id on a goroutine of its own.
func (l *Launcher) answer(id int, request BridgeRequest) {
	// The sandbox that asked: a reply never goes to the one a restart started meanwhile.
	sandbox := l.sandbox
	if sandbox == nil {
		return
	}
	if l.requests >= maxRequests {
		sandbox.Post(LauncherMessage{Kind: "refused", ID: id, Message: "terminal: too many requests at once"})
		return
	}
	// A request that asks the user claims the prompt here, on the loop, in the order the sandbox sent: the frames it wrote after
	// the request are held, not drawn before the prompt appears (Fable H1).
	if request.Kind != "fetch" && request.Kind != "base" {
		if l.confirming {
			sandbox.Post(LauncherMessage{Kind: "refused", ID: id, Message: "terminal: a prompt is open"})
			return
		}
		l.confirming = true
	}
	l.requests++
	go func() {
		reply, err := l.reply(id, request)
		if err != nil {
			sandbox.Post(LauncherMessage{Kind: "refused", ID: id, Message: err.Error()})
		} else {
			sandbox.Post(reply)
		}
		l.post(func() {
			if l.sandbox == sandbox {
				l.requests--
			}
		})
	}()
}

// reply is the answer to request id.
func (l *Launcher) reply(id int, request BridgeRequest) (LauncherMessage, error) {
	switch request.Kind {
	case "fetch":
		path, err := TerminalFile(request.Path, l.base.String())
		if err != nil {
			return LauncherMessage{}, err
		}
		text, err := l.get(l.ctx, path)
		return LauncherMessage{Kind: "reply", ID: id, Value: text}, err
	case "base":
		text, err := l.get(l.ctx, terminalPrefix+"base")
		if err != nil {
			return LauncherMessage{}, err
		}
		value, err := delta.DecodeJson([]byte(text))
		return LauncherMessage{Kind: "reply", ID: id, Value: value}, err
	case "attach":
		var file *AttachedFile
		err := l.confirm(func() error {
			var err error
			file, err = Attach(request.Path, l.prompter(), runtimePlatform())
			return err
		})
		if err != nil {
			return LauncherMessage{}, err
		}
		if file != nil {
			return LauncherMessage{Kind: "attached", ID: id, Name: file.Name, Bytes: file.Bytes}, nil
		}
		return LauncherMessage{Kind: "reply", ID: id, Value: nil}, nil
	}
	var done bool
	err := l.confirm(func() error {
		var err error
		done, err = Act(request, l.prompter(), l.options.Opener)
		return err
	})
	return LauncherMessage{Kind: "reply", ID: id, Value: done}, err
}

// confirm runs the action run (it asks the user); the request claimed the prompt (`answer`, one at a time). Afterwards the held
// frames are written and the sandbox asked to redraw.
func (l *Launcher) confirm(run func() error) error {
	err := run()
	l.sync(func() {
		l.confirming = false
		held := l.held
		l.held = nil
		l.heldBytes = 0
		for _, frame := range held {
			l.write(frame)
		}
		if l.sandbox != nil {
			l.sandbox.Post(LauncherMessage{Kind: "resize", Columns: l.columns(), Rows: l.rows()})
		}
	})
	return err
}

// launcherPrompt is the launcher's own prompt on the real terminal, for the action being confirmed: its key not the sandbox's.
type launcherPrompt struct{ l *Launcher }

func (l *Launcher) prompter() PromptTerminal { return launcherPrompt{l} }

func (p launcherPrompt) Columns() int { return p.l.columns() }

func (p launcherPrompt) Ask(lines []string) (bool, error) {
	l := p.l
	// Shown whole or not at all: a prompt is never cut.
	if len(PromptLines(lines, l.columns())) >= l.rows() {
		return false, errors.New("terminal: the prompt does not fit the terminal")
	}
	answer := make(chan bool, 1)
	l.sync(func() {
		l.prompt = &prompt{lines: lines, answer: answer, ready: time.Now().Add(promptGuard)}
		l.drawPrompt()
	})
	select {
	case yes := <-answer:
		return yes, nil
	case <-l.loopDone:
		return false, nil
	}
}

func (p launcherPrompt) Write(data string) {
	p.l.post(func() { _, _ = io.WriteString(p.l.output, data) })
}

// drawPrompt draws the open prompt on the bottom lines after closing a synchronized update the sandbox began: its attributes
// reset and its colours explicit, black on white, so no state the sandbox left (concealed, black on black: Opus E4) applies to it.
func (l *Launcher) drawPrompt() {
	lines := PromptLines(l.prompt.lines, l.columns())
	top := max(1, l.rows()-len(lines)+1)
	var drawn strings.Builder
	for index, line := range lines {
		fmt.Fprintf(&drawn, "\x1b[%d;1H\x1b[0;30;47m\x1b[2K %s ", top+index, line)
	}
	_, _ = io.WriteString(l.output, "\x1b[?2026l\x1b[s"+drawn.String()+"\x1b[0m")
}

// answered is the prompt answered: cleared, the cursor back where the sandbox had it.
func (l *Launcher) answered(yes bool) {
	open := l.prompt
	l.prompt = nil
	rows := PromptLines(open.lines, l.columns())
	top := max(1, l.rows()-len(rows)+1)
	var cleared strings.Builder
	for index := range rows {
		fmt.Fprintf(&cleared, "\x1b[%d;1H\x1b[0m\x1b[2K", top+index)
	}
	_, _ = io.WriteString(l.output, cleared.String()+"\x1b[u")
	open.answer <- yes
}

// outdated is the sandbox saying its code is outdated: a new sandbox only when the server's base bundle is another.
func (l *Launcher) outdated() {
	text, err := l.get(l.ctx, terminalPrefix+"base")
	if err != nil {
		return
	}
	var hash string
	if json.Unmarshal([]byte(text), &hash) != nil {
		return
	}
	var running string
	l.sync(func() { running = l.baseHash })
	if hash != running {
		l.restart()
	}
}

// restart is the server having a new base bundle: a new sandbox with it, the old one ended.
func (l *Launcher) restart() {
	var old *Sandbox
	l.sync(func() {
		l.closeSockets()
		old = l.sandbox
		l.sandbox = nil
		l.generation++
	})
	if old != nil {
		old.Terminate()
	}
	if err := l.startSandbox(l.ctx); err != nil {
		l.finish(1)
		return
	}
	l.post(l.onResize)
}

// get is the text at path on the server, signed in; at most 8 MiB, read no further.
func (l *Launcher) get(ctx context.Context, path string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, l.base.JoinPath(path).String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+l.token)
	response, err := l.client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", fmt.Errorf("terminal: GET %s answered %d", path, response.StatusCode)
	}
	if response.ContentLength > maxBundle {
		return "", fmt.Errorf("terminal: %s exceeds 8 MiB", path)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBundle+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxBundle {
		return "", fmt.Errorf("terminal: %s exceeds 8 MiB", path)
	}
	return string(body), nil
}

// systemLocales are the system's languages: `LANGUAGE` (a colon list), then `LC_ALL`, `LC_MESSAGES`, `LANG` (`de_DE.UTF-8` is
// `de-DE`; `C` and `POSIX` name none), then the runtime's default locale.
func systemLocales() []string {
	values := append(strings.Split(os.Getenv("LANGUAGE"), ":"), os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG"))
	var locales []string
	seen := map[string]bool{}
	add := func(tag string) {
		if tag != "" && tag != "C" && tag != "POSIX" && !seen[tag] {
			seen[tag] = true
			locales = append(locales, tag)
		}
	}
	for _, value := range values {
		add(strings.ReplaceAll(localeSuffix.ReplaceAllString(value, ""), "_", "-"))
	}
	add(next.PlatformLocale())
	return locales
}
