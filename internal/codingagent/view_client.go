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
	"github.com/MichaelKinsy/PiG/tui"
)

// pig additive (D614): the terminal as a client of the plugin view registry. A Piglet selects it (viewClient: terminal);
// Stock PiG attaches none. The client attaches to the registry of the session's extension bridge, draws the mounts it is
// sent in the regions of viewSlots with the D107 view kit, and raises their intents from the keys and clicks of the
// user. Until a mount arrives it adds no component to the tree, runs no goroutine and no timer, and no frame runs its
// code.
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
)

// viewSlot is one slot the terminal draws.
type viewSlot struct {
	slot   uiview.Slot
	region viewRegion
	// rows is the most rows a mount of the slot is drawn with in a terminal of terminalRows rows. A taller view is cut
	// to it, its last row replaced by viewCutRow.
	rows func(terminalRows int) int
	// focus reports that a mount of the slot can hold the keyboard focus.
	focus bool
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

// viewSlots is the table of the slots the terminal draws. A slot that is not here is not declared, so the registry
// sends the terminal no mount of it.
var viewSlots = []viewSlot{
	{slot: uiview.SlotWidgetAbove, region: viewAboveEditor, rows: viewWidgetSlotRows},
	{slot: uiview.SlotWidgetBelow, region: viewBelowEditor, rows: viewWidgetSlotRows},
	{slot: uiview.SlotPanel, region: viewPanel, rows: viewPanelSlotRows, focus: true},
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

const viewQueueLimit = 64

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
// the columns of a mount and, per slot, the rows it is drawn with.
func terminalViewAbilities(size viewSize) uiview.ClientAbilities {
	integer := func(n int) json.RawMessage { return json.RawMessage(strconv.Itoa(max(n, 0))) }
	a := uiview.ClientAbilities{
		Nodes: subprocess.MountNodeKinds(), Markdown: true, ImageFormats: []string{},
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
		a.SlotClaims = append(a.SlotClaims, uiview.SlotClaims{Slot: s.slot, Claims: []uiview.Claim{{Name: "size.rows", Value: integer(s.rows(size.rows))}}})
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
	// editorFocused reports whether the editor holds the keyboard focus.
	editorFocused func() bool
	// listen registers an input listener and returns the function that removes it.
	listen func(listener *tui.TuiInputListener) (remove func())
	// interrupts reports whether data is the key that interrupts (app.interrupt).
	interrupts func(data string) bool
	// notify shows one notice line.
	notify func(message, level string)
	// size returns the terminal's columns and rows and whether it reports the pointer.
	size func() viewSize
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
	// focused is the mount that holds the keyboard focus, and wanted the one whose focus record asked for it and has
	// not been given it yet.
	focused, wanted *viewMount
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
		})
		c.mounts[key] = m
		c.order = append(c.order, m)
	}
	redraw := fresh || m.ref != s.Mount || m.digest != s.Digest || s.Digest == ""
	m.ref, m.intents, m.digest = s.Mount, s.Intents, s.Digest
	if redraw {
		for i := range s.Intents {
			if d := &s.Intents[i]; d.NodeKey == "" {
				m.reportNotOffered(d.Name, "the terminal offers no control for intent "+d.Name+", which names no node")
			}
		}
		m.drawable = false
		if len(s.View) > 0 {
			focus := ""
			if len(s.Focus) > 0 {
				focus = s.Focus[0].Node
			}
			if err := m.surface.Accept(s.View, focus); err != nil {
				// The surface keeps the view it had; a mount with none draws nothing.
				c.report(m, uiview.Notice{Code: uiview.NoticeUndrawn, Severity: uiview.SeverityError, Path: "view", Message: err.Error()})
				m.drawable = m.accepted
			} else {
				m.accepted, m.drawable = true, true
			}
		}
		m.lines = nil
	}
	visible := m.drawable && s.State == uiview.StateLive
	c.dirty = c.dirty || redraw || visible != m.visible
	m.visible = visible
	if fresh && slot.focus && slices.ContainsFunc(s.Focus, func(f uiview.Focus) bool { return f.Request == "mount" }) {
		c.wanted = m
	}
}

// drop ends a mount: its intent in flight is cancelled and its surface closed.
func (c *viewClient) drop(m *viewMount) {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.surface.Close()
	c.dirty = c.dirty || m.visible
	m.visible = false
	delete(c.mounts, m.key)
	c.order = slices.DeleteFunc(c.order, func(o *viewMount) bool { return o == m })
	if c.wanted == m {
		c.wanted = nil
	}
}

// settle brings the layout, the focus key and the focus up to date after a change, and asks for a frame when what the
// terminal draws changed.
func (c *viewClient) settle() {
	drawn := make([]*viewMount, 0, len(c.order))
	focusable := false
	for _, m := range c.order {
		if m.visible {
			drawn = append(drawn, m)
			focusable = focusable || m.takesFocus()
		}
	}
	if !slices.Equal(drawn, c.drawn) {
		c.drawn = drawn
		c.ui.layout(drawn)
	}
	switch {
	case focusable && c.unlisten == nil:
		c.unlisten = c.ui.listen(&c.listener)
	case !focusable && c.unlisten != nil:
		c.unlisten()
		c.unlisten = nil
	}
	if m := c.focused; m != nil && !m.takesFocus() {
		c.ui.focus(nil)
		c.dirty = true
	}
	if m := c.wanted; m != nil && m.visible {
		c.wanted = nil
		if m.takesFocus() && c.ui.editorFocused() {
			c.ui.focus(m)
			c.dirty = true
		}
	}
	// A drain that changed nothing the terminal draws asks for no frame.
	if c.dirty {
		c.dirty = false
		c.ui.render()
	}
}

// rewired tells the client that the renderer was replaced: the focus key's listener went with the old one, and the
// focus is the editor's again. It does not draw; the drain it posts does.
func (c *viewClient) rewired() {
	c.unlisten = nil
	c.wake()
}

// focusKey is the input listener of viewFocusKey: from the editor it gives the focus to the first focusable panel, from
// a panel to the next, and from the last back to the editor. While something else holds the focus it passes the key on.
func (c *viewClient) focusKey(data string) *tui.TuiInputResult {
	if !tui.MatchesKeyID(data, viewFocusKey) {
		return nil
	}
	var panels []*viewMount
	for _, m := range c.drawn {
		if m.takesFocus() {
			panels = append(panels, m)
		}
	}
	switch at := slices.Index(panels, c.focused); {
	case c.focused != nil && at >= 0 && at+1 < len(panels):
		c.ui.focus(panels[at+1])
	case c.focused != nil:
		c.ui.focus(nil)
	case len(panels) > 0 && c.ui.editorFocused():
		c.ui.focus(panels[0])
	default:
		return nil
	}
	c.ui.render()
	return &tui.TuiInputResult{Consume: true}
}

// unhandledKey takes a key the focused node of a panel did not consume. The terminal drops it.
func (c *viewClient) unhandledKey(*viewMount, string) {}

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
	arg, err := json.Marshal(members)
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
