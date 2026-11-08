package surfaces

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/surfaces/client.ts. A client host with its plugins (slice 11 §2, §4, decisions "Slice 11, P4" to
// "P6"): what a page and a terminal's sandbox both run. A client host (no address) loads the plugin scripts it started with
// beside the app's core facets, keeps its link to the server with the connection loop, and holds the kind's scripts object: a
// new set fetches the changed scripts and reloads the host in place (a failed plugin keeps its old facet; a failed fetch is
// tried again), a page's new stylesheets loaded before the reload and each plugin left with the one of the code it runs; a new
// base bundle means this client's code is outdated, which the app answers by starting over (a page reloads). The surface
// specifics (how a socket opens, how a script is fetched, what is outdated, the stylesheets) are given.
//
// Forced Go differences (D551): the callbacks that are promises in Pi (`fetchScript`, `serverBase`, `canRetry`,
// `styles.prepare`) take the client's context and block; the loop's, a watch's and an update's events run on goroutines of
// their own, one update at a time, in order. A watch's listener queues the update and returns: the reload it makes waits on
// the host's own publications, which wait for the listener's return. `Host.Connect` fails with an error where Pi's throws; it
// is reported as `connect_failed` and the link closed (the loop tries again).

// ClientErrorKind tags a ClientError.
type ClientErrorKind string

const (
	// ClientHostError: the client's host reported an error (Host).
	ClientHostError ClientErrorKind = "host"
	// ClientPluginsError: plugins left out at a load or kept old at a reload (Failed).
	ClientPluginsError ClientErrorKind = "plugins"
	// ClientUpdateFailed: an update failed (Err); it is tried again.
	ClientUpdateFailed ClientErrorKind = "update_failed"
	// ClientStylesheetFailed: a plugin's stylesheet did not load (Reason "error") or not within 10 s (Reason "timeout"; this
	// does not itself prevent the plugin's new code): Plugin, URL, Reason.
	ClientStylesheetFailed ClientErrorKind = "stylesheet_failed"
	// ClientMessages: a message rendered by a fallback, a contributed message that threw, a foreign message (each once): Report.
	ClientMessages ClientErrorKind = "messages"
	// ClientConnectFailed: the host refused the new link (Err): it was closed.
	ClientConnectFailed ClientErrorKind = "connect_failed"
)

// ClientError is what a client reports, by Kind.
type ClientError struct {
	Kind   ClientErrorKind
	Host   host.HostError
	Failed []host.PluginFailure
	Err    error
	Plugin string
	URL    string
	Reason string
	Report MessagesReport
}

// ClientStyles are a page's plugin stylesheets around each update: Prepare before the host reloads, Settle after it (or alone
// when no script changed).
type ClientStyles interface {
	// Prepare fetches, without applying them, the stylesheets of set that differ from those of the code each plugin runs or
	// did not load; it returns once each loaded, failed or waited 10 s, or when ctx ends (the client stops: what it prepared is
	// removed, Settle never follows).
	Prepare(ctx context.Context, set ScriptSet)
	// Settle applies each prepared stylesheet whose plugin runs its new code now and retires the one it replaces; a plugin that
	// kept names (its new script failed: it kept its old code) keeps its old one, its prepared one dropped unapplied; the
	// stylesheets of plugins no longer in set go.
	Settle(set ScriptSet, kept map[string]bool)
}

// ClientOptions is what StartClient takes.
type ClientOptions struct {
	// Scripts is the kind's scripts object (WebScripts, TuiScripts).
	Scripts *contracts.Contract
	// Base is the hash of the base bundle this client runs.
	Base string
	// Initial are the plugin scripts it started with (the combined file's), in any order.
	Initial []RegisteredScript
	// Modules are the modules its plugin scripts import, by specifier.
	Modules ClientModules
	// Core are the app's own facets, loaded with the plugins.
	Core []host.HostFacet
	// Open opens one socket to the server and its link; the socket reports to events.
	Open func(events SocketEvents) link.Link
	// FetchScript fetches a plugin's script of a set (at its URL), which registers itself.
	FetchScript func(ctx context.Context, entry ScriptEntry) (RegisteredScript, error)
	// ServerBase is the server's current base bundle hash, asked before each reconnect (nil: unknown, try again): another one
	// means this client is outdated (a new protocol comes with a new base bundle).
	ServerBase func(ctx context.Context) (*string, error)
	// Outdated says this client's code is outdated: the app starts it over (a page reloads); the loop has stopped.
	Outdated func()
	// CanRetry is asked before each reconnect; false stops the loop (a guest's ended share). Nil: always true.
	CanRetry     func(ctx context.Context) (bool, error)
	OnConnection func(state ConnectionState)
	// Styles are the plugins' stylesheets (a page's; a terminal has none). Nil: none.
	Styles      ClientStyles
	Environment LoopEnvironment
	// Telemetry makes the host's telemetry adapter (nil: none); incarnation names the host's run (it is read at the adapter's
	// first span, after the host was made).
	Telemetry func(incarnation func() string) telemetry.TelemetryContext
	OnError   func(err ClientError)
}

// Client is a running client: its host, once its plugins are loaded, and its Stop.
type Client struct {
	Host    host.Host
	connect *Connection
	updates *updates
}

// StartClient starts a client: its host loaded with the initial scripts, its link kept, its plugins kept current. ctx is the
// client's lifetime: it bounds the scripts object's hold and watch, the script fetches and the stylesheets' loads, so a ctx
// cancelled while the client runs stops its plugin updates (the link stays up until Stop); Pi's client uses BACKGROUND_CONTEXT.
// Pass a context that lives as long as the client.
func StartClient(options ClientOptions, ctx context.Context) (*Client, error) {
	ctx, cancel := context.WithCancel(ctx)
	var made host.Host
	var madeMu sync.Mutex
	hostOptions := host.HostOptions{OnError: func(err host.HostError) { options.OnError(ClientError{Kind: ClientHostError, Host: err}) }}
	if options.Telemetry != nil {
		// Read at the first span, which starts once the host exists (in its load at the earliest).
		hostOptions.Telemetry = options.Telemetry(func() string {
			madeMu.Lock()
			defer madeMu.Unlock()
			return made.(interface{ Incarnation() string }).Incarnation()
		})
	}
	h, err := host.CreateHost(hostOptions)
	if err != nil {
		cancel()
		return nil, err
	}
	madeMu.Lock()
	made = h
	madeMu.Unlock()
	entries := make([]SetEntry, len(options.Initial))
	for i, script := range options.Initial {
		entries[i] = entryOf(script, options.Modules)
	}
	loaded, report, err := LoadFacets(h, options.Core, entries, ctx)
	if err != nil {
		cancel()
		_ = h.Close(ctx)
		return nil, err
	}
	if len(report.Failed) > 0 {
		options.OnError(ClientError{Kind: ClientPluginsError, Failed: report.Failed})
	}
	left := map[string]bool{}
	for _, each := range report.Failed {
		left[each.Plugin] = true
	}
	u := newUpdates(h, loaded, left, options, ctx, cancel)
	loop := connect(h, options, ctx, func(state ConnectionState) {
		if state == Connected {
			u.reconnected()
		}
	})
	if err := u.follow(); err != nil {
		loop.Stop()
		u.stop()
		_ = h.Close(ctx)
		return nil, err
	}
	return &Client{Host: h, connect: loop, updates: u}, nil
}

// Stop stops the client: its connection loop, its updates, its host.
func (c *Client) Stop(ctx context.Context) error {
	c.connect.Stop()
	c.updates.stop()
	return c.Host.Close(ctx)
}

// connect keeps h's parent link to the server, asking ServerBase and CanRetry before every reconnect; onState hears each state
// after the app's OnConnection.
func connect(h host.Host, options ClientOptions, ctx context.Context, onState func(ConnectionState)) *Connection {
	return KeepConnected(LoopOptions{
		Open: func(events SocketEvents) LoopLink {
			// The loop and the host both follow the link's close; a link has one close listener.
			shared := newSharedClose(options.Open(events))
			// A client's parent is the server; the principal only labels the link (a client serves no remote types).
			if err := h.Connect(shared, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "server")}, ctx); err != nil {
				options.OnError(ClientError{Kind: ClientConnectFailed, Err: err})
				shared.Close()
			}
			return shared
		},
		OnState: func(state ConnectionState) {
			if options.OnConnection != nil {
				options.OnConnection(state)
			}
			onState(state)
		},
		CanRetry: func(ctx context.Context) (bool, error) {
			base, err := options.ServerBase(ctx)
			if err != nil {
				return false, err
			}
			if base != nil && *base != options.Base {
				options.Outdated()
				return false, nil
			}
			if options.CanRetry == nil {
				return true, nil
			}
			return options.CanRetry(ctx)
		},
		Environment: options.Environment,
	})
}

// sharedClose is a link whose close several follow (the host and the connection loop): each listener added runs once it closes.
type sharedClose struct {
	link      link.Link
	mu        sync.Mutex
	listeners []func()
	closed    bool
}

func newSharedClose(l link.Link) *sharedClose {
	s := &sharedClose{link: l}
	l.OnClose(func() {
		s.mu.Lock()
		s.closed = true
		listeners := s.listeners
		s.listeners = nil
		s.mu.Unlock()
		for _, listener := range listeners {
			listener()
		}
	})
	return s
}

func (s *sharedClose) Send(message []byte)                     { s.link.Send(message) }
func (s *sharedClose) OnMessage(listener func(message []byte)) { s.link.OnMessage(listener) }
func (s *sharedClose) Close()                                  { s.link.Close() }

// OnClose adds a listener: it runs at once when the link closed already.
func (s *sharedClose) OnClose(listener func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		listener()
		return
	}
	s.listeners = append(s.listeners, listener)
	s.mu.Unlock()
}

// entryOf is script as one plugin of a complete set: its digest, and its evaluation with modules when it is new.
func entryOf(script RegisteredScript, modules ClientModules) SetEntry {
	digest := script.Digest
	return SetEntry{Name: script.Plugin, Digest: &digest, Prepare: func() Prepared {
		facet, err := EvaluateRegistered(script, modules)
		if err != nil {
			return Prepared{Err: err}
		}
		return Prepared{Facet: facet, Digest: script.Digest}
	}}
}

// The first pause before a failed update is tried again: 1 s, doubling from there.
const updateRetryInitialMs = 1000

// The longest pause between tries of a failed update: 30 s.
const updateRetryMaxMs = 30_000

// updates are the client's plugin updates: it holds the scripts object and applies each new set in order (fetching the changed
// scripts, loading their stylesheets, reloading the host with the complete set, settling the stylesheets); a new base calls
// Outdated instead. An update that failed (a script that did not load) is tried again after a growing pause (1 s doubling to
// 30 s) until it applies or a newer set replaces it: the scripts object shows nothing new until the next rollout or reconnect.
type updates struct {
	host    host.Host
	loaded  *LoadedFacets
	options ClientOptions
	ctx     context.Context
	abort   context.CancelFunc

	mu sync.Mutex
	// scripts is the script each plugin runs, by plugin: a plugin whose new script failed keeps its old one here, so a later
	// set with that script (a reconnect's snapshot) tries it again.
	scripts map[string]RegisteredScript
	release func()
	watch   chord.WatchHandle[delta.JsonValue]
	// latest is the newest set shown: a retry applies only it.
	latest *ScriptSet
	// queue are the updates in order: one at a time, a retry's among them.
	queue   []queued
	working bool
	// retry is the pending retry's cancel, and pause the pause it waited (0: none waited yet).
	retry func()
	pause int64
}

type queued struct {
	set *ScriptSet
}

func newUpdates(h host.Host, loaded *LoadedFacets, left map[string]bool, options ClientOptions, ctx context.Context, abort context.CancelFunc) *updates {
	u := &updates{host: h, loaded: loaded, options: options, ctx: ctx, abort: abort, scripts: map[string]RegisteredScript{}}
	for _, script := range options.Initial {
		if !left[script.Plugin] {
			u.scripts[script.Plugin] = script
		}
	}
	return u
}

// follow holds the scripts object and applies every set it shows, the first included (a reconnect's snapshot too).
func (u *updates) follow() error {
	held, err := u.host.Hold(u.ctx, u.options.Scripts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.release = held.Release
	u.mu.Unlock()
	watch, err := held.Member("set").Watch(u.ctx)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.watch = watch
	u.mu.Unlock()
	chord.StartUnlessEnded(func() {
		watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
			if call.Kind != chord.WatchCallValue {
				return nil
			}
			set := ScriptSetOf(call.Value)
			u.mu.Lock()
			u.latest = &set
			retry := u.retry
			u.retry = nil
			u.pause = 0
			u.mu.Unlock()
			if retry != nil {
				retry()
			}
			u.enqueue(&set)
			return nil
		})
	})
	return nil
}

// reconnected: the link is back: the newest set is applied again when this client does not run all of it (a plugin kept its
// old code), since the scripts object shows an unchanged set no second time.
func (u *updates) reconnected() {
	u.mu.Lock()
	set := u.latest
	runs := set == nil || u.runsAll(*set)
	u.mu.Unlock()
	if !runs {
		u.enqueue(set)
	}
}

// stop stops following: the watch ends, the hold is released, a pending retry is dropped.
func (u *updates) stop() {
	u.abort()
	u.mu.Lock()
	retry, release, watch := u.retry, u.release, u.watch
	u.retry, u.release = nil, nil
	u.mu.Unlock()
	if retry != nil {
		retry()
	}
	if watch != nil {
		_, _ = watch.Stop()
	}
	if release != nil {
		release()
	}
}

// enqueue applies set after the updates before it, unless a newer set replaced it meanwhile.
func (u *updates) enqueue(set *ScriptSet) {
	u.mu.Lock()
	u.queue = append(u.queue, queued{set})
	if u.working {
		u.mu.Unlock()
		return
	}
	u.working = true
	u.mu.Unlock()
	//portlint:allow golifetime work drains the queue and returns when none is left (the `working` flag admits one drainer); there is no longer-lived owner to join
	go u.work()
}

// work applies the queued updates one after another until none is left.
func (u *updates) work() {
	for {
		u.mu.Lock()
		if len(u.queue) == 0 {
			u.working = false
			u.mu.Unlock()
			return
		}
		next := u.queue[0]
		u.queue = u.queue[1:]
		current := u.latest == next.set
		u.mu.Unlock()
		if !current || u.ctx.Err() != nil {
			continue
		}
		if !u.apply(*next.set) {
			u.retryLater(next.set)
		}
	}
}

// retryLater tries set again after the next pause (1 s doubling to 30 s).
func (u *updates) retryLater(set *ScriptSet) {
	u.mu.Lock()
	if u.pause == 0 {
		u.pause = updateRetryInitialMs
	} else {
		u.pause = min(u.pause*2, updateRetryMaxMs)
	}
	pause := u.pause
	u.mu.Unlock()
	var cancelled bool
	cancel := u.options.Environment.After(pause, func() {
		u.mu.Lock()
		if cancelled {
			u.mu.Unlock()
			return
		}
		u.retry = nil
		u.mu.Unlock()
		u.enqueue(set)
	})
	u.mu.Lock()
	u.retry = func() {
		u.mu.Lock()
		cancelled = true
		u.mu.Unlock()
		cancel()
	}
	u.mu.Unlock()
}

// apply applies set: a new base calls Outdated; else the changed scripts fetched, their new stylesheets loaded, the host
// reloaded with all and the stylesheets settled. False when it failed (reported `update_failed`): it is tried again.
func (u *updates) apply(set ScriptSet) bool {
	options := u.options
	if set.Base != options.Base {
		options.Outdated()
		return true
	}
	// The first frame usually shows what this client runs already: nothing to reload.
	u.mu.Lock()
	changed := !u.runsAll(set)
	u.mu.Unlock()
	fail := func(err error) bool {
		// A client stopped meanwhile fails its update with its host closed: nothing to report.
		if u.ctx.Err() == nil {
			options.OnError(ClientError{Kind: ClientUpdateFailed, Err: err})
		}
		return false
	}
	var scripts []RegisteredScript
	if changed {
		for _, entry := range set.Plugins {
			u.mu.Lock()
			known, has := u.scripts[entry.Name]
			u.mu.Unlock()
			if has && known.Digest == entry.Digest {
				scripts = append(scripts, known)
				continue
			}
			fetched, err := options.FetchScript(u.ctx, entry)
			if err != nil {
				return fail(err)
			}
			scripts = append(scripts, fetched)
		}
	}
	if options.Styles != nil {
		options.Styles.Prepare(u.ctx, set)
	}
	if u.ctx.Err() != nil {
		return true
	}
	kept := map[string]bool{}
	if changed {
		var err error
		if kept, err = u.reload(scripts); err != nil {
			return fail(err)
		}
	}
	if options.Styles != nil {
		options.Styles.Settle(set, kept)
	}
	return true
}

// runsAll reports whether this client runs every script of set and nothing else (u.mu held).
func (u *updates) runsAll(set ScriptSet) bool {
	if len(set.Plugins) != len(u.scripts) {
		return false
	}
	for _, entry := range set.Plugins {
		if running, ok := u.scripts[entry.Name]; !ok || running.Digest != entry.Digest {
			return false
		}
	}
	return true
}

// reload reloads the host with scripts, the complete set, and records the script each plugin runs now; it returns the plugins
// that kept their old code (or, added, were left out).
func (u *updates) reload(scripts []RegisteredScript) (map[string]bool, error) {
	options := u.options
	entries := make([]SetEntry, len(scripts))
	for i, script := range scripts {
		entries[i] = entryOf(script, options.Modules)
	}
	report, err := u.loaded.Reload(u.host, entries, u.ctx)
	if err != nil {
		return nil, err
	}
	kept := map[string]bool{}
	for _, each := range report.Failed {
		kept[each.Plugin] = true
	}
	u.mu.Lock()
	running := map[string]RegisteredScript{}
	for _, script := range scripts {
		runs, has := script, true
		if kept[script.Plugin] {
			runs, has = u.scripts[script.Plugin]
		}
		if has {
			running[script.Plugin] = runs
		}
	}
	u.scripts = running
	u.mu.Unlock()
	if len(report.Failed) > 0 {
		options.OnError(ClientError{Kind: ClientPluginsError, Failed: report.Failed})
	}
	return kept, nil
}
