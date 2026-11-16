package subprocess

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's pi-tui `new TuiAltScreen(terminal, ...)` for a subprocess extension: the host runs the `tui` port of tui-alt-screen.ts and
// the extension owns what Pi's caller owns, the Terminal and the Components. A `tui.alt` call (CallTuiAlt) drives the screen; the host answers
// the extension's terminal, component and listener needs with `tui.alt.cb` requests (RequestTuiAltCallback). docs/extension-api-parity.md records
// the contract.
//
// Pi runs on one thread: a component's render may call the screen, and the call runs at once, inside the frame. The goroutine that runs the frame
// sends the callbacks it needs, so it runs the calls the extension makes from them itself, while it waits and holds the frame lock (callback).

// tuiAltCaps are the optional members a component of the extension has, sent with its key.
type tuiAltCaps struct {
	Input           bool `json:"input"`
	Mouse           bool `json:"mouse"`
	Focusable       bool `json:"focusable"`
	WantsKeyRelease bool `json:"wantsKeyRelease"`
	// CapturesViewportInput reports that the component has Component.capturesViewportInput; its value is read with a callback each time the screen asks.
	CapturesViewportInput bool `json:"capturesViewportInput"`
}

type tuiAltTerminalBlock struct {
	Columns int  `json:"columns"`
	Rows    int  `json:"rows"`
	Kitty   bool `json:"kitty"`
}

type tuiAltCall struct {
	ID       string               `json:"id"`
	Op       string               `json:"op"`
	Terminal *tuiAltTerminalBlock `json:"terminal,omitempty"`

	ShowHardwareCursor *bool  `json:"showHardwareCursor,omitempty"`
	LogDirectory       string `json:"logDirectory,omitempty"`
	// Options is create's tuiAltOptions or showOverlay's overlay options.
	Options        json.RawMessage `json:"options,omitempty"`
	Key            *string         `json:"key"`
	Caps           *tuiAltCaps     `json:"caps,omitempty"`
	Width          int             `json:"width"`
	Lines          json.RawMessage `json:"lines"`
	Message        string          `json:"message"`
	DurationMs     int             `json:"durationMs"`
	Enabled        bool            `json:"enabled"`
	Force          bool            `json:"force"`
	Visible        bool            `json:"visible"`
	PreserveScreen bool            `json:"preserveScreen"`
	ListenerID     string          `json:"listenerId"`
	TimeoutMs      float64         `json:"timeoutMs"`
	OnLateReply    bool            `json:"onLateReply"`
	QueryID        string          `json:"queryId"`
	Action         string          `json:"action"`
	Hidden         bool            `json:"hidden"`
	Target         json.RawMessage `json:"target"`
	Data           string          `json:"data"`
	Event          json.RawMessage `json:"event"`
}

type tuiAltOptions struct {
	WheelScrollLines json.RawMessage `json:"wheelScrollLines,omitempty"`
	Mouse            *bool           `json:"mouse,omitempty"`
	CopyOnSelect     *bool           `json:"copyOnSelect,omitempty"`
	Callbacks        []string        `json:"callbacks"`
}

// tuiAltScreenState is one screen the extension constructed.
type tuiAltScreenState struct {
	id     string
	conn   *Conn
	screen *tui.TuiAltScreen
	term   *tuiAltTerminal

	// ctx is the context of every request the screen makes (callback); end, called by disposeTuiAlt, ends it. The owner waits for the extension's answer to a
	// callback while it holds the frame lock, and disposing the screen takes that lock, so the host must be able to end the wait of an extension that never answers.
	ctx context.Context
	end context.CancelFunc

	// owner is the one goroutine that runs the screen's frames, ticks and calls (tui_alt_owner.go), so the extension's components see one callback at a time.
	owner *tuiAltOwner

	// waits holds the callbacks the owner is waiting on, by request id: see callback.
	waitMu sync.Mutex
	waits  map[string]*tuiAltWait

	mu         sync.Mutex
	components map[string]*tuiAltComponent
	byComp     map[tui.Component]string
	layoutRoot tui.Component
	overlays   map[string]*tui.OverlayHandle
	listeners  map[string]*tui.TuiInputListener
}

type tuiAltRegistry struct {
	mu      sync.Mutex
	screens map[tuiAltKey]*tuiAltScreenState
}

// tuiAlt is the bridge's registry of screens, created by its first use.
func (b *UIBridge) tuiAlt() *tuiAltRegistry {
	if registry, ok := b.tuiAltState.Load().(*tuiAltRegistry); ok {
		return registry
	}
	b.tuiAltState.CompareAndSwap(nil, &tuiAltRegistry{})
	return b.tuiAltState.Load().(*tuiAltRegistry)
}

type tuiAltKey struct {
	conn *Conn
	id   string
}

// tuiAltWait is a tui.alt.cb request the owner waits on. The owner holds the frame lock while it waits when the callback belongs to a frame (and, while a
// component renders, the base container's lock), so a call the extension makes from the callback runs on the owner, as Pi's callbacks call the screen on its one
// thread.
type tuiAltWait struct {
	// frame is the frame the owner runs when inFrame is set: the callback is one of a frame's.
	frame   tui.AltScreenFrame
	inFrame bool
	// runs carries the calls to run on the waiting goroutine; done is closed when it stops waiting.
	runs chan func()
	done chan struct{}
}

// callback sends one tui.alt.cb request and decodes its response into result.
//
// The owner sends every callback of the screen: those a frame makes (a component's render, an overlay's visible callback, a style callback, a terminal write),
// those an input, a mouse event or a focus change makes (the component's input, mouse and focus, the input listeners, the option callbacks), and those the
// screen's members make while it runs a call from one of them. It waits for the answer, holding the frame lock when the callback is a frame's. The extension
// answers from the request's own thread; a call it makes meanwhile names the request as its parent (serveOnOwner) and the owner runs it, so the call applies
// at once and a later read sees it, as in Pi. A goroutine that is not the owner sends its callback as any request (nothing the renderer does sends one).
func (s *tuiAltScreenState) callback(kind string, fields map[string]any, result any) error {
	args := map[string]any{"id": s.id, "kind": kind}
	maps.Copy(args, fields)
	ctx := s.ctx
	if !s.owner.onOwner() {
		return autocompleteRequest(ctx, s.conn, RequestTuiAltCallback, args, result)
	}
	frame, inFrame := s.screen.Frame()
	id := s.conn.newRequestID()
	wait := &tuiAltWait{frame: frame, inFrame: inFrame, runs: make(chan func()), done: make(chan struct{})}
	s.waitMu.Lock()
	if s.waits == nil {
		s.waits = map[string]*tuiAltWait{}
	}
	s.waits[id] = wait
	s.waitMu.Unlock()
	defer func() {
		s.waitMu.Lock()
		delete(s.waits, id)
		s.waitMu.Unlock()
		close(wait.done)
	}()
	return requestWithID(withRequestInterjections(ctx, wait.runs), s.conn, id, RequestTuiAltCallback, args, result)
}

// serveOnOwner runs fn on the owner while it waits for the callback request parent, with the members the owner holds for it (the frame's when the callback is a
// frame's, the screen's otherwise) and whether that is a frame, and reports whether it did. It reports false when the owner does not wait for parent, or stopped
// waiting before it took the call; the call then runs as any other, queued behind the owner's task.
func (s *tuiAltScreenState) serveOnOwner(parent string, fn func(m tuiAltMembers, inFrame bool)) bool {
	if parent == "" {
		return false
	}
	s.waitMu.Lock()
	wait := s.waits[parent]
	s.waitMu.Unlock()
	if wait == nil {
		return false
	}
	ran := make(chan struct{})
	run := func() {
		defer close(ran)
		if wait.inFrame {
			fn(wait.frame, true)
			return
		}
		fn(s.screen, false)
	}
	select {
	case wait.runs <- run:
		<-ran
		return true
	case <-wait.done:
		return false
	}
}

// tuiAltTerminal is the Terminal of the screen: the extension's own Terminal, reached with callbacks. Output is delivered in order, each write
// acknowledged before the next.
type tuiAltTerminal struct {
	state   *tuiAltScreenState
	mu      sync.Mutex
	columns int
	rows    int
	kitty   bool
	onInput func(string)
	// onResize is the resize callback Start received.
	onResize func()
}

func (t *tuiAltTerminal) send(method string, fields map[string]any) {
	args := map[string]any{"method": method}
	maps.Copy(args, fields)
	_ = t.state.callback("terminal", args, nil)
}

func (t *tuiAltTerminal) Start(onInput func(string), onResize func()) error {
	t.mu.Lock()
	t.onInput, t.onResize = onInput, onResize
	t.mu.Unlock()
	return t.state.callback("terminal", map[string]any{"method": "start"}, nil)
}
func (t *tuiAltTerminal) Stop() { t.send("stop", nil) }
func (t *tuiAltTerminal) DrainInput(maxWait, idleWait time.Duration) error {
	return t.state.callback("terminal", map[string]any{"method": "drainInput", "maxMs": maxWait.Milliseconds(), "idleMs": idleWait.Milliseconds()}, nil)
}
func (t *tuiAltTerminal) Write(data string) { t.send("write", map[string]any{"data": data}) }
func (t *tuiAltTerminal) Columns() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.columns
}
func (t *tuiAltTerminal) Rows() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rows
}
func (t *tuiAltTerminal) KittyProtocolActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kitty
}
func (t *tuiAltTerminal) MoveBy(lines int)  { t.send("moveBy", map[string]any{"lines": lines}) }
func (t *tuiAltTerminal) HideCursor()       { t.send("hideCursor", nil) }
func (t *tuiAltTerminal) ShowCursor()       { t.send("showCursor", nil) }
func (t *tuiAltTerminal) ClearLine()        { t.send("clearLine", nil) }
func (t *tuiAltTerminal) ClearFromCursor()  { t.send("clearFromCursor", nil) }
func (t *tuiAltTerminal) ClearScreen()      { t.send("clearScreen", nil) }
func (t *tuiAltTerminal) SetTitle(s string) { t.send("setTitle", map[string]any{"title": s}) }
func (t *tuiAltTerminal) SetProgress(active bool) {
	t.send("setProgress", map[string]any{"active": active})
}
func (t *tuiAltTerminal) SetProgramStatus(status tui.ProgramStatus) {
	wire := map[string]any{"state": string(status.State)}
	if status.App != "" {
		wire["app"] = status.App
	}
	if status.Kind != "" {
		wire["kind"] = string(status.Kind)
	}
	if status.Message != "" {
		wire["message"] = status.Message
	}
	t.send("setProgramStatus", map[string]any{"status": wire})
}
func (t *tuiAltTerminal) RestoreOnExit(before, after string) {
	t.send("restoreOnExit", map[string]any{"before": before, "after": after})
}

// update records the terminal block of a call and reports whether the size changed.
func (t *tuiAltTerminal) update(block *tuiAltTerminalBlock) bool {
	if block == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := t.columns != block.Columns || t.rows != block.Rows
	t.columns, t.rows, t.kitty = block.Columns, block.Rows, block.Kitty
	return changed
}

// tuiAltComponent is a component of the extension: it renders, takes input, mouse and focus through callbacks.
type tuiAltComponent struct {
	state *tuiAltScreenState
	key   string
	// caps are the members the extension named with the component's key, the newest of the calls that named it. Each call that names the component stores a fresh
	// copy, and the screen reads them from the goroutine of a frame, of an input delivery, of a mouse event and of a focus change, so a reader sees one whole
	// value and takes no lock (a lock would put a reader that the frame lock waits for behind s.mu).
	caps atomic.Pointer[tuiAltCaps]
}

func (c *tuiAltComponent) Render(width int) []string {
	var result struct {
		Lines []string `json:"lines"`
	}
	if err := c.state.callback("render", map[string]any{"key": c.key, "width": width}, &result); err != nil {
		return nil
	}
	return result.Lines
}

func (c *tuiAltComponent) Invalidate() {
	_ = c.state.callback("invalidate", map[string]any{"key": c.key}, nil)
}

func (c *tuiAltComponent) HandleInput(data string) {
	if c.caps.Load().Input {
		_ = c.state.callback("input", map[string]any{"key": c.key, "data": data}, nil)
	}
}

func (c *tuiAltComponent) WantsKeyRelease() bool { return c.caps.Load().WantsKeyRelease }

// CapturesViewportInput is the component's current capturesViewportInput (Component.capturesViewportInput in packages/tui/src/tui.ts), which the
// screen reads whenever a viewport key arrives: the extension answers a callback with the value it holds now.
func (c *tuiAltComponent) CapturesViewportInput() bool {
	if !c.caps.Load().CapturesViewportInput {
		return false
	}
	var result struct {
		Captures bool `json:"captures"`
	}
	if err := c.state.callback("capturesViewportInput", map[string]any{"key": c.key}, &result); err != nil {
		return false
	}
	return result.Captures
}

func (c *tuiAltComponent) SetFocused(focused bool) {
	if c.caps.Load().Focusable {
		_ = c.state.callback("focus", map[string]any{"key": c.key, "focused": focused}, nil)
	}
}

type tuiAltMouseEvent struct {
	Type       string `json:"type"`
	Button     string `json:"button"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	ScreenX    int    `json:"screenX"`
	ScreenY    int    `json:"screenY"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Shift      bool   `json:"shift"`
	Alt        bool   `json:"alt"`
	Ctrl       bool   `json:"ctrl"`
	WheelDelta int    `json:"wheelDelta,omitempty"`
	ClickCount int    `json:"clickCount,omitempty"`
}

type tuiAltMouseResult struct {
	Handled *bool `json:"handled,omitempty"`
	Capture *bool `json:"capture,omitempty"`
	Focus   *bool `json:"focus,omitempty"`
	Render  *bool `json:"render,omitempty"`
}

func (r *tuiAltMouseResult) tuiResult() *tui.TuiMouseEventResult {
	if r == nil {
		return nil
	}
	out := &tui.TuiMouseEventResult{Render: r.Render}
	out.Handled = r.Handled != nil && *r.Handled
	out.Capture = r.Capture != nil && *r.Capture
	out.Focus = r.Focus != nil && *r.Focus
	return out
}

func (c *tuiAltComponent) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
	if !c.caps.Load().Mouse {
		return nil
	}
	var result struct {
		Result *tuiAltMouseResult `json:"result"`
	}
	wire := tuiAltMouseEvent{
		Type: string(event.Type), Button: string(event.Button), X: event.X, Y: event.Y, ScreenX: event.ScreenX, ScreenY: event.ScreenY,
		Width: event.Width, Height: event.Height, Shift: event.Shift, Alt: event.Alt, Ctrl: event.Ctrl, WheelDelta: event.WheelDelta, ClickCount: event.ClickCount,
	}
	if err := c.state.callback("mouse", map[string]any{"key": c.key, "event": wire}, &result); err != nil {
		return nil
	}
	return result.Result.tuiResult()
}

// component returns the proxy of the component keyed key, made on first sight with caps.
func (s *tuiAltScreenState) component(key *string, caps *tuiAltCaps) (*tuiAltComponent, error) {
	if key == nil || *key == "" {
		return nil, errors.New("tui.alt: component key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.components[*key]; ok {
		if caps != nil {
			named := *caps
			c.caps.Store(&named)
		}
		return c, nil
	}
	if caps == nil {
		return nil, fmt.Errorf("tui.alt: unknown component %q", *key)
	}
	c := &tuiAltComponent{state: s, key: *key}
	named := *caps
	c.caps.Store(&named)
	s.components[*key] = c
	s.byComp[c] = *key
	return c, nil
}

func (s *tuiAltScreenState) keyOf(component tui.Component) *string {
	if component == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if key, ok := s.byComp[component]; ok {
		return &key
	}
	return nil
}

func (b *UIBridge) tuiAltScreen(owner *Conn, id string) (*tuiAltScreenState, error) {
	registry := b.tuiAlt()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	s, ok := registry.screens[tuiAltKey{owner, id}]
	if !ok {
		return nil, fmt.Errorf("tui.alt: unknown screen %q", id)
	}
	return s, nil
}

// disposeTuiAlt forgets the screens of owner (all of them when ownerOnly is false and owner is nil), stopping each: it ends the screen's callbacks and waits for
// its owner to return.
func (b *UIBridge) disposeTuiAlt(owner *Conn, all bool) {
	var screens []*tuiAltScreenState
	registry := b.tuiAlt()
	registry.mu.Lock()
	for key, s := range registry.screens {
		if all || key.conn == owner {
			screens = append(screens, s)
			delete(registry.screens, key)
		}
	}
	registry.mu.Unlock()
	for _, s := range screens {
		s.end()
		// Ending the screen's callbacks lets a frame that waits on the extension finish and release the lock CancelPendingRender takes, and ends the owner after the
		// task it runs; the connection is still open (a shutdown disposes before it closes the extension), so the extension is not told to stop its terminal: end the
		// renderer's own timers only.
		s.screen.CancelPendingRender()
		s.owner.wait()
	}
}

// disposeTuiAltOf forgets the screens of every connection of extName's bridge entry (a reload or shutdown without a named owner).
func (b *UIBridge) disposeTuiAltOf(extName string) {
	b.mu.RLock()
	owner := b.extConns[extName]
	b.mu.RUnlock()
	b.disposeTuiAlt(owner, false)
}

func tuiAltResult(value any) (*CallResultPayload, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: data}, nil
}

func (s *tuiAltScreenState) overlayState(handle *tui.OverlayHandle) map[string]any {
	state := map[string]any{"hidden": handle.IsHidden(), "focused": false, "visible": false}
	focused := s.screen.GetFocusedComponent()
	s.mu.Lock()
	for key, h := range s.overlays {
		if h == handle {
			c := s.components[key]
			state["focused"] = focused != nil && tui.Component(c) == focused
		}
	}
	s.mu.Unlock()
	if bounds, ok := handle.GetBounds(); ok {
		state["visible"] = true
		state["bounds"] = map[string]int{"row": bounds.Row, "col": bounds.Col, "width": bounds.Width, "height": bounds.Height}
	}
	return state
}

// handleTuiAlt applies one tui.alt call. parent is the request the extension made it from, which is the callback request it is serving when it calls the
// screen from a callback.
func (b *UIBridge) handleTuiAlt(owner *Conn, parent string, args json.RawMessage) (*CallResultPayload, error) {
	var call tuiAltCall
	if err := json.Unmarshal(args, &call); err != nil {
		return nil, fmt.Errorf("parse tui.alt args: %w", err)
	}
	if owner == nil {
		return nil, errors.New("tui.alt needs an extension connection")
	}
	if call.Op == "create" {
		return b.tuiAltCreate(owner, &call)
	}
	s, err := b.tuiAltScreen(owner, call.ID)
	if err != nil {
		return nil, err
	}
	if call.Op == "dispose" {
		registry := b.tuiAlt()
		registry.mu.Lock()
		delete(registry.screens, tuiAltKey{owner, call.ID})
		registry.mu.Unlock()
		// The owner ends after the task that cancels the pending frame: a dispose from a callback it waits on is that task, and it ends once the callback has
		// answered; any other dispose waits for the owner to return.
		end := func(m tuiAltMembers) {
			m.CancelPendingRender()
			s.owner.close()
		}
		if !s.serveOnOwner(parent, func(m tuiAltMembers, _ bool) { end(m) }) {
			_ = s.owner.do(func() { end(s.screen) })
			s.owner.wait()
		}
		return tuiAltResult(map[string]any{})
	}
	if s.term.update(call.Terminal) {
		s.screen.RequestRender()
	}
	if call.Op == "input" && tui.IsTerminalColorReply(call.Data) && s.screen.ConsumeTerminalColorResponse(call.Data) {
		// A reply to a colour query settles the query and nothing else, and the extension may be waiting for it inside a callback the owner is waiting on (an
		// input handler that awaits queryTerminalColors, which Pi's loop lets it do: handleTerminalInput consumes the reply first, tui.ts:1121), so it does not
		// queue behind the owner.
		return tuiAltResult(map[string]any{})
	}
	if call.Op == "queryTerminalColors" {
		return s.queryTerminalColors(parent, &call)
	}
	// A call from a callback the owner waits for runs on the owner, at once, as Pi's callbacks call the screen; any other call runs after the task the owner is in.
	var result *CallResultPayload
	var callErr error
	if s.serveOnOwner(parent, func(m tuiAltMembers, inFrame bool) { result, callErr = s.run(&call, m, inFrame) }) {
		return result, callErr
	}
	if err := s.owner.do(func() { result, callErr = s.run(&call, s.screen, false) }); err != nil {
		return nil, err
	}
	return result, callErr
}

func (b *UIBridge) tuiAltCreate(owner *Conn, call *tuiAltCall) (*CallResultPayload, error) {
	if call.ID == "" {
		return nil, errors.New("tui.alt create needs an id")
	}
	registry := b.tuiAlt()
	registry.mu.Lock()
	if _, taken := registry.screens[tuiAltKey{owner, call.ID}]; taken {
		registry.mu.Unlock()
		return nil, fmt.Errorf("tui.alt: screen %q already exists", call.ID)
	}
	registry.mu.Unlock()
	s := &tuiAltScreenState{
		id: call.ID, conn: owner,
		components: map[string]*tuiAltComponent{}, byComp: map[tui.Component]string{},
		overlays: map[string]*tui.OverlayHandle{}, listeners: map[string]*tui.TuiInputListener{},
	}
	s.term = &tuiAltTerminal{state: s}
	s.term.update(call.Terminal)
	var options tui.TuiAltScreenOptions
	if len(call.Options) > 0 && string(call.Options) != "null" {
		o := &tuiAltOptions{}
		if err := json.Unmarshal(call.Options, o); err != nil {
			return nil, fmt.Errorf("parse tui.alt options: %w", err)
		}
		options.Mouse, options.CopyOnSelect = o.Mouse, o.CopyOnSelect
		if len(o.WheelScrollLines) > 0 {
			lines, err := parseTuiAltWheel(o.WheelScrollLines)
			if err != nil {
				return nil, err
			}
			options.WheelScrollLines = lines
		}
		s.bindCallbacks(&options, o.Callbacks)
	}
	//portlint:allow ctxbackground the screen outlives the call that built it; its callbacks end when disposeTuiAlt ends it or the connection closes (Request returns when it closes)
	s.ctx, s.end = context.WithCancel(context.Background())
	s.screen = tui.NewTuiAltScreen(s.term, call.ShowHardwareCursor, call.LogDirectory, options)
	// The components are the extension's: their calls back into the screen run on the owner, which waits for their callback (callback).
	s.screen.EnableFrameAccess()
	s.screen.OnTerminalColorSchemeChange(func(scheme tui.TerminalColorScheme) {
		_ = s.callback("colorScheme", map[string]any{"scheme": string(scheme)}, nil)
	})
	s.owner = newTuiAltOwner(s.ctx, s.end, owner.Done(), s.screen)
	s.owner.start()
	registry.mu.Lock()
	if registry.screens == nil {
		registry.screens = map[tuiAltKey]*tuiAltScreenState{}
	}
	registry.screens[tuiAltKey{owner, call.ID}] = s
	registry.mu.Unlock()
	return tuiAltResult(map[string]any{})
}

func parseTuiAltWheel(raw json.RawMessage) (tui.WheelScrollLines, error) {
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return tui.WheelScrollLines{Lines: number}, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && text == "auto" {
		return tui.WheelScrollLines{Auto: true}, nil
	}
	return tui.WheelScrollLines{}, errors.New(`tui.alt: wheelScrollLines is a number or "auto"`)
}

// bindCallbacks turns the callback options the extension named into functions that call it.
func (s *tuiAltScreenState) bindCallbacks(options *tui.TuiAltScreenOptions, names []string) {
	option := func(name string, args []any, result any) error {
		// Style callbacks and the end indicator run while a frame draws, on the frame's goroutine; the rest run on the input path.
		return s.callback("option", map[string]any{"name": name, "args": args}, result)
	}
	text := func(name string) func(string) string {
		return func(in string) string {
			var out struct {
				Value string `json:"value"`
			}
			if err := option(name, []any{in}, &out); err != nil {
				return in
			}
			return out.Value
		}
	}
	for _, name := range names {
		switch name {
		case "copySelection":
			options.CopySelection = func(in string) error {
				var out struct {
					Value json.RawMessage `json:"value"`
				}
				if err := option(name, []any{in}, &out); err != nil {
					return err
				}
				var ok bool
				if json.Unmarshal(out.Value, &ok) == nil {
					if ok {
						return nil
					}
					return errors.New("")
				}
				var message string
				_ = json.Unmarshal(out.Value, &message)
				return errors.New(message)
			}
		case "openUrl":
			options.OpenURL = func(url string) { _ = option(name, []any{url}, nil) }
		case "searchMatchStyle":
			options.SearchMatchStyle = text(name)
		case "searchCurrentMatchStyle":
			options.SearchCurrentMatchStyle = text(name)
		case "searchNavigationButtonStyle":
			options.SearchNavigationButtonStyle = func(in string, hovered bool) string {
				var out struct {
					Value string `json:"value"`
				}
				if err := option(name, []any{in, hovered}, &out); err != nil {
					return in
				}
				return out.Value
			}
		case "scrollToEndIndicator":
			options.ScrollToEndIndicator = func() string {
				var out struct {
					Value string `json:"value"`
				}
				_ = option(name, nil, &out)
				return out.Value
			}
		case "onRightClickPaste":
			options.OnRightClickPaste = func() { _ = option(name, nil, nil) }
		}
	}
}

func (s *tuiAltScreenState) focusedKey() any {
	if key := s.keyOf(s.screen.GetFocusedComponent()); key != nil {
		return *key
	}
	return nil
}

// tuiAltMembers are the members of the screen that take a lock the frame holds: *tui.TuiAltScreen takes them, tui.AltScreenFrame (for the goroutine that
// runs the frame, which holds them) does not.
type tuiAltMembers interface {
	GetScreenLines() []string
	HasActiveSelection() bool
	GetCopyOnSelect() bool
	SetCopyOnSelect(enabled bool)
	GetShowHardwareCursor() bool
	SetShowHardwareCursor(enabled bool)
	GetClearOnShrink() bool
	SetClearOnShrink(enabled bool)
	ResetTextSelection()
	SetOnDebug(onDebug func())
	SetLayoutRoot(component tui.Component)
	SetFocus(component tui.Component)
	AddChild(component tui.Component)
	RemoveChild(component tui.Component)
	Clear()
	Children() []tui.Component
	HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult
	ViewportTop() int
	IsFollowingOutput() bool
	ScrollBy(lines int)
	ScrollToTop()
	ScrollToBottom()
	CancelPendingRender()
}

// run applies call to the screen on the owner. inFrame says the call comes from a callback of a frame that the owner waits for and runs on the owner, and m is
// the frame it holds; otherwise m is the screen.
func (s *tuiAltScreenState) run(call *tuiAltCall, m tuiAltMembers, inFrame bool) (*CallResultPayload, error) {
	screen := s.screen
	empty := func() (*CallResultPayload, error) { return tuiAltResult(map[string]any{}) }
	if inFrame {
		switch call.Op {
		case "render", "renderNow", "start", "stop", "copyActiveSelectionToClipboard", "queryTerminalColors", "input":
			// pig divergence (D208): Pi re-enters its own frame or recurses here (renderNow inside render renders the frame again, start and stop nest a
			// terminal start and stop, the Promise-returning copy and color query run inside the frame, input dispatches input from render); the frame here
			// holds its lock for the render it waits on, which these would take again, so the call fails and the screen goes on.
			return nil, errTuiAltInFrame(call.Op)
		}
	}
	switch call.Op {
	case "start":
		// Pi's TuiBase.start() order (tui.ts:956): the screen enters the alternate screen, the terminal starts on it, the cursor is hidden, then the
		// colour-scheme reports, the cell-size query and the first frame.
		screen.BeforeTerminalStart()
		if err := s.term.Start(func(data string) { screen.HandleTerminalInput(data) }, func() { screen.RequestRender() }); err != nil {
			return nil, err
		}
		// Through the screen, which takes the writer lock: a frame of the screen's timer may be writing.
		screen.HideCursor()
		screen.StartRequestingRender()
		return empty()
	case "stop":
		screen.ShowCursor()
		screen.StopWithOptions(tui.StopOptions{PreserveScreen: call.PreserveScreen})
		s.term.Stop()
		return empty()
	case "renderNow":
		screen.RenderNow(call.Force)
		return empty()
	case "requestRender":
		screen.RequestRender(call.Force)
		return empty()
	case "invalidate":
		// The root is flagged for the next frame and the components are invalidated below, once each and at once, as Pi's
		// TUI.invalidate does on its one thread (tui.ts:951-954). screen.Invalidate would walk the same components a second time,
		// and during a frame only when the frame ends, after the render that asked has read its own invalidation back.
		screen.Container.Invalidate()
		// Pi's invalidate reaches the mounted roots (the layout root, else the children) and every overlay's component, each of the extension.
		// The renderer's container lock is taken before s.mu: a frame holds it while it waits on a component's render, which calls back into s.
		children := m.Children()
		s.mu.Lock()
		var targets []tui.Component
		if s.layoutRoot != nil {
			targets = append(targets, s.layoutRoot)
		} else {
			targets = children
		}
		for key := range s.overlays {
			targets = append(targets, s.components[key])
		}
		s.mu.Unlock()
		for _, c := range targets {
			c.Invalidate()
		}
		return empty()
	case "render":
		return tuiAltResult(map[string]any{"lines": nonNilLines(screen.RenderSnapshot(call.Width))})
	case "getScreenLines":
		return tuiAltResult(map[string]any{"lines": nonNilLines(m.GetScreenLines())})
	case "scrollBy":
		var lines float64
		if err := json.Unmarshal(call.Lines, &lines); err != nil {
			return nil, errors.New("tui.alt: scrollBy lines is a number")
		}
		m.ScrollBy(int(lines))
		return empty()
	case "scrollToTop":
		m.ScrollToTop()
		return empty()
	case "scrollToBottom":
		m.ScrollToBottom()
		return empty()
	case "flash":
		screen.Flash(call.Message, call.DurationMs)
		return empty()
	case "setWheelScrollLines":
		lines, err := parseTuiAltWheel(call.Lines)
		if err != nil {
			return nil, err
		}
		screen.SetWheelScrollLines(lines)
		return empty()
	case "setCopyOnSelect":
		m.SetCopyOnSelect(call.Enabled)
		return empty()
	case "setShowHardwareCursor":
		m.SetShowHardwareCursor(call.Enabled)
		return empty()
	case "setClearOnShrink":
		m.SetClearOnShrink(call.Enabled)
		return empty()
	case "resetTextSelection":
		m.ResetTextSelection()
		return empty()
	case "copyActiveSelectionToClipboard":
		return tuiAltResult(map[string]any{"value": screen.CopyActiveSelectionToClipboard()})
	case "state":
		return tuiAltResult(map[string]any{
			"viewportTop": m.ViewportTop(), "isFollowingOutput": m.IsFollowingOutput(), "fullRedraws": screen.FullRedraws(),
			"hasOverlay": screen.HasVisibleOverlay(), "hasOverlayEntries": screen.HasOverlay(), "hasActiveSelection": m.HasActiveSelection(),
			"copyOnSelect": m.GetCopyOnSelect(), "showHardwareCursor": m.GetShowHardwareCursor(), "clearOnShrink": m.GetClearOnShrink(),
			"focusedKey": s.focusedKey(),
		})
	case "addChild", "removeChild":
		c, err := s.component(call.Key, call.Caps)
		if err != nil {
			return nil, err
		}
		if call.Op == "addChild" {
			m.AddChild(c)
		} else {
			m.RemoveChild(c)
		}
		return empty()
	case "clear":
		m.Clear()
		return empty()
	case "setLayoutRoot", "setFocus":
		var component tui.Component
		if call.Key != nil {
			c, err := s.component(call.Key, call.Caps)
			if err != nil {
				return nil, err
			}
			component = c
		}
		if call.Op == "setLayoutRoot" {
			s.mu.Lock()
			s.layoutRoot = component
			s.mu.Unlock()
			m.SetLayoutRoot(component)
		} else {
			m.SetFocus(component)
		}
		return empty()
	case "showOverlay":
		c, err := s.component(call.Key, call.Caps)
		if err != nil {
			return nil, err
		}
		var layout *extension.OverlayLayout
		if len(call.Options) > 0 && string(call.Options) != "null" {
			layout = &extension.OverlayLayout{}
			if err := json.Unmarshal(call.Options, layout); err != nil {
				return nil, fmt.Errorf("parse overlay options: %w", err)
			}
		}
		spec := overlaySpec(layout)
		if call.Visible {
			spec.Visible = func(termWidth, termHeight int) bool {
				var out struct {
					Value bool `json:"value"`
				}
				if err := s.callback("visible", map[string]any{"key": c.key, "termWidth": termWidth, "termHeight": termHeight}, &out); err != nil {
					return false
				}
				return out.Value
			}
		}
		handle := screen.ShowOverlay(c, spec.Options())
		s.mu.Lock()
		s.overlays[c.key] = handle
		s.mu.Unlock()
		return tuiAltResult(s.overlayState(handle))
	case "hideOverlay":
		screen.HideOverlay()
		return empty()
	case "overlay":
		return s.overlayControl(call)
	case "addInputListener":
		listener := tui.TuiInputListener(func(data string) *tui.TuiInputResult {
			var out struct {
				Consume bool    `json:"consume"`
				Data    *string `json:"data"`
			}
			if err := s.callback("listener", map[string]any{"listenerId": call.ListenerID, "data": data}, &out); err != nil {
				return nil
			}
			return &tui.TuiInputResult{Consume: out.Consume, Data: out.Data}
		})
		s.mu.Lock()
		s.listeners[call.ListenerID] = &listener
		s.mu.Unlock()
		screen.AddInputListener(&listener)
		return empty()
	case "removeInputListener":
		s.mu.Lock()
		listener := s.listeners[call.ListenerID]
		delete(s.listeners, call.ListenerID)
		s.mu.Unlock()
		if listener != nil {
			screen.RemoveInputListener(listener)
		}
		return empty()
	case "setOnDebug":
		if call.Enabled {
			m.SetOnDebug(func() { _ = s.callback("debug", nil, nil) })
		} else {
			m.SetOnDebug(nil)
		}
		return empty()
	case "setTerminalColorSchemeNotifications":
		screen.SetTerminalColorSchemeNotifications(call.Enabled)
		return empty()
	case "handleMouse":
		var event tuiAltMouseEvent
		if err := json.Unmarshal(call.Event, &event); err != nil {
			return nil, fmt.Errorf("parse mouse event: %w", err)
		}
		result := m.HandleMouse(tui.TuiMouseEvent{
			Type: tui.TuiMouseEventType(event.Type), Button: tui.TuiMouseButton(event.Button), X: event.X, Y: event.Y, ScreenX: event.ScreenX, ScreenY: event.ScreenY,
			Width: event.Width, Height: event.Height, Shift: event.Shift, Alt: event.Alt, Ctrl: event.Ctrl, WheelDelta: event.WheelDelta, ClickCount: event.ClickCount,
		})
		if result == nil {
			return tuiAltResult(map[string]any{"result": nil})
		}
		return tuiAltResult(map[string]any{"result": map[string]any{"handled": result.Handled, "capture": result.Capture, "focus": result.Focus, "render": result.Render}})
	case "input":
		screen.HandleTerminalInput(call.Data)
		return empty()
	case "resize":
		screen.RequestRender()
		return empty()
	}
	return nil, fmt.Errorf("tui.alt: unknown op %q", call.Op)
}

// errTuiAltInFrame is D208's error for a call that cannot run while a component renders.
func errTuiAltInFrame(op string) error {
	return fmt.Errorf("tui.alt: %s cannot run while a component renders", op)
}

// queryTerminalColors starts a colour query and answers it when the terminal has. The query is started on the owner, as every call is, and its answer is waited
// for off it: the reply arrives as input, which the owner must be free to run (Pi's handleTerminalInput consumes the reply on the loop while a handler that
// awaited the query is suspended). The call is rejected from a frame's callback (D208), where the frame holds the lock its input would take.
func (s *tuiAltScreenState) queryTerminalColors(parent string, call *tuiAltCall) (*CallResultPayload, error) {
	options := tui.TerminalColorQueryOptions{TimeoutMs: call.TimeoutMs}
	if call.OnLateReply {
		options.OnLateReply = func(colors tui.TerminalColors) {
			// The late reply is consumed off the owner (handleTuiAlt); the extension's callback for it goes out on the owner.
			s.owner.post(func() {
				_ = s.callback("lateColors", map[string]any{"queryId": call.QueryID, "colors": tuiAltColorsWire(colors)}, nil)
			})
		}
	}
	var pending <-chan tui.TerminalColorsResult
	var rejected error
	start := func(_ tuiAltMembers, inFrame bool) {
		if inFrame {
			rejected = errTuiAltInFrame(call.Op)
			return
		}
		pending = s.screen.QueryTerminalColors(options)
	}
	if !s.serveOnOwner(parent, start) {
		if err := s.owner.do(func() { start(s.screen, false) }); err != nil {
			return nil, err
		}
	}
	if rejected != nil {
		return nil, rejected
	}
	select {
	case result := <-pending:
		if result.Err != nil {
			return nil, result.Err
		}
		return tuiAltResult(map[string]any{"colors": tuiAltColorsWire(result.Colors)})
	case <-s.ctx.Done():
		return nil, errTuiAltScreenEnded
	}
}

func nonNilLines(lines []string) []string {
	if lines == nil {
		return []string{}
	}
	return lines
}

func (s *tuiAltScreenState) overlayControl(call *tuiAltCall) (*CallResultPayload, error) {
	if call.Key == nil {
		return nil, errors.New("tui.alt overlay needs a key")
	}
	s.mu.Lock()
	handle := s.overlays[*call.Key]
	s.mu.Unlock()
	if handle == nil {
		return nil, fmt.Errorf("tui.alt: unknown overlay %q", *call.Key)
	}
	switch call.Action {
	case "hide":
		handle.Hide()
		s.mu.Lock()
		delete(s.overlays, *call.Key)
		s.mu.Unlock()
	case "setHidden":
		handle.SetHidden(call.Hidden)
	case "focus":
		handle.Focus()
	case "unfocus":
		switch {
		case len(call.Target) == 0:
			handle.Unfocus()
		case string(call.Target) == "null":
			handle.UnfocusWith(tui.OverlayUnfocusOptions{})
		default:
			var target struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(call.Target, &target); err != nil {
				return nil, fmt.Errorf("parse unfocus target: %w", err)
			}
			c, err := s.component(&target.Key, nil)
			if err != nil {
				return nil, err
			}
			handle.UnfocusWith(tui.OverlayUnfocusOptions{Target: c})
		}
	case "state":
	default:
		return nil, fmt.Errorf("tui.alt: unknown overlay action %q", call.Action)
	}
	return tuiAltResult(s.overlayState(handle))
}

// overlaySpec converts the extension's serialised overlay options (nil for none) into the renderer's spec.
func overlaySpec(layout *extension.OverlayLayout) tui.OverlaySpec {
	if layout == nil {
		return tui.OverlaySpec{}
	}
	value := func(v *extension.OverlaySizeValue) *tui.OverlayValue {
		if v == nil {
			return nil
		}
		return &tui.OverlayValue{Value: v.Value, Percent: v.Percent, Invalid: v.Invalid}
	}
	spec := tui.OverlaySpec{
		Width: value(layout.Width), MinWidth: layout.MinWidth, MaxHeight: value(layout.MaxHeight), Anchor: layout.Anchor,
		OffsetX: layout.OffsetX, OffsetY: layout.OffsetY, Row: value(layout.Row), Col: value(layout.Col), NonCapturing: layout.NonCapturing,
	}
	if m := layout.Margin; m != nil {
		if m.All != nil {
			spec.Margin = tui.OverlayMarginAll(*m.All)
		} else {
			spec.Margin = tui.OverlayMarginSpec{Top: m.Top, Right: m.Right, Bottom: m.Bottom, Left: m.Left}
		}
	}
	return spec
}

// tuiAltColorsWire is the JSON form of terminal colors: foreground, background and palette, each present only when reported, as {r,g,b}.
func tuiAltColorsWire(colors tui.TerminalColors) map[string]any {
	rgb := func(c tui.RgbColor) map[string]float64 { return map[string]float64{"r": c.R, "g": c.G, "b": c.B} }
	out := map[string]any{}
	if colors.Foreground != nil {
		out["foreground"] = rgb(*colors.Foreground)
	}
	if colors.Background != nil {
		out["background"] = rgb(*colors.Background)
	}
	if colors.Palette != nil {
		palette := make([]map[string]float64, len(colors.Palette))
		for i, c := range colors.Palette {
			palette[i] = rgb(c)
		}
		out["palette"] = palette
	}
	return out
}
