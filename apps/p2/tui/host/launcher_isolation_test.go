package host_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/gorilla/websocket"
)

// The isolation tests of tui-isolation.test.ts and tui-launcher.test.ts guard a JS sandbox: Pi runs the server-shipped TUI bundle in a permission-dropped worker thread.
// Here the facets are compiled in and run in the launcher's process (D611), so the INTENTIONS are checked on what a visitor can observe instead (see D611):
// what a hostile server can make the launcher fetch, run, reveal or allocate; that a crashing or stuck runtime still leaves the terminal restored once; and that
// a crash is reported sanitized.

// hostile is a server that is not a p2 server: it records what the launcher asks of it and advertises a bundle to run.
type hostile struct {
	server *httptest.Server
	mu     sync.Mutex
	paths  []string
	cookie []string
	// chord answers the WebSocket path; nil refuses it.
	chord func(w http.ResponseWriter, r *http.Request)
	chords atomic.Int32
}

func newHostile(t *testing.T, chord func(w http.ResponseWriter, r *http.Request)) *hostile {
	t.Helper()
	h := &hostile{chord: chord}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.paths = append(h.paths, r.URL.Path)
		h.cookie = append(h.cookie, r.Header.Get("Cookie"))
		h.mu.Unlock()
		switch r.URL.Path {
		case "/__p2_tui":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"` + strings.Repeat("a", 64) + `","bundle":"/__p2_tui/bundle.mjs"}`))
		case "/chord":
			h.chords.Add(1)
			if h.chord != nil {
				h.chord(w, r)
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.server.Close)
	return h
}

func (h *hostile) seen() (paths, cookies []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.paths...), append([]string(nil), h.cookie...)
}

func (h *hostile) offline(t *testing.T, more func(*host.ClientOptions)) *launcher {
	return startLauncher(t, launcherOptions{address: mustURL(h.server.URL), offline: true, options: func(options *host.ClientOptions) {
		options.Credentials = host.Credentials{Cookie: "owner=fixture-only"}
		options.Poll = 20 * time.Millisecond
		options.Reconnect = 10 * time.Millisecond
		if more != nil {
			more(options)
		}
	}})
}

// Pi: "launcher denies unknown capabilities, hides cookies and closes a preparing worker" (the first two thirds; the third is the close test below), and
// "permissions drop precedes module evaluation; all machine escape attempts fail".
//
// Intention: whatever a server says, the visitor's machine is touched only through the launcher's fixed actions, after local confirmation; a server cannot make
// the launcher fetch or run code, nor learn what the visitor's credentials are beyond sending them to itself.
func TestLauncherDeniesUnknownCapabilitiesHidesCookiesAndNeverFetchesOrRunsTheCodeAServerAdvertises(t *testing.T) {
	server := newHostile(t, nil)
	l := server.offline(t, nil)
	pollFor(t, 10*time.Second, func() bool {
		paths, _ := server.seen()
		return containsString(paths, "/__p2_tui") && containsString(paths, "/chord")
	})
	// What the runtime may ask of the launcher is a named action; anything else is denied and has no effect.
	for _, name := range []string{"readFile", "writeFile", "clipboard", "openUrl", "spawn", "exec", "env"} {
		if got := awaitAnswer(t, l.grant(name, map[string]any{"path": "/private", "url": "file:///private", "text": "x"})); got.OK || got.Error != "denied" {
			t.Fatalf("%s: %+v", name, got)
		}
	}
	if len(l.copied()) != 0 || len(l.openedURLs()) != 0 {
		t.Fatalf("a denied request had an effect: copied %v opened %v", l.copied(), l.openedURLs())
	}
	time.Sleep(100 * time.Millisecond)
	paths, cookies := server.seen()
	for _, path := range paths {
		if path != "/__p2_tui" && path != "/chord" {
			t.Fatalf("the launcher asked the server for %q: only its fixed routes are ever requested, never the bundle it advertises (%v)", path, paths)
		}
	}
	for _, cookie := range cookies {
		if cookie != "owner=fixture-only" {
			t.Fatalf("a request carried cookie %q", cookie)
		}
	}
	// The runtime is never given the credentials: nothing it can read or snapshot holds the cookie.
	if encoded := snapshotJSON(t, l); strings.Contains(encoded, "fixture-only") {
		t.Fatalf("the cookie reached the runtime: %s", encoded)
	}
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// Pi: "sandbox close before readiness rejects startup and concurrent termination waits for exit" (and the third part of "launcher denies unknown capabilities,
// hides cookies and closes a preparing worker").
//
// Intention: a client closed before its link is ready ends cleanly: concurrent closes all return only after the one cleanup is done, the visitor's terminal is
// restored exactly once, and nothing is left running.
func TestClosingBeforeTheLinkIsReadyEndsCleanlyAndConcurrentClosesWaitForOneCleanup(t *testing.T) {
	server := newHostile(t, nil)
	l := server.offline(t, nil)
	if l.client.Connected() {
		t.Fatal("the hostile server cannot have connected the link")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.client.Close()
			// Every caller returns after the cleanup: the terminal was restored by then.
			if l.terminal.stopCount() != 1 {
				t.Errorf("a close returned before the terminal was restored once: stops=%d", l.terminal.stopCount())
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("close never returned")
	}
	if !contains(l.terminal.written(), "\x1b[?1049l") {
		t.Fatalf("the terminal's modes were not reset: %q", l.terminal.written())
	}
}

// crashSurface is a "sessions" slot's surface that fails on demand.
type crashSurface struct {
	armedRender, armedInput atomic.Bool
	value                   any
	block                   chan struct{}
	entered                 chan struct{}
}

func (s *crashSurface) Render(int) []string {
	if s.armedRender.Load() {
		if s.block != nil {
			select {
			case s.entered <- struct{}{}:
			default:
			}
			<-s.block
		}
		s.crash()
	}
	return []string{"Original server facet"}
}
func (s *crashSurface) crash() { panic(s.value) }
func (s *crashSurface) Invalidate() {}
func (s *crashSurface) Dispose()    {}
func (s *crashSurface) HandleInput(string) {
	if s.armedInput.Load() {
		s.crash()
	}
}

func crashingFacet(surface *crashSurface) chordhost.HostFacet {
	return chordhost.HostFacet{Name: "crash", Facet: tuiruntime.TuiFacet("sessions", func(*tuiruntime.TuiRuntime) tuiruntime.TuiSurface { return surface }, nil, nil)}
}

// Pi: "a crash reports its sanitized stack: an uncaught exception and a throwing message handler".
//
// Intention: a runtime that crashes (here a panic while rendering and a panic in an input handler) is reported once, with its message and the frames above the
// failure, without control characters, escape sequences or bulk, and the visitor's terminal is restored like on any exit.
func TestACrashReportsItsSanitizedStackAPanicWhileRenderingAndInAnInputHandler(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger func(l *launcher, s *crashSurface)
		frame   string
	}{
		{"render", func(l *launcher, s *crashSurface) {
			s.armedRender.Store(true)
			l.client.Post(func() { l.client.TUI().RequestRender(true) })
		}, "crashSurface"},
		{"input handler", func(l *launcher, s *crashSurface) {
			s.armedInput.Store(true)
			l.terminal.SendInput("x")
		}, "crashSurface"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			surface := &crashSurface{value: "bad \x1b]52;c;secret\x07 render"}
			var mu sync.Mutex
			var failures []string
			var exits atomic.Int32
			l := startLauncher(t, launcherOptions{facets: []chordhost.HostFacet{crashingFacet(surface)}, options: func(options *host.ClientOptions) {
				options.OnFailure = func(message string) { mu.Lock(); failures = append(failures, message); mu.Unlock() }
				options.OnExit = func() { exits.Add(1) }
			}})
			pollFor(t, 10*time.Second, func() bool { return strings.Contains(l.terminal.screen(), "Original server facet") })
			tc.trigger(l, surface)
			pollFor(t, 10*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return len(failures) > 0 })
			mu.Lock()
			message := failures[0]
			count := len(failures)
			mu.Unlock()
			if !strings.HasPrefix(message, "TUI crashed with a panic:\nbad ]52;c;secret render\n    at ") || !strings.Contains(message, tc.frame) {
				t.Fatalf("crash report = %q", message)
			}
			for _, r := range strings.ReplaceAll(message, "\n", "") {
				if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
					t.Fatalf("a control character in %q", message)
				}
			}
			if strings.Contains(message, "/home/") || strings.Contains(message, "/Users/") || strings.Contains(message, "/tmp/") {
				t.Fatalf("a local path in %q", message)
			}
			pollFor(t, 10*time.Second, func() bool { return l.terminal.stopCount() == 1 })
			if !contains(l.terminal.written(), "\x1b[?1049l") {
				t.Fatalf("the terminal was not restored after the crash: %q", l.terminal.written())
			}
			time.Sleep(100 * time.Millisecond)
			mu.Lock()
			defer mu.Unlock()
			if len(failures) != count || count != 1 || exits.Load() != 1 {
				t.Fatalf("a crash is reported once: failures %d exits %d", len(failures), exits.Load())
			}
		})
	}
}

// Pi: "sandbox kills huge V8 allocations and watchdog kills external allocations".
//
// Intention: a runtime gone wrong (here one that never returns from a render) cannot hold the visitor's terminal hostage: Close still restores it, within a bound,
// and the launcher's own interface does not depend on the runtime answering. (Memory use of a compiled-in facet is not bounded by the launcher: D611.)
func TestAStuckRuntimeCannotHoldTheTerminalHostageCloseStillRestoresIt(t *testing.T) {
	surface := &crashSurface{value: "released", block: make(chan struct{}), entered: make(chan struct{}, 1)}
	l := startLauncher(t, launcherOptions{facets: []chordhost.HostFacet{crashingFacet(surface)}})
	t.Cleanup(func() { close(surface.block) })
	pollFor(t, 10*time.Second, func() bool { return strings.Contains(l.terminal.screen(), "Original server facet") })
	surface.armedRender.Store(true)
	l.client.Post(func() { l.client.TUI().RequestRender(true) })
	select {
	case <-surface.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the render never started")
	}
	done := make(chan struct{})
	go func() { l.client.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Close waited on a runtime that never answers")
	}
	if l.terminal.stopCount() != 1 || !contains(l.terminal.written(), "\x1b[?1049l") {
		t.Fatalf("the terminal was not restored: stops=%d written=%q", l.terminal.stopCount(), l.terminal.written())
	}
}

// Intention of the same test: what a server controls about sizes is bounded. A link frame larger than the launcher's bound is not buffered: the link is dropped
// and reconnected, the launcher keeps running.
func TestAServerFrameBeyondTheBoundDropsTheLinkNotTheLauncher(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := newHostile(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.BinaryMessage, make([]byte, host.MaxLinkFrame+1))
		_, _, _ = conn.ReadMessage()
	})
	var failed atomic.Int32
	l := server.offline(t, func(options *host.ClientOptions) { options.OnFailure = func(string) { failed.Add(1) } })
	pollFor(t, 20*time.Second, func() bool { return server.chords.Load() >= 3 })
	if failed.Load() != 0 || l.client.Runtime() == nil {
		t.Fatalf("the launcher failed on an oversized frame: failures %d", failed.Load())
	}
}

func (l *launcher) openedURLs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.opened...)
}

func snapshotJSON(t *testing.T, l *launcher) string {
	t.Helper()
	encoded, err := json.Marshal(l.client.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
