package runtime

// TuiRuntime: the host UI state outside replaceable facets (selection, drafts, scroll positions survive a reload), the slot surfaces, the layout root
// and the TuiContext handed to commands and overlays.
//
// Forced Go differences: every method is called on the TUI's owner loop (the loop that renders and handles input); callbacks that arrive on other
// goroutines (a watched reference, a registry change) reach the runtime through Post, which queues them on that loop with tui.PostToOwner. A TS
// `focused` field is the Focusable method SetFocused. Slots are registered as *TuiSlot so that a reload's replacement is a different pointer.
//
// pi: apps/p2/src/runtime/tui.ts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// TuiSurface is a mounted slot. The optional parts are separate interfaces the runtime asks for: Focuser, Commander, Contexter, Layouter, StateSaver.
type TuiSurface interface {
	pitui.Component
	// Dispose releases what the surface holds (called once when it leaves).
	Dispose()
}

// Focuser is a surface with a focus target (its editor).
type Focuser interface{ Focus() pitui.Component }

// Commander is a surface that takes named commands.
type Commander interface{ Command(name string) }

// Contexter reports context terms while the surface is the active screen (section 6.3), e.g. `editorFocused`, `editorEmpty`, `busy`.
type Contexter interface{ Contexts() []string }

// SurfaceLayout is the fullscreen layout (P0c): a body that fills the screen (the chat's transcript ScrollView, which carries the header) and a part docked under it
// (pending area, editor). Without it, the header and the surface fill the body. NoHeader is `header: false`.
type SurfaceLayout struct {
	Body     pitui.Component
	Dock     pitui.Component
	NoHeader bool
}

// Layouter is a surface with a fullscreen layout; nil means none.
type Layouter interface{ Layout() *SurfaceLayout }

// StateSaver writes UI state the runtime keeps (the reading position) before a reload snapshot.
type StateSaver interface{ SaveState() }

// IdentityKey is a viewed conversation's key: drafts, submissions and reading positions are kept per identity (P0c).
func IdentityKey(session string, conversation int) string {
	return fmt.Sprintf("%s#%d", session, conversation)
}

// TuiFactory builds a slot's surface.
type TuiFactory func(runtime *TuiRuntime) TuiSurface

// TuiSlot is a registered slot factory; a reload registers a new pointer.
type TuiSlot struct{ Factory TuiFactory }

// TuiSlots are the slot factories by name.
var TuiSlots = p2runtime.NewKeyedRegistry[*TuiSlot]("p2.tui-slots")

// TuiFacet is a TUI facet: one slot and, optionally, its commands (section 7.1); setup declares what else it serves (a registry its plugin defines).
func TuiFacet(name string, factory TuiFactory, commands p2runtime.Keyed[TuiCommand], setup func(facet host.FacetHost) error) host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		if setup != nil {
			if err := setup(facet); err != nil {
				return err
			}
		}
		return p2runtime.Contribute(facet, "p2.tui-slot/"+contractSafe(name), func(self host.StoreSelf) error {
			p2runtime.ContributeKeyed(self, TuiSlots, p2runtime.Entry(name, &TuiSlot{Factory: factory}))
			if len(commands) > 0 {
				p2runtime.ContributeKeyed(self, TuiCommands, commands...)
			}
			return nil
		})
	})
}

// contractSafe maps a slot name (`composer:chip`) onto the characters a contract name allows.
func contractSafe(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '~', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, name)
}

// panelMinColumns: below this many columns the side panel is hidden: the chat would be too narrow next to it.
const panelMinColumns = 100

// Screen is a screen's slot name.
type Screen = string

// Guest is a guest's share grant (name as the guest typed it, the sessions shared with it), from `Access`.
type Guest struct {
	Name string
	// Sessions maps a session to "view" or "full".
	Sessions map[string]string
}

// Submission is a submitted message awaiting its entry.
type Submission struct {
	Text string
	ID   string
	// Mode is "steer" or "followUp".
	Mode string
}

type overlayRecord struct {
	id     string
	handle *pitui.OverlayHandle
	closed bool
	close  func()
}

// TuiRuntime is the runtime.
type TuiRuntime struct {
	focused atomic.Bool
	// Name is this TUI's own name (`tui:<uuid>`): the viewer it counts as in session metrics.
	Name         string
	Session      string
	HasSession   bool
	Conversation int
	// Mains are each session's main conversation (1, or its current `/tree` branch), as the sessions plugin last saw its row.
	Mains     map[string]int
	Screen    Screen
	Connected bool
	// ShowThinking and ThinkingSetting: the `hideThinkingBlock` setting (unset = hidden) that ShowThinking last followed. The toggle command overrides it until the
	// setting itself changes.
	ShowThinking    bool
	ThinkingSetting bool
	ExpandedTools   bool
	// Origin is the server origin (from the launcher), shown in the header.
	Origin string
	// Drafts and Submissions are keyed by IdentityKey; Anchors are reading positions by identity (absent = following the end).
	Drafts      map[string]string
	Submissions map[string]Submission
	Anchors     map[string]any
	// Notices are the status lines (newest last), cleared on navigation.
	Notices []components.Notice
	// Root is the layout root (TuiAltScreen.SetLayoutRoot): body over dock, rebuilt when the screen or its surface changes.
	Root *pitui.VStack

	rootParts     []any
	builtInHeader *components.BuiltInHeader
	builtInFooter *components.BuiltInFooter
	statusLines   *components.StatusLines
	placeholder   *pitui.Text
	surfaces      map[string]TuiSurface
	surfaceOrder  []string
	factories     map[string]*TuiSlot
	slotTable     *p2runtime.KeyedTable[*TuiSlot]
	commandTable  *p2runtime.KeyedTable[TuiCommand]
	sourceTable   *p2runtime.KeyedTable[TuiSlashSource]
	changing      bool
	overlays      []*overlayRecord
	panel         *components.PanelFrame
	panelFocused  bool
	listeners     []*func()
	scope         *chordtui.TuiScope
	sourcesRead   func() (any, bool)
	commandsRead  func() (any, bool)
	slotsRead     func() (any, bool)
	disposed      atomic.Bool

	// OnExit and OnReload are the worker runtime's hooks.
	OnExit   func()
	OnReload func()
	// OnGrant is the launcher bridge for C actions (set by the worker runtime); denied without one (tests, bare hosts). It blocks until the launcher answers, so
	// it is not called from the owner loop's input path.
	OnGrant func(action string, payload any) GrantResult
	// LauncherAnswers answer launcher questions bound to a grant (e.g. P5's `copyText`); unknown questions get nil.
	LauncherAnswers map[string]func(payload any) any
	// Role is the visitor's role, "" until known.
	Role TuiRole
	// Grant is a guest's share grant, nil for the owner.
	Grant *Guest

	Host host.Host
	TUI  pitui.TUI
	// Appearance is the guest-local preferences, terminal colors and the instance theme (sections 4.2, 5.3).
	Appearance *TuiAppearance
	// Commands is the command registry dispatcher and the current keymap (section 6).
	Commands *TuiCommandDispatcher
	// Ctx is the context handed to commands and overlays.
	Ctx *TuiRuntimeContext

	ctx    context.Context
	cancel context.CancelFunc
}

// NewTuiRuntime builds the runtime; initial is the theme to start with, nil to resolve one from the (empty) preferences.
func NewTuiRuntime(h host.Host, ui pitui.TUI, initial *theme.Theme) *TuiRuntime {
	rt := &TuiRuntime{
		Mains: map[string]int{}, Screen: "sessions", Conversation: 1, ThinkingSetting: true,
		Drafts: map[string]string{}, Submissions: map[string]Submission{}, Anchors: map[string]any{},
		LauncherAnswers: map[string]func(any) any{}, surfaces: map[string]TuiSurface{}, factories: map[string]*TuiSlot{},
		Host: h, TUI: ui, placeholder: pitui.NewPaddedText("Loading TUI facets…", 1, 0, nil),
		OnExit: func() {}, OnReload: func() {},
		OnGrant: func(string, any) GrantResult { return GrantResult{Error: "unavailable"} },
	}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	var id [16]byte
	_, _ = rand.Read(id[:])
	id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
	hexed := hex.EncodeToString(id[:])
	rt.Name = "tui:" + hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:]
	rt.Root = pitui.NewVStack(nil, pitui.StackOptions{})
	rt.Commands = NewTuiCommandDispatcher(TuiCommandDispatcherOptions{
		Ctx:       func() TuiContext { return rt.Ctx },
		Contexts:  rt.Contexts,
		Exclusive: func() [][]string { return [][]string{rt.screens(), {"owner", "guest"}} },
		OnError:   rt.Fail,
	})
	rt.Ctx = &TuiRuntimeContext{rt: rt}
	rt.Appearance = NewTuiAppearance(func(appearance *TuiAppearance) {
		rt.Commands.SetUserBindings(appearance.Keybindings, appearance.KeyPlatform())
		hide := true
		if appearance.Settings.HideThinkingBlock != nil {
			hide = *appearance.Settings.HideThinkingBlock
		}
		if hide != rt.ThinkingSetting {
			rt.ThinkingSetting = hide
			rt.ShowThinking = !hide
		}
		rt.TUI.Invalidate()
		rt.Changed()
		rt.TUI.RequestRender(true)
	}, initial)
	rt.builtInHeader = components.NewBuiltInHeader(&headerSource{rt: rt})
	rt.builtInFooter = components.NewBuiltInFooter(rt.helpLine)
	rt.statusLines = components.NewStatusLines(rt.Ctx, func() []components.Notice { return rt.Notices })
	rt.scope = chordtui.NewTuiScope(rt.ctx, h, rt.postChanged)
	return rt
}

// Post queues fn on the TUI's owner loop; the callbacks of watched references and registries use it.
func (rt *TuiRuntime) Post(fn func()) {
	if rt.disposed.Load() {
		return
	}
	_ = rt.TUI.PostToOwner(rt.ctx, func() {
		if !rt.disposed.Load() {
			fn()
		}
	})
}

func (rt *TuiRuntime) postChanged() { rt.Post(rt.reconcileAndChange) }

// SetFocused is Focusable: the runtime is pi-tui's focused component; it forwards focus (the cursor) to the active surface.
func (rt *TuiRuntime) SetFocused(focused bool) {
	rt.focused.Store(focused)
	if focus := rt.focusOf(rt.surfaces[rt.Screen]); focus != nil {
		focus.SetFocused(focused && !rt.panelFocused)
	}
}

// IsFocused is whether the runtime holds pi-tui's focus.
func (rt *TuiRuntime) IsFocused() bool { return rt.focused.Load() }

// CapturesViewportInput: while the panel has the focus, pi-tui's viewport keys come here (to scroll it) instead of the transcript.
func (rt *TuiRuntime) CapturesViewportInput() bool { return rt.panelFocused }

func (rt *TuiRuntime) focusOf(surface TuiSurface) pitui.Focusable {
	if surface == nil {
		return nil
	}
	if focuser, ok := surface.(Focuser); ok {
		if focusable, ok := focuser.Focus().(pitui.Focusable); ok {
			return focusable
		}
	}
	return nil
}

// GuestMode is a guest's mode in the session it views: "view" or "full"; "" for the owner or outside a shared session.
func (rt *TuiRuntime) GuestMode() string {
	if !rt.HasSession || rt.Grant == nil {
		return ""
	}
	return rt.Grant.Sessions[rt.Session]
}

// Identity is the viewed conversation's key, "" off a session.
func (rt *TuiRuntime) Identity() string {
	if !rt.HasSession {
		return ""
	}
	return IdentityKey(rt.Session, rt.Conversation)
}

// Header is a plugin's `header` slot, else the built-in one. Chat shows it at the top of its transcript.
func (rt *TuiRuntime) Header() pitui.Component {
	if surface, ok := rt.surfaces["header"]; ok {
		return surface
	}
	return rt.builtInHeader
}

// Notify adds a status line; an identical last line collapses into a count (pi's showStatus).
func (rt *TuiRuntime) Notify(message string, kind NoticeKind) {
	text := render.LineText(message)
	k := components.NoticeKind(kind)
	if n := len(rt.Notices); n > 0 && rt.Notices[n-1].Text == text && rt.Notices[n-1].Kind == k {
		rt.Notices[n-1].Count++
	} else {
		rt.Notices = append(slices.Clone(rt.Notices), components.Notice{Kind: k, Text: text, Count: 1})
		if len(rt.Notices) > 3 {
			rt.Notices = rt.Notices[len(rt.Notices)-3:]
		}
	}
	rt.Changed()
}

// Slots are the mounted slot surfaces whose key starts with prefix (`composer:` chips, section 7.2), in key order.
func (rt *TuiRuntime) Slots(prefix string) []TuiSurface {
	var names []string
	for name := range rt.surfaces {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	slices.SortStableFunc(names, jsvalue.LocaleCompareNumeric)
	out := make([]TuiSurface, len(names))
	for i, name := range names {
		out[i] = rt.surfaces[name]
	}
	return out
}

// Surface is the mounted surface of a slot name, nil when none.
func (rt *TuiRuntime) Surface(name string) TuiSurface { return rt.surfaces[name] }

// SaveState lets surfaces store what a reload snapshot carries (see src/tui/snapshot.ts).
func (rt *TuiRuntime) SaveState() {
	for _, name := range rt.surfaceOrder {
		if saver, ok := rt.surfaces[name].(StateSaver); ok {
			saver.SaveState()
		}
	}
}

// ClearNotices drops the status lines.
func (rt *TuiRuntime) ClearNotices() {
	if len(rt.Notices) == 0 {
		return
	}
	rt.Notices = nil
	rt.Changed()
}

// Keys is the current keymap, rebuilt (and installed with SetKeybindings) when commands or preferences change.
func (rt *TuiRuntime) Keys() *pitui.TUIKeybindingsManager { return rt.Commands.Manager() }

// Theme is the instance theme.
func (rt *TuiRuntime) Theme() *theme.ThemeRef { return rt.Appearance.Theme }

// AddListener registers a change listener and returns its remover.
func (rt *TuiRuntime) AddListener(listener func()) func() {
	entry := &listener
	rt.listeners = append(rt.listeners, entry)
	return func() {
		rt.listeners = slices.DeleteFunc(rt.listeners, func(each *func()) bool { return each == entry })
	}
}

// screens are the mutually exclusive screen context terms.
func (rt *TuiRuntime) screens() []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if seen[name] || name == "status" || name == "header" || name == "footer" || strings.Contains(name, ":") {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range []string{"sessions", "chat", "models"} {
		add(name)
	}
	names := make([]string, 0, len(rt.factories))
	for name := range rt.factories {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		add(name)
	}
	return out
}

// Contexts is the active context set (section 6.3): screen, role, overlay, and the active screen's own terms.
func (rt *TuiRuntime) Contexts() map[string]bool {
	active := map[string]bool{rt.Screen: true}
	if rt.Role != "" {
		active[string(rt.Role)] = true
	}
	if rt.Connected {
		active["connected"] = true
	}
	for i := len(rt.overlays) - 1; i >= 0; i-- {
		if top := rt.overlays[i]; top.handle.IsFocused() {
			active["overlay"] = true
			active["overlay:"+top.id] = true
			break
		}
	}
	if rt.panelShown() {
		active["panel"] = true
	}
	if rt.panelFocused {
		active["panelFocused"] = true
	}
	if contexter, ok := rt.surfaces[rt.Screen].(Contexter); ok {
		for _, term := range contexter.Contexts() {
			active[term] = true
		}
	}
	return active
}

// overlayWrapper is a pi-tui overlay whose focused input passes overlay-scoped commands first (section 6.2 stage 3).
type overlayWrapper struct {
	rt        *TuiRuntime
	component pitui.Component
}

func (w *overlayWrapper) Render(width int) []string { return w.component.Render(width) }
func (w *overlayWrapper) Invalidate()               { w.component.Invalidate() }
func (w *overlayWrapper) SetFocused(focused bool) {
	if focusable, ok := w.component.(pitui.Focusable); ok {
		focusable.SetFocused(focused)
	}
}
func (w *overlayWrapper) HandleInput(data string) {
	if !w.rt.Commands.Dispatch(data, nil) {
		if handler, ok := w.component.(pitui.InputHandler); ok {
			handler.HandleInput(data)
		}
	}
	w.rt.TUI.RequestRender()
}

type overlayHandle struct {
	record *overlayRecord
}

func (h overlayHandle) ID() string   { return h.record.id }
func (h overlayHandle) Close()       { h.record.close() }
func (h overlayHandle) Closed() bool { return h.record.closed }

// Overlay shows a pi-tui overlay whose focused input passes overlay-scoped commands first (section 6.2 stage 3).
func (rt *TuiRuntime) Overlay(id string, component pitui.Component, options pitui.OverlaySpec) TuiOverlayHandle {
	record := &overlayRecord{id: id}
	record.handle = rt.TUI.ShowOverlay(&overlayWrapper{rt: rt, component: component}, options.Options())
	record.close = func() {
		if record.closed {
			return
		}
		record.closed = true
		record.handle.Hide()
		rt.overlays = slices.DeleteFunc(rt.overlays, func(each *overlayRecord) bool { return each == record })
		// However it closes (its own action, navigation, reload, plugin replacement), it releases what it holds.
		if disposable, ok := component.(Disposable); ok {
			disposable.Dispose()
		}
		rt.Changed()
	}
	rt.overlays = append(rt.overlays, record)
	rt.Changed()
	return overlayHandle{record}
}

// CloseOverlays closes every overlay, newest first.
func (rt *TuiRuntime) CloseOverlays() {
	open := slices.Clone(rt.overlays)
	for i := len(open) - 1; i >= 0; i-- {
		open[i].close()
	}
}

type panelHandle struct {
	rt    *TuiRuntime
	id    string
	frame *components.PanelFrame
}

func (h panelHandle) ID() string { return h.id }
func (h panelHandle) Close() {
	if h.rt.panel == h.frame {
		h.rt.ClosePanel()
	}
}
func (h panelHandle) Closed() bool { return h.rt.panel != h.frame }

// OpenPanel shows content in the side panel, replacing (and disposing) what it showed.
func (rt *TuiRuntime) OpenPanel(id string, content components.PanelContent, title func() string) TuiPanelHandle {
	rt.ClosePanel()
	frame := components.NewPanelFrame(rt.Ctx, id, content, title)
	rt.panel = frame
	rt.Changed()
	return panelHandle{rt: rt, id: id, frame: frame}
}

// ClosePanel closes the side panel.
func (rt *TuiRuntime) ClosePanel() {
	frame := rt.panel
	if frame == nil {
		return
	}
	rt.panel = nil
	rt.FocusPanel(false)
	if disposable, ok := frame.Content.(Disposable); ok {
		disposable.Dispose()
	}
	rt.Changed()
}

// FocusPanel moves the focus into the panel (true) or back to the screen; a hidden or closed panel never takes it.
func (rt *TuiRuntime) FocusPanel(focused bool) {
	next := focused && rt.panelShown()
	if next == rt.panelFocused {
		return
	}
	rt.panelFocused = next
	if rt.panel != nil {
		rt.panel.Focused = next
	}
	rt.SetFocused(rt.focused.Load())
	rt.Changed()
}

// panelShown is whether the panel is open and the terminal wide enough to show it.
func (rt *TuiRuntime) panelShown() bool { return rt.panel != nil && rt.panelFits() }

func (rt *TuiRuntime) panelFits() bool { return rt.TUI.Terminal().Columns() >= panelMinColumns }

// panelInput takes the keys of the focused panel: back to the screen, scrolling; false for keys it does not use.
func (rt *TuiRuntime) panelInput(data string, scroll *pitui.ScrollView) bool {
	page := max(1, scroll.ViewportHeight()-1)
	half := max(1, scroll.ViewportHeight()/2)
	steps := []struct {
		id    string
		delta int
	}{
		{"tui.altScreen.pageUp", -page}, {"tui.altScreen.pageDown", page}, {"tui.select.pageUp", -page}, {"tui.select.pageDown", page},
		{"tui.altScreen.halfPageUp", -half}, {"tui.altScreen.halfPageDown", half}, {"tui.altScreen.lineUp", -1}, {"tui.altScreen.lineDown", 1},
		{"tui.select.up", -1}, {"tui.select.down", 1},
	}
	switch {
	case rt.Commands.Matches(data, "tui.select.cancel"):
		rt.FocusPanel(false)
	case rt.Commands.Matches(data, "tui.altScreen.top"):
		scroll.ScrollToStart()
	case rt.Commands.Matches(data, "tui.altScreen.bottom"):
		scroll.ScrollToEnd()
	default:
		index := slices.IndexFunc(steps, func(step struct {
			id    string
			delta int
		}) bool {
			return rt.Commands.Matches(data, step.id)
		})
		if index < 0 {
			return false
		}
		scroll.ScrollBy(steps[index].delta)
	}
	return true
}

// Start follows the slot, command and slash registries (once the host published them): each change reconciles the surfaces and the dispatcher.
func (rt *TuiRuntime) Start() {
	rt.Post(rt.reconcileAndChange)
}

func (rt *TuiRuntime) follow() {
	rt.sourcesRead = chordtui.Registry(rt.scope, TuiSlashSources.Registry)
	rt.commandsRead = chordtui.Registry(rt.scope, TuiCommands.Registry)
	rt.slotsRead = chordtui.Registry(rt.scope, TuiSlots.Registry)
}

func (rt *TuiRuntime) reconcileAndChange() {
	rt.reconcile()
	rt.Changed()
}

func (rt *TuiRuntime) reconcile() {
	if rt.sourcesRead == nil {
		rt.follow()
	}
	if value, ok := rt.sourcesRead(); ok {
		if table, ok := value.(*p2runtime.KeyedTable[TuiSlashSource]); ok && table != rt.sourceTable {
			rt.sourceTable = table
			rt.Commands.SetSources(table)
		}
	}
	if value, ok := rt.commandsRead(); ok {
		if table, ok := value.(*p2runtime.KeyedTable[TuiCommand]); ok && table != rt.commandTable {
			rt.commandTable = table
			// A facet reload replaces command values: overlays and the panel opened by old values close (section 7.1).
			rt.CloseOverlays()
			rt.ClosePanel()
			rt.Commands.SetCommands(table)
		}
	}
	if value, ok := rt.slotsRead(); ok {
		if table, ok := value.(*p2runtime.KeyedTable[*TuiSlot]); ok && table != rt.slotTable {
			rt.slotTable = table
			rt.applySlots(table)
		}
	}
}

func (rt *TuiRuntime) applySlots(table *p2runtime.KeyedTable[*TuiSlot]) {
	for _, name := range slices.Clone(rt.surfaceOrder) {
		if current, _ := table.Get(name); current != rt.factories[name] {
			rt.surfaces[name].Dispose()
			delete(rt.surfaces, name)
			rt.surfaceOrder = slices.DeleteFunc(rt.surfaceOrder, func(each string) bool { return each == name })
		}
	}
	rt.factories = map[string]*TuiSlot{}
	for _, name := range table.Keys() {
		rt.factories[name], _ = table.Get(name)
	}
	rt.changing = true
	defer func() { rt.changing = false }()
	for _, name := range table.Keys() {
		if _, ok := rt.surfaces[name]; !ok {
			slot, _ := table.Get(name)
			rt.surfaces[name] = slot.Factory(rt)
			rt.surfaceOrder = append(rt.surfaceOrder, name)
		}
	}
}

// Changed redraws: it tells the listeners, rebuilds the layout when it changed and requests a render.
func (rt *TuiRuntime) Changed() {
	if rt.changing {
		return
	}
	for _, listener := range slices.Clone(rt.listeners) {
		(*listener)()
	}
	rt.syncLayout()
	rt.TUI.RequestRender()
}

// sameParts compares the layout parts by identity.
func sameParts(a, b []any) bool {
	return slices.EqualFunc(a, b, func(x, y any) bool { return x == y })
}

// syncLayout is the body over the dock (coding-agent chat-viewport.ts): rebuilt only when the screen, its surface or a slot changed.
func (rt *TuiRuntime) syncLayout() {
	surface := rt.surfaces[rt.Screen]
	var layout *SurfaceLayout
	if layouter, ok := surface.(Layouter); ok {
		layout = layouter.Layout()
	}
	var footer pitui.Component = rt.builtInFooter
	if own, ok := rt.surfaces["footer"]; ok {
		footer = own
	}
	var surfacePart, body, dock any
	if surface != nil {
		surfacePart = surface
	}
	if layout != nil {
		body, dock = layout.Body, layout.Dock
	}
	var panel any
	if rt.panel != nil {
		panel = rt.panel
	}
	var noHeader any = false
	if layout != nil && layout.NoHeader {
		noHeader = true
	}
	parts := []any{surfacePart, body, dock, noHeader, footer, rt.Header(), panel}
	for _, name := range rt.surfaceOrder {
		if focus := rt.focusOf(rt.surfaces[name]); focus != nil {
			focus.SetFocused(name == rt.Screen && rt.focused.Load() && !rt.panelFocused)
		}
	}
	if sameParts(parts, rt.rootParts) {
		return
	}
	rt.rootParts = parts
	rt.Root.Clear()
	var main pitui.Component
	if layout != nil {
		main = layout.Body
	} else {
		var screen pitui.Component = rt.placeholder
		if surface != nil {
			screen = surface
		}
		main = pitui.NewVStack([]pitui.StackChild{
			{Component: rt.Header(), StackEntryOptions: pitui.StackEntryOptions{Shrink: new(1), MinSize: new(0)}},
			{Component: screen, StackEntryOptions: pitui.StackEntryOptions{Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)}},
		}, pitui.StackOptions{})
	}
	if rt.panel != nil {
		main = rt.besidePanel(main, rt.panel)
	}
	rt.Root.AddChild(main, pitui.StackEntryOptions{Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)})
	dockChildren := []pitui.StackChild{{Component: rt.statusLines, StackEntryOptions: pitui.StackEntryOptions{Shrink: new(1), MinSize: new(0)}}}
	if layout != nil && layout.Dock != nil {
		dockChildren = append(dockChildren, pitui.StackChild{Component: layout.Dock, StackEntryOptions: pitui.StackEntryOptions{Shrink: new(1), MinSize: new(3)}})
	}
	dockChildren = append(dockChildren, pitui.StackChild{Component: footer, StackEntryOptions: pitui.StackEntryOptions{Shrink: new(1), MinSize: new(0)}})
	rt.Root.AddChild(pitui.NewVStack(dockChildren, pitui.StackOptions{}), pitui.StackEntryOptions{Grow: new(0), Shrink: new(1), MinSize: new(1)})
	rt.TUI.Invalidate()
}

// besidePanel is the screen's body with the panel on its right (about 40% of the width); the panel hides on narrow terminals.
func (rt *TuiRuntime) besidePanel(body pitui.Component, panel *components.PanelFrame) pitui.Component {
	gap := 1
	return pitui.NewHStack([]pitui.StackChild{
		{Component: body, StackEntryOptions: pitui.StackEntryOptions{Basis: new(0), Grow: new(3), Shrink: new(1), MinSize: new(40)}},
		{Component: panel.Root, StackEntryOptions: pitui.StackEntryOptions{
			Basis: new(0), Grow: new(2), Shrink: new(1), MinSize: new(40),
			Visible: func(viewport pitui.LayoutViewport) bool { return viewport.Width >= panelMinColumns },
		}},
	}, pitui.StackOptions{Gap: &gap})
}

// Fail shows a failure as an error status line.
func (rt *TuiRuntime) Fail(err error) { rt.Notify(err.Error(), NoticeError) }

// Perform shows action's failure, if it fails.
func (rt *TuiRuntime) Perform(action func() error) {
	if err := action(); err != nil {
		rt.Fail(err)
	}
}

// MainOf is the main conversation of session (1 until its row is known).
func (rt *TuiRuntime) MainOf(session string) int {
	if main, ok := rt.Mains[session]; ok {
		return main
	}
	return 1
}

// Navigate shows a screen; a session or conversation change switches the viewed identity (overlays close). Another session opens at its main
// conversation. An empty session with hasSession false is no session. conversation nil keeps the viewed one (or the main one on a switch).
func (rt *TuiRuntime) Navigate(screen Screen, session string, hasSession bool, conversation *int) {
	next := rt.Conversation
	switch {
	case conversation != nil:
		next = *conversation
	case session != rt.Session || hasSession != rt.HasSession:
		next = 1
		if hasSession {
			next = rt.MainOf(session)
		}
	}
	if session != rt.Session || hasSession != rt.HasSession || next != rt.Conversation {
		rt.CloseOverlays()
		rt.ClosePanel()
	}
	rt.Session, rt.HasSession = session, hasSession
	rt.Conversation = next
	rt.Screen = screen
	rt.Notices = nil
	rt.Changed()
}

// NavigateScreen shows screen of the viewed session.
func (rt *TuiRuntime) NavigateScreen(screen Screen) {
	rt.Navigate(screen, rt.Session, rt.HasSession, nil)
}

// HandleInput is stages 4-5 of section 6.2: the alt screen (stage 2) already ran; commands by context (stage 4), then the screen (stage 5). A focused panel takes
// its keys first and never passes the rest to the screen (typing stays out of the editor).
func (rt *TuiRuntime) HandleInput(data string) {
	var panel *components.PanelFrame
	if rt.panelFocused && rt.panelShown() {
		panel = rt.panel
	}
	if rt.panelFocused && panel == nil {
		rt.FocusPanel(false)
	}
	if panel != nil {
		if rt.panelInput(data, panel.Scroll) || rt.Commands.Dispatch(data, nil) {
			rt.Changed()
		}
	} else if rt.Commands.Dispatch(data, nil) {
		rt.Changed()
	} else if handler, ok := rt.surfaces[rt.Screen].(pitui.InputHandler); ok {
		handler.HandleInput(data)
	}
	rt.TUI.RequestRender()
}

// Invalidate drops cached renders.
func (rt *TuiRuntime) Invalidate() {
	rt.builtInHeader.Invalidate()
	for _, name := range rt.surfaceOrder {
		rt.surfaces[name].Invalidate()
	}
}

// Render is the layout root's lines without a viewport (tests); the terminal renders Root through TuiAltScreen.
func (rt *TuiRuntime) Render(width int) []string {
	rt.syncLayout()
	// Every part at its natural height (a VStack without a viewport gives the body its zero basis).
	var lines []string
	for _, child := range rt.Root.Children() {
		if stack, ok := child.(*pitui.VStack); ok {
			for _, part := range stack.Children() {
				lines = append(lines, part.Render(width)...)
			}
		} else {
			lines = append(lines, child.Render(width)...)
		}
	}
	return lines
}

// headerHints are the header key hints (coding-agent's startup header lists, limited to what p2 binds).
func (rt *TuiRuntime) headerHints(expanded bool) []string {
	hints := rt.Ctx.Hints()
	hint := func(id, label string) string {
		if hints.KeyText(id) != "" {
			return hints.Key(id, label)
		}
		return ""
	}
	raw := hints.Raw
	var list []string
	if expanded {
		list = []string{
			hint("app.interrupt", "to interrupt"), hint("app.clear", "to clear"), hint("app.exit", "to exit (empty)"),
			hint("app.thinking.cycle", "to cycle thinking level"), hint("app.model.select", "to select model"),
			hint("app.tools.expand", "to expand tools"), hint("app.thinking.toggle", "to expand thinking"),
			hint("p2.sessions", "for sessions"), hint("app.message.followUp", "to queue follow-up"),
			hint("app.message.dequeue", "to edit queued messages"), hint("tui.altScreen.pageUp", "to scroll up"),
			hint("tui.altScreen.search", "to search the transcript"), raw("/", "for commands"),
		}
	} else {
		list = []string{
			hint("app.interrupt", "interrupt"), raw(hints.KeyText("app.clear")+"/"+hints.KeyText("app.exit"), "clear/exit"),
			raw("/", "commands"), hint("app.tools.expand", "more"),
		}
	}
	return slices.DeleteFunc(list, func(each string) bool { return each == "" })
}

func (rt *TuiRuntime) helpLine() string {
	hints := rt.Ctx.Hints()
	hint := func(id, label string) string {
		if hints.KeyText(id) != "" {
			return hints.Key(id, label)
		}
		return ""
	}
	parts := []string{
		hint("p2.sessions", "sessions"), hint("app.model.select", "model"), hint("p2.palette", "commands"), hints.Raw("/hotkeys", "keys"), hint("app.exit", "exit"),
	}
	return strings.Join(slices.DeleteFunc(parts, func(each string) bool { return each == "" }), "  ")
}

// Dispose closes the overlays and the panel, stops following the registries and disposes every surface.
func (rt *TuiRuntime) Dispose() {
	rt.CloseOverlays()
	rt.ClosePanel()
	rt.disposed.Store(true)
	rt.scope.Dispose()
	rt.cancel()
	for _, name := range rt.surfaceOrder {
		rt.surfaces[name].Dispose()
	}
	rt.surfaces = map[string]TuiSurface{}
	rt.surfaceOrder = nil
}

// headerSource is what the built-in header reads each render.
type headerSource struct{ rt *TuiRuntime }

func (h *headerSource) Ctx() render.RenderContext { return h.rt.Ctx }
func (h *headerSource) HintKey(id, description string) string {
	return h.rt.Ctx.Hints().Key(id, description)
}
func (h *headerSource) HintRaw(key, description string) string {
	return h.rt.Ctx.Hints().Raw(key, description)
}
func (h *headerSource) Origin() string          { return h.rt.Origin }
func (h *headerSource) Connected() bool         { return h.rt.Connected }
func (h *headerSource) Expanded() bool          { return h.rt.ExpandedTools }
func (h *headerSource) Hints() []string         { return h.rt.headerHints(false) }
func (h *headerSource) ExpandedHints() []string { return h.rt.headerHints(true) }

// Role is `guest · <name> (unverified) · view only|full access` (section 4.1) once Access names the grant.
func (h *headerSource) Role() string {
	rt := h.rt
	if rt.Role == RoleGuest && rt.Grant != nil {
		mode := "view only"
		if rt.GuestMode() == "full" {
			mode = "full access"
		}
		return fmt.Sprintf("guest · %s (unverified) · %s", jsstring.Slice(render.LineText(rt.Grant.Name), 0, 40), mode)
	}
	return string(rt.Role)
}
