package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"sync"
)

// Pi's TuiAltScreen (pi-tui tui-alt-screen.ts): the alternate-screen TUI host with a scrollable, application-owned viewport. The host runs
// the renderer (the `tui` port of tui-alt-screen.ts); the extension supplies the [Terminal] it draws on and the [Component]s it shows, and the
// host calls back into them (docs/extension-api-parity.md, "TuiAltScreen"). Every member that reads or changes the renderer is a host round
// trip and reports its error; the members Pi serves from the object itself (Mode, Children, Terminal, ViewportTui, SetOnDebug,
// OnTerminalColorSchemeChange) answer locally.
//
// Components are compared by identity, as in Pi, so every [Component] passed to the screen must be a non-nil pointer. Every member may be called
// from a component's render and applies at once, except Render, RenderNow, Start, Stop, CopyActiveSelectionToClipboard and QueryTerminalColors,
// which the host fails with "tui.alt: <op> cannot run while a component renders".

// ProgramStatus is pi-tui's ProgramStatus (OSC 7501): State is idle, working, blocked, done, error or clear; an empty App, Kind or Message is
// upstream's absent field.
type ProgramStatus struct {
	State   string `json:"state"`
	App     string `json:"app,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Message string `json:"message,omitempty"`
}

// Terminal is pi-tui's Terminal interface: the terminal a [TuiAltScreen] draws on. Start receives the callbacks the screen wants input and
// resize reports through. DrainInput is Pi's Promise<void> and returns when the input is drained.
type Terminal interface {
	Start(onInput func(data string), onResize func())
	Stop()
	DrainInput(maxMs, idleMs int)
	Write(data string)
	Columns() int
	Rows() int
	KittyProtocolActive() bool
	MoveBy(lines int)
	HideCursor()
	ShowCursor()
	ClearLine()
	ClearFromCursor()
	ClearScreen()
	SetTitle(title string)
	SetProgress(active bool)
	SetProgramStatus(status ProgramStatus)
}

// TerminalRestoreOnExit is pi-tui's optional Terminal.restoreOnExit.
type TerminalRestoreOnExit interface {
	RestoreOnExit(before, after string)
}

// Component is pi-tui's Component: Render returns the lines for width. The components of a [TuiAltScreen] are compared by identity (Pi's
// reference identity), so a component MUST be a non-nil pointer; the screen's methods return an error for any other value.
type Component interface {
	Render(width int) []string
	Invalidate()
}

// TuiInputHandler is a component's optional handleInput.
type TuiInputHandler interface{ HandleInput(data string) }

// TuiFocusable is a component's optional `focused` flag: the screen sets it when the component gains or loses focus.
type TuiFocusable interface{ SetFocused(focused bool) }

// TuiKeyReleaseWanter is a component's optional wantsKeyRelease.
type TuiKeyReleaseWanter interface{ WantsKeyRelease() bool }

// TuiMouseResult is pi-tui's TuiMouseEventResult. A nil field is absent.
type TuiMouseResult struct {
	Handled *bool `json:"handled,omitempty"`
	Capture *bool `json:"capture,omitempty"`
	Focus   *bool `json:"focus,omitempty"`
	Render  *bool `json:"render,omitempty"`
}

// TuiMouseHandler is a component's optional handleMouse; a nil result is Pi's undefined.
type TuiMouseHandler interface {
	HandleMouse(event MouseEvent) *TuiMouseResult
}

// TuiInputResult is a TuiInputListener's result: Consume stops the input, a non-nil Data replaces it.
type TuiInputResult struct {
	Consume bool
	Data    *string
}

// TuiInputListener is pi-tui's TuiInputListener; a nil result passes the input on unchanged.
type TuiInputListener func(data string) *TuiInputResult

// TuiRgb is pi-tui's RgbColor.
type TuiRgb struct {
	R int `json:"r"`
	G int `json:"g"`
	B int `json:"b"`
}

// TerminalColors is pi-tui's TerminalColors: what the terminal reported for its theme. The palette is set only when all 16 colors arrived.
type TerminalColors struct {
	Foreground *TuiRgb  `json:"foreground,omitempty"`
	Background *TuiRgb  `json:"background,omitempty"`
	Palette    []TuiRgb `json:"palette,omitempty"`
}

// TuiAltScreenOptions is pi-tui's TuiAltScreenOptions. A nil Mouse or CopyOnSelect is Pi's default (true). WheelScrollLines is a number of
// lines, or "auto" in WheelScrollAuto; its zero value is Pi's default, 1. The callback fields stay in this process; the host calls them.
type TuiAltScreenOptions struct {
	WheelScrollLines float64
	WheelScrollAuto  bool
	Mouse            *bool
	CopyOnSelect     *bool
	// CopySelection copies text to the clipboard: a nil error is Pi's true, an error with a message is that message, and an error with an empty
	// message is false.
	CopySelection               func(text string) error
	OpenURL                     func(url string)
	SearchMatchStyle            func(text string) string
	SearchCurrentMatchStyle     func(text string) string
	SearchNavigationButtonStyle func(text string, hovered bool) string
	ScrollToEndIndicator        func() string
	OnRightClickPaste           func()
}

// TuiOverlayHandle is pi-tui's OverlayHandle of an overlay a [TuiAltScreen] shows.
type TuiOverlayHandle struct {
	screen *TuiAltScreen
	key    string
}

// TuiAltScreen is pi-tui's TuiAltScreen.
type TuiAltScreen struct {
	ext      *Extension
	id       string
	terminal Terminal
	options  TuiAltScreenOptions

	mu            sync.Mutex
	children      []Component
	keys          map[Component]string
	byKey         map[string]Component
	nextKey       int
	listeners     map[string]TuiInputListener
	nextListener  int
	onDebug       func()
	schemeHandler map[int]func(scheme string)
	nextScheme    int
	lateReply     map[string]func(TerminalColors)
	visible       map[string]func(termWidth, termHeight int) bool
	nextQuery     int
}

// NewTuiAltScreen is `new TuiAltScreen(terminal, showHardwareCursor, logDirectory, options)`; a nil showHardwareCursor or empty logDirectory is
// Pi's undefined.
func NewTuiAltScreen(ctx Context, terminal Terminal, showHardwareCursor *bool, logDirectory string, options TuiAltScreenOptions) (*TuiAltScreen, error) {
	if ctx.ext == nil {
		return nil, errors.New("NewTuiAltScreen needs a context of a running extension")
	}
	if terminal == nil {
		return nil, errors.New("NewTuiAltScreen needs a terminal")
	}
	ext := ctx.ext
	ext.tuiAlt.mu.Lock()
	ext.tuiAlt.next++
	id := "t" + strconv.Itoa(ext.tuiAlt.next)
	ext.tuiAlt.mu.Unlock()
	s := &TuiAltScreen{
		ext: ext, id: id, terminal: terminal, options: options,
		keys: map[Component]string{}, byKey: map[string]Component{},
		listeners: map[string]TuiInputListener{}, schemeHandler: map[int]func(string){},
	}
	opts := map[string]any{}
	if options.WheelScrollAuto {
		opts["wheelScrollLines"] = "auto"
	} else if options.WheelScrollLines != 0 {
		opts["wheelScrollLines"] = options.WheelScrollLines
	}
	if options.Mouse != nil {
		opts["mouse"] = *options.Mouse
	}
	if options.CopyOnSelect != nil {
		opts["copyOnSelect"] = *options.CopyOnSelect
	}
	var callbacks []string
	add := func(present bool, name string) {
		if present {
			callbacks = append(callbacks, name)
		}
	}
	add(options.CopySelection != nil, "copySelection")
	add(options.OpenURL != nil, "openUrl")
	add(options.SearchMatchStyle != nil, "searchMatchStyle")
	add(options.SearchCurrentMatchStyle != nil, "searchCurrentMatchStyle")
	add(options.SearchNavigationButtonStyle != nil, "searchNavigationButtonStyle")
	add(options.ScrollToEndIndicator != nil, "scrollToEndIndicator")
	add(options.OnRightClickPaste != nil, "onRightClickPaste")
	if len(callbacks) > 0 {
		opts["callbacks"] = callbacks
	}
	fields := map[string]any{"options": opts}
	if showHardwareCursor != nil {
		fields["showHardwareCursor"] = *showHardwareCursor
	}
	if logDirectory != "" {
		fields["logDirectory"] = logDirectory
	}
	// The screen is registered first: the host may call back while it builds the screen.
	ext.tuiAlt.mu.Lock()
	if ext.tuiAlt.screens == nil {
		ext.tuiAlt.screens = map[string]*TuiAltScreen{}
	}
	ext.tuiAlt.screens[id] = s
	ext.tuiAlt.mu.Unlock()
	if err := s.call("create", fields, nil); err != nil {
		ext.tuiAlt.mu.Lock()
		delete(ext.tuiAlt.screens, id)
		ext.tuiAlt.mu.Unlock()
		return nil, err
	}
	return s, nil
}

type tuiAltRegistry struct {
	mu      sync.Mutex
	next    int
	screens map[string]*TuiAltScreen
	// callbacks holds, per goroutine, the request of the host callback it is serving: a call it makes is that request's child, in its own
	// ordering lane, because a call in the lane of the call the callback answers would wait for it.
	callbacks map[uint64]Context
}

// serveCallback records ctx as the request the calling goroutine serves until the returned function runs.
func (r *tuiAltRegistry) serveCallback(ctx Context) func() {
	id := goroutineID()
	r.mu.Lock()
	if r.callbacks == nil {
		r.callbacks = map[uint64]Context{}
	}
	previous, nested := r.callbacks[id]
	r.callbacks[id] = ctx
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		if nested {
			r.callbacks[id] = previous
		} else {
			delete(r.callbacks, id)
		}
		r.mu.Unlock()
	}
}

// callContext is the context a call of the calling goroutine goes out on: the callback request it serves, else the extension's.
func (r *tuiAltRegistry) callContext(ext *Extension) Context {
	id := goroutineID()
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx, ok := r.callbacks[id]; ok {
		return ctx
	}
	return Context{ext: ext}
}

// goroutineID is the calling goroutine's number, read from its stack header ("goroutine 12 [running]:").
func goroutineID() uint64 {
	var buf [64]byte
	header := buf[:runtime.Stack(buf[:], false)]
	header = bytes.TrimPrefix(header, []byte("goroutine "))
	id, _ := strconv.ParseUint(string(header[:bytes.IndexByte(header, ' ')]), 10, 64)
	return id
}

func (s *TuiAltScreen) call(op string, fields map[string]any, out any) error {
	args := map[string]any{"id": s.id, "op": op}
	for k, v := range fields {
		args[k] = v
	}
	args["terminal"] = map[string]any{"columns": s.terminal.Columns(), "rows": s.terminal.Rows(), "kitty": s.terminal.KittyProtocolActive()}
	result, err := s.ext.tuiAlt.callContext(s.ext).callHost("tui.alt", args)
	if err := callResultError(result, err); err != nil {
		return err
	}
	if out != nil && result != nil && len(result.Result) > 0 {
		return json.Unmarshal(result.Result, out)
	}
	return nil
}

// componentFields names component for the host: its key and, with it, the members it has. Pi compares components by reference, so a component
// must be a non-nil pointer; anything else is an error and no host call is made.
func (s *TuiAltScreen) componentFields(component Component) (map[string]any, error) {
	if component == nil {
		return nil, errors.New("tui: component is nil")
	}
	value := reflect.ValueOf(component)
	if value.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("tui: component %T must be a pointer (Pi compares components by reference)", component)
	}
	if value.IsNil() {
		return nil, errors.New("tui: component is nil")
	}
	s.mu.Lock()
	key, ok := s.keys[component]
	if !ok {
		s.nextKey++
		key = "c" + strconv.Itoa(s.nextKey)
		s.keys[component] = key
		s.byKey[key] = component
	}
	s.mu.Unlock()
	caps := map[string]any{"input": false, "mouse": false, "focusable": false, "wantsKeyRelease": false}
	if _, ok := component.(TuiInputHandler); ok {
		caps["input"] = true
	}
	if _, ok := component.(TuiMouseHandler); ok {
		caps["mouse"] = true
	}
	if _, ok := component.(TuiFocusable); ok {
		caps["focusable"] = true
	}
	if w, ok := component.(TuiKeyReleaseWanter); ok {
		caps["wantsKeyRelease"] = w.WantsKeyRelease()
	}
	return map[string]any{"key": key, "caps": caps}, nil
}

func (s *TuiAltScreen) componentFor(key any) Component {
	name, ok := key.(string)
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey[name]
}

type tuiAltState struct {
	ViewportTop        int     `json:"viewportTop"`
	IsFollowingOutput  bool    `json:"isFollowingOutput"`
	FullRedraws        int     `json:"fullRedraws"`
	HasOverlay         bool    `json:"hasOverlay"`
	HasOverlayEntries  bool    `json:"hasOverlayEntries"`
	HasActiveSelection bool    `json:"hasActiveSelection"`
	CopyOnSelect       bool    `json:"copyOnSelect"`
	ShowHardwareCursor bool    `json:"showHardwareCursor"`
	ClearOnShrink      bool    `json:"clearOnShrink"`
	FocusedKey         *string `json:"focusedKey"`
}

func (s *TuiAltScreen) state() (tuiAltState, error) {
	var st tuiAltState
	err := s.call("state", nil, &st)
	return st, err
}

// Mode is pi-tui's mode: always "fullscreen".
func (s *TuiAltScreen) Mode() string { return "fullscreen" }

// ViewportTui is pi-tui's [Symbol.VIEWPORT_TUI] marker: always true.
func (s *TuiAltScreen) ViewportTui() bool { return true }

// Terminal is pi-tui's terminal: the terminal the screen was built with.
func (s *TuiAltScreen) Terminal() Terminal { return s.terminal }

// Children is pi-tui's children: a copy of the screen's children.
//
// pig divergence (D207): Pi's `children` is a live array; a live array cannot cross the process boundary, so this returns a snapshot. Changing
// the returned list changes nothing and it does not follow later changes: the children are changed only with [TuiAltScreen.AddChild],
// [TuiAltScreen.RemoveChild] and [TuiAltScreen.Clear] (there is no setChildren).
func (s *TuiAltScreen) Children() []Component {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Component(nil), s.children...)
}

// AddChild is Container.addChild.
func (s *TuiAltScreen) AddChild(component Component) error {
	fields, err := s.componentFields(component)
	if err != nil {
		return err
	}
	if err := s.call("addChild", fields, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.children = append(s.children, component)
	s.mu.Unlock()
	return nil
}

// RemoveChild is Container.removeChild; a component that is not a child is ignored. A component that is not a pointer is an error.
func (s *TuiAltScreen) RemoveChild(component Component) error {
	fields, err := s.componentFields(component)
	if err != nil {
		return err
	}
	if err := s.call("removeChild", fields, nil); err != nil {
		return err
	}
	s.mu.Lock()
	for i, child := range s.children {
		if child == component {
			s.children = append(s.children[:i], s.children[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return nil
}

// Clear is Container.clear.
func (s *TuiAltScreen) Clear() error {
	if err := s.call("clear", nil, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.children = nil
	s.mu.Unlock()
	return nil
}

// Invalidate is Container.invalidate.
func (s *TuiAltScreen) Invalidate() error { return s.call("invalidate", nil, nil) }

// Render is Container.render: the lines of the layout root, or of the children, at width.
func (s *TuiAltScreen) Render(width int) ([]string, error) {
	var out struct {
		Lines []string `json:"lines"`
	}
	err := s.call("render", map[string]any{"width": width}, &out)
	return out.Lines, err
}

// HandleMouse is Container.handleMouse; a nil result is Pi's undefined.
func (s *TuiAltScreen) HandleMouse(event MouseEvent) (*TuiMouseResult, error) {
	var out struct {
		Result *TuiMouseResult `json:"result"`
	}
	err := s.call("handleMouse", map[string]any{"event": event}, &out)
	return out.Result, err
}

// SetOnDebug is the assignment of pi-tui's onDebug: the callback the host runs on Shift+Ctrl+D; nil clears it.
func (s *TuiAltScreen) SetOnDebug(callback func()) error {
	s.mu.Lock()
	s.onDebug = callback
	s.mu.Unlock()
	return s.call("setOnDebug", map[string]any{"enabled": callback != nil}, nil)
}

// FullRedraws is pi-tui's fullRedraws.
func (s *TuiAltScreen) FullRedraws() (int, error) {
	st, err := s.state()
	return st.FullRedraws, err
}

// ViewportTop is pi-tui's viewportTop.
func (s *TuiAltScreen) ViewportTop() (int, error) {
	st, err := s.state()
	return st.ViewportTop, err
}

// IsFollowingOutput is pi-tui's isFollowingOutput.
func (s *TuiAltScreen) IsFollowingOutput() (bool, error) {
	st, err := s.state()
	return st.IsFollowingOutput, err
}

// HasOverlay is pi-tui's hasOverlay: whether an overlay is visible.
func (s *TuiAltScreen) HasOverlay() (bool, error) {
	st, err := s.state()
	return st.HasOverlay, err
}

// HasOverlayEntries is pi-tui's hasOverlayEntries: whether an overlay is mounted.
func (s *TuiAltScreen) HasOverlayEntries() (bool, error) {
	st, err := s.state()
	return st.HasOverlayEntries, err
}

// HasActiveSelection is pi-tui's hasActiveSelection.
func (s *TuiAltScreen) HasActiveSelection() (bool, error) {
	st, err := s.state()
	return st.HasActiveSelection, err
}

// GetCopyOnSelect is pi-tui's getCopyOnSelect.
func (s *TuiAltScreen) GetCopyOnSelect() (bool, error) {
	st, err := s.state()
	return st.CopyOnSelect, err
}

// GetShowHardwareCursor is pi-tui's getShowHardwareCursor.
func (s *TuiAltScreen) GetShowHardwareCursor() (bool, error) {
	st, err := s.state()
	return st.ShowHardwareCursor, err
}

// GetClearOnShrink is pi-tui's getClearOnShrink.
func (s *TuiAltScreen) GetClearOnShrink() (bool, error) {
	st, err := s.state()
	return st.ClearOnShrink, err
}

// GetFocusedComponent is pi-tui's getFocusedComponent: the component passed to SetFocus (or an overlay component), nil for none.
func (s *TuiAltScreen) GetFocusedComponent() (Component, error) {
	st, err := s.state()
	if err != nil || st.FocusedKey == nil {
		return nil, err
	}
	return s.componentFor(*st.FocusedKey), nil
}

// GetScreenLines is pi-tui's getScreenLines: the lines of the last rendered frame.
func (s *TuiAltScreen) GetScreenLines() ([]string, error) {
	var out struct {
		Lines []string `json:"lines"`
	}
	err := s.call("getScreenLines", nil, &out)
	return out.Lines, err
}

// SetShowHardwareCursor is pi-tui's setShowHardwareCursor.
func (s *TuiAltScreen) SetShowHardwareCursor(enabled bool) error {
	return s.call("setShowHardwareCursor", map[string]any{"enabled": enabled}, nil)
}

// SetClearOnShrink is pi-tui's setClearOnShrink.
func (s *TuiAltScreen) SetClearOnShrink(enabled bool) error {
	return s.call("setClearOnShrink", map[string]any{"enabled": enabled}, nil)
}

// SetCopyOnSelect is pi-tui's setCopyOnSelect.
func (s *TuiAltScreen) SetCopyOnSelect(enabled bool) error {
	return s.call("setCopyOnSelect", map[string]any{"enabled": enabled}, nil)
}

// SetWheelScrollLines is pi-tui's setWheelScrollLines: a number of lines, or "auto" with auto true.
func (s *TuiAltScreen) SetWheelScrollLines(lines float64, auto bool) error {
	var value any = lines
	if auto {
		value = "auto"
	}
	return s.call("setWheelScrollLines", map[string]any{"lines": value}, nil)
}

// SetFocus is pi-tui's setFocus; nil focuses nothing.
func (s *TuiAltScreen) SetFocus(component Component) error {
	if component == nil {
		return s.call("setFocus", map[string]any{"key": nil}, nil)
	}
	fields, err := s.componentFields(component)
	if err != nil {
		return err
	}
	return s.call("setFocus", fields, nil)
}

// SetLayoutRoot is pi-tui's setLayoutRoot; nil is Pi's undefined.
func (s *TuiAltScreen) SetLayoutRoot(component Component) error {
	if component == nil {
		return s.call("setLayoutRoot", map[string]any{"key": nil}, nil)
	}
	fields, err := s.componentFields(component)
	if err != nil {
		return err
	}
	return s.call("setLayoutRoot", fields, nil)
}

// ScrollBy is pi-tui's scrollBy.
func (s *TuiAltScreen) ScrollBy(lines int) error {
	return s.call("scrollBy", map[string]any{"lines": lines}, nil)
}

// ScrollToTop is pi-tui's scrollToTop.
func (s *TuiAltScreen) ScrollToTop() error { return s.call("scrollToTop", nil, nil) }

// ScrollToBottom is pi-tui's scrollToBottom.
func (s *TuiAltScreen) ScrollToBottom() error { return s.call("scrollToBottom", nil, nil) }

// Flash is pi-tui's flash: a message over the viewport for durationMs (zero is Pi's default).
func (s *TuiAltScreen) Flash(message string, durationMs int) error {
	fields := map[string]any{"message": message}
	if durationMs != 0 {
		fields["durationMs"] = durationMs
	}
	return s.call("flash", fields, nil)
}

// ResetTextSelection is pi-tui's resetTextSelection.
func (s *TuiAltScreen) ResetTextSelection() error { return s.call("resetTextSelection", nil, nil) }

// CopyActiveSelectionToClipboard is pi-tui's copyActiveSelectionToClipboard (a Promise in Pi): it returns once the copy finished.
func (s *TuiAltScreen) CopyActiveSelectionToClipboard() (bool, error) {
	var out struct {
		Value bool `json:"value"`
	}
	err := s.call("copyActiveSelectionToClipboard", nil, &out)
	return out.Value, err
}

// Start is pi-tui's start: it starts the terminal, enters the alternate screen and requests the first frame.
func (s *TuiAltScreen) Start() error { return s.call("start", nil, nil) }

// Stop is pi-tui's stop; preserveScreen is TuiStopOptions.preserveScreen.
func (s *TuiAltScreen) Stop(preserveScreen bool) error {
	return s.call("stop", map[string]any{"preserveScreen": preserveScreen}, nil)
}

// RenderNow is pi-tui's renderNow.
func (s *TuiAltScreen) RenderNow(force bool) error {
	return s.call("renderNow", map[string]any{"force": force}, nil)
}

// RequestRender is pi-tui's requestRender.
func (s *TuiAltScreen) RequestRender(force bool) error {
	return s.call("requestRender", map[string]any{"force": force}, nil)
}

// TuiOverlayOptions is pi-tui's OverlayOptions: the serialisable fields of [OverlayOptions] and Pi's `visible(termWidth, termHeight)` callback,
// which the host calls whenever it decides whether the overlay shows (a nil Visible is always visible).
type TuiOverlayOptions struct {
	OverlayOptions
	Visible func(termWidth, termHeight int) bool `json:"-"`
}

// ShowOverlay is pi-tui's showOverlay; nil options are Pi's defaults.
func (s *TuiAltScreen) ShowOverlay(component Component, options *TuiOverlayOptions) (*TuiOverlayHandle, error) {
	fields, err := s.componentFields(component)
	if err != nil {
		return nil, err
	}
	if options != nil {
		fields["options"] = options.OverlayOptions
		if options.Visible != nil {
			fields["visible"] = true
			s.mu.Lock()
			if s.visible == nil {
				s.visible = map[string]func(int, int) bool{}
			}
			s.visible[fields["key"].(string)] = options.Visible
			s.mu.Unlock()
		}
	}
	if err := s.call("showOverlay", fields, nil); err != nil {
		return nil, err
	}
	return &TuiOverlayHandle{screen: s, key: fields["key"].(string)}, nil
}

// HideOverlay is pi-tui's hideOverlay: it removes the topmost overlay.
func (s *TuiAltScreen) HideOverlay() error { return s.call("hideOverlay", nil, nil) }

// AddInputListener is pi-tui's addInputListener: it returns the function that removes the listener.
func (s *TuiAltScreen) AddInputListener(listener TuiInputListener) (func(), error) {
	s.mu.Lock()
	s.nextListener++
	id := strconv.Itoa(s.nextListener)
	s.listeners[id] = listener
	s.mu.Unlock()
	if err := s.call("addInputListener", map[string]any{"listenerId": id}, nil); err != nil {
		return nil, err
	}
	return func() { _ = s.removeListener(id) }, nil
}

func (s *TuiAltScreen) removeListener(id string) error {
	s.mu.Lock()
	_, ok := s.listeners[id]
	delete(s.listeners, id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return s.call("removeInputListener", map[string]any{"listenerId": id}, nil)
}

// RemoveInputListener is pi-tui's removeInputListener; a listener that is not registered is ignored.
func (s *TuiAltScreen) RemoveInputListener(listener TuiInputListener) error {
	target := reflect.ValueOf(listener).Pointer()
	s.mu.Lock()
	var id string
	for candidate, registered := range s.listeners {
		if reflect.ValueOf(registered).Pointer() == target {
			id = candidate
			break
		}
	}
	s.mu.Unlock()
	if id == "" {
		return nil
	}
	return s.removeListener(id)
}

// OnTerminalColorSchemeChange is pi-tui's onTerminalColorSchemeChange: the listener receives "dark" or "light"; the result removes it.
func (s *TuiAltScreen) OnTerminalColorSchemeChange(listener func(scheme string)) func() {
	s.mu.Lock()
	s.nextScheme++
	id := s.nextScheme
	s.schemeHandler[id] = listener
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.schemeHandler, id)
		s.mu.Unlock()
	}
}

// SetTerminalColorSchemeNotifications is pi-tui's setTerminalColorSchemeNotifications.
func (s *TuiAltScreen) SetTerminalColorSchemeNotifications(enabled bool) error {
	return s.call("setTerminalColorSchemeNotifications", map[string]any{"enabled": enabled}, nil)
}

// QueryTerminalColors is pi-tui's queryTerminalColors (a Promise in Pi): it returns when the terminal answered or timeoutMs passed. A reply
// after the timeout goes to onLateReply, which may be nil.
func (s *TuiAltScreen) QueryTerminalColors(timeoutMs float64, onLateReply func(TerminalColors)) (TerminalColors, error) {
	fields := map[string]any{"timeoutMs": timeoutMs, "onLateReply": onLateReply != nil}
	if onLateReply != nil {
		s.mu.Lock()
		s.nextQuery++
		id := "q" + strconv.Itoa(s.nextQuery)
		if s.lateReply == nil {
			s.lateReply = map[string]func(TerminalColors){}
		}
		s.lateReply[id] = onLateReply
		s.mu.Unlock()
		fields["queryId"] = id
	}
	var out struct {
		Colors TerminalColors `json:"colors"`
	}
	err := s.call("queryTerminalColors", fields, &out)
	return out.Colors, err
}

func (h *TuiOverlayHandle) control(action string, fields map[string]any) (*tuiOverlayState, error) {
	args := map[string]any{"key": h.key, "action": action}
	for k, v := range fields {
		args[k] = v
	}
	var state tuiOverlayState
	err := h.screen.call("overlay", args, &state)
	return &state, err
}

type tuiOverlayState struct {
	Hidden  bool           `json:"hidden"`
	Focused bool           `json:"focused"`
	Visible bool           `json:"visible"`
	Bounds  *OverlayBounds `json:"bounds,omitempty"`
}

// Hide is OverlayHandle.hide: it removes the overlay for good.
func (h *TuiOverlayHandle) Hide() error { _, err := h.control("hide", nil); return err }

// SetHidden is OverlayHandle.setHidden.
func (h *TuiOverlayHandle) SetHidden(hidden bool) error {
	_, err := h.control("setHidden", map[string]any{"hidden": hidden})
	return err
}

// IsHidden is OverlayHandle.isHidden.
func (h *TuiOverlayHandle) IsHidden() (bool, error) {
	state, err := h.control("state", nil)
	return state.Hidden, err
}

// Focus is OverlayHandle.focus.
func (h *TuiOverlayHandle) Focus() error { _, err := h.control("focus", nil); return err }

// Unfocus is OverlayHandle.unfocus: with no options focus goes where the screen puts it; with options it goes to options.Target, which is nil
// for nothing or a component of the screen.
func (h *TuiOverlayHandle) Unfocus(options ...UnfocusOptions) error {
	fields := map[string]any{}
	if len(options) > 0 {
		target := options[0].Target
		if target == nil {
			fields["target"] = nil
		} else {
			component, ok := target.(Component)
			if !ok {
				return fmt.Errorf("tui: unfocus target %T is not a component", target)
			}
			named, err := h.screen.componentFields(component)
			if err != nil {
				return err
			}
			fields["target"] = map[string]any{"key": named["key"]}
		}
	}
	_, err := h.control("unfocus", fields)
	return err
}

// IsFocused is OverlayHandle.isFocused.
func (h *TuiOverlayHandle) IsFocused() (bool, error) {
	state, err := h.control("state", nil)
	return state.Focused, err
}

// GetBounds is OverlayHandle.getBounds: the last rendered rectangle of a visible overlay, nil otherwise.
func (h *TuiOverlayHandle) GetBounds() (*OverlayBounds, error) {
	state, err := h.control("state", nil)
	return state.Bounds, err
}

// dispatchTuiAlt answers a host callback (`tui.alt.cb`) of a screen.
func (e *Extension) dispatchTuiAlt(ctx Context, raw json.RawMessage) (any, error) {
	defer e.tuiAlt.serveCallback(ctx)()
	var request struct {
		ID         string          `json:"id"`
		Kind       string          `json:"kind"`
		Method     string          `json:"method"`
		Key        string          `json:"key"`
		Width      int             `json:"width"`
		TermWidth  int             `json:"termWidth"`
		TermHeight int             `json:"termHeight"`
		Data       string          `json:"data"`
		Focused    bool            `json:"focused"`
		Event      MouseEvent      `json:"event"`
		ListenerID string          `json:"listenerId"`
		Scheme     string          `json:"scheme"`
		Colors     TerminalColors  `json:"colors"`
		QueryID    string          `json:"queryId"`
		Name       string          `json:"name"`
		Args       json.RawMessage `json:"args"`
		Lines      int             `json:"lines"`
		Title      string          `json:"title"`
		Active     bool            `json:"active"`
		Status     ProgramStatus   `json:"status"`
		Before     string          `json:"before"`
		After      string          `json:"after"`
		MaxMs      int             `json:"maxMs"`
		IdleMs     int             `json:"idleMs"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	e.tuiAlt.mu.Lock()
	s := e.tuiAlt.screens[request.ID]
	e.tuiAlt.mu.Unlock()
	if s == nil {
		return nil, fmt.Errorf("tui.alt: unknown screen %q", request.ID)
	}
	empty := map[string]any{}
	component := func() (Component, error) {
		c := s.componentFor(request.Key)
		if c == nil {
			return nil, fmt.Errorf("tui.alt: unknown component %q", request.Key)
		}
		return c, nil
	}
	switch request.Kind {
	case "terminal":
		t := s.terminal
		switch request.Method {
		case "start":
			t.Start(func(data string) { _ = s.call("input", map[string]any{"data": data}, nil) }, func() { _ = s.call("resize", nil, nil) })
		case "stop":
			t.Stop()
		case "write":
			t.Write(request.Data)
		case "moveBy":
			t.MoveBy(request.Lines)
		case "hideCursor":
			t.HideCursor()
		case "showCursor":
			t.ShowCursor()
		case "clearLine":
			t.ClearLine()
		case "clearFromCursor":
			t.ClearFromCursor()
		case "clearScreen":
			t.ClearScreen()
		case "setTitle":
			t.SetTitle(request.Title)
		case "setProgress":
			t.SetProgress(request.Active)
		case "setProgramStatus":
			t.SetProgramStatus(request.Status)
		case "restoreOnExit":
			if r, ok := t.(TerminalRestoreOnExit); ok {
				r.RestoreOnExit(request.Before, request.After)
			}
		case "drainInput":
			t.DrainInput(request.MaxMs, request.IdleMs)
		default:
			return nil, fmt.Errorf("tui.alt: unknown terminal method %q", request.Method)
		}
		return empty, nil
	case "render":
		c, err := component()
		if err != nil {
			return nil, err
		}
		lines := c.Render(request.Width)
		if lines == nil {
			lines = []string{}
		}
		return map[string]any{"lines": lines}, nil
	case "input":
		c, err := component()
		if err != nil {
			return nil, err
		}
		if h, ok := c.(TuiInputHandler); ok {
			h.HandleInput(request.Data)
		}
		return empty, nil
	case "mouse":
		c, err := component()
		if err != nil {
			return nil, err
		}
		var result *TuiMouseResult
		if h, ok := c.(TuiMouseHandler); ok {
			result = h.HandleMouse(request.Event)
		}
		return map[string]any{"result": result}, nil
	case "focus":
		c, err := component()
		if err != nil {
			return nil, err
		}
		if f, ok := c.(TuiFocusable); ok {
			f.SetFocused(request.Focused)
		}
		return empty, nil
	case "invalidate":
		c, err := component()
		if err != nil {
			return nil, err
		}
		c.Invalidate()
		return empty, nil
	case "listener":
		s.mu.Lock()
		listener := s.listeners[request.ListenerID]
		s.mu.Unlock()
		out := map[string]any{}
		if listener != nil {
			if result := listener(request.Data); result != nil {
				if result.Consume {
					out["consume"] = true
				}
				if result.Data != nil {
					out["data"] = *result.Data
				}
			}
		}
		return out, nil
	case "debug":
		s.mu.Lock()
		callback := s.onDebug
		s.mu.Unlock()
		if callback != nil {
			callback()
		}
		return empty, nil
	case "colorScheme":
		s.mu.Lock()
		listeners := make([]func(string), 0, len(s.schemeHandler))
		for i := 1; i <= s.nextScheme; i++ {
			if l, ok := s.schemeHandler[i]; ok {
				listeners = append(listeners, l)
			}
		}
		s.mu.Unlock()
		for _, l := range listeners {
			l(request.Scheme)
		}
		return empty, nil
	case "lateColors":
		s.mu.Lock()
		callback := s.lateReply[request.QueryID]
		delete(s.lateReply, request.QueryID)
		s.mu.Unlock()
		if callback != nil {
			callback(request.Colors)
		}
		return empty, nil
	case "visible":
		s.mu.Lock()
		visible := s.visible[request.Key]
		s.mu.Unlock()
		if visible == nil {
			return map[string]any{"value": true}, nil
		}
		return map[string]any{"value": visible(request.TermWidth, request.TermHeight)}, nil
	case "option":
		return s.runOption(request.Name, request.Args)
	}
	return nil, fmt.Errorf("tui.alt: unknown callback %q", request.Kind)
}

func (s *TuiAltScreen) runOption(name string, raw json.RawMessage) (any, error) {
	// The arguments are positional: [text], [url] or [text, hovered].
	var positional []json.RawMessage
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &positional); err != nil {
			return nil, err
		}
	}
	var args struct {
		Text    string
		URL     string
		Hovered bool
	}
	if len(positional) > 0 {
		var first string
		if err := json.Unmarshal(positional[0], &first); err != nil {
			return nil, err
		}
		args.Text, args.URL = first, first
	}
	if len(positional) > 1 {
		if err := json.Unmarshal(positional[1], &args.Hovered); err != nil {
			return nil, err
		}
	}
	o := s.options
	switch name {
	case "copySelection":
		if o.CopySelection == nil {
			return nil, errors.New("tui.alt: no copySelection callback")
		}
		if err := o.CopySelection(args.Text); err != nil {
			if err.Error() == "" {
				return map[string]any{"value": false}, nil
			}
			return map[string]any{"value": err.Error()}, nil
		}
		return map[string]any{"value": true}, nil
	case "openUrl":
		if o.OpenURL != nil {
			o.OpenURL(args.URL)
		}
		return map[string]any{}, nil
	case "searchMatchStyle":
		if o.SearchMatchStyle != nil {
			return map[string]any{"value": o.SearchMatchStyle(args.Text)}, nil
		}
	case "searchCurrentMatchStyle":
		if o.SearchCurrentMatchStyle != nil {
			return map[string]any{"value": o.SearchCurrentMatchStyle(args.Text)}, nil
		}
	case "searchNavigationButtonStyle":
		if o.SearchNavigationButtonStyle != nil {
			return map[string]any{"value": o.SearchNavigationButtonStyle(args.Text, args.Hovered)}, nil
		}
	case "scrollToEndIndicator":
		if o.ScrollToEndIndicator != nil {
			return map[string]any{"value": o.ScrollToEndIndicator()}, nil
		}
	case "onRightClickPaste":
		if o.OnRightClickPaste != nil {
			o.OnRightClickPaste()
		}
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("tui.alt: no %s callback", name)
}
