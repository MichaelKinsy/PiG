package host_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

var footer = regexp.MustCompile(`\d%/\d`)

// newSession is the keys of "New session": Ctrl+A, Ctrl+K and the workspace as the cwd.
func (l *launcher) newSession(workspace string) {
	l.t.Helper()
	l.terminal.SendInput(keyEnter)
	l.terminal.SendInput("\x01")
	l.terminal.SendInput("\x0b")
	l.typed(workspace)
	l.terminal.SendInput(keyEnter)
	poll(l.t, func() bool { return l.client.Snapshot().Session != "" })
	poll(l.t, func() bool { return l.client.Snapshot().Screen == "chat" })
	// Keys reach the editor only once the conversation is live for this participant.
	poll(l.t, func() bool { return !contains(l.terminal.screen(), "Connecting conversation") })
}

func TestServerShippedTUIDrivesCreateSendQueuesStopAndReconnect(t *testing.T) {
	var failMu sync.Mutex
	var failed []string
	// Server defaults sit below the visitor's local preferences; privileged bindings never come from the server.
	mine := theme.DarkTheme.WithColor("accent", theme.Str("#123456"))
	mine.Name = "mine"
	l := startReal(t, launcherOptions{
		options: func(options *host.ClientOptions) {
			options.Reconnect, options.Poll = 20*time.Millisecond, 50*time.Millisecond
			options.Capabilities = &host.Capabilities{TrueColor: true, Platform: "linux"}
			options.OnFailure = func(message string) { failMu.Lock(); failed = append(failed, message); failMu.Unlock() }
		},
		preferences: &prefs.TuiLocalPreferences{
			Keybindings: pitui.KeybindingsConfig{"app.model.select": {"f2"}}, Theme: "mine", Settings: prefs.TuiSettings{},
			Themes: map[string]theme.ThemeJSON{"mine": mine}, Problems: []string{},
		},
	})
	fixture := l.fixture
	if err := os.WriteFile(filepath.Join(fixture.DataDir, "tui.json"), []byte(`{"keybindings":{"app.model.select":"ctrl+g","app.clipboard.pasteImage":"f3"},"theme":"light"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	noFailures := func() {
		failMu.Lock()
		defer failMu.Unlock()
		if len(failed) != 0 {
			t.Fatalf("failures: %v", failed)
		}
	}
	l.waitScreen(15*time.Second, "New session")
	noFailures()
	pollFor(t, 5*time.Second, func() bool { return contains(l.terminal.written(), "38;2;18;52;86") })
	l.terminal.SendInput("\x07")
	l.terminal.SendInput("\x1bOR")
	time.Sleep(100 * time.Millisecond)
	if contains(l.terminal.screen(), "Choose default model") {
		t.Fatal("local F2 overrides the server's Ctrl+G")
	}
	l.terminal.SendInput("\x1bOQ")
	l.waitScreen(10*time.Second, "Choose default model")
	l.terminal.SendInput(keyEsc)
	l.waitScreen(10*time.Second, "New session")
	l.newSession(fixture.Workspace)
	l.typed("hello terminal")
	l.terminal.SendInput(keyEnter)
	l.waitScreen(20*time.Second, "Echo: hello terminal")
	// pi's footer: conversation context against the owner's model catalog, model on the right.
	pollFor(t, 10*time.Second, func() bool { return footer.MatchString(l.terminal.screen()) })
	if !contains(l.terminal.screen(), "(faux) faux-1") {
		t.Fatal("the footer names the model")
	}
	// The worker replacement step of Pi (`client.reload()`) has no referent: the facets are compiled into this executable. What it protects, the
	// visitor's unsent draft surviving, is asserted here and across the reconnect below.
	l.typed("unsent draft")
	poll(t, func() bool { return l.draft() == "unsent draft" })
	selected := l.client.Snapshot().Session
	l.terminal.Resize(80, 24)
	l.terminal.SendInput("\x15")
	l.typed("slow stream")
	l.terminal.SendInput(keyEnter)
	// pi's working indicator in the editor border.
	l.waitScreen(10*time.Second, "Working...")
	l.typed("queued text")
	// Alt+Enter in kitty's encoding: the virtual terminal reports the kitty protocol, which the runtime knows.
	l.terminal.SendInput("\x1b[13;3u")
	l.waitScreen(10*time.Second, "Follow-up: queued text")
	l.terminal.SendInput("\x1b[1;3A")
	poll(t, func() bool { return strings.Contains(l.draft(), "queued text") })
	l.terminal.SendInput(keyEsc)
	poll(t, func() bool { return !contains(l.terminal.screen(), "Working...") })
	port := fixture.Server.Port
	plugins := fixture.Server.Plugins
	if err := fixture.Server.Close(p2test.Background); err != nil {
		t.Fatal(err)
	}
	poll(t, func() bool { return !l.client.Connected() })
	entry, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake, noWeb := true, false
	restarted, err := hosts.StartServer(hosts.ServerOptions{DataDir: fixture.DataDir, Port: &port, Fake: &fake, Web: &noWeb, Plugins: plugins, Entry: entry, ExecArgv: []string{hosts.WorkerArg}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close(context.Background()) })
	pollFor(t, 20*time.Second, func() bool {
		screen := l.terminal.screen()
		return l.client.Connected() && contains(screen, "Echo: hello terminal") && contains(screen, "queued text")
	})
	if l.client.Snapshot().Session != selected {
		t.Fatal("the session changed across the reconnect")
	}
	noFailures()
}

func TestALinkThatWentSilentWithoutACloseALaptopSleptTheRelayDroppedItReconnects(t *testing.T) {
	var silent atomic.Bool
	var sockets atomic.Int32
	var failMu sync.Mutex
	var failed []string
	// The first socket dies silently: once `silent`, nothing arrives or leaves, and the far end's close never arrives either; only terminating it here
	// ends it.
	l := startReal(t, launcherOptions{options: func(options *host.ClientOptions) {
		options.Reconnect, options.Poll = 20*time.Millisecond, 50*time.Millisecond
		options.Capabilities = &host.Capabilities{TrueColor: true, Platform: "linux"}
		options.OnFailure = func(message string) { failMu.Lock(); failed = append(failed, message); failMu.Unlock() }
		options.Transport = &silentTransport{Transport: host.CookieTransport(options.URL, options.Credentials), silent: &silent, sockets: &sockets}
	}})
	l.waitScreen(15*time.Second, "New session")
	l.newSession(l.fixture.Workspace)
	silent.Store(true)
	// Chord's heartbeat closes the silent link after 30 s (the default of every host, Pi's too); the launcher then drops the socket.
	pollFor(t, 60*time.Second, func() bool { return sockets.Load() == 2 && l.client.Connected() })
	pollFor(t, 15*time.Second, func() bool { return !contains(l.terminal.screen(), "Connecting conversation") })
	failMu.Lock()
	defer failMu.Unlock()
	if len(failed) != 0 {
		t.Fatalf("failures: %v", failed)
	}
}

// silentTransport is Pi's wrapper: the first socket dies silently once `silent` is set.
type silentTransport struct {
	host.Transport
	silent  *atomic.Bool
	sockets *atomic.Int32
}

func (s *silentTransport) Socket(path string, events host.SocketEvents) host.Socket {
	dies := s.sockets.Add(1) == 1
	var mu sync.Mutex
	gone, terminated, ended := false, false, false
	end := func() {
		mu.Lock()
		already := ended
		ended = true
		mu.Unlock()
		if !already {
			events.Close()
		}
	}
	quiet := func() bool { return dies && s.silent.Load() }
	inner := s.Transport.Socket(path, host.SocketEvents{
		Open:    events.Open,
		Refused: events.Refused,
		Message: func(data []byte, binary bool) {
			if !quiet() {
				events.Message(data, binary)
			}
		},
		Close: func() {
			mu.Lock()
			gone = true
			finished := !quiet() || terminated
			mu.Unlock()
			if finished {
				end()
			}
		},
	})
	return &silentSocket{inner: inner, quiet: quiet, terminate: func() {
		mu.Lock()
		terminated = true
		closed := gone
		mu.Unlock()
		inner.Terminate()
		if closed {
			end()
		}
	}}
}

type silentSocket struct {
	inner     host.Socket
	quiet     func() bool
	terminate func()
}

func (s *silentSocket) Open() bool { return s.inner.Open() }
func (s *silentSocket) Send(data []byte) error {
	if s.quiet() {
		return nil
	}
	return s.inner.Send(data)
}
func (s *silentSocket) Terminate() { s.terminate() }

func TestAClickOnTheEditorKeepsKeysOnTheRuntimeCtrlCStillClearsAndTheScreenStillSwitches(t *testing.T) {
	l := startReal(t, launcherOptions{mouse: host.MouseOn, options: func(options *host.ClientOptions) {
		options.Capabilities = &host.Capabilities{TrueColor: true, Platform: "linux"}
	}})
	l.waitScreen(15*time.Second, "New session")
	l.newSession(l.fixture.Workspace)
	l.typed("clicked draft")
	poll(t, func() bool { return l.draft() == "clicked draft" })
	l.waitScreen(10*time.Second, "clicked draft")
	// An SGR click on the editor's line: pi-tui would focus the editor itself, bypassing the runtime's commands.
	row := -1
	for index, line := range l.terminal.Viewport() {
		if contains(line, "clicked draft") {
			row = index + 1
			break
		}
	}
	if row <= 0 {
		t.Fatalf("the draft is not on the screen:\n%s", l.terminal.screen())
	}
	l.terminal.SendInput(press(4, row))
	l.terminal.SendInput(release(4, row))
	l.terminal.SendInput("!")
	poll(t, func() bool { return strings.Contains(l.draft(), "!") })
	// Ctrl+C (kitty encoding; the virtual terminal reports the protocol) is app.clear.
	l.terminal.SendInput("\x1b[99;5u")
	poll(t, func() bool { return l.draft() == "" })
	// Ctrl+R (p2.sessions) opens the sessions screen, whose search gets the next keys, not the chat draft.
	l.terminal.SendInput("\x1b[114;5u")
	poll(t, func() bool { return l.client.Snapshot().Screen == "sessions" })
	l.terminal.SendInput("x")
	time.Sleep(200 * time.Millisecond)
	if l.draft() != "" {
		t.Fatalf("the chat draft got the key: %q", l.draft())
	}
}

func TestAServerOnANewerLinkProtocolIsReportedAsAnOutdatedBuildNotRetriedForever(t *testing.T) {
	var failMu sync.Mutex
	var failed []string
	var exited atomic.Bool
	newer := &newerTransport{sockets: make(chan *newerSocket, 8)}
	l := startLauncher(t, launcherOptions{offline: true, options: func(options *host.ClientOptions) {
		options.Transport = newer
		newer.Transport = host.CookieTransport(options.URL, options.Credentials)
		options.OnFailure = func(message string) { failMu.Lock(); failed = append(failed, message); failMu.Unlock() }
		options.OnExit = func() { exited.Store(true) }
	}})
	_ = l
	(<-newer.sockets).fire()
	pollFor(t, 20*time.Second, exited.Load)
	failMu.Lock()
	defer failMu.Unlock()
	if len(failed) != 1 || failed[0] != host.OutdatedBuildMessage {
		t.Fatalf("failures = %v", failed)
	}
}

// newerTransport is a server whose link greeting is of the next protocol version: the test fires the socket once the client holds it.
type newerTransport struct {
	host.Transport
	sockets chan *newerSocket
}

func (n *newerTransport) Socket(path string, events host.SocketEvents) host.Socket {
	if path != "/chord" {
		return n.Transport.Socket(path, events)
	}
	socket := &newerSocket{events: events}
	n.sockets <- socket
	return socket
}

type newerSocket struct {
	events host.SocketEvents
	once   sync.Once
}

func (s *newerSocket) fire() {
	s.events.Open()
	if hello, err := link.BinaryCodec.Encode(&link.HelloMessage{Protocol: data.ProtocolVersion + 1, Incarnation: "newer", Calls: 256}); err == nil {
		s.events.Message(hello, true)
	}
}
func (*newerSocket) Open() bool        { return true }
func (*newerSocket) Send([]byte) error { return nil }
func (s *newerSocket) Terminate()      { s.once.Do(s.events.Close) }

// scriptedSurface is the "sessions" surface of Pi's FAKE_WORKER: it requests a copy on "r" (off the owner loop, as the runtime's bridge does).
type scriptedSurface struct {
	rt      *tuiruntime.TuiRuntime
	answers chan tuiruntime.GrantResult
}

func (s *scriptedSurface) Render(int) []string { return []string{"READY"} }
func (s *scriptedSurface) Invalidate()         {}
func (s *scriptedSurface) Dispose()            {}
func (s *scriptedSurface) HandleInput(data string) {
	if data == "r" {
		go func() { s.answers <- s.rt.OnGrant("copy", map[string]any{"text": "requested text"}) }()
	}
}

// Pi: "launcher: the clipboard is written only after the copy key or a confirmation; the editor result is dropped after navigation; suspend resets modes".
func TestLauncherClipboardIsWrittenOnlyAfterTheCopyKeyOrAConfirmationTheEditorResultIsDroppedAfterNavigationSuspendResetsModes(t *testing.T) {
	surface := &scriptedSurface{answers: make(chan tuiruntime.GrantResult, 4)}
	facet := chordhost.HostFacet{Name: "fake", Facet: tuiruntime.TuiFacet("sessions", func(rt *tuiruntime.TuiRuntime) tuiruntime.TuiSurface {
		surface.rt = rt
		return surface
	}, nil, nil)}
	editing, suspended := make(chan struct{}), make(chan struct{})
	var editorStarted, suspendStarted atomic.Bool
	l := startLauncher(t, launcherOptions{mouse: host.MouseOn, facets: []chordhost.HostFacet{facet}, actionOptions: func(options *host.LauncherActionOptions) {
		options.Editor = []string{"fake-editor"}
		options.RunEditor = func([]string, string) (int, bool) {
			editorStarted.Store(true)
			<-editing
			return 0, true
		}
		options.SuspendProcess = func() error {
			suspendStarted.Store(true)
			<-suspended
			return nil
		}
		options.ReadClipboard = &host.ClipboardReaders{Files: func() []string { return nil }, Image: func() *host.ClipboardImage { return nil }, Text: func() string { return "" }}
	}})
	var editorResults atomic.Int32
	l.client.Do(func() {
		rt := l.client.Runtime()
		rt.LauncherAnswers["copyText"] = func(any) any { return map[string]any{"text": "the answer"} }
		rt.LauncherAnswers["editorText"] = func(any) any { return map[string]any{"text": "draft"} }
		rt.LauncherAnswers["editorResult"] = func(any) any { editorResults.Add(1); return map[string]any{"applied": true} }
		rt.Navigate("sessions", "s1", true, nil)
	})
	l.waitScreen(10*time.Second, "READY")
	for _, key := range []string{"a", keyEnter, keyEsc, "\x03", "\x16", "c", "y"} {
		l.terminal.SendInput(key)
	}
	time.Sleep(100 * time.Millisecond)
	if len(l.copied()) != 0 {
		t.Fatalf("unrelated keys wrote the clipboard: %v", l.copied())
	}
	l.terminal.SendInput(keyCtrlX)
	poll(t, func() bool { return len(l.copied()) == 1 })
	if l.copied()[0] != "the answer" {
		t.Fatalf("copied %q", l.copied())
	}
	l.terminal.SendInput("r")
	pollFor(t, 10*time.Second, func() bool {
		return contains(l.terminal.screen(), "requested text") || contains(l.terminal.screen(), "Copy ")
	})
	t.Logf("consent screen:\n%s", l.terminal.screen())
	if len(l.copied()) != 1 {
		t.Fatal("a request copied before the confirmation")
	}
	l.terminal.SendInput(keyEsc)
	if got := awaitAnswer(t, surface.answers); got.Error != "denied" {
		t.Fatalf("declined: %+v", got)
	}
	if len(l.copied()) != 1 {
		t.Fatal("denied: nothing copied")
	}
	l.terminal.clear()
	l.terminal.SendInput("r")
	pollFor(t, 10*time.Second, func() bool { return contains(l.terminal.written(), "requested text") })
	l.terminal.SendInput("y")
	poll(t, func() bool { return len(l.copied()) == 2 })
	if got := awaitAnswer(t, surface.answers); !got.OK {
		t.Fatalf("confirmed: %+v", got)
	}

	// External editor: runtime IO suspended, terminal lent; navigating meanwhile drops the result.
	time.Sleep(1500 * time.Millisecond)
	l.terminal.clear()
	l.terminal.SendInput(keyCtrlG)
	poll(t, editorStarted.Load)
	if l.terminal.stopCount() != 1 {
		t.Fatalf("stops = %d", l.terminal.stopCount())
	}
	if written := l.terminal.written(); !contains(written, "\x1b[?7h") || !contains(written, "\x1b[?1049l") {
		t.Fatalf("the terminal was not lent: %q", written)
	}
	l.client.Do(func() { l.client.Runtime().Navigate("sessions", "s2", true, nil) })
	time.Sleep(100 * time.Millisecond)
	l.terminal.clear()
	close(editing)
	pollFor(t, 10*time.Second, func() bool { return contains(l.terminal.written(), "\x1b[?1049h\x1b[?7l") })
	if editorResults.Load() != 0 {
		t.Fatal("the editor result reached the runtime after navigation")
	}

	// Suspend: modes undone and the terminal stopped before SIGTSTP; restored on SIGCONT.
	l.terminal.clear()
	l.terminal.SendInput("\x1a")
	poll(t, suspendStarted.Load)
	if l.terminal.stopCount() != 2 {
		t.Fatalf("stops = %d", l.terminal.stopCount())
	}
	if written := l.terminal.written(); !contains(written, "\x1b[?7h\x1b[?1000l") {
		t.Fatalf("modes not undone before suspend: %q", written)
	}
	close(suspended)
	pollFor(t, 10*time.Second, func() bool { return contains(l.terminal.written(), "\x1b[?1049h") })
}

// Pi: "changed server facet bundle swaps worker after readiness; invalid reload keeps the old version".
//
// Intention (D611): a new server build goes live for an attached visitor without a blank screen or a lost state. The TUI facets are compiled into the client, so
// the attached client cannot follow a changed facet; what the visitor must keep is the link, the screen, the unsent draft and a working conversation while the
// server's plugins (the sessions surface's and the chat's) are rolled out. (An invalid reload keeping the old version has no referent: compiled code is never invalid
// at load.)
func TestAServerPluginReloadWhileATUIClientIsAttachedKeepsItsLinkScreenAndDraftAndTheConversationKeepsWorking(t *testing.T) {
	var rebuilds p2test.Rebuilds
	l := startReal(t, launcherOptions{}, func(options *hosts.ServerOptions) { options.BuildRevision = rebuilds.Revision })
	l.openNewestSession()
	l.typed("kept draft")
	poll(t, func() bool { return l.draft() == "kept draft" })
	session := l.client.Snapshot().Session
	for _, plugin := range []string{"sessions", "chat", "status"} {
		rebuilds.Rebuild(plugin)
	}
	result, err := l.fixture.Server.Reload([]string{"sessions", "chat", "status"}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Server.Failed) != 0 {
		t.Fatalf("the reload failed: %+v", result.Server.Failed)
	}
	time.Sleep(500 * time.Millisecond)
	if !l.client.Connected() || l.client.Snapshot().Session != session || l.draft() != "kept draft" || !contains(l.terminal.screen(), "kept draft") {
		t.Fatalf("the attached client lost its state: connected %v session %q draft %q\n%s", l.client.Connected(), l.client.Snapshot().Session, l.draft(), l.terminal.screen())
	}
	l.terminal.SendInput(keyEnter)
	l.waitScreen(20*time.Second, "Echo: kept draft")
}
