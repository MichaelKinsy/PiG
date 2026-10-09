package tui

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"maps"
	"math"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D91): TuiSurface is a renderer Pi does not have. It keeps
// Pi's component tree and reports each frame to a Piglet frontend session as
// retained-tree ops instead of painting the terminal.

// TuiSurface draws the interactive layout through a [frontend.Session]. The
// document's leaf components become the session's main nodes. Tool cards are
// reported as [frontend.ToolCard] and messages as [frontend.MarkdownText]; an
// assistant message is a run of nodes, one per part, whose ids extend the
// message's id. The dock renders as one dock node, or around the input editor
// as [frontend.Editor], or around a [NativeComponent] in its place, with the
// working indicator and the footer as their own nodes, while no overlay that
// takes the keys shows. Overlays are reported as [frontend.Overlay] nodes.
// A frame costs what changed, not the transcript's length: a component that
// invalidates itself reports each change to the surface, which rebuilds only
// the components it was told about and those it must poll.
type TuiSurface struct {
	tuiBase

	session frontend.Session
	// terminal is where the session draws, which takes PiG's terminal
	// queries; the surface paints nothing there.
	terminal io.Writer
	document Component
	dock     Component
	started  bool
	// suspended records a Suspend the session has not seen Resume for.
	suspended bool
	// statusTerminal is the Terminal method's terminal, which reports the
	// program status to the session. The constructors set it, so Terminal
	// takes no lock: a component asks for it while doRender holds t.mu.
	statusTerminal *surfaceTerminal
	// onApplyError receives a frame the session failed to draw.
	onApplyError func(error)

	// surfaceMain is the retained main region.
	surfaceMain
	docked []surfaceEntry
	// overlays is the overlay region of the last frame.
	overlays []surfaceEntry
	// nativeComp is the native component the last frame reported in the
	// dock, nativeIDText its node id, and nativeSeq numbers those ids.
	nativeComp   NativeComponent
	nativeIDText string
	nativeSeq    int
	// viewSources are the components whose view holds a focused list, by
	// the id of the node the last frame reported them in (D107), for
	// Action.ViewNode.
	viewSources map[string]viewSourceRef
	// dockLayouts are the components of each dock blob with a view, by its
	// node id in the last frame, for a click on the view (D91).
	dockLayouts map[string]dockLayout
	// resend replaces every node on the next frame.
	resend bool
	hooks  SurfaceHooks
	// fullRedraws counts the frames that replaced every node.
	fullRedraws int

	hasRendered bool
	// sessionFile is the session file the last frame that carried one
	// reported, once sessionFileSent.
	sessionFile     string
	sessionFileSent bool
	// paletteTheme and paletteTerminal are the theme and terminal colors
	// the last palette was built from, and palette the theme the last
	// frame that carried one reported.
	paletteTheme          *Theme
	paletteTerminal       *TerminalColors
	palette               *frontend.Theme
	prevWidth, prevHeight int
	// prevMainCols and prevDockCols are the session widths of the last frame.
	prevMainCols, prevDockCols int
	// clickScreen is the screen ScreenLines reports while Click dispatches.
	clickScreen []string
	// lastSessionClick is the last click a session made on an extension's
	// view, which counts the next one (D91). Owner loop only.
	lastSessionClick *componentClick

	// mark is the mark the last frame that carried one reported, once
	// markSent.
	mark     frontend.Mark
	markSent bool
}

// Mode reports the regular mode: the surface stands in for Pi's main-screen
// renderer (tui.ts TuiMode).
func (t *TuiSurface) Mode() TuiMode { return TuiModeRegular }

// FullRedraws counts the frames that replaced every node, the surface's
// full redraws (tui.ts fullRedraws).
func (t *TuiSurface) FullRedraws() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fullRedraws
}

type surfaceEntry struct {
	id   string
	node frontend.Node
	// ver numbers the build of a main entry; zero for an entry the surface
	// does not retain.
	ver uint64
}

// SurfaceHooks supply state that the component tree does not hold. They run
// where the surface renders, on the interactive owner loop. A nil hook
// reports nothing.
type SurfaceHooks struct {
	// Streaming returns the assistant message still being generated, or
	// nil.
	Streaming func() *AssistantMessageComponent
	// ToolDiff returns the unified diff that a finished call's result value
	// (the value SetResultValue recorded) carries, or nil.
	ToolDiff func(name string, arguments map[string]any, result any) *frontend.Diff
	// Editor returns the input editor that the dock reports as a
	// [frontend.Editor] node, or nil.
	Editor func() *Editor
	// EditorSendable reports whether the input editor takes a send now.
	EditorSendable func() bool
	// Working returns the working indicator the dock reports above the
	// editor node, and false while none shows.
	Working func() (frontend.Working, bool)
	// Footer returns the footer node of the dock component c, and false
	// when c is not the footer or the footer does not show.
	Footer func(c Component) (frontend.Footer, bool)
	// SessionFile returns the file the current session persists to, or ""
	// for a session that is not persisted.
	SessionFile func() string
	// Mark returns the application's mark. It runs on every frame, and the
	// surface reports the mark when it differs from the last one reported,
	// so it should not rebuild an unchanged mark.
	Mark func() frontend.Mark
	// Mouse reports whether the run's terminal layout hands the mouse to
	// components, as Pi's fullscreen mode does; a click on an extension's
	// view reaches its component only then (D91).
	Mouse func() bool
}

// SetHooks installs the hooks the next frame reads. The next frame rebuilds
// every node from them.
func (t *TuiSurface) SetHooks(hooks SurfaceHooks) {
	t.mu.Lock()
	t.hooks = hooks
	t.mainGen++
	t.mu.Unlock()
}

var _ TUI = (*TuiSurface)(nil)

// NewTuiSurface creates a surface renderer for session at the process
// terminal's size. onApplyError, if set, receives every failed frame.
func NewTuiSurface(session frontend.Session, onApplyError func(error)) *TuiSurface {
	t := newTuiSurface(io.Discard, session, onApplyError)
	t.updateSize()
	t.statusTerminal.Terminal = t.tuiBase.Terminal()
	return t
}

// NewTuiSurfaceWithSize creates a fixed-size surface renderer for tests.
func NewTuiSurfaceWithSize(session frontend.Session, cols, rows int, onApplyError func(error)) *TuiSurface {
	t := newTuiSurface(io.Discard, session, onApplyError)
	t.fixedSize = true
	t.setFixedDimensionsLocked(cols, rows)
	t.statusTerminal.Terminal = t.tuiBase.Terminal()
	return t
}

func newTuiSurface(out io.Writer, session frontend.Session, onApplyError func(error)) *TuiSurface {
	t := &TuiSurface{
		tuiBase: tuiBase{
			out:                out,
			terminalBackground: &terminalBackgroundQueries{},
			showHardwareCursor: os.Getenv("PI_HARDWARE_CURSOR") == "1",
			now:                time.Now,
			afterFunc: func(d time.Duration, fn func()) stoppableTimer {
				return time.AfterFunc(d, fn)
			},
		},
		session:        session,
		statusTerminal: newSurfaceTerminal(session),
		terminal:       io.Discard,
		onApplyError:   onApplyError,
		surfaceMain:    newSurfaceMain(),
	}
	t.render = t.doRender
	t.mountedRoots = t.getMountedRoots
	return t
}

// Session returns the frontend session the renderer draws through.
func (t *TuiSurface) Session() frontend.Session { return t.session }

// pig additive (D91): PiG reads the terminal's colors and appearance under a
// frontend session as it does under its ANSI renderer, from the terminal's
// replies to its own query and its light/dark reports.

// SetTerminalOut sets the terminal the session draws on, which the surface
// writes the terminal color query to. Until it is set, the query goes
// nowhere and times out.
func (t *TuiSurface) SetTerminalOut(out io.Writer) { t.terminal = out }

// TerminalOut returns the terminal SetTerminalOut set, for PiG's other
// writes while the session draws.
func (t *TuiSurface) TerminalOut() io.Writer { return t.terminal }

// QueryTerminalColors queries the terminal the session draws on for its
// colors, as the ANSI renderer queries its own; the replies are consumed by
// ConsumeTerminalColorResponse.
func (t *TuiSurface) QueryTerminalColors(options TerminalColorQueryOptions) <-chan TerminalColorsResult {
	return t.queryTerminalColors(t.terminal, options)
}

// SetTerminalColorSchemeNotifications turns the light/dark reports (mode
// 2031) of the terminal the session draws on on or off. While the surface is
// stopped only the preference changes; Start and Resume apply it, as the
// main-screen renderer's Start does.
func (t *TuiSurface) SetTerminalColorSchemeNotifications(enabled bool) {
	t.input.mu.Lock()
	if t.input.schemeNotifications == enabled {
		t.input.mu.Unlock()
		return
	}
	t.input.schemeNotifications = enabled
	t.input.mu.Unlock()
	t.mu.Lock()
	stopped := t.stopped
	t.mu.Unlock()
	if !stopped {
		writeColorSchemeNotifications(t.terminal, enabled)
	}
}

// SetLayout sets the transcript document and the input dock.
func (t *TuiSurface) SetLayout(document, dock Component) {
	t.mu.Lock()
	t.document, t.dock = document, dock
	t.resend = true
	t.mu.Unlock()
	t.RequestRender()
}

func (t *TuiSurface) getMountedRoots() []Component {
	t.mu.Lock()
	defer t.mu.Unlock()
	roots := make([]Component, 0, 2)
	for _, root := range []Component{t.document, t.dock} {
		if root != nil {
			roots = append(roots, root)
		}
	}
	return roots
}

// HandleFrontendInput offers terminal input to the session. A sequence that
// changes the session's region widths repaints.
func (t *TuiSurface) HandleFrontendInput(data string) bool {
	if !t.session.HandleInput(data) {
		return false
	}
	mainCols, dockCols := t.session.Columns()
	t.mu.Lock()
	changed := t.hasRendered && (mainCols != t.prevMainCols || dockCols != t.prevDockCols)
	t.mu.Unlock()
	if changed {
		t.RequestRender()
	}
	return true
}

// Start begins rendering and draws the whole tree. A session still
// suspended is resumed first.
func (t *TuiSurface) Start() {
	t.mu.Lock()
	resume := t.suspended
	t.suspended = false
	t.stopped = false
	t.started = true
	t.resend = true
	t.mu.Unlock()
	if t.colorSchemeNotificationsEnabled() {
		writeColorSchemeNotifications(t.terminal, true)
	}
	if resume {
		t.session.Resume()
	}
	t.statusTerminal.resume()
	t.Render()
}

// Stop stops rendering.
func (t *TuiSurface) Stop() { t.StopWithOptions(StopOptions{}) }

// StopWithOptions stops rendering. The session owns the terminal surface, so
// PreserveScreen changes nothing. The components stop reporting their
// invalidations, and a later start rebuilds every node.
func (t *TuiSurface) StopWithOptions(StopOptions) {
	t.mu.Lock()
	wasStopped := t.stopped
	t.stopped = true
	t.started = false
	t.cancelPendingRenderLocked()
	t.forgetMain()
	t.mu.Unlock()
	// The light/dark reports stop with the surface, as the main-screen
	// renderer's Stop stops them.
	if !wasStopped && t.colorSchemeNotificationsEnabled() {
		writeColorSchemeNotifications(t.terminal, false)
	}
	t.statusTerminal.pause()
}

// pig additive (D91): PiG lends the terminal to an external editor or, on a
// job-control stop, to the shell without ending the frontend session, and
// the frame after the handoff carries only what changed meanwhile.

// Suspend stops rendering while another program owns the terminal and tells
// the session so. Unlike Stop, the components keep reporting their
// invalidations, so the frame after Resume carries only what changed. It does
// nothing before Start, after Stop, or while suspended.
func (t *TuiSurface) Suspend() {
	t.mu.Lock()
	if !t.started || t.stopped {
		t.mu.Unlock()
		return
	}
	t.suspended = true
	t.stopped = true
	t.cancelPendingRenderLocked()
	t.mu.Unlock()
	// The terminal's light/dark reports stop while another program owns
	// it, as the main-screen renderer's Stop does.
	if t.colorSchemeNotificationsEnabled() {
		writeColorSchemeNotifications(t.terminal, false)
	}
	t.statusTerminal.pause()
	t.session.Suspend()
}

// Resume tells the session that PiG owns the terminal again and draws what
// changed since Suspend, which is no frame when nothing changed. It does
// nothing unless suspended; after a Stop it only resumes the session.
func (t *TuiSurface) Resume() {
	t.mu.Lock()
	if !t.suspended {
		t.mu.Unlock()
		return
	}
	t.suspended = false
	started := t.started
	t.mu.Unlock()
	if started && t.colorSchemeNotificationsEnabled() {
		writeColorSchemeNotifications(t.terminal, true)
	}
	t.session.Resume()
	if started {
		t.statusTerminal.resume()
	}
	t.mu.Lock()
	render := t.started
	if render {
		t.stopped = false
	}
	t.mu.Unlock()
	if render {
		t.Render()
	}
}

// ForceFullRender replaces every node on the next frame.
func (t *TuiSurface) ForceFullRender() {
	t.mu.Lock()
	t.resend = true
	t.mu.Unlock()
	t.RequestRender()
}

// RepaintAll replaces every node now.
func (t *TuiSurface) RepaintAll() {
	t.mu.Lock()
	t.resend = true
	t.mu.Unlock()
	t.Render()
}

// RenderSnapshot returns the document and dock lines at width.
func (t *TuiSurface) RenderSnapshot(width int) []string {
	t.lockFrame()
	defer t.unlockFrame()
	var lines []string
	for _, root := range []Component{t.document, t.dock} {
		if root != nil {
			lines = append(lines, root.Render(width)...)
		}
	}
	return lines
}

// SetClearOnShrink has no effect: the session lays out its own document.
func (t *TuiSurface) SetClearOnShrink(bool) {}

// SetShowHardwareCursor records the setting; the session draws no hardware
// cursor.
func (t *TuiSurface) SetShowHardwareCursor(enabled bool) {
	t.mu.Lock()
	t.showHardwareCursor = enabled
	t.mu.Unlock()
}

// regionWidth caps a session's column count at the terminal width.
func regionWidth(cols, width int) int {
	if cols <= 0 || cols > width {
		return width
	}
	return cols
}

func (t *TuiSurface) doRender() {
	mainCols, dockCols := t.session.Columns()
	t.lockFrame()
	if t.stopped || !t.started {
		t.unlockFrame()
		return
	}
	t.lastRenderAt = t.now()
	t.updateSize()
	width, height := max(1, t.width), max(1, t.height)
	widthChanged := t.hasRendered && t.prevWidth != width
	heightChanged := t.hasRendered && t.prevHeight != height

	var ops []frontend.Op
	if t.resend {
		ops = appendRemoveAll(ops, frontend.RegionMain, t.main)
		ops = appendRemoveAll(ops, frontend.RegionDock, t.docked)
		ops = appendRemoveAll(ops, frontend.RegionOverlay, t.overlays)
		t.main, t.docked, t.overlays = nil, nil, nil
		t.resend = false
		t.fullRedraws++
		t.mainGen++
	}
	ops = t.updateMain(ops, regionWidth(mainCols, width))
	overlays, capturing := t.overlayEntries(regionWidth(dockCols, width), height)
	var dock []surfaceEntry
	if t.dock != nil {
		dock = t.dockEntries(regionWidth(dockCols, width), capturing)
	}
	ops, t.docked = diffSurfaceRegion(ops, frontend.RegionDock, t.docked, dock)
	ops, t.overlays = diffSurfaceRegion(ops, frontend.RegionOverlay, t.overlays, overlays)
	var sessionFile *string
	if t.hooks.SessionFile != nil {
		if file := t.hooks.SessionFile(); !t.sessionFileSent || file != t.sessionFile {
			t.sessionFile, t.sessionFileSent = file, true
			sessionFile = new(file)
		}
	}
	palette := t.framePalette()
	mark := t.frameMark()
	t.hasRendered = true
	t.prevWidth, t.prevHeight = width, height
	t.prevMainCols, t.prevDockCols = mainCols, dockCols
	onWidth, onHeight := t.onWidthChange, t.onHeightChange
	session, onApplyError := t.session, t.onApplyError
	t.unlockFrame()

	if len(ops) > 0 || sessionFile != nil || palette != nil || mark != nil {
		if err := session.Apply(frontend.Frame{Ops: ops, SessionFile: sessionFile, Theme: palette, Mark: mark}); err != nil && onApplyError != nil {
			onApplyError(err)
		}
	}
	if widthChanged && onWidth != nil {
		go onWidth(width)
	}
	if heightChanged && onHeight != nil {
		go onHeight(height)
	}
}

// pig additive (D91): a frame carries the palette of the theme PiG draws
// with when it changed, for a frontend that draws in colors of its own.

// framePalette returns the active theme's palette when it differs from the
// one the last frame carried, and nil otherwise. It rebuilds the palette
// only after the theme or the terminal's colors changed.
func (t *TuiSurface) framePalette() *frontend.Theme {
	theme, terminal := ActiveTheme(), currentTerminalColors()
	if t.palette != nil && theme == t.paletteTheme && terminal == t.paletteTerminal {
		return nil
	}
	t.paletteTheme, t.paletteTerminal = theme, terminal
	next := surfacePalette(theme)
	if prev := t.palette; prev != nil && prev.Name == next.Name && prev.Dark == next.Dark && maps.Equal(prev.Colors, next.Colors) {
		return nil
	}
	t.palette = &next
	sent := next
	sent.Colors = maps.Clone(next.Colors)
	return &sent
}

// pig additive (D91): a frame carries the application's mark when it
// changed, for a frontend that shows a brand of its own.

// frameMark returns the mark the Mark hook reports when it differs from the
// one the last frame carried, as a copy, and nil otherwise.
func (t *TuiSurface) frameMark() *frontend.Mark {
	if t.hooks.Mark == nil {
		return nil
	}
	next := t.hooks.Mark()
	if t.markSent && next.Width == t.mark.Width && next.Height == t.mark.Height && slices.Equal(next.Pixels, t.mark.Pixels) {
		return nil
	}
	t.mark, t.markSent = next, true
	sent := next
	sent.Pixels = slices.Clone(next.Pixels)
	return &sent
}

// surfacePalette is the palette of theme: each token the theme sets, as
// Colors resolves it, so a faint token is mixed toward the background,
// and its export colors, all as #rrggbb. A token at the terminal's default
// color, or at one of its sixteen ANSI colors, follows the terminal's own
// palette and is left out.
func surfacePalette(theme *Theme) frontend.Theme {
	values := theme.Colors()
	colors := make(map[string]string, len(theme.concreteColors)+len(themeExportTokens))
	for token, concrete := range theme.concreteColors {
		if indexed, ok := concrete.(IndexedColor); ok && indexed.Index < 16 {
			continue
		}
		colors[token] = ColorToHex(values[token])
	}
	for i, value := range []string{theme.ExportPageBg, theme.ExportCardBg, theme.ExportInfoBg} {
		if color, err := ParseColor(value); err == nil {
			colors[themeExportTokens[i]] = ColorToHex(color)
		}
	}
	return frontend.Theme{Name: theme.Name, Dark: theme.Appearance() == "dark", Colors: colors}
}

// dockEntries reports the dock as one Lines node. While the input editor, or
// a native component in its place, is mounted in the dock and no overlay
// that takes the keys shows, the dock is instead the lines above it, the
// working indicator, the editor or the native component's node, the lines
// below it, and the footer. A standalone status indicator draws as the
// working node, not as lines.
func (t *TuiSurface) dockEntries(width int, capturing bool) []surfaceEntry {
	var editor *Editor
	if t.hooks.Editor != nil {
		editor = t.hooks.Editor()
	}
	if editor != nil && (editor.IsRemote() || !mountsEditor(t.dock, editor)) {
		editor = nil
	}
	var native NativeComponent
	var nativeNode frontend.Node
	if editor == nil && !capturing {
		native, nativeNode = dockSlot(t.dock)
	}
	if native == nil {
		t.nativeComp, t.nativeIDText = nil, ""
	}
	if capturing || (editor == nil && native == nil) {
		lines := t.dock.Render(width)
		node := frontend.Lines{Lines: surfaceLines(lines)}
		if !capturing {
			// pig additive (D91): a component in the editor's place, such
			// as an extension's ui.custom without overlay, keeps its view.
			node.View = t.dockBlobView("dock", lines, width)
		}
		return []surfaceEntry{{id: "dock", node: node}}
	}
	var working frontend.Working
	hasWorking := false
	if t.hooks.Working != nil {
		working, hasWorking = t.hooks.Working()
	}
	var footer frontend.Footer
	hasFooter := false
	var above, below []string
	var aboveParts, belowParts []dockPart
	slot := surfaceEntry{id: "editor"}
	found := false
	var walk func(Component)
	walk = func(c Component) {
		if container, ok := c.(*Container); ok {
			children := container.Children()
			// A container that holds the native component frames it, as
			// the settings frame does; like the editor's, its borders are
			// not drawn.
			framed := native != nil && slices.Contains(children, Component(native))
			for _, child := range children {
				if _, border := child.(*DynamicBorder); framed && border {
					continue
				}
				walk(child)
			}
			return
		}
		switch {
		case editor != nil && c == Component(editor):
			node, rest := editor.frontendParts(width)
			node.Sendable = t.hooks.EditorSendable != nil && t.hooks.EditorSendable()
			slot.node = node
			below = append(below, rest...)
			found = true
			return
		case native != nil && c == Component(native):
			slot = surfaceEntry{id: t.nativeID(native), node: nativeNode}
			found = true
			return
		}
		if _, ok := c.(*StatusIndicator); ok && hasWorking {
			return
		}
		if t.hooks.Footer != nil {
			if f, ok := t.hooks.Footer(c); ok {
				footer, hasFooter = f, true
				return
			}
		}
		lines := c.Render(width)
		part := dockPart{component: c, lines: lines, view: surfaceView(c, width, lines)}
		if found {
			below = append(below, lines...)
			belowParts = append(belowParts, part)
		} else {
			above = append(above, lines...)
			aboveParts = append(aboveParts, part)
		}
	}
	walk(t.dock)
	entries := make([]surfaceEntry, 0, 5)
	entries = append(entries, surfaceEntry{id: "dock", node: frontend.Lines{Lines: surfaceLines(above), View: t.dockPartsView("dock", aboveParts, width)}})
	if hasWorking {
		entries = append(entries, surfaceEntry{id: "working", node: working})
	}
	belowView := t.dockPartsView("dock.below", belowParts, width)
	if len(below) != dockPartsRows(belowParts) {
		belowView = nil // the editor's completion rows lead the blob
	}
	entries = append(entries,
		slot,
		surfaceEntry{id: "dock.below", node: frontend.Lines{Lines: surfaceLines(below), View: belowView}},
	)
	if hasFooter {
		entries = append(entries, surfaceEntry{id: "footer", node: footer})
	}
	return entries
}

// overlayEntries reports each visible overlay, bottom to top, as its lines at
// the width Pi lays it out at, and whether one of them takes the keys from a
// dock the session shows: a fullscreen overlay over a session that shows the
// dock itself takes the keys but leaves the dock's nodes as they are.
func (t *TuiSurface) overlayEntries(width, height int) (entries []surfaceEntry, capturing bool) {
	t.applyOverlayCommand(overlayCommand{kind: overlayGeometryChanged, width: width, height: height})
	t.refreshOverlayVisibility(width, height)
	clear(t.viewSources)
	clear(t.dockLayouts)
	snapshot := t.overlaySnapshot()
	if !snapshot.VisibleAny {
		return nil, false
	}
	screen, hasScreen := t.Screen()
	for i := range snapshot.Entries {
		entry := &snapshot.Entries[i]
		if !entry.visible() {
			continue
		}
		// pig additive (D91): a fullscreen overlay covers the session's
		// screen, and only a session that shows one gets it.
		fullscreen := entry.opts.Fullscreen && hasScreen
		layoutWidth, layoutHeight := width, height
		if fullscreen {
			layoutWidth, layoutHeight = screen.Columns, screen.Rows
		}
		layout := resolveOverlayLayout(entry.opts, 0, layoutWidth, layoutHeight)
		lines := renderOverlayEntry(entry, layout.width, layout.maxHeight, layout.hasMaxHeight)
		anchor := string(entry.opts.anchor)
		if anchor == "" {
			anchor = string(overlayCenter)
		}
		node := frontend.Overlay{
			Lines: surfaceLines(lines), Width: layout.width, Anchor: anchor, NonCapturing: entry.opts.nonCapturing,
			Fullscreen: fullscreen, View: surfaceView(entry.component, layout.width, lines),
		}
		if _, ok := entry.component.(MouseHandler); ok && fullscreen {
			node.Click = &frontend.Area{Rows: len(lines), Columns: layout.width}
		}
		id := "overlay." + strconv.FormatUint(uint64(entry.id), 10)
		if node.View != nil && node.View.Focus != "" {
			t.recordViewSource(id, entry.component, layout.width)
		}
		entries = append(entries, surfaceEntry{id: id, node: node})
		// pig additive (D91): the session draws its own dock under a
		// fullscreen overlay, so the dock keeps its editor and footer nodes.
		capturing = capturing || (!entry.opts.nonCapturing && (!fullscreen || !screen.ShowsDock))
	}
	return entries, capturing
}

// mountsEditor reports whether root is editor or mounts it through
// containers.
func mountsEditor(root Component, editor *Editor) bool {
	switch c := root.(type) {
	case *Editor:
		return c == editor
	case *Container:
		return slices.ContainsFunc(c.Children(), func(child Component) bool { return mountsEditor(child, editor) })
	}
	return false
}

// pig additive (D91): a frontend session renders the editor's text natively
// and edits it through Env.Edit and Env.Undo.

// frontendParts renders the editor as Render does and returns its text as an
// Editor node and the completion list drawn below it. The frontend frames the
// text itself, so neither border is drawn; the working status that the top
// border carries arrives as its own node.
func (e *Editor) frontendParts(width int) (node frontend.Editor, below []string) {
	lines := e.Render(width)
	return frontend.Editor{Text: e.Text(), Cursor: e.cursorOffset()}, lines[2+e.renderedVisibleLineCount:]
}

// cursorOffset is the cursor's offset in Text in UTF-16 code units.
func (e *Editor) cursorOffset() int {
	offset := e.cursor[1]
	for _, line := range e.lines[:e.cursor[0]] {
		offset += jsstring.Length(line) + 1
	}
	return offset
}

// setCursorOffset puts the cursor at a UTF-16 offset into Text, clamped to
// the text.
func (e *Editor) setCursorOffset(offset int) {
	offset = max(0, offset)
	for i, line := range e.lines {
		n := jsstring.Length(line)
		if offset <= n || i == len(e.lines)-1 {
			e.cursor[0] = i
			e.setCursorCol(min(offset, n))
			return
		}
		offset -= n + 1
	}
}

// ApplyEdit replaces the UTF-16 code units [from, to) of Text with text as
// one undo step, then puts the cursor at the UTF-16 offset cursor of the
// result. Offsets are clamped to the text. An edit that leaves the text as it
// was only moves the cursor. Inserted text is normalized as typed text is, and
// a cursor after the insertion moves with any change in its length.
func (e *Editor) ApplyEdit(from, to int, text string, cursor int) {
	if e.remote != nil {
		return
	}
	current := e.Text()
	length := jsstring.Length(current)
	from = min(max(0, from), length)
	to = min(max(from, to), length)
	inserted := normalizeEditorText(text)
	if cursor >= from+jsstring.Length(text) {
		cursor += jsstring.Length(inserted) - jsstring.Length(text)
	}
	next := jsstring.Canonical(jsstring.Slice(current, 0, from) + inserted + jsstring.Slice(current, to))
	e.lastAction = ""
	if next == current {
		e.setCursorOffset(cursor)
		e.Invalidate()
		return
	}
	e.saveHistory()
	e.lines = strings.Split(next, "\n")
	e.inputHistIdx = -1
	e.inputHistSaved = nil
	e.setCursorOffset(cursor)
	e.Invalidate()
	e.refreshAutocomplete()
	e.notifyChange()
}

// Undo reverts the last change through the editor's undo history, as the
// undo key does.
func (e *Editor) Undo() {
	if e.remote != nil {
		return
	}
	e.lastAction = ""
	e.undo()
}

// appendNodes appends the nodes of the leaf component c, whose id is id and
// whose retained state is state.
func (t *TuiSurface) appendNodes(entries []surfaceEntry, id string, state *surfaceComponent, c Component, width int) []surfaceEntry {
	switch c := c.(type) {
	case *ToolExecutionComponent:
		return append(entries, surfaceEntry{id: id, node: c.frontendTool(width, t.hooks.ToolDiff)})
	case *UserMessageComponent:
		if node, ok := c.frontendNode(); ok {
			return append(entries, surfaceEntry{id: id, node: node})
		}
		return entries
	case *AssistantMessageComponent:
		var partIDs *[]string
		if state != nil {
			partIDs = &state.partIDs
		}
		return c.appendFrontendNodes(entries, id, partIDs, width, t.streaming == c)
	}
	lines := c.Render(width)
	return append(entries, surfaceEntry{id: id, node: linesNode(state, lines, clickArea(c), surfaceView(c, width, lines))})
}

// TranscriptNodes returns the nodes the main transcript reports for c laid
// out at width: a user message's MarkdownText, an assistant message's parts
// (streaming marks it as being generated), or a tool card's ToolCard, with
// toolDiff the session's ToolDiff hook. Another component has none. An
// extension's view of Pi's conversation components (D107) carries them.
func TranscriptNodes(c Component, width int, streaming bool, toolDiff func(string, map[string]any, any) *frontend.Diff) []frontend.Node {
	switch c := c.(type) {
	case *ToolExecutionComponent:
		return []frontend.Node{c.frontendTool(width, toolDiff)}
	case *UserMessageComponent:
		if node, ok := c.frontendNode(); ok {
			return []frontend.Node{node}
		}
	case *AssistantMessageComponent:
		entries := c.appendFrontendNodes(nil, "", nil, width, streaming)
		nodes := make([]frontend.Node, len(entries))
		for i, entry := range entries {
			nodes[i] = entry.node
		}
		return nodes
	}
	return nil
}

// frontendNode is the message's MarkdownText with the images it carries; an
// empty message draws no Markdown lines and has none.
func (u *UserMessageComponent) frontendNode() (frontend.MarkdownText, bool) {
	if strings.TrimSpace(u.content) == "" {
		return frontend.MarkdownText{}, false
	}
	return frontend.MarkdownText{Role: frontend.RoleUser, Text: u.content, Images: u.frontendImages()}, true
}

// frontendImages decodes the message's images on a frontend's first use,
// so the terminal renderer never pays for them, and returns the same slice
// after. pig additive (D91): Pi draws no user message images.
func (u *UserMessageComponent) frontendImages() []frontend.ViewImage {
	if u.decoded == nil && len(u.images) > 0 {
		u.decoded = make([]frontend.ViewImage, len(u.images))
		for i, block := range u.images {
			u.decoded[i] = frontendImage(block)
		}
	}
	return u.decoded
}

// linesNode returns the Lines node of a component's rendered lines and Click
// area, and reuses the node built last frame while both are unchanged. The
// state keeps its own copy of the lines, so a component that rewrites its
// slice in place still gets a new node. pig additive (D91): the Click area
// is a [Clickable] component's.
func linesNode(state *surfaceComponent, lines []string, click *frontend.Area, view *frontend.View) frontend.Lines {
	if state == nil {
		return frontend.Lines{Lines: surfaceLines(lines), Click: click, View: view}
	}
	if state.rendered != nil && slices.Equal(state.rendered, lines) && areasEqual(state.lines.Click, click) && viewsEqual(state.lines.View, view) {
		return state.lines
	}
	state.rendered = slices.Clone(lines)
	state.lines = frontend.Lines{Lines: surfaceLines(lines), Click: click, View: view}
	return state.lines
}

// appendFrontendNodes appends one node per part of the message, in the order
// Render draws them: each text block as MarkdownText and each thinking run as
// Thinking, both as their source, and the terminal error line as the lines
// Render draws for it, without the padding and spacers the frontend lays out
// itself. Part i of the content has the id "<id>.<i>" and the error line
// "<id>.e", so a part keeps its node while later parts stream in. partIDs,
// if set, keeps the part ids from one build to the next.
func (b *AssistantMessageComponent) appendFrontendNodes(entries []surfaceEntry, id string, partIDs *[]string, width int, streaming bool) []surfaceEntry {
	run := 0
	for i, seg := range b.segments {
		var partID string
		switch {
		case partIDs == nil:
			partID = id + "." + strconv.Itoa(i)
		case i < len(*partIDs):
			partID = (*partIDs)[i]
		default:
			partID = id + "." + strconv.Itoa(i)
			*partIDs = append(*partIDs, partID)
		}
		last := streaming && i == len(b.segments)-1
		if !seg.thinking {
			entries = append(entries, surfaceEntry{id: partID, node: frontend.MarkdownText{Role: frontend.RoleAssistant, Text: seg.md.Content, Streaming: last}})
			continue
		}
		// updateContent decides a run's visibility the same way.
		hidden, overridden := b.thinkingVisibilityOverrides[run]
		if !overridden {
			hidden = b.hidden
		}
		run++
		entries = append(entries, surfaceEntry{id: partID, node: frontend.Thinking{Text: seg.md.Content, Hidden: hidden, Streaming: last}})
	}
	if b.hasTerminalError() {
		entries = append(entries, surfaceEntry{id: id + ".e", node: frontend.Lines{Lines: surfaceLines(wrapText(b.terminalErrorText(), max(1, width-b.outputPad*2)))}})
	}
	return entries
}

// surfaceLines removes the cursor marker and OSC 133 zone prefixes, which only
// a terminal grid interprets.
func surfaceLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.ReplaceAll(stripOsc133ZonePrefix(line), widthx.CursorMarker, "")
	}
	return out
}

func (c *ToolExecutionComponent) frontendTool(width int, toolDiff func(string, map[string]any, any) *frontend.Diff) frontend.ToolCard {
	tool := frontend.ToolCard{
		Name:      c.Name,
		Arguments: c.decodedArguments(),
		Header:    widthx.StripAnsi(c.ArgsPreview),
		Output:    c.Output,
		Elapsed:   c.Elapsed,
		Expanded:  !c.Collapsed,
	}
	switch {
	case c.State == ToolStateError && c.aborted:
		tool.Status = frontend.ToolCancelled
	case c.State == ToolStateError:
		tool.Status = frontend.ToolError
	case c.State == ToolStateDone:
		tool.Status = frontend.ToolDone
	case c.executionStarted:
		tool.Status = frontend.ToolRunning
	default:
		tool.Status = frontend.ToolPending
	}
	if c.definition != nil {
		if c.definitionDirty.Load() || c.definitionCall == nil {
			c.updateDefinition()
		}
		tool.Result = []string{}
		if c.definitionResultComponent != nil {
			rendered := c.definitionResultComponent.Render(width)
			tool.Result = surfaceLines(rendered)
			tool.ResultView = surfaceView(c.definitionResultComponent, width, rendered)
		}
	}
	if tool.Status == frontend.ToolDone && toolDiff != nil {
		tool.Diff = toolDiff(c.Name, tool.Arguments, c.definitionResult)
	}
	tool.Images = c.frontendImagesAt(width) // pig additive (D91): a frontend draws the result's images natively
	return tool
}

// frontendImagesAt returns the images a frontend draws after the card's
// output at width: the image blocks Pi's updateDisplay shows, those with
// data and a MIME type while images show (tool-execution.ts:338-341), each
// MaxWidthCells wide as renderImages draws it. Pi also requires a terminal
// image protocol there; a frontend draws the images itself, so its own
// capability decides. Each image is decoded and hashed once, and the same
// slice returns while the blocks and the width stay.
func (c *ToolExecutionComponent) frontendImagesAt(width int) []frontend.ViewImage {
	if !c.ShowImages || len(c.ImageBlocks) == 0 {
		return nil
	}
	maxW := imageMaxWidthCells(width, c.ImageWidthCells)
	if maxW == c.frontendImageWidth && slices.Equal(c.ImageBlocks, c.frontendImageSources) {
		return c.frontendImages
	}
	prev, prevSources := c.frontendImages, c.frontendImageSources
	var images []frontend.ViewImage
	for _, block := range c.ImageBlocks {
		if block.Data == "" || block.MIMEType == "" {
			continue
		}
		var img frontend.ViewImage
		if i := slices.Index(prevSources, block); i >= 0 {
			img = prev[shownImageIndex(prevSources, i)]
		} else {
			img = frontendImage(block)
		}
		img.MaxWidthCells = maxW
		images = append(images, img)
	}
	c.frontendImages, c.frontendImageSources, c.frontendImageWidth = images, c.ImageBlocks, maxW
	return images
}

// frontendImage is block decoded for a frontend, identified by the SHA-256
// of its bytes. Undecodable data keeps no bytes: the frontend shows Pi's
// text in its place.
func frontendImage(block ImageBlock) frontend.ViewImage {
	img := frontend.ViewImage{MimeType: block.MIMEType}
	if data, err := base64.StdEncoding.DecodeString(block.Data); err == nil {
		sum := sha256.Sum256(data)
		img.Ref, img.Data = hex.EncodeToString(sum[:]), data
	}
	return img
}

// shownImageIndex is the index, among the blocks a frontend shows, of
// blocks[i]: blocks without data or a MIME type are not shown.
func shownImageIndex(blocks []ImageBlock, i int) int {
	n := 0
	for _, block := range blocks[:i] {
		if block.Data != "" && block.MIMEType != "" {
			n++
		}
	}
	return n
}

func appendRemoveAll(ops []frontend.Op, region frontend.Region, entries []surfaceEntry) []frontend.Op {
	for i, entry := range slices.Backward(entries) {
		ops = append(ops, frontend.Op{Kind: frontend.Remove, Region: region, ID: entry.id, Index: i})
	}
	return ops
}

// diffSurfaceMain appends the ops that turn prev into next, the ops
// diffSurfaceRegion appends, but compares only the run between the longest
// prefix and suffix of entries that were not rebuilt, which are equal
// without a comparison.
func diffSurfaceMain(ops []frontend.Op, prev, next []surfaceEntry) []frontend.Op {
	same := func(a, b surfaceEntry) bool { return a.ver != 0 && a.ver == b.ver }
	lo := 0
	for lo < len(prev) && lo < len(next) && same(prev[lo], next[lo]) {
		lo++
	}
	hi := 0
	for hi < len(prev)-lo && hi < len(next)-lo && same(prev[len(prev)-1-hi], next[len(next)-1-hi]) {
		hi++
	}
	ops, _ = diffSurfaceWindow(ops, frontend.RegionMain, prev[lo:len(prev)-hi], next[lo:len(next)-hi], lo)
	return ops
}

// diffSurfaceRegion appends the ops that turn prev into next and returns next
// as the new retained state. Removed ids go first, from the end, then each
// position is updated in place, moved by remove and insert, or inserted.
func diffSurfaceRegion(ops []frontend.Op, region frontend.Region, prev, next []surfaceEntry) ([]frontend.Op, []surfaceEntry) {
	return diffSurfaceWindow(ops, region, prev, next, 0)
}

// diffSurfaceWindow is diffSurfaceRegion for a run of the region that starts
// at index offset, where no id of the run appears outside it.
func diffSurfaceWindow(ops []frontend.Op, region frontend.Region, prev, next []surfaceEntry, offset int) ([]frontend.Op, []surfaceEntry) {
	// The common frame keeps every id in place and only updates nodes.
	if slices.EqualFunc(prev, next, func(a, b surfaceEntry) bool { return a.id == b.id }) {
		for i, entry := range next {
			if !entriesEqual(prev[i], entry) {
				ops = append(ops, frontend.Op{Kind: frontend.Update, Region: region, ID: entry.id, Index: offset + i, Node: entry.node})
			}
		}
		return ops, next
	}
	keep := make(map[string]bool, len(next))
	for _, entry := range next {
		keep[entry.id] = true
	}
	work := make([]surfaceEntry, 0, len(prev))
	for i, entry := range slices.Backward(prev) {
		if !keep[entry.id] {
			ops = append(ops, frontend.Op{Kind: frontend.Remove, Region: region, ID: entry.id, Index: offset + i})
		}
	}
	for _, entry := range prev {
		if keep[entry.id] {
			work = append(work, entry)
		}
	}
	for i, entry := range next {
		switch {
		case i < len(work) && work[i].id == entry.id:
			if !entriesEqual(work[i], entry) {
				ops = append(ops, frontend.Op{Kind: frontend.Update, Region: region, ID: entry.id, Index: offset + i, Node: entry.node})
			}
			work[i] = entry
			continue
		default:
			if at := slices.IndexFunc(work, func(e surfaceEntry) bool { return e.id == entry.id }); at >= 0 {
				ops = append(ops, frontend.Op{Kind: frontend.Remove, Region: region, ID: entry.id, Index: offset + at})
				work = slices.Delete(work, at, at+1)
			}
			ops = append(ops, frontend.Op{Kind: frontend.Insert, Region: region, ID: entry.id, Index: offset + i, Node: entry.node})
			work = slices.Insert(work, i, entry)
		}
	}
	return ops, work
}

// entriesEqual reports whether two entries with the same id hold equal
// nodes. An entry the surface did not rebuild keeps its build number, which
// spares comparing its node.
func entriesEqual(a, b surfaceEntry) bool {
	return (a.ver != 0 && a.ver == b.ver) || nodesEqual(a.node, b.node)
}

// nodesEqual reports whether two nodes are deeply equal. It compares Lines,
// the most frequent node, the message and editor nodes, and the Working,
// Footer, Selector, Settings and Overlay nodes that every dock frame
// rebuilds without reflect.DeepEqual, which allocates on every call.
// Lines built by surfaceLines are never nil, so the nil and empty slices that
// DeepEqual tells apart do not occur.
func nodesEqual(a, b frontend.Node) bool {
	switch a := a.(type) {
	case frontend.Lines:
		b, ok := b.(frontend.Lines)
		return ok && slices.Equal(a.Lines, b.Lines) && areasEqual(a.Click, b.Click) && viewsEqual(a.View, b.View)
	case frontend.MarkdownText:
		b, ok := b.(frontend.MarkdownText)
		return ok && a.Role == b.Role && a.Text == b.Text && a.Streaming == b.Streaming && imagesEqual(a.Images, b.Images)
	case frontend.Thinking:
		b, ok := b.(frontend.Thinking)
		return ok && a == b
	case frontend.Editor:
		b, ok := b.(frontend.Editor)
		return ok && a == b
	case frontend.Working:
		b, ok := b.(frontend.Working)
		return ok && a.Kind == b.Kind && a.Message == b.Message && a.Interval == b.Interval &&
			(a.Frames == nil) == (b.Frames == nil) && slices.Equal(a.Frames, b.Frames)
	case frontend.Footer:
		b, ok := b.(frontend.Footer)
		return ok && footersEqual(a, b)
	case frontend.Selector:
		b, ok := b.(frontend.Selector)
		return ok && selectorsEqual(a, b)
	case frontend.Settings:
		b, ok := b.(frontend.Settings)
		return ok && settingsEqual(a, b)
	case frontend.Overlay:
		b, ok := b.(frontend.Overlay)
		return ok && overlaysEqual(a, b)
	}
	return reflect.DeepEqual(a, b)
}

// imagesEqual compares images by what a frontend draws: their bytes, which
// Ref identifies, their MIME type and their bounds.
func imagesEqual(a, b []frontend.ViewImage) bool {
	return slices.EqualFunc(a, b, func(x, y frontend.ViewImage) bool {
		return x.Ref == y.Ref && len(x.Data) == len(y.Data) && x.MimeType == y.MimeType && x.Filename == y.Filename &&
			x.MaxWidthCells == y.MaxWidthCells && x.MaxHeightCells == y.MaxHeightCells
	})
}

// footersEqual compares two footers field by field; a NaN cost equals a NaN
// cost, so it does not repeat an update every frame.
func footersEqual(a, b frontend.Footer) bool {
	sameCost := a.UsageTotals.Cost == b.UsageTotals.Cost || (math.IsNaN(a.UsageTotals.Cost) && math.IsNaN(b.UsageTotals.Cost))
	sameRate := a.LatestCacheHitRate == b.LatestCacheHitRate ||
		(a.LatestCacheHitRate != nil && b.LatestCacheHitRate != nil && *a.LatestCacheHitRate == *b.LatestCacheHitRate)
	a.UsageTotals.Cost, b.UsageTotals.Cost = 0, 0
	return sameCost && sameRate &&
		a.Cwd == b.Cwd && a.GitBranch == b.GitBranch && a.SessionName == b.SessionName &&
		a.UsageTotals == b.UsageTotals && a.UsingSubscription == b.UsingSubscription &&
		a.ContextUsage == b.ContextUsage && a.AutoCompact == b.AutoCompact && a.Experimental == b.Experimental &&
		a.Model == b.Model && a.Provider == b.Provider && a.ThinkingLevel == b.ThinkingLevel && a.Routed == b.Routed &&
		(a.ExtensionStatuses == nil) == (b.ExtensionStatuses == nil) && slices.Equal(a.ExtensionStatuses, b.ExtensionStatuses)
}
