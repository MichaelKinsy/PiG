package codingagent

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// viewTestHost is a registry with a plugin behind it that a test scripts.
type viewTestHost struct {
	reg    *uiview.Registry
	invoke func(ctx context.Context, inv uiview.Invocation) (uiview.Result, error)
}

func (h *viewTestHost) Views() *uiview.Registry { return h.reg }

func (h *viewTestHost) Invoke(ctx context.Context, inv uiview.Invocation) (uiview.Result, error) {
	return h.invoke(ctx, inv)
}

func (h *viewTestHost) InvokeViewIntentFrom(ctx context.Context, t *uiview.Attachment, extension string, ref uiview.Ref, nodeKey, intent string, arg json.RawMessage) uiview.Result {
	return t.Invoke(ctx, extension, ref, nodeKey, intent, arg, h)
}

// viewTestLoop stands in for the interactive loop: tasks run when the test runs them.
type viewTestLoop struct {
	t         *testing.T
	mu        sync.Mutex
	tasks     []func()
	posted    chan struct{}
	full      bool
	laid      [][]*viewMount
	renders   int
	focused   *viewMount
	elsewhere bool
	// row is the tab row the client laid, onRow reports that it holds the focus, and typed that the editor holds text.
	row     *viewTabs
	onRow   bool
	typed   bool
	notices []string
	// onNotice runs when the client writes a notice, before it is recorded.
	onNotice  func()
	size      viewSize
	listeners []*tui.TuiInputListener
	// keybindings are the user's: Pi's defaults until a test gives an action other keys.
	keybindings *KeybindingsManager
	// frames is every frame the client drew over the screen, closed ones too, in the order it drew them.
	frames []*viewTestFrame
	// waiting is the row the client laid for the dialogs that hold no keyboard, or nil.
	waiting *viewWaiting
}

// viewTestFrame is a frame the test loop drew over the screen. It behaves as the renderer's overlay does: it takes the
// focus when it is drawn, and closed while it holds the focus it gives it back to what held it before.
type viewTestFrame struct {
	l      *viewTestLoop
	mount  *viewMount
	title  string
	before *viewMount
	closed bool
}

func (fr *viewTestFrame) Close() {
	fr.closed = true
	if fr.l.focused != fr.mount {
		return
	}
	if fr.before != nil && fr.before.visible {
		fr.l.focus(fr.before)
		return
	}
	fr.l.focus(nil)
}

func (fr *viewTestFrame) Leave() { fr.l.focus(nil) }

// open is the frames on the screen, the top one last.
func (l *viewTestLoop) open() []*viewTestFrame {
	var open []*viewTestFrame
	for _, fr := range l.frames {
		if !fr.closed {
			open = append(open, fr)
		}
	}
	return open
}

// focus moves the keyboard focus to mount, or to the editor with nil, as the renderer does.
func (l *viewTestLoop) focus(mount *viewMount) {
	previous, wasOnRow := l.focused, l.onRow
	l.focused, l.elsewhere, l.onRow = mount, false, false
	if wasOnRow {
		l.row.SetFocused(false)
	}
	if previous != nil && previous != mount {
		previous.SetFocused(false)
	}
	if mount != nil {
		mount.SetFocused(true)
	}
}

func newViewTestLoop(t *testing.T) *viewTestLoop {
	return &viewTestLoop{t: t, posted: make(chan struct{}, 4096), size: viewSize{columns: 40, rows: 24}, keybindings: NewKeybindingsManagerFromBindings(nil, "")}
}

func (l *viewTestLoop) post(fn func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.full {
		return false
	}
	l.tasks = append(l.tasks, fn)
	l.posted <- struct{}{}
	return true
}

func (l *viewTestLoop) ui() viewClientUI {
	return viewClientUI{
		post: l.post,
		postWait: func(_ context.Context, fn func()) {
			l.mu.Lock()
			l.tasks = append(l.tasks, fn)
			l.mu.Unlock()
			l.posted <- struct{}{}
		},
		layout: func(mounts []*viewMount) { l.laid = append(l.laid, slices.Clone(mounts)) },
		render: func() { l.renders++ },
		focus:  l.focus,
		float: func(mount *viewMount, title string) viewFloat {
			fr := &viewTestFrame{l: l, mount: mount, title: title, before: l.focused}
			l.frames = append(l.frames, fr)
			l.focus(mount)
			return fr
		},
		waitRow:       func(row *viewWaiting) { l.waiting = row },
		editorFocused: func() bool { return l.focused == nil && !l.elsewhere && !l.onRow },
		editorEmpty:   func() bool { return !l.typed },
		tabRow:        func(row *viewTabs) { l.row = row },
		focusRow: func(row *viewTabs) {
			previous := l.focused
			l.focused, l.elsewhere, l.onRow = nil, false, true
			if previous != nil {
				previous.SetFocused(false)
			}
			row.SetFocused(true)
		},
		confirms: func(data string) bool { return data == "\r" },
		listen: func(listener *tui.TuiInputListener) func() {
			l.listeners = append(l.listeners, listener)
			return func() {
				l.listeners = slices.DeleteFunc(l.listeners, func(o *tui.TuiInputListener) bool { return o == listener })
			}
		},
		interrupts: func(data string) bool { return l.keybindings.Matches(data, string(AppInterrupt)) },
		reserved:   func(data string) string { return viewReservedAction(l.keybindings, data) },
		notify: func(message, level string) {
			if l.onNotice != nil {
				l.onNotice()
			}
			l.notices = append(l.notices, level+": "+message)
		},
		size: func() viewSize { return l.size },
	}
}

// run runs every task the loop holds, and those they post.
func (l *viewTestLoop) run() {
	for {
		l.mu.Lock()
		if len(l.tasks) == 0 {
			l.mu.Unlock()
			return
		}
		fn := l.tasks[0]
		l.tasks = l.tasks[1:]
		l.mu.Unlock()
		fn()
	}
}

// key sends data through the input listeners and reports whether one consumed it.
func (l *viewTestLoop) key(data string) bool {
	for _, listener := range slices.Clone(l.listeners) {
		if result := (*listener)(data); result != nil && result.Consume {
			return true
		}
	}
	return false
}

func (l *viewTestLoop) drawn() []*viewMount {
	if len(l.laid) == 0 {
		return nil
	}
	return l.laid[len(l.laid)-1]
}

func viewText(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"root": map[string]any{"kind": "text", "text": text}})
	return raw
}

func viewList(title string) json.RawMessage {
	return json.RawMessage(`{"root":{"kind":"vstack","children":[{"kind":"text","text":` + strconv.Quote(title) + `},` +
		`{"kind":"select-list","id":"pick","maxVisible":4,"items":[{"value":"a","label":"Alpha"},{"value":"b","label":"Beta"}]}]},"focus":"pick"}`)
}

var viewPickIntent = uiview.IntentDecl{
	Name: "ext.pick", NodeKey: "pick", Cancellable: true, TimeoutMs: 30000,
	Arg: uiview.Shape{Type: "object", Properties: map[string]uiview.Shape{"value": {Type: "string"}}, Required: []string{"value"}},
}

// viewRows is what the mount draws at 40 columns, without styling, padding and blank rows.
func viewRows(m *viewMount) string {
	var rows []string
	for _, row := range m.Render(40) {
		if row = strings.TrimSpace(widthx.StripAnsi(row)); row != "" {
			rows = append(rows, row)
		}
	}
	return strings.Join(rows, "\n")
}

type viewFixture struct {
	t      *testing.T
	host   *viewTestHost
	loop   *viewTestLoop
	client *viewClient
}

func newViewFixture(t *testing.T) *viewFixture {
	t.Helper()
	f := &viewFixture{t: t, host: &viewTestHost{reg: uiview.New(uiview.Options{})}, loop: newViewTestLoop(t)}
	f.host.invoke = func(context.Context, uiview.Invocation) (uiview.Result, error) {
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	f.client = attachViewClient(t.Context(), f.host, f.loop.ui())
	if f.client == nil {
		t.Fatal("the client did not attach")
	}
	t.Cleanup(f.client.detach)
	return f
}

func (f *viewFixture) mount(key string, slot uiview.Slot, c uiview.Content) uiview.Ref {
	f.t.Helper()
	st, err := f.host.reg.Mount("ext", uiview.MountArgs{Key: key, Slot: slot, Content: c})
	if err != nil {
		f.t.Fatalf("mount %s: %v", key, err)
	}
	f.loop.run()
	return st.Mount
}

func (f *viewFixture) update(ref uiview.Ref, c uiview.Content) uiview.Ref {
	f.t.Helper()
	st, err := f.host.reg.Update("ext", uiview.UpdateArgs{Mount: ref, Content: c})
	if err != nil {
		f.t.Fatalf("update: %v", err)
	}
	f.loop.run()
	return st.Mount
}

func (f *viewFixture) transition(op string, ref uiview.Ref) {
	f.t.Helper()
	if _, err := f.host.reg.Transition("ext", op, uiview.RefArgs{Mount: ref}); err != nil {
		f.t.Fatalf("%s: %v", op, err)
	}
	f.loop.run()
}

// awaitResult waits until the client's queue holds an intent's result and the loop holds the drain that applies it, and
// returns the queue as it is then. The client queues a result and then wakes the loop, so the result alone does not say
// that the loop can apply it: a test that ran the loop between the two left the intent in flight, and the client drops
// an activation made while an intent is in flight.
func (f *viewFixture) awaitResult() []viewItem {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.client.mu.Lock()
		queue := slices.Clone(f.client.queue)
		f.client.mu.Unlock()
		f.loop.mu.Lock()
		held := len(f.loop.tasks)
		f.loop.mu.Unlock()
		if held > 0 && slices.ContainsFunc(queue, func(it viewItem) bool { return it.result != nil }) {
			return queue
		}
		if time.Now().After(deadline) {
			f.t.Fatal("no intent result reached the client's queue")
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *viewFixture) notices(key string) []uiview.Notice {
	f.t.Helper()
	d, err := f.host.reg.Diagnostics("ext", uiview.StateArgs{Key: key})
	if err != nil {
		f.t.Fatal(err)
	}
	return d.Notices
}

// pig additive (D614): a client that is attached and was never sent a mount adds nothing to the loop: no component, no
// input listener, no goroutine, no timer. Extensions read its abilities.
func TestViewClientIsInertUntilAMount(t *testing.T) {
	synctest.Test(t, testViewClientIsInertUntilAMount)
}

func testViewClientIsInertUntilAMount(t *testing.T) {
	// The count is of the test's own bubble: a goroutine of another test is not the client's.
	before := testenv.LiveGoroutines()
	f := newViewFixture(t)
	f.loop.run()
	synctest.Wait()
	if n := testenv.LiveGoroutines() - before; n != 0 {
		t.Errorf("attaching started %d goroutines", n)
	}
	f.client.wake()
	f.loop.run()
	if len(f.loop.laid) != 0 || len(f.loop.listeners) != 0 || len(f.loop.notices) != 0 || f.loop.focused != nil || f.loop.renders != 0 {
		t.Errorf("an attached client with no mount touched the loop: layouts %d, listeners %d, notices %q, frames asked for %d", len(f.loop.laid), len(f.loop.listeners), f.loop.notices, f.loop.renders)
	}
	a := f.host.reg.Abilities()
	if a == nil {
		t.Fatal("extensions read no abilities while the terminal is attached")
	}
	var slots []uiview.Slot
	for i := range viewSlots {
		slots = append(slots, viewSlots[i].slot)
	}
	if !slices.Equal(a.Slots, slots) || !a.Input.Keyboard || !a.Input.Text || a.Input.Pointer || a.Drawing != nil || !slices.Equal(a.ImageFormats, terminalViewImageFormats) || a.Limits != terminalViewLimits {
		t.Errorf("abilities = %+v", a)
	}
	for _, file := range []string{"view_client.go", "view_client_mount.go", "view_client_wire.go"} {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range parsed.Imports {
			if spec.Path.Value == `"time"` {
				t.Errorf("%s imports time: the client has no timer", file)
			}
		}
	}
}

// pig additive (D614): the terminal states its size as claims: the columns of a mount, and per slot the rows it is drawn
// with. A resize and a change of the pointer are stated again.
func TestViewClientStatesItsSize(t *testing.T) {
	f := newViewFixture(t)
	claims := func() map[string]string {
		out := map[string]string{}
		a := f.host.reg.Abilities()
		for _, c := range a.Claims {
			out[c.Name] = string(c.Value)
		}
		for _, s := range a.SlotClaims {
			for _, c := range s.Claims {
				out[string(s.Slot)+"/"+c.Name] = string(c.Value)
			}
		}
		if a.Input.Pointer {
			out["pointer"] = "true"
		}
		return out
	}
	want := map[string]string{
		"client.kind": `"terminal"`, "size.columns": "40", "widget-above/size.rows": "10", "widget-below/size.rows": "10", "panel/size.rows": "12",
		"dialog/size.rows": strconv.Itoa(viewDialogCells(24)), "dialog/size.columns": strconv.Itoa(viewDialogCells(40)),
	}
	if got := claims(); !mapsEqual(got, want) {
		t.Fatalf("claims = %v, want %v", got, want)
	}
	f.loop.size = viewSize{columns: 100, rows: 50, pointer: true}
	f.client.wake()
	f.loop.run()
	want = map[string]string{
		"client.kind": `"terminal"`, "size.columns": "100", "widget-above/size.rows": "10", "widget-below/size.rows": "10", "panel/size.rows": "25", "pointer": "true",
		"dialog/size.rows": strconv.Itoa(viewDialogCells(50)), "dialog/size.columns": strconv.Itoa(viewDialogCells(100)),
	}
	if got := claims(); !mapsEqual(got, want) {
		t.Fatalf("claims after a resize = %v, want %v", got, want)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// pig additive (D614): a mount is drawn when it arrives, redrawn on an update, out of the tree while hidden, back when
// shown, and gone when it is removed, finished or broken.
func TestViewClientFollowsTheLifecycle(t *testing.T) {
	for _, end := range []string{"remove", "finish", "break"} {
		t.Run(end, func(t *testing.T) {
			f := newViewFixture(t)
			ref := f.mount("m", uiview.SlotWidgetAbove, uiview.Content{View: viewText("first")})
			if drawn := f.loop.drawn(); len(drawn) != 1 || viewRows(drawn[0]) != "first" {
				t.Fatalf("after the mount the layout is %v", drawn)
			}
			m := f.loop.drawn()[0]
			ref = f.update(ref, uiview.Content{View: viewText("second")})
			if got := viewRows(m); got != "second" {
				t.Fatalf("after the update the mount draws %q", got)
			}
			f.transition("hide", ref)
			if drawn := f.loop.drawn(); len(drawn) != 0 {
				t.Fatalf("a hidden mount is in the layout: %v", drawn)
			}
			f.transition("show", ref)
			if drawn := f.loop.drawn(); len(drawn) != 1 || drawn[0] != m || viewRows(m) != "second" {
				t.Fatalf("a shown mount is not back as it was: %v", drawn)
			}
			switch end {
			case "remove":
				f.transition("remove", ref)
			case "finish":
				if _, err := f.host.reg.Finish("ext", uiview.FinishArgs{Mount: ref}); err != nil {
					t.Fatal(err)
				}
				f.loop.run()
			case "break":
				f.host.reg.Break("", uiview.ReasonSessionReplaced)
				f.loop.run()
			}
			if drawn := f.loop.drawn(); len(drawn) != 0 || len(f.client.mounts) != 0 || len(f.client.order) != 0 {
				t.Fatalf("after %s the client still holds a mount: layout %v, mounts %d", end, drawn, len(f.client.mounts))
			}
			if len(f.loop.listeners) != 0 {
				t.Fatal("the focus key stayed bound with no panel")
			}
		})
	}
}

// pig additive (D614): the regions of the slots: widget-above and panel mounts are in the layout in mount order, and a
// mount is drawn at the columns it is given, cut to its slot's rows with a notice for the plugin.
func TestViewClientCutsATallViewToItsSlot(t *testing.T) {
	f := newViewFixture(t)
	var lines []string
	for i := range 30 {
		lines = append(lines, "row "+strconv.Itoa(i))
	}
	f.mount("tall", uiview.SlotWidgetAbove, uiview.Content{View: viewText(strings.Join(lines, "\n"))})
	m := f.loop.drawn()[0]
	rows := m.Render(40)
	if len(rows) != viewWidgetRows || !strings.Contains(widthx.StripAnsi(rows[viewWidgetRows-1]), viewCutRow) || !strings.Contains(strings.Join(rows, "\n"), "row 0") {
		t.Fatalf("a 30-row view in a widget slot draws %d rows: %q", len(rows), rows)
	}
	for _, row := range rows {
		if w := widthx.VisibleWidth(row); w > 40 {
			t.Fatalf("row %q is %d columns wide in a 40 column terminal", row, w)
		}
	}
	again := m.Render(40)
	if &again[0] != &rows[0] {
		t.Fatal("an unchanged mount was laid out again for the next frame")
	}
	notices := f.notices("tall")
	if len(notices) != 1 || notices[0].Code != "limit" || notices[0].Measured == nil || *notices[0].Measured < 30 || notices[0].Limit == nil || *notices[0].Limit != viewWidgetRows || notices[0].Count != 1 {
		t.Fatalf("notices = %+v, want one limit notice of 30 rows over %d", notices, viewWidgetRows)
	}
	if narrow := m.Render(20); &narrow[0] == &rows[0] {
		t.Fatal("a mount was not laid out again for another width")
	}
}

// pig additive (D614): a view the D107 validator rejects draws nothing and the plugin reads an undrawn notice; an
// intent that names no node is reported as not offered.
func TestViewClientReportsWhatItCannotDrawOrOffer(t *testing.T) {
	f := newViewFixture(t)
	f.mount("bad", uiview.SlotPanel, uiview.Content{
		View:    json.RawMessage(`{"root":{"kind":"select-list"}}`),
		Intents: []uiview.IntentDecl{{Name: "ext.refresh", Arg: uiview.Shape{Type: "object"}}},
	})
	if drawn := f.loop.drawn(); len(drawn) != 0 {
		t.Fatalf("a rejected view is in the layout: %v", drawn)
	}
	var codes []string
	for _, n := range f.notices("bad") {
		codes = append(codes, n.Code)
	}
	slices.Sort(codes)
	if !slices.Equal(codes, []string{"undrawn", "unsupported"}) {
		t.Fatalf("notice codes = %v, want undrawn and unsupported", codes)
	}
}

// pig additive (D614): a panel whose focus record asks for the focus takes it from the editor; Enter on its list raises
// the intent of the node with the item's value, off the loop; the handler's own update reaches the client's queue before
// the intent's result, so the loop draws the update first.
func TestViewClientRaisesAnIntentFromAKeyAndOrdersItsAnswer(t *testing.T) {
	f := newViewFixture(t)
	var ref uiview.Ref
	var got uiview.Invocation
	f.host.invoke = func(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
		got = inv
		if _, err := f.host.reg.Update("ext", uiview.UpdateArgs{Mount: ref, View: viewList("picked"), Intents: []uiview.IntentDecl{viewPickIntent}}); err != nil {
			t.Errorf("the handler's update: %v", err)
		}
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	ref = f.mount("p", uiview.SlotPanel, uiview.Content{View: viewList("choose"), Intents: []uiview.IntentDecl{viewPickIntent}, Focus: []uiview.Focus{{Node: "pick", Request: "mount"}}})
	m := f.loop.drawn()[0]
	if f.loop.focused != m || !m.focused {
		t.Fatal("a panel whose focus record asks for the focus did not take it from the editor")
	}
	m.HandleInput("\x1b[B")
	m.HandleInput("\r")
	queue := f.awaitResult()
	if string(got.Arg) != `{"value":"b"}` || got.Intent != "ext.pick" || got.NodeKey != "pick" || got.Mount.Key != "p" {
		t.Fatalf("the plugin was asked %+v (arg %s)", got, got.Arg)
	}
	delivery := slices.IndexFunc(queue, func(it viewItem) bool { return it.delivery != nil && it.delivery.Snapshot.Mount.Rev == ref.Rev+1 })
	result := slices.IndexFunc(queue, func(it viewItem) bool { return it.result != nil })
	if delivery < 0 || delivery > result {
		t.Fatalf("the handler's update is at %d and the intent's result at %d of the queue", delivery, result)
	}
	f.loop.run()
	if rows := viewRows(m); !strings.Contains(rows, "picked") || m.cancel != nil {
		t.Fatalf("after the answer the panel draws %q, intent in flight: %v", rows, m.cancel != nil)
	}
	if len(f.loop.notices) != 0 {
		t.Fatalf("a settled intent wrote %q", f.loop.notices)
	}
}

// pig additive (D614): what a handler mounts or updates before it answers is applied before the answer: when the
// refusal's warning is written, the mount the handler made is already the client's.
func TestViewClientAppliesAHandlersDeliveriesBeforeItsResult(t *testing.T) {
	f := newViewFixture(t)
	f.host.invoke = func(context.Context, uiview.Invocation) (uiview.Result, error) {
		if _, err := f.host.reg.Mount("ext", uiview.MountArgs{Key: "side", Slot: uiview.SlotWidgetAbove, View: viewText("made by the handler")}); err != nil {
			t.Errorf("the handler's mount: %v", err)
		}
		return uiview.Result{Outcome: uiview.OutcomeRefused, Error: "no"}, nil
	}
	f.mount("p", uiview.SlotPanel, uiview.Content{View: viewList("choose"), Intents: []uiview.IntentDecl{viewPickIntent}, Focus: []uiview.Focus{{Node: "pick", Request: "mount"}}})
	held := -1
	f.loop.onNotice = func() { held = len(f.client.mounts) }
	f.loop.drawn()[0].HandleInput("\r")
	f.awaitResult()
	f.loop.run()
	if len(f.loop.notices) != 1 || held != 2 {
		t.Fatalf("notices %q; the client held %d mounts when the result was applied, want the handler's too (2)", f.loop.notices, held)
	}
}

// pig additive (D614): a hole in the delivery numbers means the client missed one, and it resyncs: a removal it never
// received is applied all the same.
func TestViewClientResyncsAfterAMissedDelivery(t *testing.T) {
	f := newViewFixture(t)
	a := f.mount("a", uiview.SlotWidgetAbove, uiview.Content{View: viewText("a1")})
	b := f.mount("b", uiview.SlotWidgetAbove, uiview.Content{View: viewText("b1")})
	if _, err := f.host.reg.Transition("ext", "remove", uiview.RefArgs{Mount: b}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.reg.Update("ext", uiview.UpdateArgs{Mount: a, View: viewText("a2")}); err != nil {
		t.Fatal(err)
	}
	f.client.mu.Lock()
	if len(f.client.queue) != 2 || f.client.queue[0].delivery.Snapshot.State != uiview.StateRemoved {
		f.client.mu.Unlock()
		t.Fatalf("setup: the queue does not hold the removal and the update")
	}
	f.client.queue = f.client.queue[1:]
	f.client.deliveries--
	f.client.mu.Unlock()
	f.loop.run()
	drawn := f.loop.drawn()
	if len(drawn) != 1 || viewRows(drawn[0]) != "a2" || len(f.client.mounts) != 1 {
		t.Fatalf("after a missed removal the client draws %d mounts and holds %d", len(drawn), len(f.client.mounts))
	}
}

// pig additive (D614): one intent per mount is in flight. An activation made before it settled is dropped and counted;
// one made after it settled and its update was drawn is raised.
func TestViewClientDropsAnActivationWhileAnIntentIsInFlight(t *testing.T) {
	f := newViewFixture(t)
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	f.host.invoke = func(ctx context.Context, _ uiview.Invocation) (uiview.Result, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
		}
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return calls }
	ref := f.mount("p", uiview.SlotPanel, uiview.Content{View: viewList("choose"), Intents: []uiview.IntentDecl{viewPickIntent}, Focus: []uiview.Focus{{Node: "pick", Request: "mount"}}})
	m := f.loop.drawn()[0]
	m.HandleInput("\r")
	m.HandleInput("\r")
	if f.client.droppedActivations != 1 {
		t.Fatalf("dropped activations = %d, want 1", f.client.droppedActivations)
	}
	close(release)
	f.awaitResult()
	f.loop.run()
	if n := count(); n != 1 {
		t.Fatalf("two activations in one tick reached the plugin %d times", n)
	}
	f.update(ref, uiview.Content{View: viewList("again"), Intents: []uiview.IntentDecl{viewPickIntent}})
	if !strings.Contains(viewRows(m), "again") {
		t.Fatal("the update was not drawn")
	}
	release = make(chan struct{})
	close(release)
	m.HandleInput("\r")
	f.awaitResult()
	f.loop.run()
	if n := count(); n != 2 || f.client.droppedActivations != 1 {
		t.Fatalf("an activation after the first settled: plugin calls %d, dropped %d", n, f.client.droppedActivations)
	}
}

// pig additive (D614): the key that interrupts leaves a focused panel and cancels its cancellable intent in flight; a
// refused intent writes one warning; the key is consumed by the panel and nothing replays it.
func TestViewClientEscapeLeavesThePanelAndCancelsItsIntent(t *testing.T) {
	f := newViewFixture(t)
	cancelled := make(chan struct{})
	f.host.invoke = func(ctx context.Context, _ uiview.Invocation) (uiview.Result, error) {
		<-ctx.Done()
		close(cancelled)
		return uiview.Result{}, ctx.Err()
	}
	f.mount("p", uiview.SlotPanel, uiview.Content{View: viewList("choose"), Intents: []uiview.IntentDecl{viewPickIntent}, Focus: []uiview.Focus{{Node: "pick", Request: "mount"}}})
	m := f.loop.drawn()[0]
	m.HandleInput("\r")
	m.HandleInput("\x1b")
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Escape did not cancel the cancellable intent in flight")
	}
	if f.loop.focused != nil || m.focused {
		t.Fatal("Escape did not return the focus to the editor")
	}
	f.awaitResult()
	f.loop.run()
	if m.cancel != nil || len(f.loop.notices) != 0 {
		t.Fatalf("a cancelled intent left flight %v and notices %q", m.cancel != nil, f.loop.notices)
	}
	f.host.invoke = func(context.Context, uiview.Invocation) (uiview.Result, error) {
		return uiview.Result{Outcome: uiview.OutcomeRefused, Error: "not now"}, nil
	}
	if !f.loop.key("\x1b[17~") || f.loop.focused != m {
		t.Fatal("F6 from the editor did not focus the panel")
	}
	m.HandleInput("\r")
	f.awaitResult()
	f.loop.run()
	if !slices.Equal(f.loop.notices, []string{"warning: ext: ext.pick refused: not now"}) {
		t.Fatalf("notices = %q", f.loop.notices)
	}
}

// pig additive (D614): in fullscreen mode a press on a panel's list gives the panel the focus and the click on an item
// raises the node's intent; a mount of a widget slot takes the click and never the focus.
func TestViewClientClickFocusesAPanelAndRaisesAnIntent(t *testing.T) {
	f := newViewFixture(t)
	args := make(chan string, 2)
	f.host.invoke = func(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
		args <- inv.Mount.Key + " " + string(inv.Arg)
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	list := json.RawMessage(`{"root":{"kind":"select-list","id":"pick","maxVisible":4,"items":[{"value":"a","label":"Alpha"},{"value":"b","label":"Beta"}]},"focus":"pick"}`)
	f.mount("w", uiview.SlotWidgetAbove, uiview.Content{View: list, Intents: []uiview.IntentDecl{viewPickIntent}})
	f.mount("p", uiview.SlotPanel, uiview.Content{View: list, Intents: []uiview.IntentDecl{viewPickIntent}})
	gesture := func(m *viewMount) *tui.TuiMouseDispatchResult {
		t.Helper()
		m.Render(40)
		event := tui.TuiMouseEvent{Type: tui.MousePress, Button: tui.MouseButtonLeft, X: 2, Y: 1, ScreenX: 2, ScreenY: 9, Width: 40, Height: 2}
		pressed := m.HandleMouse(event)
		if pressed == nil || pressed.Target.Component != tui.Component(m) {
			t.Fatalf("the press was not taken by the mount: %+v", pressed)
		}
		if pressed.FocusTarget != nil {
			f.loop.ui().focus(m)
		}
		event.Type = tui.MouseRelease
		m.HandleMouse(event)
		event.Type, event.ClickCount = tui.MouseClick, 1
		m.HandleMouse(event)
		select {
		case got := <-args:
			if want := m.key.key + ` {"value":"b"}`; got != want {
				t.Fatalf("the click raised %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the click raised no intent")
		}
		f.awaitResult()
		f.loop.run()
		return pressed
	}
	widget, panel := f.loop.drawn()[0], f.loop.drawn()[1]
	if pressed := gesture(widget); pressed.Focus || pressed.FocusTarget != nil || f.loop.focused != nil {
		t.Fatalf("a press on a widget slot's mount asked for the focus: %+v", pressed)
	}
	if pressed := gesture(panel); !pressed.Focus || pressed.FocusTarget != tui.Component(panel) || f.loop.focused != panel || !panel.focused {
		t.Fatalf("a press on a panel's list did not focus the panel: %+v", pressed)
	}
}

// viewStackPanels draws the panels of this test one under the other, as a slot without tabs does.
func viewStackPanels(t *testing.T) {
	slot := viewSlotOf(uiview.SlotPanel)
	slot.tabs = false
	t.Cleanup(func() { slot.tabs = true })
}

// pig additive (D614): F6 is bound only while a focusable panel is drawn. It moves the focus from the editor through
// the panels and back, and passes the key on while something else (a selector) holds the focus. The panels of this
// test are stacked: it pins the walk of a slot without tabs.
func TestViewClientFocusKeyCyclesPanels(t *testing.T) {
	viewStackPanels(t)
	f := newViewFixture(t)
	f.mount("w", uiview.SlotWidgetAbove, uiview.Content{View: viewList("widget")})
	if len(f.loop.listeners) != 0 {
		t.Fatal("a widget slot's mount bound the focus key")
	}
	f.mount("a", uiview.SlotPanel, uiview.Content{View: viewList("first")})
	refB := f.mount("b", uiview.SlotPanel, uiview.Content{View: viewList("second")})
	if len(f.loop.listeners) != 1 || f.loop.focused != nil {
		t.Fatalf("listeners %d, focused %v: a panel without a focus request must not take the focus", len(f.loop.listeners), f.loop.focused)
	}
	drawn := f.loop.drawn()
	a, b := drawn[1], drawn[2]
	if f.loop.key("x") {
		t.Fatal("the focus key's listener consumed another key")
	}
	const f6 = "\x1b[17~"
	for i, want := range []*viewMount{a, b, nil, a} {
		if !f.loop.key(f6) || f.loop.focused != want {
			t.Fatalf("press %d: focused %v, want %v", i+1, f.loop.focused, want)
		}
	}
	f.loop.focused, f.loop.elsewhere = nil, true
	a.SetFocused(false)
	if f.loop.key(f6) {
		t.Fatal("the focus key was consumed while a selector held the focus")
	}
	f.loop.elsewhere = false
	f.loop.key(f6)
	f.loop.key(f6)
	if f.loop.focused != b {
		t.Fatal("setup: panel b is not focused")
	}
	f.transition("hide", refB)
	if f.loop.focused != nil {
		t.Fatal("hiding the focused panel did not return the focus to the editor")
	}
}

// pig additive (D614): a loop that does not drain is sent at most viewQueueLimit deliveries; the next one is refused,
// which is the registry's gap, and the next drain resyncs and draws the current view.
func TestViewClientSlowLoopUsesTheRegistrysGap(t *testing.T) {
	f := newViewFixture(t)
	ref := f.mount("m", uiview.SlotWidgetAbove, uiview.Content{View: viewText("v0")})
	m := f.loop.drawn()[0]
	for i := 1; i <= viewQueueLimit+6; i++ {
		st, err := f.host.reg.Update("ext", uiview.UpdateArgs{Mount: ref, View: viewText("v" + strconv.Itoa(i))})
		if err != nil {
			t.Fatal(err)
		}
		ref = st.Mount
	}
	if !f.client.att.InGap() {
		t.Fatal("the registry did not open a gap for a client whose queue is full")
	}
	f.client.mu.Lock()
	queued := f.client.deliveries
	f.client.mu.Unlock()
	if queued != viewQueueLimit {
		t.Fatalf("the client holds %d deliveries, want %d", queued, viewQueueLimit)
	}
	f.loop.run()
	if got, want := viewRows(m), "v"+strconv.Itoa(viewQueueLimit+6); got != want {
		t.Fatalf("after the resync the mount draws %q, want %q", got, want)
	}
	if f.client.att.InGap() {
		t.Fatal("the gap stayed open after the drain")
	}
	f.update(ref, uiview.Content{View: viewText("after")})
	if got := viewRows(m); got != "after" {
		t.Fatalf("a delivery after the resync draws %q", got)
	}
}

// pig additive (D614): the loop's queue being full does not block the sink: the drain is handed over off the sink's
// goroutine.
func TestViewClientSinkNeverWaitsForTheLoop(t *testing.T) {
	f := newViewFixture(t)
	f.loop.mu.Lock()
	f.loop.full = true
	f.loop.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := f.host.reg.Mount("ext", uiview.MountArgs{Key: "m", Slot: uiview.SlotPanel, View: viewText("x")}); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a mount waited for the loop")
	}
	select {
	case <-f.loop.posted:
	case <-time.After(5 * time.Second):
		t.Fatal("the drain never reached the loop")
	}
	f.loop.run()
	if len(f.loop.drawn()) != 1 {
		t.Fatal("the mount was not drawn")
	}
}

// pig additive (D614): a second client on the same registry is sent the same numbered sequence as the terminal.
func TestViewClientAndAWireClientAreSentTheSameSequence(t *testing.T) {
	f := newViewFixture(t)
	type seen struct {
		seq   uint64
		rev   uint64
		state uiview.State
	}
	var wire []seen
	other, _, err := f.host.reg.Attach(terminalViewAbilities(viewSize{columns: 80, rows: 24}), func(d uiview.Delivery) bool {
		wire = append(wire, seen{d.Seq, d.Snapshot.Mount.Rev, d.Snapshot.State})
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Detach()
	f.loop.run()
	step := func(want uiview.State, drawn int) {
		t.Helper()
		last := wire[len(wire)-1]
		if f.client.seq != last.seq || last.state != want || len(f.loop.drawn()) != drawn {
			t.Fatalf("the terminal is at delivery %d with %d mounts drawn; the wire client at %+v, want state %s", f.client.seq, len(f.loop.drawn()), last, want)
		}
		if m := f.client.mounts[viewMountKey{"ext", "m"}]; m != nil && m.ref.Rev != last.rev {
			t.Fatalf("the terminal draws revision %d and the wire client was sent %d", m.ref.Rev, last.rev)
		}
	}
	ref := f.mount("m", uiview.SlotPanel, uiview.Content{View: viewText("one")})
	step(uiview.StateLive, 1)
	ref = f.update(ref, uiview.Content{View: viewText("two")})
	step(uiview.StateLive, 1)
	f.transition("hide", ref)
	step(uiview.StateHidden, 0)
	f.transition("show", ref)
	step(uiview.StateLive, 1)
	f.transition("remove", ref)
	step(uiview.StateRemoved, 0)
	if len(wire) != 5 {
		t.Fatalf("the wire client was sent %d deliveries, want 5", len(wire))
	}
}

// pig additive (D614): detaching cancels the intents in flight, drops the mounts and leaves the registry with no client.
func TestViewClientDetachEndsEverything(t *testing.T) {
	f := newViewFixture(t)
	started, cancelled := make(chan struct{}), make(chan struct{})
	f.host.invoke = func(ctx context.Context, _ uiview.Invocation) (uiview.Result, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return uiview.Result{}, ctx.Err()
	}
	f.mount("p", uiview.SlotPanel, uiview.Content{View: viewList("choose"), Intents: []uiview.IntentDecl{viewPickIntent}, Focus: []uiview.Focus{{Node: "pick", Request: "mount"}}})
	m := f.loop.drawn()[0]
	m.HandleInput("\r")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the intent did not reach the plugin")
	}
	f.client.detach()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("detaching did not cancel the intent in flight")
	}
	if len(f.loop.drawn()) != 0 || f.loop.focused != nil || len(f.loop.listeners) != 0 || len(f.client.mounts) != 0 {
		t.Fatalf("after the detach: layout %v, focused %v, listeners %d", f.loop.drawn(), f.loop.focused, len(f.loop.listeners))
	}
	if f.host.reg.Abilities() != nil {
		t.Fatal("the registry still states abilities after the terminal detached")
	}
	f.loop.run()
	if _, err := f.host.reg.Mount("ext", uiview.MountArgs{Key: "late", Slot: uiview.SlotPanel, View: viewText("x")}); err == nil || err.Code != uiview.CodeUnsupported {
		t.Fatalf("a mount after the detach: %v, want unsupported", err)
	}
}

// pig additive (D614): an intent's argument is built from its declared shape and what the node gives.
func TestIntentArgument(t *testing.T) {
	str := func(s string) *string { return &s }
	object := func(required []string, properties map[string]uiview.Shape) uiview.Shape {
		return uiview.Shape{Type: "object", Properties: properties, Required: required}
	}
	for _, tc := range []struct {
		name  string
		shape uiview.Shape
		value *string
		item  string
		want  string
	}{
		{"string", uiview.Shape{Type: "string"}, str(`a"b`), "", `"a\"b"`},
		{"string without a value", uiview.Shape{Type: "string"}, nil, "", ""},
		{"integer", uiview.Shape{Type: "integer"}, str("42"), "", "42"},
		{"integer from text", uiview.Shape{Type: "integer"}, str("x"), "", ""},
		{"number", uiview.Shape{Type: "number"}, str("1.5"), "", "1.5"},
		{"boolean", uiview.Shape{Type: "boolean"}, str("true"), "", "true"},
		{"boolean from text", uiview.Shape{Type: "boolean"}, str("yes"), "", ""},
		{"enum member", uiview.Shape{Type: "enum", Enum: []string{"a", "b"}}, str("b"), "", `"b"`},
		{"enum outsider", uiview.Shape{Type: "enum", Enum: []string{"a", "b"}}, str("c"), "", ""},
		{"empty object", object(nil, nil), nil, "", `{}`},
		{"value and item", object([]string{"value", "item"}, map[string]uiview.Shape{"value": {Type: "string"}, "item": {Type: "string"}}), str("on"), "theme", `{"item":"theme","value":"on"}`},
		{"optional members the node lacks", object(nil, map[string]uiview.Shape{"value": {Type: "string"}, "item": {Type: "string"}, "other": {Type: "string"}}), nil, "", `{}`},
		{"required member the node lacks", object([]string{"other"}, map[string]uiview.Shape{"other": {Type: "string"}}), str("v"), "", ""},
		{"array", uiview.Shape{Type: "array", Items: &uiview.Shape{Type: "string"}}, str("v"), "", ""},
	} {
		arg, ok := intentArgument(tc.shape, tc.value, tc.item)
		if got := string(arg); ok != (tc.want != "") || (ok && got != tc.want) {
			t.Errorf("%s: argument %q (%v), want %q", tc.name, got, ok, tc.want)
		}
	}
}
