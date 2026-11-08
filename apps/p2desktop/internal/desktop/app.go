// Package desktop is the p2 desktop app: the p2 server running in this process, listening on loopback, and one window of
// the system's web view showing it, with the menus, tray, notifications and pig:// links a desktop app has.
//
// It is a client of p2 like the browser tab: the server, its protocol and its data are p2's, unchanged. Nothing here reaches
// into the server except the owner clients it opens like any other (the notifier) and the lifecycle it owns (start, close).
// The framework is behind package shell.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/leakcheck"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell"
)

var configureFetch sync.Once

// Config is what New takes.
type Config struct {
	Platform shell.Platform
	// Title is the window title until the page sets its own.
	Title string
	// DataDir is p2's data directory ("": P2_DATA_DIR, else ~/.pi/p2, as `p2 server`).
	DataDir string
	// Fake runs the server on the faux provider (nil: P2_FAKE=1).
	Fake *bool
	// Idle is how long a session worker stays up when idle (0: p2's default).
	Idle time.Duration
	// StateDir keeps the desktop app's own state ("": the user's config directory).
	StateDir string
	// Port is the loopback port the server asks for. nil: the one of the last launch if free, else any free port. 0: any
	// free port.
	Port *int
	// TrayIcon is the tray icon's PNG, a monochrome template image when TrayTemplate; nil leaves the app without a tray.
	TrayIcon     []byte
	TrayTemplate bool
	// LeakCheck makes shutdown check that no goroutine started by the server outlives it, and report it on stderr.
	LeakCheck bool
	// Errors receives diagnostics (nil: stderr).
	Errors func(format string, args ...any)
}

// App is the desktop app.
type App struct {
	cfg  Config
	p    shell.Platform
	base leakcheck.Snapshot

	mu       sync.Mutex
	window   shell.Window
	server   *hosts.P2Server
	notifier *notifier
	origin   string
	pending  string
	closed   bool
	starting sync.WaitGroup
	// notices are the notifications being shown (the first can wait for the user's answer); cancel ends the waits.
	notices sync.WaitGroup
	cancel  context.CancelFunc
	ctx     context.Context
	// ready is closed once the server is listening and the window loaded it (or the start failed).
	ready chan struct{}
	// leak is the shutdown's leak check result, nil when it passed or was not asked for.
	leak error
}

// New is an App that has not started.
func New(cfg Config) *App {
	if cfg.Errors == nil {
		cfg.Errors = func(format string, args ...any) { fmt.Fprintf(os.Stderr, "p2 desktop: "+format+"\n", args...) }
	}
	if cfg.StateDir == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			cfg.StateDir = filepath.Join(dir, "p2-desktop")
		}
	}
	if cfg.Title == "" {
		cfg.Title = "PiG"
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &App{cfg: cfg, p: cfg.Platform, ready: make(chan struct{}), ctx: ctx, cancel: cancel}
}

// Ready is closed once the server is up and the window shows it (or the start failed).
func (a *App) Ready() <-chan struct{} { return a.ready }

// Origin is the server's origin ("http://127.0.0.1:<port>"), empty before it listens.
func (a *App) Origin() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.origin
}

// LeakError is what the shutdown's leak check found, valid once Run returned.
func (a *App) LeakError() error { return a.leak }

// Run runs the app until it quits. It returns at once, doing nothing, when another process of the app holds the single
// instance lock (that process got this one's command line).
func (a *App) Run() error {
	if !a.p.SingleInstance() {
		return nil
	}
	if a.cfg.LeakCheck {
		a.base = leakcheck.Take()
	}
	return a.p.Run(shell.Handlers{
		OpenURL:           a.openURL,
		SecondInstance:    a.secondInstance,
		NotificationClick: a.notificationClick,
		Activate:          func() { a.show() },
		Command:           a.command,
		Quit:              a.shutdown,
	}, a.start)
}

// start runs on the event loop once the app launched: menus, tray, the window, and the server in the background so the
// window is up at once.
func (a *App) start() {
	a.p.SetMenu(buildMenu(runtime.GOOS, a.p.Debug()))
	if len(a.cfg.TrayIcon) > 0 {
		if err := a.p.NewTray(shell.TraySpec{Icon: a.cfg.TrayIcon, Template: a.cfg.TrayTemplate, ToolTip: a.cfg.Title, Menu: buildTray()}); err != nil {
			a.cfg.Errors("no tray icon: %v", err)
		}
	}
	a.openWindow()
	a.starting.Add(1)
	go func() {
		defer a.starting.Done()
		a.startServer()
	}()
}

func (a *App) startServer() {
	defer close(a.ready)
	server, err := a.listen()
	if err != nil {
		a.cfg.Errors("the server did not start: %v", err)
		a.p.ShowError("The p2 server did not start", err.Error())
		a.p.Quit()
		return
	}
	origin := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(server.Port))
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		_ = server.Close(context.Background())
		return
	}
	a.server, a.origin = server, origin
	route := a.pending
	a.pending = ""
	window := a.window
	a.mu.Unlock()
	if a.cfg.StateDir != "" {
		if err := saveState(a.cfg.StateDir, state{Port: server.Port}); err != nil {
			a.cfg.Errors("remembering the port: %v", err)
		}
	}
	if note, err := startNotifier(server.Host, a.sessionStopped); err != nil {
		a.cfg.Errors("no notifications of finished sessions: %v", err)
	} else {
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			note.close()
		} else {
			a.notifier = note
			a.mu.Unlock()
		}
	}
	if route == "" {
		route = "/"
	}
	if window != nil {
		window.Load(origin + route)
	}
}

// pig additive (D480): the server runs in the app's process on loopback; the port of the last launch is asked for first so the
// web UI keeps its per-origin settings.
// listen starts the server on the port asked for: the last one when it is free, else any free port.
func (a *App) listen() (*hosts.P2Server, error) {
	port := 0
	switch {
	case a.cfg.Port != nil:
		port = *a.cfg.Port
	case a.cfg.StateDir != "":
		port = loadState(a.cfg.StateDir).Port
	}
	// As `p2 server` does, before any provider request or login fetch of this process.
	configureFetch.Do(p2runtime.ConfigureFetch)
	options := hosts.ServerOptions{DataDir: a.cfg.DataDir, Port: &port, Fake: a.cfg.Fake, Idle: a.cfg.Idle}
	server, err := hosts.StartServer(options)
	var listenErr *net.OpError
	if err != nil && port != 0 && errors.As(err, &listenErr) {
		// The port of the last launch is taken: any free one, and the web UI starts with its defaults on the new origin.
		zero := 0
		options.Port = &zero
		server, err = hosts.StartServer(options)
	}
	return server, err
}

// openWindow opens the main window, on the server's page once it is up.
func (a *App) openWindow() {
	a.mu.Lock()
	origin := a.origin
	a.mu.Unlock()
	spec := shell.WindowSpec{
		Title:      a.cfg.Title,
		Width:      1280,
		Height:     840,
		MinWidth:   480,
		MinHeight:  360,
		StateKey:   "main",
		Background: "light-dark(#ffffff, #1b1b1d)",
		DevTools:   a.p.Debug(),
		Microphone: true,
	}
	if origin != "" {
		spec.URL, spec.Origin = origin+"/", origin
	}
	// Without an origin the window shows a blank page; Load gives it the server's origin once the server listens.
	window, err := a.p.NewWindow(spec)
	if err != nil {
		a.cfg.Errors("window: %v", err)
		return
	}
	window.OnClosed(func() {
		a.mu.Lock()
		if a.window == window {
			a.window = nil
		}
		a.mu.Unlock()
	})
	a.mu.Lock()
	a.window = window
	a.mu.Unlock()
}

// show brings the window forward, opening it when it was closed, on route when given.
func (a *App) show() {
	a.mu.Lock()
	window := a.window
	a.mu.Unlock()
	if window == nil {
		a.openWindow()
		a.mu.Lock()
		window = a.window
		a.mu.Unlock()
	}
	if window != nil {
		window.Focus()
	}
}

// navigate opens a page of the server in the window.
func (a *App) navigate(route string) {
	a.mu.Lock()
	origin := a.origin
	if origin == "" {
		// The server is still starting; startServer loads it.
		a.pending = route
		a.mu.Unlock()
		a.show()
		return
	}
	window := a.window
	a.mu.Unlock()
	if window == nil {
		a.openWindow()
		a.mu.Lock()
		window = a.window
		a.mu.Unlock()
	}
	if window != nil {
		window.Load(origin + route)
		window.Focus()
	}
}

func (a *App) openURL(raw string) {
	route, ok := RouteOf(raw)
	if !ok {
		a.cfg.Errors("ignoring a link it does not know: %q", raw)
		return
	}
	a.navigate(route)
}

func (a *App) secondInstance(args []string) {
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), Scheme+"://") {
			a.openURL(arg)
			return
		}
	}
	a.show()
}

func (a *App) notificationClick(id string) {
	if session, ok := strings.CutPrefix(id, "session/"); ok {
		if route, ok := sessionRoute(session); ok {
			a.navigate(route)
			return
		}
	}
	a.show()
}

func (a *App) command(cmd shell.Command) {
	switch cmd {
	case shell.CmdShow:
		a.show()
	case shell.CmdQuit:
		a.p.Quit()
	}
}

// sessionStopped tells the user a session stopped working, unless the window is in front.
func (a *App) sessionStopped(session, kind string) {
	a.mu.Lock()
	window := a.window
	a.mu.Unlock()
	if window != nil && window.Focused() {
		return
	}
	title, body := "Session finished", "A session stopped working."
	if kind == "failed" {
		title, body = "Session needs attention", "A session stopped with an error."
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.notices.Add(1)
	a.mu.Unlock()
	// Not on the notifier's goroutine: the first notification waits for the user's answer to the system's question, and
	// the status watch behind the notifier must keep flowing, and must be stoppable.
	go func() {
		defer a.notices.Done()
		err := a.p.Notify(a.ctx, shell.Notification{ID: "session/" + session, Title: title, Body: body})
		if err != nil && !errors.Is(err, shell.ErrNotificationsDenied) && a.ctx.Err() == nil {
			a.cfg.Errors("notification: %v", err)
		}
	}()
}

// shutdown runs when the event loop stopped: the notifier and the server close (workers, sockets, host, store, lock), then
// the leak check, when asked for, looks at what is left.
func (a *App) shutdown() {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	a.cancel()
	a.starting.Wait()
	a.mu.Lock()
	server, note := a.server, a.notifier
	a.server, a.notifier = nil, nil
	a.mu.Unlock()
	if note != nil {
		note.close()
	}
	a.notices.Wait()
	if server != nil {
		if err := server.Close(context.Background()); err != nil {
			a.cfg.Errors("closing the server: %v", err)
		}
	}
	if a.cfg.LeakCheck {
		if a.leak = leakcheck.Check(a.base, 10*time.Second); a.leak != nil {
			a.cfg.Errors("leak check: %v", a.leak)
		} else {
			a.cfg.Errors("leak check: no goroutine outlived the server")
		}
	}
}
