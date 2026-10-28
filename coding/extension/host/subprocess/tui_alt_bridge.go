package subprocess

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's pi-tui `new TuiAltScreen(terminal, ...)` for a subprocess extension (D204 closed): the host runs the `tui` port of tui-alt-screen.ts and
// the extension owns what Pi's caller owns, the Terminal and the Components. A `tui.alt` call (CallTuiAlt) drives the screen; the host answers
// the extension's terminal, component and listener needs with `tui.alt.cb` requests (RequestTuiAltCallback). docs/extension-api-parity.md records
// the contract.

// tuiAltCaps are the optional members a component of the extension has, sent with its key.
type tuiAltCaps struct {
	Input           bool `json:"input"`
	Mouse           bool `json:"mouse"`
	Focusable       bool `json:"focusable"`
	WantsKeyRelease bool `json:"wantsKeyRelease"`
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
	Options    json.RawMessage `json:"options,omitempty"`
	Key        *string         `json:"key"`
	Caps       *tuiAltCaps     `json:"caps,omitempty"`
	Width      int             `json:"width"`
	Lines      json.RawMessage `json:"lines"`
	Message    string          `json:"message"`
	DurationMs int             `json:"durationMs"`
	Enabled    bool            `json:"enabled"`
	Force      bool            `json:"force"`
	Visible    bool            `json:"visible"`
	// InFrame marks a call the extension makes from inside a callback the frame waits on (a component's render, an overlay's visible callback, a style
	// callback): the SDK knows its calling thread is in one. Only such a call must not wait on the frame lock.
	InFrame        bool            `json:"inFrame"`
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

	// inFrame counts the callbacks the renderer's frame is waiting on (a component's render, a style callback, an overlay's visible callback); frame
	// is the renderer state those callbacks may read, taken as the frame began. The frame goroutine holds the renderer's frame lock while it waits, so a
	// call the extension makes from such a callback (Pi lets render read the screen) is answered without taking it: see opInFrame.
	inFrame atomic.Int32
	frame   atomic.Pointer[tui.AltScreenFrameState]

	deferMu       sync.Mutex
	deferred      []*tuiAltCall
	deferDraining bool

	mu         sync.Mutex
	components map[string]*tuiAltComponent
	byComp     map[tui.Component]string
	layoutRoot tui.Component
	overlays   map[string]*tui.OverlayHandle
	listeners  map[string]*tui.TuiInputListener
	disposed   bool
}

type tuiAltRegistry struct {
	mu      sync.Mutex
	screens map[tuiAltKey]*tuiAltScreenState
}

type tuiAltKey struct {
	conn *Conn
	id   string
}

// callback sends one tui.alt.cb request and decodes its response into result.
func (s *tuiAltScreenState) callback(kind string, fields map[string]any, result any) error {
	args := map[string]any{"id": s.id, "kind": kind}
	for k, v := range fields {
		args[k] = v
	}
	//portlint:allow ctxbackground the screen outlives the call that built it; a callback ends with the connection (Request returns when it closes)
	return autocompleteRequest(context.Background(), s.conn, RequestTuiAltCallback, args, result)
}

// frameCallback is callback for the callbacks a frame waits on.
func (s *tuiAltScreenState) frameCallback(kind string, fields map[string]any, result any) error {
	if state, ok := s.screen.FrameState(); ok {
		s.frame.Store(&state)
		s.inFrame.Add(1)
		defer s.inFrame.Add(-1)
	}
	return s.callback(kind, fields, result)
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
	for k, v := range fields {
		args[k] = v
	}
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
	caps  tuiAltCaps
}

func (c *tuiAltComponent) Render(width int) []string {
	var result struct {
		Lines []string `json:"lines"`
	}
	if err := c.state.frameCallback("render", map[string]any{"key": c.key, "width": width}, &result); err != nil {
		return nil
	}
	return result.Lines
}

func (c *tuiAltComponent) Invalidate() {
	_ = c.state.callback("invalidate", map[string]any{"key": c.key}, nil)
}

func (c *tuiAltComponent) HandleInput(data string) {
	if c.caps.Input {
		_ = c.state.callback("input", map[string]any{"key": c.key, "data": data}, nil)
	}
}

func (c *tuiAltComponent) WantsKeyRelease() bool { return c.caps.WantsKeyRelease }

func (c *tuiAltComponent) SetFocused(focused bool) {
	if c.caps.Focusable {
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
	if !c.caps.Mouse {
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
			c.caps = *caps
		}
		return c, nil
	}
	if caps == nil {
		return nil, fmt.Errorf("tui.alt: unknown component %q", *key)
	}
	c := &tuiAltComponent{state: s, key: *key, caps: *caps}
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
	b.tuiAlt.mu.Lock()
	defer b.tuiAlt.mu.Unlock()
	s, ok := b.tuiAlt.screens[tuiAltKey{owner, id}]
	if !ok {
		return nil, fmt.Errorf("tui.alt: unknown screen %q", id)
	}
	return s, nil
}

// disposeTuiAlt forgets the screens of owner (all of them when ownerOnly is false and owner is nil), stopping each.
func (b *UIBridge) disposeTuiAlt(owner *Conn, all bool) {
	var screens []*tuiAltScreenState
	b.tuiAlt.mu.Lock()
	for key, s := range b.tuiAlt.screens {
		if all || key.conn == owner {
			screens = append(screens, s)
			delete(b.tuiAlt.screens, key)
		}
	}
	b.tuiAlt.mu.Unlock()
	for _, s := range screens {
		s.mu.Lock()
		s.disposed = true
		s.mu.Unlock()
		// The connection is gone, so a stop could not reach the extension's terminal; end the renderer's own timers only.
		s.screen.CancelPendingRender()
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

func (b *UIBridge) handleTuiAlt(owner *Conn, args json.RawMessage) (*CallResultPayload, error) {
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
		b.tuiAlt.mu.Lock()
		delete(b.tuiAlt.screens, tuiAltKey{owner, call.ID})
		b.tuiAlt.mu.Unlock()
		s.screen.CancelPendingRender()
		return tuiAltResult(map[string]any{})
	}
	if s.term.update(call.Terminal) {
		s.screen.RequestRender()
	}
	if call.InFrame && s.inFrame.Load() > 0 {
		return s.opInFrame(&call)
	}
	return s.op(&call)
}

func (b *UIBridge) tuiAltCreate(owner *Conn, call *tuiAltCall) (*CallResultPayload, error) {
	if call.ID == "" {
		return nil, errors.New("tui.alt create needs an id")
	}
	b.tuiAlt.mu.Lock()
	if _, taken := b.tuiAlt.screens[tuiAltKey{owner, call.ID}]; taken {
		b.tuiAlt.mu.Unlock()
		return nil, fmt.Errorf("tui.alt: screen %q already exists", call.ID)
	}
	b.tuiAlt.mu.Unlock()
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
	s.screen = tui.NewTuiAltScreen(s.term, call.ShowHardwareCursor, call.LogDirectory, options)
	s.screen.OnTerminalColorSchemeChange(func(scheme tui.TerminalColorScheme) {
		_ = s.callback("colorScheme", map[string]any{"scheme": string(scheme)}, nil)
	})
	b.tuiAlt.mu.Lock()
	if b.tuiAlt.screens == nil {
		b.tuiAlt.screens = map[tuiAltKey]*tuiAltScreenState{}
	}
	b.tuiAlt.screens[tuiAltKey{owner, call.ID}] = s
	b.tuiAlt.mu.Unlock()
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
		// Style callbacks and the end indicator run while a frame draws; the rest run on the input path.
		return s.frameCallback("option", map[string]any{"name": name, "args": args}, result)
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

func (s *tuiAltScreenState) op(call *tuiAltCall) (*CallResultPayload, error) {
	screen := s.screen
	empty := func() (*CallResultPayload, error) { return tuiAltResult(map[string]any{}) }
	switch call.Op {
	case "start":
		screen.StartRequestingRender()
		if err := s.term.Start(func(data string) { screen.HandleTerminalInput(data) }, func() { screen.RequestRender() }); err != nil {
			return nil, err
		}
		s.term.HideCursor()
		return empty()
	case "stop":
		s.term.ShowCursor()
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
		screen.Invalidate()
		// Pi's invalidate reaches the mounted roots (the layout root, else the children) and every overlay's component, each of the extension.
		// The renderer's container lock is taken before s.mu: a frame holds it while it waits on a component's render, which calls back into s.
		children := screen.Children()
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
		return tuiAltResult(map[string]any{"lines": nonNilLines(screen.GetScreenLines())})
	case "scrollBy":
		var lines float64
		if err := json.Unmarshal(call.Lines, &lines); err != nil {
			return nil, errors.New("tui.alt: scrollBy lines is a number")
		}
		screen.ScrollBy(int(lines))
		return empty()
	case "scrollToTop":
		screen.ScrollToTop()
		return empty()
	case "scrollToBottom":
		screen.ScrollToBottom()
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
		screen.SetCopyOnSelect(call.Enabled)
		return empty()
	case "setShowHardwareCursor":
		screen.SetShowHardwareCursor(call.Enabled)
		return empty()
	case "setClearOnShrink":
		screen.SetClearOnShrink(call.Enabled)
		return empty()
	case "resetTextSelection":
		screen.ResetTextSelection()
		return empty()
	case "copyActiveSelectionToClipboard":
		return tuiAltResult(map[string]any{"value": screen.CopyActiveSelectionToClipboard()})
	case "state":
		return tuiAltResult(map[string]any{
			"viewportTop": screen.ViewportTop(), "isFollowingOutput": screen.IsFollowingOutput(), "fullRedraws": screen.FullRedraws(),
			"hasOverlay": screen.HasVisibleOverlay(), "hasOverlayEntries": screen.HasOverlay(), "hasActiveSelection": screen.HasActiveSelection(),
			"copyOnSelect": screen.GetCopyOnSelect(), "showHardwareCursor": screen.GetShowHardwareCursor(), "clearOnShrink": screen.GetClearOnShrink(),
			"focusedKey": s.focusedKey(),
		})
	case "addChild", "removeChild":
		c, err := s.component(call.Key, call.Caps)
		if err != nil {
			return nil, err
		}
		if call.Op == "addChild" {
			screen.AddChild(c)
		} else {
			screen.RemoveChild(c)
		}
		return empty()
	case "clear":
		screen.Clear()
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
			screen.SetLayoutRoot(component)
		} else {
			screen.SetFocus(component)
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
				if err := s.frameCallback("visible", map[string]any{"key": c.key, "termWidth": termWidth, "termHeight": termHeight}, &out); err != nil {
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
			screen.SetOnDebug(func() { _ = s.callback("debug", nil, nil) })
		} else {
			screen.SetOnDebug(nil)
		}
		return empty()
	case "setTerminalColorSchemeNotifications":
		screen.SetTerminalColorSchemeNotifications(call.Enabled)
		return empty()
	case "queryTerminalColors":
		options := tui.TerminalColorQueryOptions{TimeoutMs: call.TimeoutMs}
		if call.OnLateReply {
			options.OnLateReply = func(colors tui.TerminalColors) {
				_ = s.callback("lateColors", map[string]any{"queryId": call.QueryID, "colors": tuiAltColorsWire(colors)}, nil)
			}
		}
		result := <-screen.QueryTerminalColors(options)
		if result.Err != nil {
			return nil, result.Err
		}
		return tuiAltResult(map[string]any{"colors": tuiAltColorsWire(result.Colors)})
	case "handleMouse":
		var event tuiAltMouseEvent
		if err := json.Unmarshal(call.Event, &event); err != nil {
			return nil, fmt.Errorf("parse mouse event: %w", err)
		}
		result := screen.HandleMouse(tui.TuiMouseEvent{
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

// opInFrame answers a call the extension marked as made from a callback the frame waits on. Pi's single thread lets a render read the screen and even change it; here the frame goroutine holds the
// frame lock, so a call that takes it would wait on a frame that waits on the call. A read answers from the state as the frame began, which is
// what Pi's render reads. A change returns at once and applies after the frame, in call order. What needs the frame itself (render, renderNow,
// start, stop, the selection copy) is an error: Pi would re-enter its own frame.
func (s *tuiAltScreenState) opInFrame(call *tuiAltCall) (*CallResultPayload, error) {
	frame := s.frame.Load()
	if frame == nil {
		return s.op(call)
	}
	screen := s.screen
	switch call.Op {
	case "state":
		return tuiAltResult(map[string]any{
			"viewportTop": screen.ViewportTop(), "isFollowingOutput": screen.IsFollowingOutput(), "fullRedraws": screen.FullRedraws(),
			"hasOverlay": screen.HasVisibleOverlay(), "hasOverlayEntries": screen.HasOverlay(), "hasActiveSelection": frame.HasActiveSelection,
			"copyOnSelect": frame.CopyOnSelect, "showHardwareCursor": frame.ShowHardwareCursor, "clearOnShrink": frame.ClearOnShrink,
			"focusedKey": s.focusedKey(),
		})
	case "getScreenLines":
		return tuiAltResult(map[string]any{"lines": nonNilLines(frame.ScreenLines)})
	case "render", "renderNow", "start", "stop", "copyActiveSelectionToClipboard", "queryTerminalColors":
		return nil, fmt.Errorf("tui.alt: %s cannot run while a component renders", call.Op)
	case "requestRender", "dispose":
		return s.op(call)
	}
	s.deferMu.Lock()
	s.deferred = append(s.deferred, call)
	start := !s.deferDraining
	s.deferDraining = true
	s.deferMu.Unlock()
	if start {
		//portlint:allow golifetime the drain ends when the deferred queue is empty; each change waits only for the renderer's locks, which the frame that deferred it releases
		go s.drainDeferred()
	}
	return tuiAltResult(map[string]any{})
}

// drainDeferred applies the changes made during a frame, in order; each waits on the renderer's locks until the frame ended.
func (s *tuiAltScreenState) drainDeferred() {
	for {
		s.deferMu.Lock()
		if len(s.deferred) == 0 {
			s.deferDraining = false
			s.deferMu.Unlock()
			return
		}
		call := s.deferred[0]
		s.deferred = s.deferred[1:]
		s.deferMu.Unlock()
		_, _ = s.op(call)
	}
}
