// Package surfacehost is the native desktop client as a surface host: a third host next to Chord's web and terminal
// hosts, consuming the same surfaces contract in process. It runs Chord's client (surfaces.StartClient) with a link to
// the p2 server's Host over a memory pair as the device owner: no JavaScript, no webview, no loopback. The client loads
// the Go plugins this kind of client has (registered scripts whose default export is a facet, as the web and terminal
// load theirs), holds the desktop's scripts object, and keeps its link with the connection loop. The host follows the
// active theme and language (the app kit's Themes, ThemeChoice, Translations and locale choice), and gives each screen a
// Scope (chord/next/tui's TuiScope: references and watches that call one `changed` and release at Dispose).
//
// A view is a pure function of what a Scope reads: nothing here draws. The package imports no GUI framework; the MyGo
// views are in shell/mygoshell/native.
package surfacehost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/chord/next/tui"
)

// Options is what Open takes.
type Options struct {
	// Server is the in-process server's host; the client joins it as the device owner (loopback's rule, p2's `authenticate`).
	Server host.Host
	// Plugins are the client's Go plugins: each registered script's default export is the plugin's facet.
	Plugins []surfaces.RegisteredScript
	// Modules are the host packages the plugins' require answers, by specifier.
	Modules surfaces.ClientModules
	// Mode is the window's colour mode at start, "dark" or "light".
	Mode string
	// Preferred are the device's languages, most preferred first.
	Preferred []string
	// Changed is called after any change of the host's own state (theme, language, connection), from any goroutine: the
	// window draws again. Screens' scopes have their own.
	Changed func()
	// Outdated says the server publishes another base than this build's: the app is outdated and starts over.
	Outdated func()
	// OnError receives what the client reports: host errors, plugins left out, failed updates, messages of the language.
	OnError func(error)
}

// Host is the open client.
type Host struct {
	Client host.Host

	ctx    context.Context
	cancel context.CancelFunc
	client *surfaces.Client
	opts   Options

	theme    *surfaces.ThemeFollower
	language *surfaces.LanguageFollower

	mu         sync.Mutex
	current    surfaces.Theme
	connection surfaces.ConnectionState
	failed     []host.PluginFailure
	closed     bool
	wakes      []func()
}

// Open starts the client: it joins opts.Server, loads the kit's registries (Themes, Translations), the PiG themes and
// the plugins, and follows the active theme and language. A plugin whose script failed is left out and reported through
// OnError and Failed.
func Open(opts Options) (*Host, error) {
	if opts.Server == nil {
		return nil, errors.New("surfacehost: no server host")
	}
	if opts.Mode == "" {
		opts.Mode = "dark"
	}
	if opts.Changed == nil {
		opts.Changed = func() {}
	}
	if opts.Outdated == nil {
		opts.Outdated = func() {}
	}
	if opts.OnError == nil {
		opts.OnError = func(error) {}
	}
	ctx, cancel := context.WithCancel(chordctx.Background())
	h := &Host{ctx: ctx, cancel: cancel, opts: opts, current: surfaces.FallbackThemes[opts.Mode], connection: surfaces.Connecting}
	core := []host.HostFacet{
		{Name: "chord.themes", Facet: surfaces.ThemesFacet()},
		{Name: "chord.translations", Facet: surfaces.TranslationsFacet()},
	}
	core = append(core, BrandHostFacets()...)
	owner := p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}
	byName := map[string]surfaces.RegisteredScript{}
	for _, script := range opts.Plugins {
		byName[script.Plugin] = script
	}
	client, err := surfaces.StartClient(surfaces.ClientOptions{
		Scripts: DesktopScripts,
		Base:    BaseHash,
		Initial: opts.Plugins,
		Modules: opts.Modules,
		Core:    core,
		// One memory pair per attempt: the server's end is accepted as the device owner.
		Open: func(events surfaces.SocketEvents) link.Link {
			down, up := link.MemoryLinkPair()
			if err := opts.Server.Accept(down, host.LinkOptions{Principal: p2runtime.ToChord(owner)}, ctx); err != nil {
				opts.OnError(fmt.Errorf("surfacehost: the server refused the link: %w", err))
				up.Close()
				return up
			}
			events.Opened()
			return up
		},
		// The scripts are Go functions of this build: the server's set can only name them.
		FetchScript: func(_ context.Context, entry surfaces.ScriptEntry) (surfaces.RegisteredScript, error) {
			if script, ok := byName[entry.Name]; ok && script.Digest == entry.Digest {
				return script, nil
			}
			return surfaces.RegisteredScript{}, fmt.Errorf("surfacehost: this build has no script %q (digest %s)", entry.Name, entry.Digest)
		},
		// The server runs in this process: its base is this build's.
		ServerBase: func(context.Context) (*string, error) { base := BaseHash; return &base, nil },
		Outdated:   opts.Outdated,
		OnConnection: func(state surfaces.ConnectionState) {
			h.mu.Lock()
			h.connection = state
			h.mu.Unlock()
			opts.Changed()
		},
		Environment: h.environment(),
		OnError: func(err surfaces.ClientError) {
			if err.Kind == surfaces.ClientPluginsError {
				h.mu.Lock()
				h.failed = append(h.failed, err.Failed...)
				h.mu.Unlock()
			}
			opts.OnError(describe(err))
		},
	}, ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	h.client, h.Client = client, client.Host
	fail := func(err error) (*Host, error) {
		h.shutdown()
		return nil, err
	}
	h.theme, err = surfaces.FollowActiveTheme(h.Client, opts.Mode, func(theme surfaces.Theme) {
		h.mu.Lock()
		h.current = theme
		h.mu.Unlock()
		opts.Changed()
	}, ctx)
	if err != nil {
		return fail(err)
	}
	h.language, err = surfaces.FollowActiveLanguage(h.Client, opts.Preferred, func(*surfaces.Language) { opts.Changed() },
		func(report surfaces.MessagesReport) { opts.OnError(fmt.Errorf("surfacehost: messages: %+v", report)) }, ctx)
	if err != nil {
		return fail(err)
	}
	return h, nil
}

// describe is a client error as an error.
func describe(err surfaces.ClientError) error {
	switch err.Kind {
	case surfaces.ClientPluginsError:
		return fmt.Errorf("surfacehost: plugins left out: %+v", err.Failed)
	case surfaces.ClientUpdateFailed, surfaces.ClientConnectFailed:
		return fmt.Errorf("surfacehost: %s: %w", err.Kind, err.Err)
	case surfaces.ClientHostError:
		return fmt.Errorf("surfacehost: host: %+v", err.Host)
	default:
		return fmt.Errorf("surfacehost: %s: %+v", err.Kind, err)
	}
}

// environment is the connection loop's: the system clock and timers, always online, and wakes the app gives (the window
// became active again).
func (h *Host) environment() surfaces.LoopEnvironment {
	return surfaces.LoopEnvironment{
		Now: func() int64 { return time.Now().UnixMilli() },
		After: func(ms int64, callback func()) func() {
			timer := time.AfterFunc(time.Duration(ms)*time.Millisecond, callback)
			return func() { timer.Stop() }
		},
		Online: func() bool { return true },
		OnWake: func(listener func()) func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.wakes = append(h.wakes, listener)
			index := len(h.wakes) - 1
			return func() {
				h.mu.Lock()
				h.wakes[index] = nil
				h.mu.Unlock()
			}
		},
	}
}

// Wake says the device may have been away (the window is active again): the connection is checked at once.
func (h *Host) Wake() {
	h.mu.Lock()
	wakes := append([]func(){}, h.wakes...)
	h.mu.Unlock()
	for _, wake := range wakes {
		if wake != nil {
			wake()
		}
	}
}

// Failed are the plugins left out at the load.
func (h *Host) Failed() []host.PluginFailure {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failed
}

// Connection is the state of the link to the server.
func (h *Host) Connection() surfaces.ConnectionState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connection
}

// Theme is the active theme (the user's pick, else the first PiG theme of the mode).
func (h *Host) Theme() surfaces.Theme {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.current
}

// SetMode says the window's colour mode changed (the system's light or dark): the active theme is chosen again.
func (h *Host) SetMode(mode string) { h.theme.SetMode(mode) }

// Language is the host's active language (nil before its translations arrived).
func (h *Host) Language() *surfaces.Language { return surfaces.HostLanguage(h.Client).Read() }

// Scope is a screen's Chord resources: references and watches held until its Dispose, every change calling changed.
func (h *Host) Scope(changed func()) *tui.TuiScope { return tui.NewTuiScope(h.ctx, h.Client, changed) }

// Context is the host's context: it ends at Close.
func (h *Host) Context() context.Context { return h.ctx }

// Close stops the followers, the client and its host; the link to the server closes with it.
func (h *Host) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.mu.Unlock()
	h.shutdown()
}

func (h *Host) shutdown() {
	if h.theme != nil {
		h.theme.Stop()
	}
	if h.language != nil {
		h.language.Stop()
	}
	h.cancel()
	if h.client != nil {
		_ = h.client.Stop(chordctx.Background())
	}
}
