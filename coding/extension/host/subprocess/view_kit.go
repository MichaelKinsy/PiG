package subprocess

// view_kit.go: the host side of the extension component kit (D107,
// docs/plan/extension-component-kit.md). A viewSurface turns the views one
// extension surface sends into a tree of PiG's tui ports, renders it, owns
// the interactive lists' state, and reports the tree with its layout to a
// D91 frontend.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/tui"
)

// Bounds of one view (§2).
const (
	viewMaxNodes = 4096  // pig additive (D107): spec §2 bound
	viewMaxDepth = 64    // pig additive (D107): spec §2 bound
	viewMaxItems = 10000 // pig additive (D107): spec §2 bound
)

var viewColorPattern = lazyregexp.New(`^#[0-9a-fA-F]{6}$`)

// viewSurface is one extension surface that carries views: a ui.custom
// overlay, a widget, the header or footer, or a renderer's output.
type viewSurface struct {
	mu sync.Mutex
	// key is the ui.custom key events carry; "" for surfaces without input.
	key    string
	images *viewImageStore
	// sendEvent delivers a list callback to the extension (nil: no input).
	sendEvent func(ViewEventPayload)
	// sendEvicted tells the extension which image refs to send again.
	sendEvicted func(refs []string)
	// repaint asks the owner to draw again after a change the surface made
	// on its own: a key its list took, a loader frame.
	repaint func()
	// report reports a rejected frame (nil: an in-memory surface, reported
	// on stderr).
	report func(surface string, err error)

	frame     *viewFrame
	instances map[string]*viewInstance
	rev       uint64

	// themeBase is the active theme the frame's tree was built from.
	themeBase *tui.Theme
	closed    bool
	// rejected records that a rejected frame was reported once.
	rejected bool
	// mouseTarget is the component that took the press of the gesture in
	// progress, and mouseReleased reports that its release arrived; the
	// gesture's click then still goes to it, as in Pi's renderer.
	mouseTarget   *tui.TuiMouseDispatchTarget
	mouseReleased bool
	loaderGen     uint64
	// cardChanged reports that a tool card drew again on its own since the
	// surface last counted a revision.
	cardChanged atomic.Bool
	// disposers dispose the components of the frames the surface built
	// that no later frame keeps (tool cards without an id); they run
	// outside s.mu by the next accept or close.
	disposers []func()
}

// viewFrame is one accepted frame.
type viewFrame struct {
	raw     []byte
	payload ViewPayload
	// lines are the frame's own lines (annotated mode); nil when the view
	// is authoritative.
	lines []string
	// root is the tree the host renders; nil when an annotated view did not
	// reproduce its lines and only the lines remain.
	root    tui.Component
	tree    *frontend.ViewNode
	theme   *tui.Theme
	focus   *viewInstance
	images  map[string]*viewImage
	loaders []*viewLoader
	// conversations are the frame's conversation nodes (§2.1).
	conversations []*viewConversation
}

// viewInstance is a host-owned interactive list kept across frames (§4.2).
type viewInstance struct {
	id, kind string
	ctorKey  []byte
	list     *tui.SelectList
	settings *tui.SettingsList
	node     *frontend.ViewNode
	// sentFilter, sentSelected and sentValues are the setter values the
	// extension sent last, applied again only when they change.
	sentFilter   *string
	sentSelected *int
	sentValues   map[string]string
	pending      []ViewEventPayload
	used         bool

	// A conversation node's component (§2.1), dispose its background work
	// (a tool card's), and sent the node its sender sent last.
	assistant *tui.AssistantMessageComponent
	tool      *tui.ToolExecutionComponent
	bash      *tui.BashExecutionComponent
	dispose   func()
	sent      *ViewNode
}

type viewLoader struct {
	loader   *tui.Loader
	node     *frontend.ViewNode
	interval time.Duration
}

func newViewSurface(key string, images *viewImageStore) *viewSurface {
	return &viewSurface{key: key, images: images, instances: map[string]*viewInstance{}}
}

// errViewUnchanged reports a frame equal to the current one.
var errViewUnchanged = errors.New("view unchanged")

// accept installs a frame. lines nil with a view is an authoritative view;
// lines with a view is a Node frame whose view survives only when it
// reproduces lines at view.width. An error leaves the current frame.
func (s *viewSurface) accept(raw json.RawMessage, lines []string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("view surface closed")
	}
	if cur := s.frame; cur != nil && bytes.Equal(cur.raw, raw) && slices.Equal(cur.lines, lines) && (lines == nil) == (cur.lines == nil) {
		s.mu.Unlock()
		return errViewUnchanged
	}
	s.mu.Unlock()

	var payload ViewPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode view: %w", err)
	}
	refs, err := validateView(&payload, lines != nil)
	if err != nil {
		return err
	}
	pinned, missing, evicted, err := s.images.admit(payload.Images, refs)
	if len(evicted) > 0 && s.sendEvicted != nil {
		s.sendEvicted(evicted)
	}
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		if s.sendEvicted != nil {
			s.sendEvicted(missing)
		}
		return fmt.Errorf("view names unknown images %v", missing)
	}
	payload.Images = nil // the store holds the bytes now

	// Disposers and evicted refs run and send after s.mu is released.
	var dispose []func()
	var released []string
	defer func() {
		for _, d := range dispose {
			d()
		}
		s.notifyEvicted(released)
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		released = append(released, s.images.release(pinned)...)
		return errors.New("view surface closed")
	}
	// The components of the shown frame stay live until a new frame replaces it.
	shown := s.disposers
	s.disposers = nil
	f := &viewFrame{raw: append([]byte(nil), raw...), payload: payload, images: pinned}
	if lines != nil {
		f.lines = append([]string{}, lines...)
	}
	base := tui.ActiveTheme()
	if err := s.buildLocked(f, base); err != nil {
		s.disposers = append(shown, s.disposers...)
		released = append(released, s.images.release(pinned)...)
		return err
	}
	dispose = shown
	if f.lines != nil {
		if got := f.root.Render(payload.Width); !slices.Equal(got, f.lines) {
			// The view does not describe the lines: keep the lines alone.
			f.root, f.tree, f.focus, f.loaders = nil, nil, nil, nil
		}
	}
	old := s.frame
	s.frame = f
	s.themeBase = base
	s.rev++
	if old != nil {
		released = append(released, s.images.release(old.images)...)
	}
	s.startLoadersLocked()
	return nil
}

// notifyEvicted tells the extension which refs the store dropped. Callers
// release images under s.mu and notify after releasing it, so the notify
// keeps wire order without a detached goroutine.
func (s *viewSurface) notifyEvicted(refs []string) {
	if len(refs) > 0 && s.sendEvicted != nil {
		s.sendEvicted(refs)
	}
}

// close stops the surface: loaders stop and its images are unpinned.
func (s *viewSurface) close() {
	var dispose []func()
	var released []string
	defer func() {
		for _, d := range dispose {
			d()
		}
		s.notifyEvicted(released)
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	dispose, s.disposers = s.disposers, nil
	for _, inst := range s.instances {
		if inst.dispose != nil {
			dispose = append(dispose, inst.dispose)
		}
	}
	s.loaderGen++
	if s.frame != nil {
		released = s.images.release(s.frame.images)
		s.frame.images = nil
	}
}

// Render returns the lines the terminal draws at width.
func (s *viewSurface) Render(width int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.frame
	if f == nil {
		return nil
	}
	if f.lines != nil {
		return slices.Clone(f.lines)
	}
	if s.cardChanged.Swap(false) {
		s.rev++
	}
	s.refreshThemeLocked()
	return slices.Clone(f.root.Render(width))
}

// Invalidate drops the components' caches.
func (s *viewSurface) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frame != nil && s.frame.root != nil {
		s.frame.root.Invalidate()
	}
}

// refreshThemeLocked rebuilds the tree when the active theme changed, as a
// theme switch re-styles every Pi component.
func (s *viewSurface) refreshThemeLocked() {
	base := tui.ActiveTheme()
	if base == s.themeBase || s.frame == nil || s.frame.lines != nil {
		return
	}
	if err := s.buildLocked(s.frame, base); err == nil {
		s.themeBase = base
		s.rev++
	}
}

// HandleViewInput gives a key to the focused list when it binds the key.
func (s *viewSurface) HandleViewInput(data string) bool {
	s.mu.Lock()
	f := s.frame
	if f == nil || f.lines != nil || f.focus == nil || s.closed {
		s.mu.Unlock()
		return false
	}
	inst := f.focus
	kb := tui.GetTUIKeybindings()
	bound := kb.Matches(data, tui.KBSelectUp) || kb.Matches(data, tui.KBSelectDown) ||
		kb.Matches(data, tui.KBSelectConfirm) || kb.Matches(data, tui.KBSelectCancel)
	if inst.settings != nil {
		bound = bound || data == " " || inst.settings.SearchEnabled() || inst.settings.SubmenuComponent() != nil
	}
	if !bound {
		s.mu.Unlock()
		return false
	}
	if inst.list != nil {
		inst.list.HandleInput(data)
	} else {
		inst.settings.HandleInput(data)
		inst.settings.Reset()
	}
	events := inst.pending
	inst.pending = nil
	s.rev++
	send, repaint := s.sendEvent, s.repaint
	s.mu.Unlock()
	if send != nil {
		for _, ev := range events {
			send(ev)
		}
	}
	if repaint != nil {
		repaint()
	}
	return true
}

// HandleViewMouse gives a mouse event, local to the view's first cell, to
// the view's components as Pi's renderer would: a
// press goes to the component under the pointer, and the drag, release and
// click of its gesture to the component that took the press
// (tui-alt-screen.ts handleMouseEvent). A list's callbacks then reach the
// extension as for keys (§4.4). Only a ui.custom view takes the mouse, as
// only it takes keys (§4.3), and a Node view, whose components live in the
// Node runtime, takes none here.
func (s *viewSurface) HandleViewMouse(event extension.RemoteMouseEvent) extension.ViewMouseResult {
	s.mu.Lock()
	f := s.frame
	if f == nil || f.lines != nil || f.root == nil || s.closed || s.sendEvent == nil {
		s.mouseTarget, s.mouseReleased = nil, false
		s.mu.Unlock()
		return extension.ViewMouseResult{}
	}
	ev := tui.TuiMouseEvent{
		Type: tui.TuiMouseEventType(event.Type), Button: tui.TuiMouseButton(event.Button),
		X: event.X, Y: event.Y, ScreenX: event.ScreenX, ScreenY: event.ScreenY, Width: event.Width, Height: event.Height,
		Shift: event.Shift, Alt: event.Alt, Ctrl: event.Ctrl, WheelDelta: event.WheelDelta, ClickCount: event.ClickCount,
	}
	var result *tui.TuiMouseDispatchResult
	if target := s.mouseTarget; target != nil && (!s.mouseReleased || ev.Type == tui.MouseClick) {
		result = tui.DispatchMouseEvent(target.Component, tui.RetargetMouseEvent(ev, *target))
		switch ev.Type {
		case tui.MouseRelease:
			s.mouseReleased = true
		case tui.MouseClick:
			s.mouseTarget, s.mouseReleased = nil, false
		}
	} else {
		s.mouseTarget, s.mouseReleased = nil, false
		result = tui.DispatchMouseEvent(f.root, ev)
		if result != nil && ev.Type == tui.MousePress {
			target := result.Target
			s.mouseTarget = &target
		}
	}
	var events []ViewEventPayload
	for _, id := range slices.Sorted(maps.Keys(s.instances)) {
		inst := s.instances[id]
		events = append(events, inst.pending...)
		inst.pending = nil
		if inst.settings != nil {
			inst.settings.Reset()
		}
	}
	if result == nil && len(events) == 0 {
		s.mu.Unlock()
		return extension.ViewMouseResult{}
	}
	s.rev++
	send, repaint := s.sendEvent, s.repaint
	s.mu.Unlock()
	for _, ev := range events {
		send(ev)
	}
	if repaint != nil {
		repaint()
	}
	if result == nil {
		return extension.ViewMouseResult{}
	}
	return extension.ViewMouseResult{Handled: true, Capture: result.Capture, Focus: result.Focus, Render: result.Render}
}

// FrontendView returns the structure of Render(width) for a D91 frontend.
func (s *viewSurface) FrontendView(width int) *frontend.View {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.frame
	if f == nil || f.tree == nil {
		return nil
	}
	if f.lines == nil {
		if s.cardChanged.Swap(false) {
			s.rev++
		}
		s.refreshThemeLocked()
		f.root.Render(width)
	}
	for _, inst := range s.instances {
		inst.refreshNode()
	}
	for _, l := range f.loaders {
		if l.node != nil {
			l.node.Frame = l.loader.Frame % max(1, len(l.loader.Frames))
		}
	}
	f.refreshConversationsLocked()
	root := cloneViewNode(*f.tree)
	theme := map[string]string(nil)
	if len(f.payload.Theme) > 0 {
		theme = make(map[string]string, len(f.payload.Theme))
		maps.Copy(theme, f.payload.Theme)
	}
	return &frontend.View{Root: root, Theme: theme, Focus: f.payload.Focus, Seq: s.rev}
}

func cloneViewNode(n frontend.ViewNode) frontend.ViewNode {
	if n.Children != nil {
		children := make([]frontend.ViewNode, len(n.Children))
		for i, c := range n.Children {
			children[i] = cloneViewNode(c)
		}
		n.Children = children
	}
	if n.Submenu != nil {
		sub := cloneViewNode(*n.Submenu)
		n.Submenu = &sub
	}
	if n.Stack != nil {
		stack := *n.Stack
		n.Stack = &stack
	}
	n.Items = slices.Clone(n.Items)
	n.Lines = slices.Clone(n.Lines)
	n.Frames = slices.Clone(n.Frames)
	n.Transcript = slices.Clone(n.Transcript)
	return n
}

// refreshNode copies a list's live state into its frontend node.
func (inst *viewInstance) refreshNode() {
	if inst.list == nil && inst.settings == nil {
		return
	}
	n := inst.node
	if inst.list != nil {
		items := inst.list.FilteredItems()
		n.Items = n.Items[:0]
		for _, it := range items {
			n.Items = append(n.Items, frontend.ViewItem{Value: it.Value, Label: it.Label, Description: it.Description})
		}
		n.Selected = -1
		if len(items) > 0 {
			n.Selected = inst.list.SelectedIndex()
		}
		n.MaxVisible = inst.list.MaxVisible()
		if inst.sentFilter != nil {
			n.Query = *inst.sentFilter
		}
		return
	}
	sl := inst.settings
	n.Items = n.Items[:0]
	for _, it := range sl.DisplayedItems() {
		n.Items = append(n.Items, frontend.ViewItem{ID: it.ID, Label: it.Label, Description: it.Description, CurrentValue: it.CurrentValue, Values: slices.Clone(it.Values), Submenu: it.Submenu != nil})
	}
	n.Selected = sl.SelectedIndex()
	n.MaxVisible = sl.MaxVisible()
	n.Query = sl.Query()
	n.Searchable = sl.SearchEnabled()
	n.Submenu = nil
	if sub, ok := sl.SubmenuComponent().(*viewSubmenu); ok && sub != nil {
		node := *sub.node
		n.Submenu = &node
	}
}

// ── validation ──────────────────────────────────────────────────────────────

// validateView checks the schema (§5.2) and returns the image refs the view
// names. annotated frames must say what width their lines were laid out at.
func validateView(v *ViewPayload, annotated bool) ([]string, error) {
	if annotated && v.Width <= 0 {
		return nil, errors.New("a view annotating lines needs its width")
	}
	for token, value := range v.Theme {
		if !slices.Contains(tui.ViewOverridableTokens, token) {
			return nil, fmt.Errorf("theme token %q cannot be overridden", token)
		}
		if !viewColorPattern.MatchString(value) {
			return nil, fmt.Errorf("theme override %s=%q is not #rrggbb", token, value)
		}
	}
	c := viewChecker{ids: map[string]string{}, theme: tui.ActiveTheme()}
	c.node(&v.Root, 1)
	if c.err != nil {
		return nil, c.err
	}
	if v.Focus != "" {
		kind, ok := c.ids[v.Focus]
		if !ok || (kind != ViewKindSelectList && kind != ViewKindSettingsList) {
			return nil, fmt.Errorf("focus %q is not a list of the view", v.Focus)
		}
	}
	return c.refs, nil
}

type viewChecker struct {
	ids   map[string]string
	refs  []string
	count int
	theme *tui.Theme
	err   error
	// fgTokens and bgTokens are the theme's tokens, read on the first check.
	fgTokens, bgTokens map[string]string
}

func (c *viewChecker) fail(format string, args ...any) {
	if c.err == nil {
		c.err = fmt.Errorf(format, args...)
	}
}

// hasToken reports whether the theme defines token as a foreground
// (background false) or background token. Theme.Fg and Theme.Bg panic on an
// unknown token, as Pi's theme throws.
func (c *viewChecker) hasToken(token string, background bool) bool {
	if c.fgTokens == nil {
		c.fgTokens, c.bgTokens = c.theme.ANSIPalette()
	}
	tokens := c.fgTokens
	if background {
		tokens = c.bgTokens
	}
	_, ok := tokens[token]
	return ok
}

func (c *viewChecker) fg(token string) {
	if token != "" && !c.hasToken(token, false) {
		c.fail("unknown foreground token %q", token)
	}
}

func (c *viewChecker) bg(token string) {
	if token != "" && !c.hasToken(token, true) {
		c.fail("unknown background token %q", token)
	}
}

func (c *viewChecker) node(n *ViewNode, depth int) {
	if c.err != nil {
		return
	}
	c.count++
	if c.count > viewMaxNodes {
		c.fail("view has more than %d nodes", viewMaxNodes)
		return
	}
	if depth > viewMaxDepth {
		c.fail("view is deeper than %d", viewMaxDepth)
		return
	}
	if n.ID != "" {
		if _, dup := c.ids[n.ID]; dup {
			c.fail("duplicate view id %q", n.ID)
			return
		}
		c.ids[n.ID] = n.Kind
	}
	for _, p := range []*int{n.PaddingX, n.PaddingY, n.Lines, n.MaxVisible, n.Gap, n.MaxWidthCells, n.MaxHeightCells, n.Frame, n.OutputPad, n.ImageWidthCells} {
		if p != nil && *p < 0 {
			c.fail("negative size in %s node", n.Kind)
		}
	}
	switch n.Kind {
	case ViewKindContainer, ViewKindHStack, ViewKindVStack:
		if n.Kind != ViewKindContainer && n.Align != "" && !slices.Contains([]string{"stretch", "start", "center", "end"}, n.Align) {
			c.fail("unknown stack align %q", n.Align)
		}
	case ViewKindBox, ViewKindText:
		c.bg(n.Bg)
	case ViewKindTruncatedText, ViewKindSpacer:
	case ViewKindMarkdown:
		if s := n.DefaultTextStyle; s != nil {
			c.fg(s.Color)
			c.bg(s.BgColor)
		}
	case ViewKindDynamicBorder:
		c.fg(n.Color)
	case ViewKindSelectList, ViewKindSettingsList:
		if n.ID == "" {
			c.fail("%s needs an id", n.Kind)
		}
		if len(n.Items) > viewMaxItems {
			c.fail("%s has more than %d items", n.Kind, viewMaxItems)
		}
		if n.Kind == ViewKindSettingsList {
			for i := range n.Items {
				if sub := n.Items[i].Submenu; sub != nil {
					c.node(sub, depth+1)
				}
			}
		}
	case ViewKindImage:
		if n.Ref == "" || n.MimeType == "" {
			c.fail("image needs ref and mimeType")
		}
		c.refs = append(c.refs, n.Ref)
		c.fg(n.FallbackColor)
	case ViewKindLoader:
		c.fg(n.SpinnerColor)
		c.fg(n.MessageColor)
		if n.AssistantMessage != nil {
			c.fail("a loader's message is a string")
		}
	case ViewKindUserMessage, ViewKindAssistantMessage, ViewKindToolExecution, ViewKindBashExecution, ViewKindDiff:
		c.checkConversation(n)
	case ViewKindLines:
		if n.Image != nil {
			c.refs = append(c.refs, n.Image.Ref)
		}
		if l := n.List; l != nil {
			switch {
			case len(l.Items) > viewMaxItems:
				c.fail("list annotation has more than %d items", viewMaxItems)
			case len(l.Items) != len(n.Content):
				c.fail("list annotation has %d items for %d rows", len(l.Items), len(n.Content))
			case l.SelectedIndex < -1 || l.SelectedIndex >= len(l.Items):
				c.fail("list annotation selects %d of %d items", l.SelectedIndex, len(l.Items))
			}
		}
	default:
		c.fail("unknown view kind %q", n.Kind)
	}
	if len(n.Children) > 0 && !slices.Contains([]string{ViewKindContainer, ViewKindBox, ViewKindHStack, ViewKindVStack}, n.Kind) {
		c.fail("%s node cannot have children", n.Kind)
	}
	for i := range n.Children {
		c.node(&n.Children[i], depth+1)
	}
}

// ── building ────────────────────────────────────────────────────────────────

// viewBuilder builds one frame's tree.
type viewBuilder struct {
	s     *viewSurface
	f     *viewFrame
	theme *tui.Theme
	// disposers are the components this build replaced or made that no
	// later frame keeps.
	disposers []func()
}

// buildLocked builds f's component tree with the surface theme derived
// from base, keeping interactive instances whose constructor is unchanged.
func (s *viewSurface) buildLocked(f *viewFrame, base *tui.Theme) error {
	theme := base
	if len(f.payload.Theme) > 0 {
		colors := make(map[string]tui.Color, len(f.payload.Theme))
		for token, value := range f.payload.Theme {
			color, err := tui.ParseColor(value)
			if err != nil {
				return fmt.Errorf("theme override %s: %w", token, err)
			}
			colors[token] = color
		}
		derived, err := base.WithTokenColors(colors)
		if err != nil {
			return err
		}
		theme = derived
	}
	for _, inst := range s.instances {
		inst.used = false
	}
	b := &viewBuilder{s: s, f: f, theme: theme}
	f.theme = theme
	f.focus = nil
	f.loaders = nil
	f.conversations = nil
	tree := &frontend.ViewNode{}
	root, err := b.build(&f.payload.Root, tree)
	s.disposers = append(s.disposers, b.disposers...)
	if err != nil {
		return err
	}
	for id, inst := range s.instances {
		if !inst.used {
			if inst.dispose != nil {
				s.disposers = append(s.disposers, inst.dispose)
			}
			delete(s.instances, id)
		}
	}
	f.root = root
	f.tree = tree
	if f.payload.Focus != "" {
		f.focus = s.instances[f.payload.Focus]
	}
	return nil
}

func (b *viewBuilder) fgFn(token string) func(string) string {
	if token == "" {
		return nil
	}
	theme := b.theme
	return func(text string) string { return theme.Fg(token, text) }
}

func (b *viewBuilder) bgFn(token string) func(string) string {
	if token == "" {
		return nil
	}
	theme := b.theme
	return func(text string) string { return theme.Bg(token, text) }
}

func identity(text string) string { return text }

func intOr(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

// viewRecorder records the rows and width each node drew for the frontend
// structure. It is transparent to rendering.
type viewRecorder struct {
	tui.Component
	node *frontend.ViewNode
}

func (r *viewRecorder) Render(width int) []string {
	lines := r.Component.Render(width)
	r.node.Rows = len(lines)
	r.node.Width = width
	return lines
}

// HandleInput forwards keys to a recorded list (a settings submenu's list).
func (r *viewRecorder) HandleInput(data string) {
	if h, ok := r.Component.(tui.InputHandler); ok {
		h.HandleInput(data)
	}
}

// HandleMouse hands the mouse to the recorded component, which Pi's tree
// holds directly.
func (r *viewRecorder) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	return tui.DispatchMouseEvent(r.Component, event)
}

// viewLines is a lines node: rows a component of the extension's own drew.
type viewLines struct{ content []string }

func (l *viewLines) Render(int) []string { return l.content }
func (l *viewLines) Invalidate()         {}

func (b *viewBuilder) build(n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	out.Kind = frontend.ViewKind(n.Kind)
	out.ID = n.ID
	if n.Stack != nil {
		out.Stack = &frontend.ViewStack{Basis: intOr(n.Stack.Basis, -1), Grow: intOr(n.Stack.Grow, 0), Shrink: intOr(n.Stack.Shrink, 1), MinSize: intOr(n.Stack.MinSize, 0), MaxSize: intOr(n.Stack.MaxSize, -1)}
	}
	children := func() ([]tui.Component, error) {
		out.Children = make([]frontend.ViewNode, len(n.Children))
		comps := make([]tui.Component, len(n.Children))
		for i := range n.Children {
			c, err := b.build(&n.Children[i], &out.Children[i])
			if err != nil {
				return nil, err
			}
			comps[i] = c
		}
		return comps, nil
	}
	var c tui.Component
	switch n.Kind {
	case ViewKindContainer:
		comps, err := children()
		if err != nil {
			return nil, err
		}
		c = tui.NewContainer(comps...)
	case ViewKindBox:
		comps, err := children()
		if err != nil {
			return nil, err
		}
		out.PaddingX, out.PaddingY, out.Bg = intOr(n.PaddingX, 1), intOr(n.PaddingY, 1), n.Bg
		box := tui.NewPaddedBox(out.PaddingX, out.PaddingY, b.bgFn(n.Bg))
		for _, comp := range comps {
			box.AddChild(comp)
		}
		c = box
	case ViewKindText:
		out.Text, out.PaddingX, out.PaddingY, out.Bg = n.Text, intOr(n.PaddingX, 1), intOr(n.PaddingY, 1), n.Bg
		c = tui.NewPaddedText(n.Text, out.PaddingX, out.PaddingY, b.bgFn(n.Bg))
	case ViewKindTruncatedText:
		out.Text, out.PaddingX, out.PaddingY = n.Text, intOr(n.PaddingX, 0), intOr(n.PaddingY, 0)
		c = tui.NewTruncatedText(n.Text, out.PaddingX, out.PaddingY)
	case ViewKindMarkdown:
		out.Text, out.PaddingX, out.PaddingY = n.Text, intOr(n.PaddingX, 0), intOr(n.PaddingY, 0)
		theme := tui.MarkdownThemeFor(b.theme)
		var style *tui.DefaultTextStyle
		if s := n.DefaultTextStyle; s != nil {
			out.TextStyle = &frontend.ViewTextStyle{Color: s.Color, BgColor: s.BgColor, Bold: s.Bold, Italic: s.Italic, Strikethrough: s.Strikethrough, Underline: s.Underline}
			style = &tui.DefaultTextStyle{Color: b.fgFn(s.Color), BgColor: b.bgFn(s.BgColor), Bold: s.Bold, Italic: s.Italic, Strikethrough: s.Strikethrough, Underline: s.Underline}
		}
		c = tui.NewMarkdownWithOptions(n.Text, out.PaddingX, out.PaddingY, &theme, style, &tui.MarkdownOptions{RenderLatex: n.RenderLatex})
	case ViewKindSpacer:
		out.Rows = intOr(n.Lines, 1)
		c = tui.NewSpacer(out.Rows)
	case ViewKindDynamicBorder:
		token := n.Color
		if token == "" {
			token = "border"
		}
		out.Color = token
		c = tui.NewDynamicBorder(b.fgFn(token))
	case ViewKindSelectList:
		c = b.selectList(n, out)
	case ViewKindSettingsList:
		comp, err := b.settingsList(n, out)
		if err != nil {
			return nil, err
		}
		c = comp
	case ViewKindImage:
		img := b.f.images[n.Ref]
		out.Color = n.FallbackColor
		out.Image = &frontend.ViewImage{Ref: img.ref, MimeType: n.MimeType, Data: img.data, Filename: n.Filename, MaxWidthCells: intOr(n.MaxWidthCells, 0), MaxHeightCells: intOr(n.MaxHeightCells, 0)}
		opts := tui.ImageOptions{Filename: n.Filename, ImageID: intOr(n.ImageID, 0)}
		if n.MaxWidthCells != nil {
			opts.MaxWidthCells = *n.MaxWidthCells
		}
		if n.MaxHeightCells != nil {
			opts.MaxHeightCells = *n.MaxHeightCells
		}
		fallback := b.fgFn(n.FallbackColor)
		if fallback == nil {
			fallback = identity
		}
		c = tui.NewImage(img.b64, n.MimeType, tui.ImageTheme{FallbackColor: fallback}, opts, nil)
	case ViewKindLoader:
		c = b.loader(n, out)
	case ViewKindUserMessage:
		c = b.userMessage(n, out)
	case ViewKindAssistantMessage:
		c = b.assistantMessage(n, out)
	case ViewKindToolExecution:
		c = b.toolExecution(n, out)
	case ViewKindBashExecution:
		c = b.bashExecution(n, out)
	case ViewKindDiff:
		c = b.diff(n, out)
	case ViewKindHStack, ViewKindVStack:
		comps, err := children()
		if err != nil {
			return nil, err
		}
		entries := make([]tui.StackChild, len(comps))
		for i, comp := range comps {
			entries[i] = tui.StackChild{Component: comp}
			if st := n.Children[i].Stack; st != nil {
				entries[i].StackEntryOptions = tui.StackEntryOptions{Basis: st.Basis, Grow: st.Grow, Shrink: st.Shrink, MinSize: st.MinSize, MaxSize: st.MaxSize}
			}
		}
		out.Gap, out.Align = intOr(n.Gap, 0), n.Align
		if out.Align == "" {
			out.Align = "stretch"
		}
		opts := tui.StackOptions{Gap: n.Gap, Align: n.Align}
		if n.Kind == ViewKindHStack {
			c = tui.NewHStack(entries, opts)
		} else {
			c = tui.NewVStack(entries, opts)
		}
	case ViewKindLines:
		out.Lines = slices.Clone(n.Content)
		if n.Image != nil {
			if img := b.f.images[n.Image.Ref]; img != nil {
				out.Image = &frontend.ViewImage{Ref: img.ref, MimeType: img.mimeType, Data: img.data}
			}
		}
		if n.Progress != nil {
			out.Progress = &frontend.ViewProgress{Value: n.Progress.Value, Max: n.Progress.Max}
		}
		if l := n.List; l != nil {
			list := &frontend.ViewList{Items: make([]frontend.ViewListItem, len(l.Items)), Selected: l.SelectedIndex}
			for i, item := range l.Items {
				list.Items[i] = frontend.ViewListItem{Label: item.Label, Detail: item.Detail, Columns: slices.Clone(item.Columns)}
			}
			out.List = list
		}
		c = &viewLines{content: slices.Clone(n.Content)}
	default:
		return nil, fmt.Errorf("unknown view kind %q", n.Kind)
	}
	return &viewRecorder{Component: c, node: out}, nil
}

// ctorKey is a list's constructor identity: the node without the setter
// state (§4.2).
func ctorKey(n *ViewNode) []byte {
	ctor := *n
	ctor.SelectedIndex, ctor.Filter, ctor.Stack = nil, nil, nil
	ctor.Items = slices.Clone(n.Items)
	for i := range ctor.Items {
		ctor.Items[i].CurrentValue = ""
	}
	key, _ := json.Marshal(ctor)
	return key
}

// instance returns the kept instance for n, or a fresh one.
func (b *viewBuilder) instance(n *ViewNode, out *frontend.ViewNode) (inst *viewInstance, fresh bool) {
	key := ctorKey(n)
	if inst = b.s.instances[n.ID]; inst != nil && inst.kind == n.Kind && bytes.Equal(inst.ctorKey, key) {
		inst.used = true
		inst.node = out
		return inst, false
	}
	inst = &viewInstance{id: n.ID, kind: n.Kind, ctorKey: key, node: out, used: true}
	b.s.instances[n.ID] = inst
	return inst, true
}

// applySetters applies the extension's filter and selection when they
// changed since it sent them last (§4.2): filter first, as upstream
// setFilter resets the selection.
func (inst *viewInstance) applySetters(n *ViewNode, fresh bool, setFilter func(string), setSelected func(int)) {
	if n.Filter != nil && (fresh || inst.sentFilter == nil || *inst.sentFilter != *n.Filter) {
		setFilter(*n.Filter)
	}
	inst.sentFilter = n.Filter
	if n.SelectedIndex != nil && (fresh || inst.sentSelected == nil || *inst.sentSelected != *n.SelectedIndex) {
		setSelected(*n.SelectedIndex)
	}
	inst.sentSelected = n.SelectedIndex
}

func (b *viewBuilder) selectList(n *ViewNode, out *frontend.ViewNode) tui.Component {
	inst, fresh := b.instance(n, out)
	if fresh {
		items := make([]tui.SelectItem, len(n.Items))
		for i, it := range n.Items {
			items[i] = tui.SelectItem{Value: it.Value, Label: it.Label, Description: it.Description}
		}
		var layout tui.SelectListLayoutOptions
		if n.Layout != nil {
			if width := n.Layout.MinPrimaryColumnWidth; width != nil {
				layout.MinPrimaryColumnWidth = *width
			}
			if width := n.Layout.MaxPrimaryColumnWidth; width != nil {
				layout.MaxPrimaryColumnWidth = *width
			}
		}
		inst.list = tui.NewSelectList(items, intOr(n.MaxVisible, 5), tui.SelectListThemeFor(b.theme), layout)
		b.wireSelectEvents(inst)
	} else {
		inst.list.SetTheme(tui.SelectListThemeFor(b.theme))
	}
	inst.applySetters(n, fresh, inst.list.SetFilter, inst.list.SetSelectedIndex)
	return inst.list
}

func (b *viewBuilder) wireSelectEvents(inst *viewInstance) {
	list := inst.list
	event := func(typ string, item tui.SelectItem) {
		inst.pending = append(inst.pending, ViewEventPayload{
			Key: b.s.key, Node: inst.id, Type: typ, Index: list.SelectedIndex(),
			Item: &ViewItem{Value: item.Value, Label: item.Label, Description: item.Description},
		})
	}
	list.OnSelect = func(item tui.SelectItem) { event(ViewEventSelect, item) }
	list.OnSelectionChange = func(item tui.SelectItem) { event(ViewEventSelectionChange, item) }
	list.OnCancel = func() {
		inst.pending = append(inst.pending, ViewEventPayload{Key: b.s.key, Node: inst.id, Type: ViewEventCancel})
	}
}

func (b *viewBuilder) settingsList(n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	inst, fresh := b.instance(n, out)
	theme := tui.SettingsListThemeFor(b.theme)
	if fresh {
		items := make([]tui.SettingItem, len(n.Items))
		for i := range n.Items {
			it := &n.Items[i]
			items[i] = tui.SettingItem{ID: it.ID, Label: it.Label, Description: it.Description, CurrentValue: it.CurrentValue, Values: slices.Clone(it.Values)}
			if it.Submenu != nil {
				items[i].Submenu = b.submenu(*it.Submenu)
			}
		}
		inst.settings = tui.NewSettingsList(items, max(1, intOr(n.MaxVisible, 1)), theme, func(id, value string) {
			inst.pending = append(inst.pending, ViewEventPayload{Key: b.s.key, Node: inst.id, Type: ViewEventChange, ID: id, Value: value})
		}, func() {
			inst.pending = append(inst.pending, ViewEventPayload{Key: b.s.key, Node: inst.id, Type: ViewEventCancel})
		}, tui.SettingsListOptions{EnableSearch: n.EnableSearch})
		inst.sentValues = map[string]string{}
		for _, it := range n.Items {
			inst.sentValues[it.ID] = it.CurrentValue
		}
	} else {
		for _, it := range n.Items {
			if last, ok := inst.sentValues[it.ID]; !ok || last != it.CurrentValue {
				inst.settings.UpdateValue(it.ID, it.CurrentValue)
			}
			inst.sentValues[it.ID] = it.CurrentValue
		}
	}
	inst.settings.SetTheme(theme)
	inst.applySetters(n, fresh, inst.settings.SetQuery, inst.settings.SetSelectedIndex)
	return inst.settings, nil
}

// submenu builds a settings item's submenu: the node's tree, whose first
// select list completes it, done(item.value) on select and done() on
// cancel, as Pi's submenu factories do.
func (b *viewBuilder) submenu(node ViewNode) func(string, func(*string, *tui.SubmenuDoneOptions)) tui.Component {
	theme := b.theme
	return func(_ string, done func(*string, *tui.SubmenuDoneOptions)) tui.Component {
		sub := &viewBuilder{s: &viewSurface{instances: map[string]*viewInstance{}, key: b.s.key}, f: b.f, theme: theme}
		out := &frontend.ViewNode{}
		comp, err := sub.build(&node, out)
		if err != nil {
			return &viewLines{}
		}
		for _, inst := range sub.s.instances {
			if inst.list == nil {
				continue
			}
			inst.list.OnSelect = func(item tui.SelectItem) { value := item.Value; done(&value, nil) }
			inst.list.OnCancel = func() { done(nil, nil) }
			inst.list.OnSelectionChange = nil
			break
		}
		return &viewSubmenu{viewRecorder: comp.(*viewRecorder), instances: sub.s.instances}
	}
}

// viewSubmenu routes keys to the submenu's first select list.
type viewSubmenu struct {
	*viewRecorder
	instances map[string]*viewInstance
}

func (m *viewSubmenu) HandleInput(data string) {
	for _, inst := range m.instances {
		if inst.list != nil {
			inst.list.HandleInput(data)
			inst.pending = nil
			return
		}
	}
}

func (b *viewBuilder) loader(n *ViewNode, out *frontend.ViewNode) tui.Component {
	message := "Loading..."
	if n.Message != nil {
		message = *n.Message
	}
	l := tui.NewLoader(nil, b.fgFn(n.SpinnerColor), b.fgFn(n.MessageColor), message, nil)
	// upstream: packages/tui/src/components/loader.ts DEFAULT_INTERVAL_MS
	interval := 80 * time.Millisecond
	if ind := n.Indicator; ind != nil {
		var frames []string
		if ind.Frames != nil {
			frames = append([]string{}, (*ind.Frames)...)
		}
		l.SetIndicator(&tui.LoaderIndicatorOptions{Frames: frames})
		if ind.IntervalMs > 0 {
			interval = time.Duration(ind.IntervalMs) * time.Millisecond
		}
	}
	if n.Frame != nil && len(l.Frames) > 0 {
		l.Frame = *n.Frame % len(l.Frames)
	}
	out.Text, out.Color, out.MessageColor = message, n.SpinnerColor, n.MessageColor
	out.Frames = slices.Clone(l.Frames)
	if len(l.Frames) == 0 {
		out.Frames = nil
	}
	out.Frame = l.Frame
	out.Interval = interval
	b.f.loaders = append(b.f.loaders, &viewLoader{loader: l, node: out, interval: interval})
	return l
}

// startLoadersLocked animates the frame's loaders (host-owned, §4.4): one
// owned timer per loader, stopped by the next frame or close.
func (s *viewSurface) startLoadersLocked() {
	s.loaderGen++
	if s.frame == nil || s.frame.lines != nil {
		return
	}
	gen := s.loaderGen
	for _, l := range s.frame.loaders {
		if len(l.loader.Frames) <= 1 {
			continue
		}
		s.scheduleLoader(gen, l)
	}
}

func (s *viewSurface) scheduleLoader(gen uint64, l *viewLoader) {
	time.AfterFunc(l.interval, func() {
		s.mu.Lock()
		if s.closed || s.loaderGen != gen {
			s.mu.Unlock()
			return
		}
		l.loader.Tick()
		s.rev++
		repaint := s.repaint
		s.mu.Unlock()
		if repaint != nil {
			repaint()
		}
		s.scheduleLoader(gen, l)
	})
}
