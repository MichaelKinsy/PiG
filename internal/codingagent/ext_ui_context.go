// ext_ui_context.go implements extension.UIContext for interactive mode.
//
// Bridges the coding/extension UIContext interface to the real TUI so
// subprocess extensions can show selectors, input dialogs, and
// confirmations via the editor slot (matching upstream's
// showExtensionSelector / showExtensionInput / showExtensionConfirm).
//
// pig-specific: upstream's interactive-mode.ts creates these inline;
// pig needs a separate type because the UIContext interface lives in
// coding/extension/ and the TUI lives in internal/.

package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ExtUIContext implements extension.UIContext for interactive mode.
// Subprocess extensions' Select/Confirm/Input/Editor calls are
// dispatched here via the UIBridge.
type ExtUIContext struct {
	m *InteractiveMode
}

type specialLinesComponent struct {
	mu         sync.RWMutex
	lines      []string
	linesWidth int
	render     func(width int) []string
	invalidate func()
}

func newSpecialLinesComponent(invalidate func()) *specialLinesComponent {
	return &specialLinesComponent{invalidate: invalidate}
}

// Render returns the header/footer rows for width. A built-in or login
// renderer renders at width, like upstream's component factories. Lines pushed
// by a subprocess extension carry the width they were rendered at; a frame for
// another width is never painted, whatever its rows (the extension
// re-renders on the host's width_change notification). Rows are otherwise
// painted as rendered: an over-wide row reaches the renderer's overflow check,
// as a component that does not truncate does in Pi.
func (c *specialLinesComponent) Render(width int) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.render != nil {
		return c.render(width)
	}
	if len(c.lines) == 0 {
		return nil
	}
	out := make([]string, len(c.lines))
	copy(out, c.lines)
	return widthx.FrameAt(out, c.linesWidth, width)
}

func (c *specialLinesComponent) Invalidate() {
	if c.invalidate != nil {
		c.invalidate()
	}
}

func (c *specialLinesComponent) SetLines(lines []string) { c.SetLinesAt(lines, 0) }

// SetLinesAt installs lines rendered at width (0 when unknown).
func (c *specialLinesComponent) SetLinesAt(lines []string, width int) {
	c.mu.Lock()
	c.render = nil
	c.linesWidth = width
	if len(lines) == 0 {
		c.lines = nil
	} else {
		c.lines = append([]string(nil), lines...)
	}
	c.mu.Unlock()
	if c.invalidate != nil {
		c.invalidate()
	}
}

// HasContent reports whether the component has lines or a renderer set.
func (c *specialLinesComponent) HasContent() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.render != nil || len(c.lines) > 0
}

func (c *specialLinesComponent) SetRenderer(render func(width int) []string) {
	c.mu.Lock()
	c.lines = nil
	c.render = render
	c.mu.Unlock()
	if c.invalidate != nil {
		c.invalidate()
	}
}

var _ extension.UIContext = (*ExtUIContext)(nil)

type extensionDialogResult struct {
	value     string
	cancelled bool
	err       error
}

// ReportsDialogInitiation reports that Select, Confirm, Input, and Editor mark
// their call initiated once the dialog is queued for installation.
func (u *ExtUIContext) ReportsDialogInitiation() bool { return true }

func (u *ExtUIContext) runDialog(
	ctx context.Context,
	component tui.Component,
	handle func(string),
	result func() (value string, done, cancelled bool),
) (string, error) {
	if u.m.layout == nil || u.m.tuiInst == nil {
		return "", fmt.Errorf("no TUI available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if u.m.runCtx != nil {
		merged, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(u.m.runCtx, cancel)
		defer func() {
			stop()
			cancel()
		}()
		ctx = merged
	}

	resultCh := make(chan extensionDialogResult, 1)
	u.m.runOnMain(ctx, func() {
		if err := ctx.Err(); err != nil {
			resultCh <- extensionDialogResult{cancelled: true}
			return
		}
		if u.m.extensionDialog != nil {
			resultCh <- extensionDialogResult{err: errors.New("another extension dialog is already open")}
			return
		}

		u.m.setExtensionDialogViewMode(true, u.m.extensionDialogLines(component))
		u.m.editorContainer.SetChildren(component)
		u.m.extensionDialog = &extensionDialog{component: component}
		u.m.extensionDialog.handle = func(data string) {
			handle(data)
			value, done, cancelled := result()
			if !done {
				u.m.tuiInst.Render()
				return
			}
			u.m.extensionDialog = nil
			u.m.editorContainer.SetChildren(u.m.editor)
			u.m.setExtensionDialogViewMode(false, 0)
			u.m.tuiInst.RequestRender()
			resultCh <- extensionDialogResult{value: value, cancelled: cancelled}
		}
		u.m.tuiInst.Render()
	})
	// The main loop installs the dialog before any task queued after this
	// one, so a later extension call applies after it, as upstream's
	// synchronous showExtensionSelector does.
	extension.CallInitiated(ctx)

	select {
	case res := <-resultCh:
		if res.err != nil {
			return "", res.err
		}
		if res.cancelled {
			return "", context.Canceled
		}
		return res.value, nil
	case <-ctx.Done():
		cleanupCtx := u.m.runCtx
		if cleanupCtx == nil {
			cleanupCtx = context.Background()
		}
		u.m.runOnMain(cleanupCtx, func() {
			if u.m.extensionDialog == nil || u.m.extensionDialog.component != component {
				return
			}
			u.m.extensionDialog = nil
			u.m.editorContainer.SetChildren(u.m.editor)
			u.m.setExtensionDialogViewMode(false, 0)
			u.m.tuiInst.RequestRender()
		})
		return "", ctx.Err()
	}
}

// ─── Interactive dialogs ─────────────────────────────────────────────────────

// Select shows a generic option list in the editor slot. Blocks until
// the user picks an option or cancels (Esc). Mirrors upstream's
// showExtensionSelector in interactive-mode.ts using the
// standalone ExtensionSelectorComponent (NO filter input, fixed
// title row, fixed hint row).
func (u *ExtUIContext) Select(ctx context.Context, title string, options []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	sel := tui.NewExtensionSelector(title, options)
	return u.runDialog(ctx, sel, sel.HandleInput, func() (string, bool, bool) {
		return sel.SelectedValue(), sel.Done(), sel.Cancelled()
	})
}

// Confirm shows a Yes/No selector. Mirrors upstream's
// showExtensionConfirm (interactive-mode.ts:1996-2003).
func (u *ExtUIContext) Confirm(ctx context.Context, title, message string, opts extension.ExtensionUIDialogOptions) (bool, error) {
	prompt := title
	if message != "" {
		prompt = title + "\n" + message
	}
	result, err := u.Select(ctx, prompt, []string{"Yes", "No"}, opts)
	if err != nil {
		return false, err
	}
	return result == "Yes", nil
}

// Input shows a text input in the editor slot. Blocks until the user
// submits (Enter) or cancels (Esc). Mirrors upstream's
// showExtensionInput in interactive-mode.ts.
func (u *ExtUIContext) Input(ctx context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	input := tui.NewExtensionInputComponent(title, placeholder)
	return u.runDialog(ctx, input, input.HandleInput, func() (string, bool, bool) {
		return input.Text(), input.Done(), input.Cancelled()
	})
}

// Editor shows a multi-line editor in the editor slot. Mirrors upstream's
// showExtensionEditor in interactive-mode.ts, which creates an
// ExtensionEditorComponent and swaps it into the editorContainer.
func (u *ExtUIContext) Editor(ctx context.Context, title, prefill string) (string, error) {
	ed := tui.NewExtensionEditorComponent(title, prefill)
	return u.runDialog(ctx, ed, ed.HandleInput, func() (string, bool, bool) {
		return ed.Value(), ed.Done(), ed.Cancelled()
	})
}

// ─── Notifications & Status ──────────────────────────────────────────────────

func (u *ExtUIContext) Notify(message, kind string) {
	u.m.runOnMain(u.m.runCtx, func() {
		u.m.showExtensionNotify(message, kind)
	})
}

// SetStatus forwards a keyed status to the footer and requests a render, like
// upstream setExtensionStatus. Extensions call it from their own timers, so the
// render request is what paints it while the session is idle.
func (u *ExtUIContext) SetStatus(key, text string) {
	if u.m.statusLine != nil {
		u.m.statusLine.SetExtensionStatus(key, text)
	}
	if u.m.tuiInst != nil {
		u.m.requestRender()
	}
}

func (u *ExtUIContext) SetWorkingMessage(message string) {
	if u.m != nil {
		u.m.updateStatusOnOwner(func() { u.m.setWorkingMessage(message) })
	}
}

func (u *ExtUIContext) SetWorkingVisible(visible bool) {
	if u.m != nil {
		u.m.updateStatusOnOwner(func() { u.m.setWorkingVisible(visible) })
	}
}

func (u *ExtUIContext) SetWorkingIndicator(options extension.WorkingIndicatorOptions) {
	if u.m == nil {
		return
	}
	data, err := json.Marshal(options)
	if err != nil {
		return
	}
	var parsed *workingIndicatorOptions
	if json.Unmarshal(data, &parsed) != nil {
		return
	}
	u.m.updateStatusOnOwner(func() { u.m.setWorkingIndicator(parsed) })
}

func (u *ExtUIContext) SetHiddenThinkingLabel(label string) {
	if u.m.statusLine != nil {
		u.m.statusLine.SetHiddenThinkingLabel(label)
	}
}

func (u *ExtUIContext) SetWidget(key string, content any, opts extension.ExtensionWidgetOptions) {
	// Subprocess widgets are rendered via PushProxy, not this path.
	// In-process extensions do not use widgets, so this is a noop.
}

// widthLines normalizes a header/footer frame: plain lines carry no render
// width; a subprocess frame carries the width it was rendered at.
func widthLines(factory any) (extension.WidthLines, bool) {
	switch v := factory.(type) {
	case []string:
		return extension.WidthLines{Lines: v}, true
	case extension.WidthLines:
		return v, true
	}
	return extension.WidthLines{}, false
}

func (u *ExtUIContext) SetFooter(factory any) {
	if u.m.extFooter == nil {
		return
	}
	if frame, ok := widthLines(factory); ok && len(frame.Lines) > 0 {
		u.m.extFooter.SetLinesAt(frame.Lines, frame.Width)
		if u.m.statusLine != nil {
			u.m.statusLine.SetSuppressedByExtFooter(true)
		}
		return
	}
	u.m.extFooter.SetLines(nil)
	if u.m.statusLine != nil {
		u.m.statusLine.SetSuppressedByExtFooter(false)
	}
}

func (u *ExtUIContext) SetHeader(factory any) {
	if u.m.extHeader == nil {
		return
	}
	if frame, ok := widthLines(factory); ok {
		u.m.extHeader.SetLinesAt(frame.Lines, frame.Width)
		return
	}
	u.m.restoreBuiltInHeader()
}

func (u *ExtUIContext) SetLogin(definition extension.LoginDefinition) error {
	validated, err := extension.ValidateLoginDefinition(definition)
	if err != nil {
		return err
	}
	if u.m.extHeader == nil {
		return extension.ErrUIUnavailable
	}
	if !u.m.opts.LoginVisible {
		return nil
	}
	options := u.m.opts.LoginHeaderOptions
	options.ColorOverrides = nil
	renderer := newLoginHeaderRenderer(validated, options)
	u.m.extHeader.SetRenderer(renderer.Render)
	return nil
}

func (m *InteractiveMode) restoreBuiltInHeader() {
	m.toolMu.Lock()
	expanded := m.toolsExpanded
	m.toolMu.Unlock()
	m.setBuiltInHeader(expanded)
}

func (m *InteractiveMode) setBuiltInHeader(expanded bool) {
	if m.extHeader == nil {
		return
	}
	if !m.opts.LoginVisible {
		m.extHeader.SetLines(nil)
		return
	}
	if m.opts.BuiltInHeaderLines != nil {
		m.extHeader.SetLines(m.opts.BuiltInHeaderLines)
		return
	}
	m.toolMu.Lock()
	m.builtInHeaderExpanded = expanded
	m.toolMu.Unlock()
	m.extHeader.SetRenderer(m.renderBuiltInHeader)
}

func (u *ExtUIContext) SetTitle(title string) {
	if u.m.tuiInst != nil {
		_, _ = fmt.Fprintf(os.Stdout, "\033]0;%s\007", title)
	}
}

func (u *ExtUIContext) Custom(_ context.Context, _ any, _ any) (any, error) {
	// In-process Custom() requires a real factory closure capturing
	// TUI references. Subprocess extensions instead use
	// RunRemoteOverlay() below, which serialises lines/input across
	// the bridge. In-process Go/Rust extensions that want a real
	// overlay should build their own component and call
	// RunRemoteOverlay directly.
	return nil, fmt.Errorf("custom extension components require subprocess RunRemoteOverlay or in-process implementation")
}

// RunRemoteOverlay opens a remote-backed overlay (used by the
// subprocess bridge to implement ctx.ui.custom from TypeScript
// extensions). Blocks until the remote producer signals close.
//
// The input loop runs in this goroutine: safe because subprocess
// extension calls are dispatched from the host read loop, which is
// distinct from the interactive Run() loop. While this overlay is
// open, the originating /command or event handler is awaiting its
// response on Node, so the interactive editor is idle and the
// goroutine that opens this overlay has exclusive read access to
// os.Stdin (same invariant as Select/Input/Editor above).
func (u *ExtUIContext) RunRemoteOverlay(opts extension.RemoteOverlayOptions, host extension.RemoteOverlayHost, onHandle func(extension.RemoteOverlayHandle)) (any, bool) {
	if u.m.tuiInst == nil {
		return nil, false
	}

	invalidateOverlay := func() {
		if u.m.runCtx == nil {
			u.m.requestRender()
			return
		}
		u.m.postUITask(func() { u.m.tuiInst.Render() })
	}
	overlay := newCustomOverlay(invalidateOverlay)
	overlay.terminalWidth = u.m.tuiInst.Width
	if host != nil {
		overlay.SetOnInput(host.OnInput)
	}
	if onHandle != nil {
		onHandle(overlay)
	}

	ctx := u.m.runCtx
	runOnOwner := u.m.runOnMain
	if ctx == nil {
		ctx = context.Background()
		runOnOwner = func(_ context.Context, fn func()) { fn() }
	}
	setup := make(chan func(), 1)
	runOnOwner(ctx, func() {
		var cleanup func()
		if opts.Overlay || u.m.layout == nil {
			h := u.m.tuiInst.OpenOverlay(overlay, remoteOverlayTUIOptions(opts))
			cleanup = h.Close
		} else {
			// Match upstream ctx.ui.custom(): replace the editor slot directly
			// rather than wrapping the component in a second modal shell.
			u.m.editorContainer.SetChildren(overlay)
			cleanup = func() { u.m.editorContainer.SetChildren(u.m.editor) }
		}
		u.m.tuiInst.Render()
		setup <- cleanup
	})
	var cleanup func()
	select {
	case cleanup = <-setup:
	case <-ctx.Done():
		return nil, false
	}
	defer func() {
		done := make(chan struct{})
		runOnOwner(ctx, func() {
			cleanup()
			u.m.tuiInst.Render()
			close(done)
		})
		select {
		case <-done:
		case <-ctx.Done():
		}
	}()

	inputCh, releaseInput := u.m.acquireModalInputChannel()
	defer releaseInput()

	for !overlay.Done() {
		select {
		case buf := <-inputCh:
			dispatchModalInput(overlay, []string{string(buf)}, overlay.HandleInput, overlay.Done)
		case <-overlay.Closed():
			// The extension closed its own overlay. Without this the loop stays
			// parked on inputCh and the caller only unblocks when the user
			// presses an unrelated key.
		}
	}
	return overlay.Result(), true
}

// ─── Editor access ───────────────────────────────────────────────────────────

func (u *ExtUIContext) PasteToEditor(text string) {
	if u.m.editor != nil {
		// Simulate bracketed paste per upstream
		u.m.editor.HandleInput("\x1b[200~" + text + "\x1b[201~")
		if u.m.tuiInst != nil {
			u.m.tuiInst.Render()
		}
	}
}

func (u *ExtUIContext) SetEditorText(text string) {
	if u.m.editor != nil {
		u.m.editor.SetText(text)
		if u.m.tuiInst != nil {
			u.m.tuiInst.Render()
		}
	}
}

func (u *ExtUIContext) GetEditorText() string {
	if u.m.editor != nil {
		return u.m.editor.Text()
	}
	return ""
}

// ─── Terminal input ──────────────────────────────────────────────────────────

func (u *ExtUIContext) OnTerminalInput(handler extension.TerminalInputHandler) func() {
	if u.m == nil {
		return func() {}
	}
	return u.m.addTerminalInputHandler(handler)
}

// OnRemoteTerminalInput registers a subprocess extension's listener. The
// input loop asks it off the loop and applies its verdict in input order.
func (u *ExtUIContext) OnRemoteTerminalInput(extensionName string, handler extension.RemoteTerminalInputHandler) func() {
	if u.m == nil {
		return func() {}
	}
	return u.m.addRemoteTerminalInputHandler(extensionName, handler)
}

// ─── Autocomplete ────────────────────────────────────────────────────────────

// asyncSourceAdapter bridges an extension.AsyncSuggestionSource (with
// extension.AutocompleteSuggestions) into the tui package's
// AsyncSuggestionSource (with tui.AutocompleteSuggestions). Defined
// here so neither package needs to depend on the other.
type asyncSourceAdapter struct {
	src extension.AsyncSuggestionSource
}

func (a *asyncSourceAdapter) Suggest(ctx context.Context, lines []string, line, col int) *tui.AutocompleteSuggestions {
	out := a.src.Suggest(ctx, lines, line, col)
	if out == nil {
		return nil
	}
	items := make([]tui.AutocompleteItem, 0, len(out.Items))
	for _, it := range out.Items {
		items = append(items, tui.AutocompleteItem{
			Value:       it.Value,
			Label:       it.Label,
			Description: it.Description,
		})
	}
	return &tui.AutocompleteSuggestions{Items: items, Prefix: out.Prefix}
}

// AddAutocompleteProvider accepts subprocess async suggestion sources. It does not instantiate an in-process provider factory.
func (u *ExtUIContext) AddAutocompleteProvider(factory extension.AutocompleteProviderFactory) {
	if u.m.editor == nil {
		return
	}
	src, ok := factory.(extension.AsyncSuggestionSource)
	if !ok {
		return
	}
	// Install the async-apply scheduler once so extension-supplied async
	// suggestion results are applied + rendered on the main loop, never
	// from the worker goroutine that produced them.
	if u.m.tuiInst != nil {
		u.m.editor.SetAsyncApply(func(apply func()) {
			u.m.postUITask(func() { apply(); u.m.tuiInst.Render() })
		})
	}
	u.m.editor.AddAsyncSuggestionSource(&asyncSourceAdapter{src: src})
}

func (u *ExtUIContext) SetEditorComponent(factory any) {
	if u.m.editor == nil {
		return
	}
	if factory == nil {
		u.m.editor.ClearExtensionDecorations()
		if u.m.tuiInst != nil {
			u.m.tuiInst.Render()
		}
		return
	}
	// Subprocess shim path: the bridge passes a structured payload
	// (map[string]any) containing already-rendered decoration ANSI
	// codes and optional history entries. Other shapes are ignored.
	payload, ok := factory.(map[string]any)
	if !ok {
		return
	}
	if hist, ok := payload["history"].([]string); ok {
		for _, h := range hist {
			u.m.editor.AddToHistory(h)
		}
	}
	deco, _ := payload["decoration"].(map[string]any)
	if deco != nil {
		label, _ := deco["label"].(string)
		labelPrefix, _ := deco["labelPrefix"].(string)
		labelSuffix, _ := deco["labelSuffix"].(string)
		borderPrefix, _ := deco["borderPrefix"].(string)
		borderSuffix, _ := deco["borderSuffix"].(string)
		lockBorder, _ := deco["lockBorder"].(bool)
		u.m.editor.SetExtensionModeLabel(label, labelPrefix, labelSuffix)
		u.m.editor.SetExtensionBorderColor(borderPrefix, borderSuffix)
		if lockBorder {
			u.m.editor.LockExtensionBorderColor()
		}
	}
	if u.m.tuiInst != nil {
		u.m.tuiInst.Render()
	}
}

func (u *ExtUIContext) GetEditorComponent() any {
	return nil
}

// ─── Theme ───────────────────────────────────────────────────────────────────

func (u *ExtUIContext) Theme() extension.Theme { return ActiveExtensionTheme() }

// themeColorMode is upstream's ColorMode for the terminal: "truecolor" when
// it draws 24-bit colors, else "256color".
func themeColorMode() string {
	if tui.GetCapabilities().TrueColor {
		return "truecolor"
	}
	return "256color"
}

// ActiveExtensionTheme is the active theme as extensions see it
// (ctx.ui.theme): upstream hands every mode's UI context the global theme.
func ActiveExtensionTheme() extension.Theme {
	theme := tui.ActiveTheme()
	if theme == nil {
		return nil
	}
	foregrounds, backgrounds := theme.ANSIPalette()
	return map[string]any{
		"name":        theme.Name,
		"foregrounds": foregrounds,
		"backgrounds": backgrounds,
		// modifiers is whether theme.bold and the other chalk styles draw,
		// as upstream's chalk decides from its own stdout.
		"modifiers": chalkModifiersEnabled(),
		// mode is upstream Theme.getColorMode().
		"mode": themeColorMode(),
	}
}

// GetAllThemes lists the themes the user can switch to, mirroring upstream's
// getAllThemes (getAvailableThemesWithPaths). Path is empty for built-in
// themes, matching upstream's `path: string | undefined`.
func (u *ExtUIContext) GetAllThemes() []extension.ThemeMeta {
	reg := tui.ActiveThemeRegistry()
	if reg == nil {
		return nil
	}
	names := reg.Names()
	metas := make([]extension.ThemeMeta, 0, len(names))
	for _, name := range names {
		metas = append(metas, extension.ThemeMeta{Name: name, Path: reg.PathOf(name)})
	}
	return metas
}

// GetTheme loads a theme by name without switching to it, mirroring upstream's
// getTheme (getThemeByName).
func (u *ExtUIContext) GetTheme(name string) (extension.Theme, error) {
	reg := tui.ActiveThemeRegistry()
	if reg == nil {
		return nil, fmt.Errorf("no theme registry is active")
	}
	theme := reg.Get(name)
	if theme == nil {
		return nil, fmt.Errorf("unknown theme %q", name)
	}
	return theme, nil
}

// SetTheme switches the active theme and persists the choice, mirroring
// upstream's setTheme: apply, then record it in settings when it differs.
//
// Upstream also accepts a Theme instance. A subprocess extension cannot send
// one, and no in-process caller does, so only the name form is accepted.
func (u *ExtUIContext) SetTheme(theme any) extension.SetThemeResult {
	name, ok := theme.(string)
	if !ok {
		return extension.SetThemeResult{Success: false, Error: "expected theme name string"}
	}
	if u.m == nil {
		return extension.SetThemeResult{Success: false, Error: "no interactive UI is active"}
	}
	if _, isAuto := parseAutoThemeName(name); !isAuto {
		reg := tui.ActiveThemeRegistry()
		if reg == nil || reg.Get(name) == nil {
			return extension.SetThemeResult{Success: false, Error: fmt.Sprintf("unknown theme %q", name)}
		}
	}

	tui.SetThemeSetting(name)
	if u.m.tuiInst != nil {
		u.m.tuiInst.ForceFullRender()
	}

	if u.m.opts.SettingsManager != nil && u.m.opts.Settings.Theme != name {
		if err := u.m.opts.SettingsManager.UpdateGlobal(func(gs *Settings) {
			gs.Theme = name
		}); err != nil {
			return extension.SetThemeResult{Success: false, Error: err.Error()}
		}
		u.m.opts.Settings.Theme = name
	}
	return extension.SetThemeResult{Success: true}
}

// parseAutoThemeName reports whether name is an "automatic" theme setting,
// which follows terminal appearance instead of naming a registry entry.
func parseAutoThemeName(name string) (string, bool) {
	light, _, ok := tui.ParseAutoThemeSetting(name)
	return light, ok
}

// ─── Tools state ─────────────────────────────────────────────────────────────

func (u *ExtUIContext) GetToolsExpanded() bool {
	u.m.toolMu.Lock()
	defer u.m.toolMu.Unlock()
	return u.m.toolsExpanded
}

func (u *ExtUIContext) SetToolsExpanded(expanded bool) {
	apply := func() {
		u.m.setAllToolsExpanded(expanded)
		if u.m.tuiInst != nil {
			u.m.tuiInst.Render()
		}
	}
	if u.m.runCtx == nil {
		apply()
		return
	}
	u.m.runOnMain(u.m.runCtx, apply)
}
