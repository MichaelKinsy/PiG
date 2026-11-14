package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
	"github.com/MichaelKinsy/PiG/tui"
)

// pig additive (D614): the terminal as a client of the plugin view registry. A Piglet selects it (viewClient: terminal);
// Stock PiG attaches none. The client attaches to the registry of the session's extension bridge, draws the mounts it is
// sent in the regions of viewSlots with the D107 view kit, and raises their intents from the keys and clicks of the
// user and, for a mount that holds the focus, from the keys the mount binds (view_client_bindings.go). Until a mount
// arrives it adds no component to the tree, runs no goroutine and no timer, and no frame runs its code.
//
// Every field below the mutex is owned by the interactive loop. The registry's sink and the goroutine of an intent in
// flight only append to the queue and post one drain task.

// viewHost is the registry side the client attaches to. *subprocess.UIBridge is one.
type viewHost interface {
	Views() *uiview.Registry
	InvokeViewIntentFrom(ctx context.Context, t *uiview.Attachment, extension string, ref uiview.Ref, nodeKey, intent string, arg json.RawMessage) uiview.Result
}

// viewRegion is where the terminal draws the mounts of a slot.
type viewRegion uint8

const (
	// viewAboveEditor is the widget container above the editor, after the widgets.
	viewAboveEditor viewRegion = iota
	// viewBelowEditor is the widget container below the editor, after the widgets.
	viewBelowEditor
	// viewPanel is the rows directly above the editor, under the widgets and the mounts of viewAboveEditor.
	viewPanel
	// viewDialog is a titled frame centred over the screen: an overlay of the renderer, outside the widget containers.
	viewDialog
)

// viewSlot is one slot the terminal draws.
type viewSlot struct {
	slot   uiview.Slot
	region viewRegion
	// rows is the most rows a mount of the slot is drawn with in a terminal of terminalRows rows. A taller view is cut
	// to it, its last row replaced by viewCutRow.
	rows func(terminalRows int) int
	// columns is the columns a mount of the slot is drawn with in a terminal of terminalColumns columns, and nil for a
	// slot as wide as the terminal.
	columns func(terminalColumns int) int
	// focus reports that a mount of the slot can hold the keyboard focus.
	focus bool
	// float reports that a mount of the slot is drawn over the screen in a frame of its own (see view_client_dialog.go).
	float bool
	// tabs reports that the mounts of the slot are tabs: one is drawn at a time, under a row of their titles (see
	// viewTabs). Without it every live mount of the slot is drawn, one under the other.
	tabs bool
}

// pig additive (D614): the row caps of the terminal's slots. A widget slot draws at most viewWidgetRows rows (Pi cuts a
// string widget at the same count); a panel at most half the terminal and never fewer than viewPanelMinRows.
const (
	viewWidgetRows   = 10
	viewPanelMinRows = 3
	viewCutRow       = "... (view truncated)"
)

func viewWidgetSlotRows(int) int { return viewWidgetRows }

func viewPanelSlotRows(terminalRows int) int { return max(viewPanelMinRows, terminalRows/2) }

// pig additive (D614): a dialog's frame is viewDialogFraction of the terminal's columns and of its rows, rounded down as
// the renderer rounds an overlay's fractions (tui.OverlayOptions), and its border takes one cell on every side.
const (
	viewDialogFraction = 0.8
	viewDialogBorder   = 2
)

func viewDialogSlotRows(terminalRows int) int { return viewDialogCells(terminalRows) }

func viewDialogSlotColumns(terminalColumns int) int { return viewDialogCells(terminalColumns) }

// viewDialogCells is the cells inside the frame of a dialog along one side of a terminal of terminalCells cells.
func viewDialogCells(terminalCells int) int {
	return max(1, int(float64(terminalCells)*viewDialogFraction)-viewDialogBorder)
}

// viewSlots is the table of the slots the terminal draws. A slot that is not here is not declared, so the registry
// sends the terminal no mount of it.
var viewSlots = []viewSlot{
	{slot: uiview.SlotWidgetAbove, region: viewAboveEditor, rows: viewWidgetSlotRows},
	{slot: uiview.SlotWidgetBelow, region: viewBelowEditor, rows: viewWidgetSlotRows},
	{slot: uiview.SlotPanel, region: viewPanel, rows: viewPanelSlotRows, focus: true, tabs: true},
	{slot: uiview.SlotDialog, region: viewDialog, rows: viewDialogSlotRows, columns: viewDialogSlotColumns, focus: true, float: true},
}

func viewSlotOf(slot uiview.Slot) *viewSlot {
	for i := range viewSlots {
		if viewSlots[i].slot == slot {
			return &viewSlots[i]
		}
	}
	return nil
}

// pig additive (D614): what one mount may hold for the terminal, and how many deliveries the client keeps for a loop
// that does not drain them before the registry's gap rule applies.
var terminalViewLimits = uiview.Limits{Nodes: 512, Depth: 16, SnapshotBytes: 262144, TextBytes: 65536, Mounts: 16, IntentArgBytes: 4096}

// terminalViewImageFormats are the formats of an image node the terminal draws, in the names the web client declares:
// the formats the image transcoder reads. The terminal declares them whatever it is, because a terminal without an
// image protocol draws the node too.
var terminalViewImageFormats = []string{"png", "jpeg", "gif", "webp"}

const viewQueueLimit = 64 // pig additive (D614): deliveries the client keeps for a loop that does not drain them

// pig additive (D614): the key that moves the keyboard focus to the next focusable panel and, after the last, back to the
// editor. It is not a keybinding id: Pi's keybinding table has no row for it.
const viewFocusKey = "f6"

// viewSize is what the abilities say about the terminal.
type viewSize struct {
	columns, rows int
	pointer       bool
}

// terminalViewAbilities is what the terminal declares: the slots of viewSlots, the node kinds the D107 view kit draws
// for a mount, keyboard and text input, the pointer where the terminal reports it, and as claims the kind of client,
// the columns of a mount and, per slot, the rows it is drawn with and the columns of a slot narrower than the terminal.
func terminalViewAbilities(size viewSize) uiview.ClientAbilities {
	integer := func(n int) json.RawMessage { return json.RawMessage(strconv.Itoa(max(n, 0))) }
	a := uiview.ClientAbilities{
		Nodes: subprocess.MountNodeKinds(), Markdown: true, ImageFormats: slices.Clone(terminalViewImageFormats),
		Limits: terminalViewLimits,
		Input:  uiview.Input{Keyboard: true, Pointer: size.pointer, Text: true},
		Claims: []uiview.Claim{
			{Name: "client.kind", Value: json.RawMessage(`"terminal"`)},
			{Name: "size.columns", Value: integer(size.columns)},
		},
	}
	for i := range viewSlots {
		s := &viewSlots[i]
		a.Slots = append(a.Slots, s.slot)
		claims := []uiview.Claim{{Name: "size.rows", Value: integer(s.rows(size.rows))}}
		if s.columns != nil {
			claims = append(claims, uiview.Claim{Name: "size.columns", Value: integer(s.columns(size.columns))})
		}
		a.SlotClaims = append(a.SlotClaims, uiview.SlotClaims{Slot: s.slot, Claims: claims})
	}
	return a
}

// viewClientUI is the interactive loop as the client uses it.
type viewClientUI struct {
	// post queues fn on the loop without waiting and reports whether the loop took it.
	post func(fn func()) bool
	// postWait queues fn on the loop, waiting for room until ctx ends.
	postWait func(ctx context.Context, fn func())
	// layout sets the mounts the widget containers draw, in mount order.
	layout func(mounts []*viewMount)
	// render asks for a frame.
	render func()
	// focus gives the keyboard focus to mount, or to the editor with nil.
	focus func(mount *viewMount)
	// float draws mount over the screen in a frame titled title, on top of every frame drawn before, and gives it the
	// keyboard focus. It returns the frame, or nil when the renderer drew none.
	float func(mount *viewMount, title string) viewFloat
	// waitRow sets the row that stands for the dialogs while none holds the keyboard, directly above the editor, or none
	// with nil.
	waitRow func(row *viewWaiting)
	// editorFocused reports whether the editor holds the keyboard focus.
	editorFocused func() bool
	// editorEmpty reports whether the editor holds no text.
	editorEmpty func() bool
	// tabRow sets the tab row the panel region draws above its mounts, or none with nil.
	tabRow func(row *viewTabs)
	// focusRow gives the keyboard focus to the tab row.
	focusRow func(row *viewTabs)
	// confirms reports whether data is the key that confirms a selection (tui.select.confirm).
	confirms func(data string) bool
	// listen registers an input listener and returns the function that removes it.
	listen func(listener *tui.TuiInputListener) (remove func())
	// interrupts reports whether data is the key that interrupts (app.interrupt).
	interrupts func(data string) bool
	// reserved returns the reserved action (viewReservedActions) the user's keybindings give data to, or "".
	reserved func(data string) string
	// notify shows one notice line.
	notify func(message, level string)
	// size returns the terminal's columns and rows and whether it reports the pointer.
	size func() viewSize
	// write writes bytes to the terminal ahead of the frame being drawn (nil: no such writer); placeholders reports
	// that the terminal draws Kitty Unicode placeholders. An image node is transmitted once through write and drawn
	// as placeholder cells when both hold.
	write        func(data string)
	placeholders bool
}

// viewFloat is the frame of a mount that is drawn over the screen. The renderer's overlay is one.
type viewFloat interface {
	// Close removes the frame. The keyboard focus it held returns to what held it before.
	Close()
	// Leave returns the keyboard focus to the editor.
	Leave()
}

type viewMountKey struct{ plugin, key string }

// viewIntentResult is the settled intent of one mount.
type viewIntentResult struct {
	key    viewMountKey
	flight uint64
	intent string
	result uiview.Result
}

// viewItem is one entry of the client's queue: a delivery of the registry or the result of an intent.
type viewItem struct {
	delivery *uiview.Delivery
	result   *viewIntentResult
}

// viewClient is the terminal's attachment to one registry.
type viewClient struct {
	host viewHost
	ui   viewClientUI
	att  *uiview.Attachment
	ctx  context.Context
	end  context.CancelFunc

	// images is what the mounts' surfaces share to draw image nodes.
	images *subprocess.MountImages

	mu sync.Mutex
	// queue holds the deliveries and intent results the loop has not applied, in the order they were made. deliveries
	// counts its deliveries: the sink refuses one more than viewQueueLimit, which opens the registry's gap. gap records
	// that, so the next drain resyncs.
	queue      []viewItem
	deliveries int
	gap        bool
	// scheduled reports that a drain task is on its way to the loop.
	scheduled atomic.Bool

	mounts map[viewMountKey]*viewMount
	// order is the mounts in the order they were first sent.
	order []*viewMount
	// drawn is the mounts last given to layout.
	drawn []*viewMount
	// floats is the visible mounts of float slots in the order they were shown: the last is on top. left reports that
	// one of them lost the keyboard since the last settle. waiting is the row that stands for them while none holds the
	// keyboard, and waitingOn reports that it is in the tree.
	floats    []*viewMount
	left      bool
	waiting   *viewWaiting
	waitingOn bool
	// focused is the mount that holds the keyboard focus.
	focused *viewMount
	// asks holds, in the order they arrived, the mounts that asked to be shown (they turned live) or for the focus (their
	// focus record requests it) and were not answered yet. A mount that is not live waits here.
	asks []*viewMount
	// row is the tab row, made when a slot with tabs first lists a mount; rowOn reports that it is in the tree.
	row   *viewTabs
	rowOn bool
	// front is the tab whose view is drawn, and at the entry the tab row stands on while it holds the focus: a hidden
	// mount the user can open, or a live one whose view takes no key.
	front, at *viewMount
	// stamp changes whenever what the tab row draws may have changed.
	stamp uint64
	// seq is the number of the last delivery the client applied, or of the last resync.
	seq uint64
	// dirty reports that what the terminal draws changed since the last frame the client asked for.
	dirty    bool
	declared viewSize
	unlisten func()
	listener tui.TuiInputListener
	flight   uint64
	// droppedActivations counts the activations made while an intent of the same mount was in flight. Such an
	// activation is not raised: it was made on a view whose answer the user has not seen.
	droppedActivations int
}

// attachViewClient attaches the terminal to host's registry and draws the mounts it holds. It returns nil when the
// registry takes no further client. It runs on the interactive loop.
func attachViewClient(ctx context.Context, host viewHost, ui viewClientUI) *viewClient {
	c := &viewClient{host: host, ui: ui, mounts: map[viewMountKey]*viewMount{}}
	// The conversion of an image to PNG runs off the loop, on the goroutine the surface starts for it, and the mount is
	// told on the loop that its image is ready.
	c.images = &subprocess.MountImages{
		Write: ui.write, Placeholders: ui.placeholders, ToPNG: ConvertImageBytesToPng, Decode: imageprocessing.DecodeImage,
		Post: func(fn func()) { ui.postWait(c.ctx, fn) },
	}
	c.ctx, c.end = context.WithCancel(ctx)
	c.listener = c.focusKey
	c.declared = ui.size()
	att, current, err := host.Views().Attach(terminalViewAbilities(c.declared), c.sink)
	if err != nil {
		c.end()
		return nil
	}
	c.att = att
	c.resync(current)
	c.settle()
	return c
}

// detach ends the attachment and drops every mount. It runs on the interactive loop.
func (c *viewClient) detach() {
	if c.att == nil {
		return
	}
	c.att.Detach()
	c.att = nil
	c.end()
	for _, m := range slices.Clone(c.order) {
		c.drop(m)
	}
	// The context has ended, so an image being prepared is not posted to the loop: its goroutine ends with its work.
	c.images.Wait()
	c.mu.Lock()
	c.queue, c.deliveries, c.gap = nil, 0, false
	c.mu.Unlock()
	c.settle()
}

// sink takes one delivery. It runs under the registry's lock on the goroutine whose call changed the mount, so it only
// appends and wakes the loop. A full queue refuses the delivery, which is the registry's gap.
func (c *viewClient) sink(d uiview.Delivery) bool {
	c.mu.Lock()
	if c.deliveries >= viewQueueLimit {
		c.gap = true
		c.mu.Unlock()
		c.wake()
		return false
	}
	c.queue = append(c.queue, viewItem{delivery: &d})
	c.deliveries++
	c.mu.Unlock()
	c.wake()
	return true
}

// wake posts one drain task unless one is on its way. It never waits: when the loop's queue is full the task is handed
// over by a goroutine that ends when the loop took it or the client ended.
func (c *viewClient) wake() {
	if c.scheduled.Swap(true) {
		return
	}
	if !c.ui.post(c.drain) {
		go c.ui.postWait(c.ctx, c.drain)
	}
}

// drain applies what the queue holds, in order, then repairs a gap, and brings the abilities, the layout and the focus
// up to date. It runs on the interactive loop.
func (c *viewClient) drain() {
	c.scheduled.Store(false)
	if c.att == nil {
		return
	}
	c.mu.Lock()
	items, gap := c.queue, c.gap
	c.queue, c.deliveries, c.gap = nil, 0, false
	c.mu.Unlock()
	for i := range items {
		switch it := &items[i]; {
		case it.result != nil:
			c.settled(it.result)
		case it.delivery.Seq <= c.seq:
			// Older than the last resync.
		case it.delivery.Seq != c.seq+1:
			// A hole in the numbers: the client missed a delivery and resyncs.
			gap = true
		default:
			c.seq = it.delivery.Seq
			c.apply(&it.delivery.Snapshot)
		}
	}
	if gap {
		// The registry sends nothing more until the resync, so what was queued is older than its reply.
		if current, err := c.att.Resync(); err == nil {
			c.resync(current)
		}
	}
	if size := c.ui.size(); size != c.declared {
		c.declared = size
		_ = c.att.UpdateAbilities(terminalViewAbilities(size))
		for _, m := range c.order {
			m.lines = nil
		}
	}
	c.settle()
}

// resync replaces what the client draws by every current snapshot: a mount the reply lacks is dropped, and a snapshot
// whose digest the client already drew is kept as drawn.
func (c *viewClient) resync(current uiview.Resync) {
	c.seq = current.Seq
	held := make(map[viewMountKey]bool, len(current.Snapshots))
	for i := range current.Snapshots {
		s := &current.Snapshots[i]
		held[viewMountKey{s.Plugin, s.Mount.Key}] = true
	}
	for _, m := range slices.Clone(c.order) {
		if !held[m.key] {
			c.drop(m)
		}
	}
	for i := range current.Snapshots {
		c.apply(&current.Snapshots[i])
	}
}

// apply takes one snapshot: it makes, redraws, hides or drops the mount.
func (c *viewClient) apply(s *uiview.Snapshot) {
	key := viewMountKey{s.Plugin, s.Mount.Key}
	m := c.mounts[key]
	if m != nil && m.ref.Epoch != s.Mount.Epoch {
		c.drop(m)
		m = nil
	}
	if s.State != uiview.StateLive && s.State != uiview.StateHidden {
		if m != nil {
			c.drop(m)
		}
		return
	}
	slot := viewSlotOf(s.Slot)
	if slot == nil {
		return
	}
	fresh := m == nil
	if fresh {
		m = &viewMount{c: c, key: key, slot: slot}
		m.surface = subprocess.NewMountSurface(subprocess.MountSurfaceOptions{
			Repaint:      m.repaint,
			Activate:     m.activate,
			Leave:        m.leave,
			TerminalRows: func() int { return c.ui.size().rows },
			RowBudget:    func() int { return m.slot.rows(c.ui.size().rows) },
			Images:       c.images,
		})
		c.mounts[key] = m
		c.order = append(c.order, m)
	}
	redraw := fresh || m.ref != s.Mount || m.digest != s.Digest || s.Digest == ""
	// A focus record asks for the focus when its mount is first drawn (mount) or when the revision that carries it is
	// (update); a snapshot the client already holds asks nothing again.
	var request *uiview.Focus
	for i := range s.Focus {
		if f := &s.Focus[i]; (f.Request == uiview.FocusOnMount && fresh) || (f.Request == uiview.FocusOnUpdate && (fresh || m.ref.Rev != s.Mount.Rev)) {
			request = f
		}
	}
	if m.frame != nil && m.title != s.Title {
		// The frame carries the title, so a mount with another title is drawn in a new frame, on top.
		c.unfloat(m)
	}
	m.ref, m.intents, m.digest, m.title = s.Mount, s.Intents, s.Digest, s.Title
	if redraw {
		m.bind(s.Bindings)
		for i := range s.Intents {
			// The intent a hidden mount takes is offered on the tab row while the mount is hidden, one a key of the
			// mount is bound to is offered by that key, and a dialog's dismiss intent by the key that interrupts.
			if d := &s.Intents[i]; d.NodeKey == "" && (!d.WhenHidden || !slot.tabs) && !m.binds(d.Name) && (!d.Dismiss || !slot.float) {
				m.reportNotOffered(d.Name, "the terminal offers no control for intent "+d.Name+", which names no node")
			}
		}
		m.drawable = false
		if len(s.View) > 0 {
			if err := m.surface.Accept(s.View, m.focusNode(s.Focus, request)); err != nil {
				// The surface keeps the view it had; a mount with none draws nothing.
				c.report(m, uiview.Notice{Code: uiview.NoticeUndrawn, Severity: uiview.SeverityError, Path: "view", Message: err.Error()})
				m.drawable = m.accepted
			} else {
				m.accepted, m.drawable = true, true
			}
		}
		m.lines = nil
	}
	live := m.drawable && s.State == uiview.StateLive
	hidden := s.State == uiview.StateHidden
	c.dirty = c.dirty || redraw || live != m.live || hidden != m.hidden
	// A mount that turns live asks to be shown, and one whose focus record requests it asks for the focus.
	if (slot.tabs && live && !m.live) || (slot.focus && request != nil) {
		m.asksFocus = m.asksFocus || (slot.focus && request != nil)
		c.asks = append(slices.DeleteFunc(c.asks, func(o *viewMount) bool { return o == m }), m)
	}
	m.live, m.hidden = live, hidden
}

// drop ends a mount: its intent in flight is cancelled and its surface closed.
func (c *viewClient) drop(m *viewMount) {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	c.unfloat(m)
	m.surface.Close()
	c.dirty = c.dirty || m.visible || m.hidden
	m.visible, m.live, m.hidden = false, false, false
	delete(c.mounts, m.key)
	c.order = slices.DeleteFunc(c.order, func(o *viewMount) bool { return o == m })
	c.asks = slices.DeleteFunc(c.asks, func(o *viewMount) bool { return o == m })
	if c.front == m {
		c.front = nil
	}
	if c.at == m {
		c.at = nil
	}
}

// settle answers the mounts that asked to be shown or for the focus, brings the layout, the focus key and the focus up
// to date after a change, and asks for a frame when what the terminal draws changed.
func (c *viewClient) settle() {
	c.arrange()
	waiting := c.asks[:0]
	for _, m := range c.asks {
		if !m.live {
			waiting = append(waiting, m)
			continue
		}
		c.show(m)
		c.arrange()
		if m.asksFocus {
			m.asksFocus = false
			c.grant(m)
		}
	}
	clear(c.asks[len(waiting):])
	c.asks = waiting
	c.settleFloats()
	switch stops := len(c.stops()) + len(c.floats); {
	case stops > 0 && c.unlisten == nil:
		c.unlisten = c.ui.listen(&c.listener)
	case stops == 0 && c.unlisten != nil:
		c.unlisten()
		c.unlisten = nil
	}
	// The tab row holds the focus only while it stands on an entry whose view takes no key.
	switch at := c.at; {
	case at == nil && c.row != nil && c.row.focused:
		c.ui.focus(nil)
		c.dirty = true
	case at == nil:
	case at.takesFocus():
		c.at = nil
		c.ui.focus(at)
		c.dirty = true
	case !c.row.focused:
		c.ui.focusRow(c.row)
		c.dirty = true
	}
	if m := c.focused; m != nil && !m.takesFocus() {
		c.ui.focus(nil)
		c.dirty = true
	}
	// A drain that changed nothing the terminal draws asks for no frame.
	if c.dirty {
		c.dirty = false
		c.stamp++
		c.ui.render()
	}
}

// grant answers the focus request of m, a live mount. The focus moves only where it does not take the keys from the
// user: into the view the user is already in or on whose tab the tab row stands, or from an editor that holds no text.
// A refused request is told to the plugin as an unfocused notice; the registry counts the repeats.
func (c *viewClient) grant(m *viewMount) {
	var why string
	switch {
	case c.focused == m:
		return
	case c.at != m && !c.ui.editorFocused():
		why = "another component holds the keyboard focus"
	case c.at != m && !c.ui.editorEmpty():
		why = "the editor holds text"
	case m.slot.float:
		// A dialog that waits on its row is entered: every dialog of the stack is drawn, the last on top.
		c.enter()
		return
	case !m.takesFocus():
		why = "the terminal gives the nodes of the view no keyboard focus"
	case c.at == m:
		// settle moves the focus from the tab row into the view.
		return
	default:
		c.ui.focus(m)
		c.dirty = true
		return
	}
	c.report(m, uiview.Notice{Code: uiview.NoticeUnfocused, Severity: uiview.SeverityInfo, Path: "focus", Message: why})
}

// rewired tells the client that the renderer was replaced: the focus key's listener went with the old one, and the
// focus is the editor's again. It does not draw; the drain it posts does.
func (c *viewClient) rewired() {
	c.unlisten, c.at = nil, nil
	c.wake()
}

// focusKey is the input listener of viewFocusKey: from the editor it moves to the first stop, from a stop to the next,
// and from the last back to the editor. The stops are the focusable panels, or the entries of the tab row while it is
// drawn (see stops). While a dialog is live the key moves between the editor and the dialogs and passes every other
// stop by: it draws their frames with the keyboard, and from the top frame it returns to the editor. While something
// else holds the focus it passes the key on.
func (c *viewClient) focusKey(data string) *tui.TuiInputResult {
	if !tui.MatchesKeyID(data, viewFocusKey) {
		return nil
	}
	if len(c.floats) > 0 {
		switch top := c.floats[len(c.floats)-1]; {
		case top.focused:
			top.frame.Leave()
		case c.standing() != nil || c.ui.editorFocused():
			c.enter()
		default:
			return nil
		}
		c.settle()
		c.ui.render()
		return &tui.TuiInputResult{Consume: true}
	}
	panels, standing := c.stops(), c.standing()
	switch at := slices.Index(panels, standing); {
	case standing != nil && at >= 0 && at+1 < len(panels):
		c.visit(panels[at+1])
	case standing != nil:
		c.ui.focus(nil)
	case len(panels) > 0 && c.ui.editorFocused():
		c.visit(panels[0])
	default:
		return nil
	}
	c.ui.render()
	return &tui.TuiInputResult{Consume: true}
}

// unhandledKey takes a key the focused node of a panel did not consume. It is the one place a key reaches the mount's
// bindings: the mount holds the focus and its node declined the key. The key the terminal reported is compared with the
// parsed key of each binding (tui.MatchesKeyID), never with its text, so the order the plugin wrote the modifiers in
// changes nothing. The first match raises the binding's intent, with no node, and its argument; a key no binding names
// is dropped. At most 32 bindings are compared. A key the user's keybindings give to a reserved action is asked for
// first, when the key is pressed, and reaches no binding, whatever the mount binds and whenever the keybindings changed.
func (c *viewClient) unhandledKey(m *viewMount, data string) {
	if !m.focused || !m.keys {
		return
	}
	if action := c.ui.reserved(data); action != "" {
		c.withheld(m, data, action)
		return
	}
	for i := range m.bindings {
		b := &m.bindings[i]
		if b.Key == "" || !tui.MatchesKeyID(data, b.Key) {
			continue
		}
		at := slices.IndexFunc(m.intents, func(d uiview.IntentDecl) bool { return d.Name == b.Intent && d.NodeKey == "" })
		if at < 0 {
			return
		}
		arg := b.Arg
		if len(arg) == 0 {
			arg = json.RawMessage(`{}`)
		}
		c.raise(m, &m.intents[at], arg)
		return
	}
}

// withheld tells the plugin, once for a binding, that the key it names is the user's key of the reserved action, so the
// terminal does not honour the binding.
func (c *viewClient) withheld(m *viewMount, data, action string) {
	for i := range m.bindings {
		if b := &m.bindings[i]; b.Key != "" && tui.MatchesKeyID(data, b.Key) {
			m.reportUnbound(i, b.Key, "key "+b.Key+" is the user's key of "+action+", so it raises nothing")
			return
		}
	}
}

// raise raises one intent of m off the loop. A mount has at most one intent in flight: an activation made before the
// last one settled was made on a view whose answer the user has not seen (the registry would settle it stale once the
// handler updates the view), so it is dropped and counted.
func (c *viewClient) raise(m *viewMount, d *uiview.IntentDecl, arg json.RawMessage) {
	if m.cancel != nil {
		c.droppedActivations++
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.flight++
	m.cancel, m.flight, m.cancellable = cancel, c.flight, d.Cancellable
	done := viewIntentResult{key: m.key, flight: c.flight, intent: d.Name}
	plugin, ref, node, att := m.key.plugin, m.ref, d.NodeKey, c.att
	go func() {
		done.result = c.host.InvokeViewIntentFrom(ctx, att, plugin, ref, node, done.intent, arg)
		cancel()
		c.mu.Lock()
		c.queue = append(c.queue, viewItem{result: &done})
		c.mu.Unlock()
		c.wake()
	}()
}

// settled takes the result of an intent. The plugin shows what a done intent did by updating its view. A refused, failed
// or timed-out intent gets one warning line; a stale or cancelled one nothing, as the view changed or the user left.
func (c *viewClient) settled(r *viewIntentResult) {
	m := c.mounts[r.key]
	if m == nil || m.flight != r.flight {
		return
	}
	m.cancel = nil
	switch r.result.Outcome {
	case uiview.OutcomeRefused, uiview.OutcomeFailed, uiview.OutcomeTimeout:
		message := fmt.Sprintf("%s: %s %s", r.key.plugin, r.intent, r.result.Outcome)
		if r.result.Error != "" {
			message += ": " + r.result.Error
		}
		c.ui.notify(message, "warning")
	}
}

// report tells the plugin what the terminal could not draw or offer.
func (c *viewClient) report(m *viewMount, n uiview.Notice) {
	if c.att != nil {
		_ = c.att.Report(m.key.plugin, m.ref, n)
	}
}

// intentArgument builds an intent's argument from its declared shape and what the activated node gives, as the
// reference web client does: an object's property named value takes the node's value and one named item the activated
// item's id; a boolean, string, integer, number or enum shape takes the node's value itself. It reports false when the
// shape asks for more than the node gives.
func intentArgument(shape uiview.Shape, value *string, item string) (json.RawMessage, bool) {
	if shape.Type != "object" {
		return scalarArgument(shape, value)
	}
	members := map[string]json.RawMessage{}
	for name, property := range shape.Properties {
		var member json.RawMessage
		ok := false
		switch name {
		case "value":
			member, ok = scalarArgument(property, value)
		case "item":
			if item != "" {
				member, ok = scalarArgument(property, &item)
			}
		}
		if ok {
			members[name] = member
		} else if slices.Contains(shape.Required, name) {
			return nil, false
		}
	}
	arg, err := json.Marshal(members) //portlint:allow mapkeyorder pig additive (D614), no Pi counterpart: the argument has at most the members item and value, and the registry and the extension read it as an object
	return arg, err == nil
}

func scalarArgument(shape uiview.Shape, value *string) (json.RawMessage, bool) {
	if value == nil {
		return nil, false
	}
	switch shape.Type {
	case "string":
		arg, err := json.Marshal(*value)
		return arg, err == nil
	case "enum":
		if !slices.Contains(shape.Enum, *value) {
			return nil, false
		}
		arg, err := json.Marshal(*value)
		return arg, err == nil
	case "boolean":
		if *value != "true" && *value != "false" {
			return nil, false
		}
		return json.RawMessage(*value), true
	case "integer":
		n, err := strconv.ParseInt(*value, 10, 64)
		return json.RawMessage(strconv.FormatInt(n, 10)), err == nil
	case "number":
		n, err := strconv.ParseFloat(*value, 64)
		if err != nil {
			return nil, false
		}
		arg, err := json.Marshal(n)
		return arg, err == nil
	}
	return nil, false
}
