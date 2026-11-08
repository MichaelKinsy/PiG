package host

// The launcher and the TUI in one process (D473): where upstream's trusted Node launcher relays opaque frames to a sandboxed guest worker that runs downloaded TUI
// code, this process links the compiled-in TUI facets and runs the TuiRuntime itself, yet keeps every launcher rule: terminal output passes the mode filter
// and is dropped while launcher UI shows; privileged keys are consumed before the runtime; launcher actions run only under a grant, with a consent screen; the
// server is reached only through a Transport. The runtime plays the worker: its OnGrant is the worker's `request`, LauncherAnswers its `grantRequest` answers.
//
// Forced Go differences: no bundle is fetched, hashed or swapped (the plugins are compiled in), so there is no version, no reload swap and no sandbox failure;
// the runtime's methods run on one owner loop goroutine (Post), and the launcher's own state is guarded by a mutex.
//
// pi: apps/p2/src/hosts/tui-client.ts, apps/p2/src/hosts/tui-worker-runtime.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// gestureWindow is how long after the launcher saw a selection gesture pi-tui's copy request is accepted.
const gestureWindow = 2 * time.Second

// OutdatedBuildMessage is what a client that met a server on a newer link protocol reports before it exits.
const OutdatedBuildMessage = "This p2 build is outdated: the server speaks a newer link protocol. Update p2 and run it again"

// ErrAccessEnded is the server no longer admitting this sign-in (401/403): a revoked or reset share, an archived session, a logout.
var ErrAccessEnded = errors.New("Access to this server ended")

// PreferenceSource is the visitor's validated local preferences (section 4.2) and their change feed.
type PreferenceSource struct {
	Current prefs.TuiLocalPreferences
	// Subscribe registers a listener and returns its removal; nil when the preferences never change.
	Subscribe func(listener func(next prefs.TuiLocalPreferences)) (stop func())
}

// ClientOptions are what StartClient takes.
type ClientOptions struct {
	// Terminal is the visitor's real terminal.
	Terminal pitui.Terminal
	// Facets are the TUI plugins' facets (hosts.TUIFacets); the core facet is added here.
	Facets []host.HostFacet
	// Actions are the launcher actions by name (section 3.2): K actions fire on the visitor's own privileged key, C after consent.
	Actions map[string]LauncherAction
	// Mouse is the visitor's mouse policy (launcher-only preference): which mouse modes the runtime's output may enable.
	Mouse MousePolicy
	// Role is who the launcher was admitted as: the TUI starts with that role, so owner views hold before `Access` answers.
	Role string
	URL  *url.URL
	// Preferences are sent to the runtime as data.
	Preferences  *PreferenceSource
	Capabilities *Capabilities
	// InputTap observes every input the launcher forwards to the runtime (tests).
	InputTap func(data string)
	// ColorQuery is the terminal color query's deadline.
	ColorQuery  time.Duration
	Credentials Credentials
	// Transport is how requests reach the server; default: directly to URL with Credentials.
	Transport Transport
	// Reconnect is the first reconnect delay (default 250 ms); Poll the access check's interval (default 1 s).
	Reconnect, Poll time.Duration
	OnExit          func()
	OnFailure       func(message string)
	// OnAccessEnded is called when the server stopped admitting these credentials while running (section 4.1 revocation); the client closes.
	OnAccessEnded func()
}

// Client is a running launcher.
type Client struct {
	options   ClientOptions
	transport Transport
	term      *clientTerminal
	ui        *TrustedUi
	grants    *Grants
	modes     *TerminalModes
	selection SelectionGesture
	probe     *TerminalColorProbe
	host      host.Host
	tui       *pitui.TuiAltScreen
	rt        *tuiruntime.TuiRuntime
	// access and grantRead are the owner loop's: set and read there only (syncAccess).
	access    *host.Reference
	grantRead func() (delta.JsonValue, bool)
	root      context.Context

	queue chan func()
	stop  chan struct{}
	loops sync.WaitGroup

	mu               sync.Mutex
	closed           bool
	paused           bool
	busy             bool
	suspendPrivilege bool
	target           *GrantTarget
	gestureAt        time.Time
	preferences      *prefs.TuiLocalPreferences
	privileged       *PrivilegedKeys
	keyActions       map[string]LauncherAction // privileged id -> action
	keyNames         map[string]string
	kitty            bool
	colorsKnown      bool
	capabilities     Capabilities
	socket           Socket
	link             *clientLink
	connected        bool
	pending          [][]byte
	pendingBytes     int
	retry            *time.Timer
	ended            bool
	stopPrefs        func()
	statusTimes      map[string][]time.Time
	pendingStatus    map[string]func()
	titled           bool
	progressing      bool
	closing          sync.Once
	stopPoll         chan struct{}
	scope            *chordtui.TuiScope // the access scope: set once in setup, disposed by Close
	crashed          atomic.Bool

	// reported is what report last sent (owner loop only).
	reportedTarget  string
	reportedSuspend bool
	reportedBusy    bool
	reportedSet     bool
	wheelLines      prefs.WheelLines
	wheelSet        bool
}

// clientTerminal is the terminal the runtime's TUI draws on (Pi's BridgeTerminal): its output is filtered by the launcher's mode tracking and dropped while
// launcher UI shows; its columns and rows are the real terminal's, bounded.
type clientTerminal struct{ client *Client }

func (t *clientTerminal) Start(func(string), func()) error    { return nil }
func (t *clientTerminal) Stop()                               {}
func (t *clientTerminal) DrainInput(_, _ time.Duration) error { return nil }
func (t *clientTerminal) Write(data string)                   { t.client.output(data) }
func (t *clientTerminal) Columns() int                        { return min(500, max(10, t.client.options.Terminal.Columns())) }
func (t *clientTerminal) Rows() int                           { return min(200, max(5, t.client.options.Terminal.Rows())) }
func (t *clientTerminal) KittyProtocolActive() bool {
	return t.client.options.Terminal.KittyProtocolActive()
}
func (t *clientTerminal) MoveBy(lines int) {
	if lines != 0 {
		direction := "B"
		if lines < 0 {
			direction = "A"
		}
		t.Write(fmt.Sprintf("\x1b[%d%s", abs(lines), direction))
	}
}
func (t *clientTerminal) HideCursor()                  { t.Write("\x1b[?25l") }
func (t *clientTerminal) ShowCursor()                  { t.Write("\x1b[?25h") }
func (t *clientTerminal) ClearLine()                   { t.Write("\x1b[K") }
func (t *clientTerminal) ClearFromCursor()             { t.Write("\x1b[J") }
func (t *clientTerminal) ClearScreen()                 { t.Write("\x1b[2J\x1b[H") }
func (t *clientTerminal) RestoreOnExit(string, string) {}

// N actions (section 3.2): sanitized, bounded and at most four per second each; dropped while launcher UI shows.
func (t *clientTerminal) SetTitle(title string) {
	t.client.statusMessage("title", jsSlice(title, 200), false, pitui.ProgramStatus{})
}
func (t *clientTerminal) SetProgress(active bool) {
	t.client.statusMessage("progress", "", active, pitui.ProgramStatus{})
}
func (t *clientTerminal) SetProgramStatus(status pitui.ProgramStatus) {
	t.client.statusMessage("programStatus", "", false, status)
}

// Output is the real terminal as the trusted UI's output.
type terminalOutput struct{ pitui.Terminal }

func (o terminalOutput) Columns() int { return o.Terminal.Columns() }
func (o terminalOutput) Rows() int    { return o.Terminal.Rows() }

// StartClient starts the TUI on options.Terminal.
//
// pig divergence (D611): the runtime and the launcher share this process: the plugins' TUI facets are compiled in, so no sandbox worker is started and no bundle
// is fetched, versioned or swapped (Pi: tui-sandbox.ts, tui-bootstrap.ts, the `/__p2_tui` manifest and `client.reload`).
func StartClient(options ClientOptions) (*Client, error) {
	c := &Client{
		options: options, queue: make(chan func(), 4096), stop: make(chan struct{}), stopPoll: make(chan struct{}),
		keyActions: map[string]LauncherAction{}, keyNames: map[string]string{}, statusTimes: map[string][]time.Time{}, pendingStatus: map[string]func(){},
		modes: NewTerminalModes(options.Mouse), grants: NewGrants(OriginOf(options.URL)), root: chordctx.Background(),
	}
	c.transport = options.Transport
	if c.transport == nil {
		c.transport = CookieTransport(options.URL, options.Credentials)
	}
	if options.Capabilities != nil {
		c.capabilities = *options.Capabilities
	} else {
		c.capabilities = LauncherCapabilities(nil, "")
	}
	if options.Preferences != nil {
		current := options.Preferences.Current
		c.preferences = &current
	}
	for name, action := range options.Actions {
		if action.Kind == GrantK {
			c.keyActions[action.Key] = action
			c.keyNames[action.Key] = name
		}
	}
	c.refreshPrivileged()
	c.term = &clientTerminal{client: c}
	c.ui = NewTrustedUi(terminalOutput{options.Terminal}, OriginOf(options.URL))
	c.kitty = options.Terminal.KittyProtocolActive()
	// After launcher-drawn UI: the runtime's output was discarded, so restore its modes and ask for a full redraw.
	c.ui.OnClose = func() {
		options.Terminal.Write(c.modes.Restore())
		c.Post(func() { c.tui.RequestRender(true) })
	}
	scheme, _ := ColorFgBgSchemeFromEnv()
	c.probe = NewTerminalColorProbe(TerminalColorProbeOptions{
		Write: options.Terminal.Write, Scheme: scheme, Timeout: options.ColorQuery,
		OnUpdate: func(state theme.TerminalState) {
			c.mu.Lock()
			c.colorsKnown = true
			c.mu.Unlock()
			c.Post(func() { c.rt.Appearance.Update(tuiruntime.AppearanceUpdate{Terminal: &state}) })
		},
	})
	if options.Preferences != nil && options.Preferences.Subscribe != nil {
		c.stopPrefs = options.Preferences.Subscribe(func(next prefs.TuiLocalPreferences) {
			c.mu.Lock()
			c.preferences = &next
			c.mu.Unlock()
			c.refreshPrivileged()
			c.Post(func() { c.rt.Appearance.Update(tuiruntime.AppearanceUpdate{Local: &next}) })
		})
	}
	c.loops.Add(1)
	go c.loop()
	if err := c.setup(); err != nil {
		c.Close()
		return nil, err
	}
	if err := options.Terminal.Start(c.onInput, c.onResize); err != nil {
		c.Close()
		return nil, err
	}
	c.probe.Start()
	go c.poll()
	return c, nil
}

func (c *Client) loop() {
	defer c.loops.Done()
	for {
		select {
		case fn := <-c.queue:
			c.run(fn)
		case <-c.stop:
			return
		}
	}
}

// Post runs fn on the owner loop.
func (c *Client) Post(fn func()) {
	select {
	case c.queue <- fn:
	case <-c.stop:
	}
}

// Do runs fn on the owner loop and waits for it.
func (c *Client) Do(fn func()) {
	done := make(chan struct{})
	select {
	case c.queue <- func() { defer close(done); fn() }:
		<-done
	case <-c.stop:
	}
}

// closeGrace is how long Close waits for the runtime's own teardown before it cleans the terminal without it. Pi's close awaits `worker.terminate()`, which
// kills the thread whatever it runs; a goroutine cannot be killed, so the launcher gives up on it instead, after Pi's own limit for a launcher request that the
// worker leaves unanswered (TuiSandbox.request, tui-sandbox.ts: 3000 ms).
const closeGrace = 3 * time.Second

// doWithin is Do with a limit: false when the loop did not take or finish fn in time.
func (c *Client) doWithin(limit time.Duration, fn func()) bool {
	done := make(chan struct{})
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case c.queue <- func() { defer close(done); fn() }:
	case <-c.stop:
		return true
	case <-timer.C:
		return false
	}
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// Runtime is the TuiRuntime (use it on the owner loop: Do, Post).
func (c *Client) Runtime() *tuiruntime.TuiRuntime { return c.rt }

// Terminal is the terminal the runtime draws on: its output passes the launcher's mode filter and its title, progress and status are launcher-sanitized.
func (c *Client) Terminal() pitui.Terminal { return c.term }

// TUI is the alternate-screen renderer.
func (c *Client) TUI() *pitui.TuiAltScreen { return c.tui }

// Connected is whether the chord link is open.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *Client) activeScope() *chordtui.TuiScope {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scope
}

// Snapshot is the runtime's UI state, as a reload carries it.
func (c *Client) Snapshot() (snapshot tui.Snapshot) {
	c.Do(func() { snapshot = tui.SnapshotRuntime(c.rt) })
	return snapshot
}

func (c *Client) refreshPrivileged() {
	c.mu.Lock()
	defer c.mu.Unlock()
	local := pitui.KeybindingsConfig{}
	if c.preferences != nil && c.preferences.Keybindings != nil {
		local = c.preferences.Keybindings
	}
	ids := make([]string, 0, len(c.keyActions))
	for id := range c.keyActions {
		ids = append(ids, id)
	}
	c.privileged = NewPrivilegedKeys(local, prefs.KeyPlatform{Platform: c.capabilities.Platform, WindowsKeys: c.capabilities.WindowsKeys}, ids)
}

func (c *Client) fail(message string) {
	if c.options.OnFailure != nil {
		c.options.OnFailure(message)
	}
	go c.Close()
	if c.options.OnExit != nil {
		c.options.OnExit()
	}
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// output is the runtime's terminal output: filtered, tracked and written unless launcher UI shows or the terminal is lent to a local program.
func (c *Client) output(data string) {
	// A closing client has terminated its runtime: what the runtime's last stop writes is dropped, so the cleanup undoes every mode it enabled.
	if c.isClosed() {
		return
	}
	filtered, err := c.modes.Filter(data)
	if err != nil {
		c.fail("TUI sandbox sent an invalid or oversized output frame")
		return
	}
	c.mu.Lock()
	paused := c.paused
	c.mu.Unlock()
	if !c.ui.Active() && !paused {
		c.options.Terminal.Write(filtered)
	}
}

func (c *Client) statusMessage(kind, text string, active bool, status pitui.ProgramStatus) {
	if c.ui.Active() {
		return
	}
	c.mu.Lock()
	paused := c.paused
	c.mu.Unlock()
	if paused {
		return
	}
	switch kind {
	case "title":
		clean := jsSlice(strings.Map(func(r rune) rune {
			if (r >= 0x2400 && r <= 0x2421) || r == 0xfffd {
				return ' '
			}
			return r
		}, VisibleText(text)), 80)
		c.throttle("title", func() {
			c.mu.Lock()
			c.titled = true
			c.mu.Unlock()
			c.options.Terminal.SetTitle(clean)
		})
	case "progress":
		c.mu.Lock()
		show := c.preferences != nil && c.preferences.Settings.Terminal != nil && c.preferences.Settings.Terminal.ShowTerminalProgress != nil && *c.preferences.Settings.Terminal.ShowTerminalProgress
		c.mu.Unlock()
		if !show {
			return
		}
		c.throttle("progress", func() {
			c.mu.Lock()
			c.progressing = active
			c.mu.Unlock()
			c.options.Terminal.SetProgress(active)
		})
	case "programStatus":
		switch status.State {
		case "idle", "working", "blocked", "done", "error", "clear":
		default:
			return
		}
		sanitized := pitui.ProgramStatus{State: status.State, App: "p2"}
		if status.Message != "" {
			sanitized.Message = jsSlice(status.Message, 80)
		}
		c.throttle("programStatus", func() { c.options.Terminal.SetProgramStatus(sanitized) })
	}
}

// throttle applies at most four per second per kind; a later one waits for the window and the latest wins.
func (c *Client) throttle(kind string, apply func()) {
	c.mu.Lock()
	now := time.Now()
	var recent []time.Time
	for _, t := range c.statusTimes[kind] {
		if now.Sub(t) < time.Second {
			recent = append(recent, t)
		}
	}
	if len(recent) < 4 {
		c.statusTimes[kind] = append(recent, now)
		c.mu.Unlock()
		apply()
		return
	}
	_, waiting := c.pendingStatus[kind]
	c.pendingStatus[kind] = apply
	if !waiting {
		time.AfterFunc(time.Second-now.Sub(recent[0]), func() {
			c.mu.Lock()
			latest := c.pendingStatus[kind]
			delete(c.pendingStatus, kind)
			closed := c.closed
			c.mu.Unlock()
			if !closed && latest != nil {
				latest()
			}
		})
	}
	c.mu.Unlock()
}

// ---- input ----

func (c *Client) onInput(data string) {
	if c.probe.Consume(data) {
		return
	}
	c.mu.Lock()
	if c.paused {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	// Launcher-drawn UI consumes everything; nothing reaches the runtime meanwhile.
	if c.ui.Active() {
		c.ui.HandleInput(data)
		return
	}
	// The launcher negotiates the kitty keyboard protocol; the runtime must decode accordingly.
	if kitty := c.options.Terminal.KittyProtocolActive(); kitty != c.kittyState() {
		c.setKitty(kitty)
	}
	c.mu.Lock()
	privileged := c.privileged
	suspended := c.suspendPrivilege
	c.mu.Unlock()
	if !suspended {
		if id, release, ok := privileged.Match(data); ok {
			if !release {
				go c.keyAction(id)
			}
			return
		}
	}
	// Copy on selection (rule 5): the release the launcher itself saw ending a drag or multi-click opens a short, single-use window in which pi-tui's
	// copy-on-select request (sent by the runtime on that release) is accepted.
	if c.options.Mouse != MouseOff && c.options.Mouse != "" {
		now := time.Now()
		c.mu.Lock()
		if c.selection.Feed(data, now) {
			c.gestureAt = now
		}
		c.mu.Unlock()
	}
	if len(data) <= MaxTerminalFrame {
		if c.options.InputTap != nil {
			c.options.InputTap(data)
		}
		c.Post(func() {
			c.tui.HandleTerminalInput(data)
			c.report()
		})
	}
}

func (c *Client) kittyState() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.kitty
}

func (c *Client) setKitty(kitty bool) {
	c.mu.Lock()
	c.kitty = kitty
	c.mu.Unlock()
	c.postCapabilities()
}

// capabilityApplier is the capabilities as they are now, ready to apply on the owner loop.
func (c *Client) capabilityApplier() func() {
	c.mu.Lock()
	capabilities := c.capabilities
	kitty := c.kitty
	c.mu.Unlock()
	return func() {
		pitui.SetKittyProtocolActive(kitty)
		c.rt.Appearance.Update(tuiruntime.AppearanceUpdate{Capabilities: &tuiruntime.TuiCapabilities{
			TrueColor: capabilities.TrueColor, KeyPlatform: prefs.KeyPlatform{Platform: jsSlice(capabilities.Platform, 16), WindowsKeys: capabilities.WindowsKeys},
		}})
	}
}

func (c *Client) postCapabilities() { c.Post(c.capabilityApplier()) }

func (c *Client) onResize() {
	if c.ui.Active() {
		c.ui.Render()
	}
	c.Post(func() { c.tui.RequestRender(true) })
}

// ---- launcher actions ----

func (c *Client) actionContext(grant *Grant) ActionContext {
	return ActionContext{
		Grant: grant, UI: c.ui, Origin: c.options.URL,
		WriteTerminal: c.options.Terminal.Write,
		SendInput: func(data string) {
			if c.grants.Valid(grant.ID, c.rt, "") != nil && len(data) <= MaxTerminalFrame {
				c.Post(func() { c.tui.HandleTerminalInput(data) })
			}
		},
		ReleaseTerminal: c.releaseTerminal,
		Ask: func(kind string, payload any) (any, error) {
			if c.grants.Valid(grant.ID, c.rt, "") == nil {
				return nil, ErrGrantCancelled
			}
			answer := c.ask(kind, payload)
			if c.grants.Valid(grant.ID, c.rt, "") == nil {
				return nil, ErrGrantCancelled
			}
			return answer, nil
		},
	}
}

// ask asks the runtime a launcher question bound to a grant (its `grantRequest` answer); unknown questions get nil.
func (c *Client) ask(kind string, payload any) any {
	answer := make(chan any, 1)
	c.Post(func() {
		if fn := c.rt.LauncherAnswers[kind]; fn != nil {
			answer <- fn(payload)
		} else {
			answer <- nil
		}
	})
	select {
	case value := <-answer:
		return value
	case <-c.stop:
		return nil
	}
}

func (c *Client) currentTarget() *GrantTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.target
}

// consent is the runtime's request for a C action (section 3.3): validated, shown in the trusted UI, run only after local confirmation.
func (c *Client) consent(name string, payload any) tuiruntime.GrantResult {
	denied := func(reason string) tuiruntime.GrantResult { return tuiruntime.GrantResult{Error: reason} }
	action, known := c.options.Actions[name]
	if known && action.Kind == GrantGesture {
		// Only right after the launcher saw the gesture itself; the runtime cannot open this window.
		c.mu.Lock()
		seen := c.gestureAt
		c.gestureAt = time.Time{}
		paused := c.paused
		c.mu.Unlock()
		if seen.IsZero() || time.Since(seen) > gestureWindow || c.ui.Active() || paused {
			return denied("denied")
		}
		if !action.Accept(payload) {
			return denied("denied")
		}
		grant := c.grants.Create(name, GrantGesture, c.rt, c.currentTarget())
		defer c.grants.Complete(grant.ID)
		value, err := action.Run(payload, c.actionContext(grant))
		if err != nil {
			return denied("failed")
		}
		return tuiruntime.GrantResult{OK: true, Value: value}
	}
	if !known || action.Kind != GrantC {
		return denied("denied")
	}
	c.mu.Lock()
	if c.busy || c.ui.Active() {
		c.mu.Unlock()
		return denied("busy")
	}
	target := c.target
	request := action.Consent(payload, ConsentContext{Origin: c.options.URL, Target: target})
	if request == nil {
		c.mu.Unlock()
		return denied("denied")
	}
	c.busy = true
	c.mu.Unlock()
	grant := c.grants.Create(name, GrantC, c.rt, target)
	defer func() {
		c.grants.Complete(grant.ID)
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
	}()
	facts := append([]ConsentFact(nil), request.Facts...)
	if target != nil {
		facts = append([]ConsentFact{{"Session", fmt.Sprintf("%s #%d", target.Session, target.Conversation)}}, facts...)
	}
	shown := *request
	shown.Facts = facts
	confirmed := c.ui.Confirm(shown)
	if !confirmed || c.grants.Valid(grant.ID, c.rt, "") == nil {
		if confirmed {
			return denied("cancelled")
		}
		return denied("denied")
	}
	value, err := action.Run(payload, c.actionContext(grant))
	if err != nil {
		// Local errors stay local (section 3.4): the runtime learns only that it failed.
		if !c.isClosed() {
			c.ui.Notice("Failed: "+err.Error(), 2500*time.Millisecond)
		}
		return denied("failed")
	}
	if c.grants.Valid(grant.ID, c.rt, "") == nil {
		return denied("cancelled")
	}
	return tuiruntime.GrantResult{OK: true, Value: value}
}

// keyAction is a privileged key (section 3.1 K): consumed by the caller; its action gets a grant for this one use.
func (c *Client) keyAction(id string) {
	action, ok := c.keyActions[id]
	c.mu.Lock()
	if !ok || c.busy || c.ui.Active() {
		c.mu.Unlock()
		return
	}
	c.busy = true
	target := c.target
	c.mu.Unlock()
	grant := c.grants.Create(c.keyNames[id], GrantK, c.rt, target)
	defer func() {
		c.grants.Complete(grant.ID)
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
	}()
	if err := action.RunKey(c.actionContext(grant)); err != nil && !c.isClosed() && grant.Context.Err() == nil {
		c.ui.Notice("Failed: "+err.Error(), 2500*time.Millisecond)
	}
}

// releaseTerminal hands the real terminal to a local program (external editor, job control): runtime IO is suspended, its modes are undone (without
// forgetting them) and the terminal stopped; afterwards everything is restored and the runtime redraws (section 3.2 `externalEditor`, `suspend`).
func (c *Client) releaseTerminal(run func() error) error {
	c.mu.Lock()
	c.paused = true
	c.mu.Unlock()
	c.options.Terminal.Write(c.modes.SuspendSequence())
	c.options.Terminal.Stop()
	defer func() {
		if c.isClosed() {
			return
		}
		_ = c.options.Terminal.Start(c.onInput, c.onResize)
		c.options.Terminal.Write(c.modes.ResumeSequence())
		c.mu.Lock()
		c.paused = false
		c.mu.Unlock()
		c.Post(func() { c.tui.RequestRender(true) })
	}()
	return run()
}

// ---- runtime, link and server ----

// clientLink is the host's parent link over the launcher's socket (Pi's BridgeLink): sends wait for the socket; closing it terminates the socket, so its close
// reconnects (a connection that died without a close, a laptop that slept while the relay dropped it).
type clientLink struct {
	client        *Client
	mu            sync.Mutex
	closed        bool
	listener      func([]byte)
	received      [][]byte
	closeListener func()
}

func (l *clientLink) Send(message []byte) {
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if !closed {
		l.client.send(message)
	}
}

func (l *clientLink) OnMessage(listener func([]byte)) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.listener = listener
	queued := l.received
	l.received = nil
	l.mu.Unlock()
	for _, message := range queued {
		listener(message)
	}
}

func (l *clientLink) OnClose(listener func()) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		listener()
		return
	}
	l.closeListener = listener
	l.mu.Unlock()
}

func (l *clientLink) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

func (l *clientLink) Close() {
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return
	}
	l.client.terminateSocket()
	l.drop()
}

// drop ends the link without telling the launcher, whose next socket may be open already.
func (l *clientLink) drop() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	listener := l.closeListener
	l.listener, l.closeListener, l.received = nil, nil, nil
	l.mu.Unlock()
	if listener != nil {
		listener()
	}
}

func (l *clientLink) deliver(message []byte) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	listener := l.listener
	if listener == nil {
		l.received = append(l.received, message)
	}
	l.mu.Unlock()
	if listener != nil {
		listener(message)
	}
}

var _ link.Link = (*clientLink)(nil)

func (c *Client) terminateSocket() {
	c.mu.Lock()
	socket := c.socket
	c.mu.Unlock()
	if socket != nil {
		socket.Terminate()
	}
}

func (c *Client) send(data []byte) {
	if len(data) > MaxLinkFrame {
		c.fail("TUI link frame exceeds 4 MiB")
		return
	}
	c.mu.Lock()
	socket := c.socket
	if socket != nil && socket.Open() {
		c.mu.Unlock()
		_ = socket.Send(data)
		return
	}
	c.pendingBytes += len(data)
	if c.pendingBytes > MaxLinkFrame {
		c.mu.Unlock()
		c.fail("TUI link queue exceeds 4 MiB")
		return
	}
	c.pending = append(c.pending, data)
	c.mu.Unlock()
}

func hostErrorText(failure host.HostError) string { return fmt.Sprint(failure) }

func (c *Client) setup() error {
	pitui.SetCapabilityOverrides(pitui.CapabilityOverrides{Images: new(pitui.ImageProtocol), Hyperlinks: new(bool)})
	mouse := c.options.Mouse != MouseOff && c.options.Mouse != ""
	copyOnSelectRequest := func(text string) error {
		result := c.rt.OnGrant("copy.selection", map[string]any{"text": jsSlice(text, MaxCopy)})
		switch {
		case result.OK:
			return nil
		case result.Error == "failed":
			return errors.New("Copy failed")
		}
		return errors.New("")
	}
	c.tui = pitui.NewTuiAltScreen(c.term, nil, "", pitui.TuiAltScreenOptions{
		Mouse: &mouse, WheelScrollLines: pitui.WheelScrollLines{Auto: true}, CopySelection: copyOnSelectRequest,
		// pi-tui focuses the clicked component (an editor click answers `focus: true`); the runtime is the layout's focus owner (commands first, then the
		// active screen), so a focused editor would bypass Escape, Ctrl+C, Ctrl+D, Ctrl+T and every other command, and keep getting keys after the screen
		// changed. Clicks that pi-tui maps to an overlay keep that overlay (Pi's RuntimeAltScreen.resolveMouseFocusTarget). Runs on the owner loop.
		ResolveMouseFocusTarget: func(component, resolved pitui.Component) pitui.Component {
			if resolved == component && c.rt != nil {
				return c.rt
			}
			return resolved
		},
	})
	c.tui.SetOwnerDispatcher(func(ctx context.Context, fn func()) error {
		select {
		case c.queue <- fn:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	c.tui.SetRenderDispatcher(func(render func()) {
		select {
		case c.queue <- render:
		case <-c.stop:
		}
	})
	h, err := p2runtime.NewHost(host.HostOptions{OnError: func(failure host.HostError) {
		// A server that speaks a newer link protocol was updated: this build is outdated. Pi loads the server's current bundle into a fresh worker; the
		// facets are compiled into this executable, so the visitor is told to update it (D611).
		if host.NewerPeerProtocol(failure) {
			go c.fail(OutdatedBuildMessage)
			return
		}
		switch string(failure.Kind) {
		case "routine_failed", "derive_failed", "activation_failed", "object_failed", "registry_undeclared", "unbound_long", "transaction_cancelled":
			text := hostErrorText(failure)
			c.Post(func() {
				if c.rt != nil {
					c.rt.Fail(errors.New(text))
				}
			})
		}
	}})
	if err != nil {
		return err
	}
	c.host = h
	ready := make(chan error, 1)
	c.Post(func() {
		c.rt = tuiruntime.NewTuiRuntime(h, c.tui, nil)
		ready <- nil
	})
	<-ready
	c.link = &clientLink{client: c}
	if err := h.Connect(c.link, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, c.root); err != nil {
		return err
	}
	facets := append(append([]host.HostFacet(nil), c.options.Facets...), host.HostFacet{Name: "@p2/tui-core", Facet: tui.CoreFacet})
	report, err := h.Load(facets, c.root)
	if err != nil {
		return err
	}
	var loadErr error
	c.Do(func() {
		c.rt.Origin = jsSlice(c.options.URL.String()[:len(OriginOf(c.options.URL))], 200)
		if c.options.Role == "owner" || c.options.Role == "guest" {
			c.rt.Role = tuiruntime.TuiRole(c.options.Role)
		}
		c.rt.OnExit = func() {
			go c.Close()
			if c.options.OnExit != nil {
				c.options.OnExit()
			}
		}
		c.rt.OnGrant = c.consent
		c.rt.AddListener(c.report)
		// Pi's order is capabilities, preferences, then activate: the first frame is drawn with the visitor's color mode and theme.
		c.capabilityApplier()()
		c.mu.Lock()
		local := c.preferences
		c.mu.Unlock()
		if local != nil {
			initial := *local
			c.rt.Appearance.Update(tuiruntime.AppearanceUpdate{Local: &initial})
		}
		c.rt.Start()
		// A facet left out at load (its setup threw) is shown, not silently missing.
		for _, failed := range report.Failed {
			c.rt.Fail(fmt.Errorf("TUI plugin %s failed to load: %v", failed.Plugin, failed.Error))
		}
		c.tui.SetLayoutRoot(c.rt.Root)
		c.tui.SetFocus(c.rt)
		c.tui.Start()
		c.term.SetTitle("π p2 · " + c.options.URL.Host)
	})
	scope := chordtui.NewTuiScope(c.root, h, func() { c.Post(c.syncAccess) })
	c.mu.Lock()
	c.scope = scope
	c.mu.Unlock()
	access, err := scope.Hold(p2runtime.Access, delta.NewJsonObject(0))
	if err != nil {
		loadErr = err
	} else {
		// `access` and `grantRead` are the owner loop's (syncAccess runs there): the loop takes the reference, then follows it once, so an answer that arrived
		// before this point (and was skipped as unbound) is not lost.
		c.Do(func() {
			c.access = access
			c.syncAccess()
		})
	}
	c.connect(0)
	return loadErr
}

// syncAccess follows `Access`: who this TUI is (the role and a guest's grant follow it).
func (c *Client) syncAccess() {
	if c.access == nil || c.rt == nil {
		return
	}
	variant, bound, err := c.access.Variant()
	if err != nil || !bound {
		return
	}
	role := tuiruntime.RoleGuest
	if variant == "owner" {
		role = tuiruntime.RoleOwner
	}
	if role != c.rt.Role {
		c.rt.Role = role
		c.rt.Changed()
	}
	if variant != "guest" {
		return
	}
	member := c.access.Member("grant")
	if member == nil {
		return
	}
	if c.grantRead == nil {
		c.grantRead = chordtui.WatchMember(c.activeScope(), member)
	}
	value, ok := c.grantRead()
	if !ok {
		return
	}
	grant, _ := value.(*delta.JsonObject)
	if grant == nil {
		c.rt.Grant = nil
		return
	}
	guest := &tuiruntime.Guest{Name: stringOf(grant.Value("name")), Sessions: map[string]string{}}
	if sessions, ok := grant.Value("sessions").(*delta.JsonObject); ok {
		for _, key := range sessions.Keys() {
			guest.Sessions[key] = stringOf(sessions.Value(key))
		}
	} else {
		guest.Sessions[stringOf(grant.Value("session"))] = stringOf(grant.Value("mode"))
	}
	c.rt.Grant = guest
	if session := stringOf(grant.Value("session")); session != "" && (!c.rt.HasSession || c.rt.Session != session) {
		c.rt.Navigate("chat", session, true, nil)
	}
}

func stringOf(value any) string { s, _ := value.(string); return s }

// connect opens the chord socket; a closed one reconnects with backoff.
func (c *Client) connect(attempt int) {
	if c.isClosed() {
		return
	}
	opened := false
	var current Socket
	current = c.transport.Socket("/chord", SocketEvents{
		Open: func() {
			c.mu.Lock()
			if c.closed || c.socket != current {
				c.mu.Unlock()
				current.Terminate()
				return
			}
			opened = true
			c.connected = true
			pending := c.pending
			c.pending, c.pendingBytes = nil, 0
			previous := c.link
			c.mu.Unlock()
			// `closed` is the link's own (its mutex), the swap of c.link is the client's (c.mu).
			if previous.isClosed() {
				fresh := &clientLink{client: c}
				c.mu.Lock()
				c.link = fresh
				c.mu.Unlock()
				_ = c.host.Connect(fresh, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, c.root)
			}
			for _, data := range pending {
				_ = current.Send(data)
			}
			c.Post(func() { c.connection(true) })
		},
		Message: func(data []byte, binary bool) {
			c.mu.Lock()
			stale := c.socket != current || c.closed
			l := c.link
			c.mu.Unlock()
			if stale {
				return
			}
			// Chord's links carry binary messages only.
			if !binary {
				current.Terminate()
				return
			}
			l.deliver(data)
		},
		Refused: func(status int) {
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				c.verifyAccess()
			}
		},
		Close: func() {
			c.mu.Lock()
			if c.closed || c.socket != current {
				c.mu.Unlock()
				return
			}
			c.connected = false
			c.pending, c.pendingBytes = nil, 0
			l := c.link
			delay := c.options.Reconnect
			if delay == 0 {
				delay = 250 * time.Millisecond
			}
			delay = min(5*time.Second, delay<<min(attempt, 5))
			next := attempt + 1
			if opened {
				next = 0
			}
			c.retry = time.AfterFunc(delay, func() { c.connect(next) })
			c.mu.Unlock()
			c.Post(func() { c.connection(false) })
			l.drop()
		},
	})
	c.mu.Lock()
	c.socket = current
	c.mu.Unlock()
}

// connection is the runtime's view of the socket.
func (c *Client) connection(open bool) {
	c.rt.Connected = open
	if open {
		go c.readServerPreferences()
	}
	c.rt.Changed()
}

// readServerPreferences reads the server's TUI defaults, which sit below the visitor's local preferences (section 4.2).
func (c *Client) readServerPreferences() {
	value, err := c.host.Root(func(ctx host.Ctx) (any, error) {
		remote, err := ctx.Get(prefs.TuiPreferences, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return remote.Call("read", nil, ctx)
	}, c.root, host.RootOptions{})
	c.Post(func() {
		if err != nil {
			c.rt.Fail(err)
			return
		}
		var decoded any
		if encoded, err := json.Marshal(value); err == nil {
			_ = json.Unmarshal(encoded, &decoded)
		}
		server := prefs.SanitizeServerPreferences(decoded)
		pointer := &server
		c.rt.Appearance.Update(tuiruntime.AppearanceUpdate{Server: &pointer})
	})
}

// report tells the launcher what the runtime shows: the navigation grants are bound to, the keys it may intercept and progress (owner loop).
func (c *Client) report() {
	settings := c.rt.Appearance.Settings
	lines := prefs.WheelLines{Auto: true}
	if settings.FullscreenWheelScrollLines != nil {
		lines = *settings.FullscreenWheelScrollLines
	}
	if !c.wheelSet || lines != c.wheelLines {
		c.wheelSet, c.wheelLines = true, lines
		c.tui.SetWheelScrollLines(pitui.WheelScrollLines{Auto: lines.Auto, Lines: float64(lines.Lines)})
	}
	copyOnSelect := settings.FullscreenCopyOnSelect == nil || *settings.FullscreenCopyOnSelect
	c.tui.SetCopyOnSelect(copyOnSelect)
	target := ""
	if c.rt.HasSession {
		target = fmt.Sprintf("%s#%d", c.rt.Session, c.rt.Conversation)
	}
	if !c.reportedSet || target != c.reportedTarget {
		parsed := (*GrantTarget)(nil)
		if c.rt.HasSession {
			parsed = ParseTarget(c.rt.Session, c.rt.Conversation)
		}
		c.mu.Lock()
		c.target = parsed
		c.mu.Unlock()
		c.grants.Navigate(c.rt, parsed)
	}
	// Rule 3: while anything but the runtime has focus (transcript search, a dialog's text input), keys like Ctrl+G belong to it, not to the launcher.
	suspend := c.tui.GetFocusedComponent() != pitui.Component(c.rt)
	if !c.reportedSet || suspend != c.reportedSuspend {
		c.mu.Lock()
		c.suspendPrivilege = suspend
		c.mu.Unlock()
	}
	busy := c.rt.Contexts()["busy"]
	if !c.reportedSet || busy != c.reportedBusy {
		c.term.SetProgress(busy)
		state := "idle"
		if busy {
			state = "working"
		}
		c.term.SetProgramStatus(pitui.ProgramStatus{State: pitui.ProgramState(state)})
	}
	c.reportedSet, c.reportedTarget, c.reportedSuspend, c.reportedBusy = true, target, suspend, busy
}

// poll confirms the server still admits these credentials.
func (c *Client) poll() {
	interval := c.options.Poll
	if interval == 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(c.root, 10*time.Second)
			response, err := c.transport.Request(ctx, "/__p2_tui", Request{Method: http.MethodHead})
			cancel()
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
					c.accessEnded()
				}
			}
		case <-c.stopPoll:
			return
		}
	}
}

// verifyAccess confirms a refusal with the launcher's own fixed-origin request; a lying server can only end its own session.
func (c *Client) verifyAccess() {
	go func() {
		ctx, cancel := context.WithTimeout(c.root, 10*time.Second)
		defer cancel()
		response, err := c.transport.Request(ctx, "/__p2_tui", Request{Method: http.MethodHead})
		if err != nil {
			return
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			c.accessEnded()
		}
	}()
}

func (c *Client) accessEnded() {
	c.mu.Lock()
	if c.ended || c.closed {
		c.mu.Unlock()
		return
	}
	c.ended = true
	c.mu.Unlock()
	if c.options.OnAccessEnded != nil {
		c.options.OnAccessEnded()
	}
	go c.Close()
	if c.options.OnExit != nil {
		c.options.OnExit()
	}
}

// Close ends the client: every grant, the socket, the runtime and the terminal's modes (exit and crash alike: undo every mode the runtime enabled, then the
// fixed reset, section 3.6).
func (c *Client) Close() {
	c.closing.Do(func() {
		c.mu.Lock()
		c.closed = true
		if c.retry != nil {
			c.retry.Stop()
		}
		socket := c.socket
		titled, progressing := c.titled, c.progressing
		c.mu.Unlock()
		close(c.stopPoll)
		if c.stopPrefs != nil {
			c.stopPrefs()
		}
		if socket != nil {
			socket.Terminate()
		}
		c.ui.Cancel()
		c.grants.CancelAll()
		if scope := c.activeScope(); scope != nil {
			scope.Dispose()
		}
		// A runtime that no longer answers (a facet stuck in a render or a handler) cannot hold the visitor's terminal hostage: Pi killed the worker, which here is
		// stopped at the owner loop's next turn. The terminal below is cleaned either way.
		stuck := false
		if c.tui != nil {
			stuck = !c.doWithin(closeGrace, func() {
				c.tui.Stop()
				if c.rt != nil {
					c.rt.Dispose()
				}
			})
		}
		c.probe.Stop()
		if titled {
			c.options.Terminal.SetTitle("")
		}
		if progressing {
			c.options.Terminal.SetProgress(false)
		}
		c.options.Terminal.SetProgramStatus(pitui.ProgramStatus{State: "clear"})
		c.options.Terminal.Write(c.modes.Cleanup())
		c.options.Terminal.Stop()
		if c.host != nil {
			_ = c.host.Close(c.root)
		}
		close(c.stop)
		if !stuck {
			c.loops.Wait()
		}
	})
}
