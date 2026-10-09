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
	"math"
	"os"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
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
	// mouse handles mouse events for the built-in header; installing other lines or another renderer removes it.
	mouse      func(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult
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

// Invalidate is a no-op: the component keeps no derived state, and the container invalidates every child while it renders after a theme change (tui.ts invalidate), so requesting a render here would re-enter the renderer.
func (c *specialLinesComponent) Invalidate() {}

// repaint asks the host to draw the component again, as installing new content does.
func (c *specialLinesComponent) repaint() {
	if c.invalidate != nil {
		c.invalidate()
	}
}

func (c *specialLinesComponent) SetLines(lines []string) { c.SetLinesAt(lines, 0) }

// SetLinesAt installs lines rendered at width (0 when unknown).
func (c *specialLinesComponent) SetLinesAt(lines []string, width int) {
	c.mu.Lock()
	c.render = nil
	c.mouse = nil
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
	c.SetRendererWithMouse(render, nil)
}

// SetRendererWithMouse installs a renderer and the mouse handler of the component it draws.
func (c *specialLinesComponent) SetRendererWithMouse(render func(width int) []string, mouse func(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult) {
	c.mu.Lock()
	c.lines = nil
	c.render = render
	c.mouse = mouse
	c.mu.Unlock()
	if c.invalidate != nil {
		c.invalidate()
	}
}

// HandleMouse forwards an event to the installed mouse handler, if any.
func (c *specialLinesComponent) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	c.mu.RLock()
	mouse := c.mouse
	c.mu.RUnlock()
	if mouse == nil {
		return nil
	}
	return mouse(event)
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

// dialogCountdown is a dialog's timeout option. As upstream's CountdownTimer
// does, tick shows the seconds left and expire cancels the dialog.
type dialogCountdown struct {
	timeout time.Duration
	tick    func(seconds int)
	expire  func()
}

// dialogTimeout reads ExtensionUIDialogOptions.timeout. Upstream's interactive
// dialogs count down only for a positive number of milliseconds
// (extension-selector.ts:56, extension-input.ts:60).
func dialogTimeout(opts extension.ExtensionUIDialogOptions) time.Duration {
	if opts == nil {
		return 0
	}
	data, err := json.Marshal(opts)
	if err != nil {
		return 0
	}
	var value struct {
		Timeout float64 `json:"timeout"`
	}
	if json.Unmarshal(data, &value) != nil || !(value.Timeout > 0) {
		return 0
	}
	// The countdown shows whole seconds; a timeout beyond Duration's range
	// counts down from its largest.
	longest := time.Duration(math.MaxInt64) - time.Second
	if value.Timeout >= float64(longest/time.Millisecond) {
		return longest
	}
	return time.Duration(value.Timeout * float64(time.Millisecond))
}

func (u *ExtUIContext) runDialog(
	ctx context.Context,
	component tui.Component,
	handle func(string),
	result func() (value string, done, cancelled bool),
	countdown dialogCountdown,
) (string, error) {
	if u.m.layout == nil || u.m.tuiInst == nil {
		return "", fmt.Errorf("no TUI available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The dialog's context ends when it returns, which disposes its countdown.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if u.m.runCtx != nil {
		stop := context.AfterFunc(u.m.runCtx, cancel)
		defer stop()
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

		var timer *tui.CountdownTimer
		finish := func() {
			value, done, cancelled := result()
			if !done {
				u.m.tuiInst.Render()
				return
			}
			if timer != nil {
				timer.Dispose()
			}
			u.m.extensionDialog = nil
			u.m.editorContainer.SetChildren(u.m.editor)
			u.m.setExtensionDialogViewMode(false, 0)
			u.m.tuiInst.RequestRender()
			resultCh <- extensionDialogResult{value: value, cancelled: cancelled}
		}
		u.m.setExtensionDialogViewMode(true, u.m.extensionDialogLines(component))
		u.m.editorContainer.SetChildren(component)
		u.m.extensionDialog = &extensionDialog{component: component}
		u.m.extensionDialog.handle = func(data string) {
			handle(data)
			finish()
		}
		if countdown.timeout > 0 {
			// Each second runs on this loop, so expiry replaces the dialog
			// before any frame shows a zero countdown.
			timer = tui.NewCountdownTimer(countdown.timeout, func(second func()) {
				_ = u.m.postToMain(ctx, second)
			}, func(seconds int) {
				countdown.tick(seconds)
				u.m.tuiInst.RequestRender()
			}, func() {
				countdown.expire()
				finish()
			})
			context.AfterFunc(ctx, timer.Dispose)
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
// title row, fixed hint row). A positive opts timeout counts down in the
// title and cancels the selector when it expires.
func (u *ExtUIContext) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	sel := tui.NewExtensionSelector(title, options, u.m.toggleAllTools)
	return u.runDialog(ctx, sel, sel.HandleInput, func() (string, bool, bool) {
		return sel.SelectedValue(), sel.Done(), sel.Cancelled()
	}, dialogCountdown{timeout: dialogTimeout(opts), tick: sel.SetCountdown, expire: sel.Cancel})
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
// showExtensionInput in interactive-mode.ts. A positive opts timeout counts
// down in the title and cancels the input when it expires.
func (u *ExtUIContext) Input(ctx context.Context, title, placeholder string, opts extension.ExtensionUIDialogOptions) (string, error) {
	input := tui.NewExtensionInputComponent(title, placeholder)
	return u.runDialog(ctx, input, input.HandleInput, func() (string, bool, bool) {
		return input.Text(), input.Done(), input.Cancelled()
	}, dialogCountdown{timeout: dialogTimeout(opts), tick: input.SetCountdown, expire: input.Cancel})
}

// Editor shows a multi-line editor in the editor slot. Mirrors upstream's
// showExtensionEditor in interactive-mode.ts, which creates an
// ExtensionEditorComponent and swaps it into the editorContainer.
func (u *ExtUIContext) Editor(ctx context.Context, title, prefill string) (string, error) {
	ed := tui.NewExtensionEditorComponent(title, prefill)
	ed.SetExternalEditor(func(content string, apply func(string)) {
		u.m.openExternalEditorBuffer(ctx, content, apply)
	})
	return u.runDialog(ctx, ed, ed.HandleInput, func() (string, bool, bool) {
		return ed.Value(), ed.Done(), ed.Cancelled()
	}, dialogCountdown{})
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
	// Upstream setExtensionFooter swaps the built-in footer for the
	// extension's component whatever it renders, so a frame with no lines
	// is an empty footer; only a cleared footer (nil) restores the built-in. Ownership changes before SetLinesAt, whose invalidation callback can paint synchronously.
	if frame, ok := widthLines(factory); ok {
		if u.m.statusLine != nil {
			u.m.statusLine.SetSuppressedByExtFooter(true)
		}
		u.m.extFooter.SetLinesAt(frame.Lines, frame.Width)
		return
	}
	if u.m.statusLine != nil {
		u.m.statusLine.SetSuppressedByExtFooter(false)
	}
	u.m.extFooter.SetLines(nil)
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
		u.m.extHeader.SetLines(nil)
		return nil
	}
	options := u.m.opts.LoginHeaderOptions
	options.ColorOverrides = nil
	renderer := newLoginHeaderRenderer(validated, options)
	u.m.extHeader.SetRenderer(renderer.Render)
	return nil
}

var _ extension.SpriteRegistrar = (*ExtUIContext)(nil)

// RegisterSprite adds an extension's sprite to /sprite (D2). The header redraws, since it may be the saved sprite, drawn
// as the default until now.
func (u *ExtUIContext) RegisterSprite(owner string, definition extension.ValidatedSpriteDefinition) error {
	u.m.spriteOwnersMu.Lock()
	err := piglogin.Register(owner, definition)
	if err == nil {
		if u.m.spriteOwners == nil {
			u.m.spriteOwners = map[string]struct{}{}
		}
		u.m.spriteOwners[owner] = struct{}{}
	}
	u.m.spriteOwnersMu.Unlock()
	if err != nil {
		return err
	}
	u.m.invalidateBuiltInHeader()
	return nil
}

// UnregisterSprites removes the sprites of an extension that unloaded; the header redraws in case it showed one.
func (u *ExtUIContext) UnregisterSprites(owner string) {
	u.m.spriteOwnersMu.Lock()
	piglogin.Unregister(owner)
	delete(u.m.spriteOwners, owner)
	u.m.spriteOwnersMu.Unlock()
	u.m.invalidateBuiltInHeader()
}

// dropBoundSprites removes the sprites of the build whose bridge was bound, before another build's bridge replays its own,
// and reports whether it removed any. The outgoing bridge is detached by then, so its host's shutdown no longer reaches the
// UI to remove them.
// pig divergence (D2): extension sprites leave /sprite with the build that registered them; Pi has no sprites.
func (m *InteractiveMode) dropBoundSprites() bool {
	m.spriteOwnersMu.Lock()
	defer m.spriteOwnersMu.Unlock()
	for owner := range m.spriteOwners {
		piglogin.Unregister(owner)
	}
	dropped := len(m.spriteOwners) > 0
	m.spriteOwners = nil
	return dropped
}

// invalidateBuiltInHeader redraws the header slot, which draws the active sprite while it shows the built-in header.
func (m *InteractiveMode) invalidateBuiltInHeader() {
	if m.extHeader != nil {
		m.extHeader.repaint()
	}
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
	showDetails := m.shouldShowStartupDetails()
	m.toolMu.Lock()
	m.builtInHeaderExpanded = expanded
	if !m.builtInHeaderBuilt {
		m.builtInHeaderShowDetails, m.builtInHeaderBuilt = showDetails, true
	}
	m.toolMu.Unlock()
	// pig divergence (D2): the built-in header draws the saved PiG sprite. Reading it here, off the render loop, picks up a
	// selection a game or another PiG wrote since startup (/reload restores the built-in header).
	piglogin.Refresh()
	piglogin.Active()
	m.extHeader.SetRendererWithMouse(m.renderBuiltInHeader, m.handleBuiltInHeaderMouse)
}

func (u *ExtUIContext) SetTitle(title string) {
	if u.m.tuiInst != nil {
		_, _ = fmt.Fprintf(os.Stdout, "\033]0;%s\007", title)
	}
}

// RunRemoteOverlay mounts remote custom UI as an overlay or editor-slot replacement and waits for its result. The owner loop mounts the component and transfers renderer focus; this caller drains its modal input lease off-loop. Normal completion restores the editor slot and focus before returning.
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
	defer overlay.setInputActive(u.m, false)

	ctx := u.m.runCtx
	runOnOwner := u.m.runOnOwner
	if ctx == nil {
		ctx = context.Background()
		runOnOwner = func(_ context.Context, fn func()) { fn() }
	}
	setup := make(chan func(), 1)
	runOnOwner(ctx, func() {
		var cleanup func()
		if opts.Overlay || u.m.layout == nil {
			h := u.m.tuiInst.OpenOverlay(overlay, remoteOverlayTUIOptions(opts))
			u.bindOverlayControls(overlay, h, runOnOwner)
			cleanup = h.Close
		} else {
			// Match upstream ctx.ui.custom(): replace the editor slot directly
			// rather than wrapping the component in a second modal shell.
			u.m.editorContainer.SetChildren(overlay)
			// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:showExtensionCustom
			u.m.tuiInst.SetFocus(overlay)
			overlay.setInputActive(u.m, true)
			cleanup = func() {
				u.m.editorContainer.SetChildren(u.m.editor)
				u.m.tuiInst.SetFocus(u.m.editor)
			}
		}
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

	if onHandle != nil {
		onHandle(overlay)
	}
	runOnOwner(ctx, func() { u.m.tuiInst.Render() })

	for !overlay.Done() {
		inputCh, changed := overlay.inputRoute()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, false
		case buf := <-inputCh:
			data := string(buf)
			// Theme replies and fullscreen viewport keys precede the component, as for every selector; both run on the owner loop.
			consumed := make(chan bool, 1)
			runOnOwner(ctx, func() { consumed <- u.m.consumeModalHostInput(data) })
			select {
			case handled := <-consumed:
				if handled {
					continue
				}
			case <-ctx.Done():
				return nil, false
			}
			dispatchModalInput(overlay, []string{data}, overlay.HandleInput, overlay.Done)
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

// GetEditorText reads owner-published expanded text after binding, including paste contents. Before binding the caller owns the editor directly.
func (u *ExtUIContext) GetEditorText() string {
	if snapshot := u.m.editorSnapshot.Load(); snapshot != nil {
		return *snapshot
	}
	if u.m.editor != nil {
		return u.m.editor.GetExpandedText()
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

// SetEditorComponent installs an extension's editor component in place of
// the editor, or with nil restores the editor, as Pi's setEditorComponent
// does (remote_editor.go).
func (u *ExtUIContext) SetEditorComponent(factory any) {
	editor, _ := factory.(extension.RemoteEditor)
	u.m.runOnMain(u.m.backgroundCtx, func() { u.m.setRemoteEditor(editor) })
}

func (u *ExtUIContext) GetEditorComponent() any {
	return nil
}

// ─── Theme ───────────────────────────────────────────────────────────────────

func (u *ExtUIContext) Theme() extension.Theme { return ActiveExtensionTheme() }

// ActiveExtensionTheme is the active theme as extensions see it
// (ctx.ui.theme): upstream hands every mode's UI context the global theme.
func ActiveExtensionTheme() extension.Theme {
	theme := tui.ActiveTheme()
	if theme == nil {
		return nil
	}
	return ExtensionThemePalette(theme)
}

// ExtensionThemePalette is the palette extensions receive for theme (ctx.ui.theme): its resolved escape sequences, and the host-resolved appearance and concrete colors.
func ExtensionThemePalette(theme *tui.Theme) extension.Theme {
	foregrounds, backgrounds := theme.ANSIPalette()
	palette := map[string]any{
		"name":        theme.Name,
		"foregrounds": foregrounds,
		"backgrounds": backgrounds,
		// modifiers is whether theme.bold and the other chalk styles draw,
		// as upstream's chalk decides from its own stdout.
		"modifiers": chalkModifiersEnabled(),
		// mode is upstream Theme.getColorMode(): the mode the theme's own escape sequences are built for, which style draws a concrete color in.
		"mode": string(theme.ColorMode()),
		// appearance and colors are upstream Theme.appearance and Theme.colors (theme.ts:311-336). The host resolves them: they depend on the terminal's reported colors, which the extension process cannot see.
		"appearance": string(theme.Appearance()),
		"colors":     extensionThemeColors(theme.ColorValues()),
	}
	// sourcePath is upstream Theme.sourcePath: the file a custom theme was
	// loaded from, absent for built-in themes.
	if reg := tui.ActiveThemeRegistry(); reg != nil {
		if path := reg.PathOf(theme.Name); path != "" {
			palette["sourcePath"] = path
		}
	}
	return palette
}

// extensionThemeColors is the wire form of Theme.colors: each token's pi-tui Color as {kind: "indexed", index}, {kind: "rgb", r, g, b} or {kind: "oklch", l, c, h}.
// A color with a non-finite channel is left out: Pi's rgbColor and oklchColor reject one (colors.ts:58-88), and JSON cannot carry it. A terminal reply can produce one (terminal-colors.ts:27-36 divides Infinity by Infinity for a very long channel).
func extensionThemeColors(values map[string]tui.Color) map[string]any {
	finite := func(channels ...float64) bool {
		for _, channel := range channels {
			if math.IsNaN(channel) || math.IsInf(channel, 0) {
				return false
			}
		}
		return true
	}
	colors := make(map[string]any, len(values))
	for token, color := range values {
		switch color := color.(type) {
		case tui.IndexedColor:
			colors[token] = map[string]any{"kind": "indexed", "index": color.Index}
		case tui.RgbColorValue:
			if finite(color.R, color.G, color.B) {
				colors[token] = map[string]any{"kind": "rgb", "r": color.R, "g": color.G, "b": color.B}
			}
		case tui.OklchColorValue:
			if finite(color.L, color.C, color.H) {
				colors[token] = map[string]any{"kind": "oklch", "l": color.L, "c": color.C, "h": color.H}
			}
		}
	}
	return colors
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

// GetTheme loads a theme's portable palette without selecting it. Missing names return absence, as Pi's getThemeByName does. The theme is in the terminal's color mode, as Pi's createTheme defaults it (theme.ts:599-600).
func (u *ExtUIContext) GetTheme(name string) (extension.Theme, error) {
	reg := tui.ActiveThemeRegistry()
	if reg == nil {
		return nil, nil
	}
	theme := reg.Get(name)
	if theme == nil {
		return nil, nil
	}
	return ExtensionThemePalette(theme.WithColorMode(tui.GetTerminalColorMode())), nil
}

// SetTheme disables automatic switching, applies a named theme, and persists a successful choice.
// An unknown name falls back to the system theme and returns failure without changing the stored selection (theme.ts setTheme).
func (u *ExtUIContext) SetTheme(theme any) extension.SetThemeResult {
	name, ok := theme.(string)
	if !ok {
		return extension.SetThemeResult{Success: false, Error: "expected theme name string"}
	}
	if u.m == nil {
		return extension.SetThemeResult{Success: false, Error: "no interactive UI is active"}
	}
	// upstream 0.99.1 interactive-mode.ts:2575-2586 extension setTheme: select by name, and persist only a theme that loaded.
	core := u.m.theme()
	// An extension calls off the owner loop, which alone may touch the renderer.
	core.renderer = func() tui.Renderer { return ownerInvalidatingRenderer{Renderer: u.m.tuiInst, m: u.m} }
	if err := core.setThemeName(name, false); err != nil {
		return extension.SetThemeResult{Success: false, Error: err.Error()}
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

// ownerInvalidatingRenderer runs Invalidate on the owner loop and requests the render an extension's theme change needs.
type ownerInvalidatingRenderer struct {
	tui.Renderer
	m *InteractiveMode
}

func (r ownerInvalidatingRenderer) Invalidate() {
	if r.Renderer == nil {
		return
	}
	// The embedded renderer, not this wrapper: the wrapper's own Invalidate would recurse.
	base := r.Renderer
	invalidate := func() { base.Invalidate(); base.RequestRender() }
	if r.m.runCtx == nil {
		invalidate()
		return
	}
	r.m.runOnMain(r.m.runCtx, invalidate)
}
