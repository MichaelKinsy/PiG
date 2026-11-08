package components

// The chat transcript as incremental components, as coding-agent's DurableTui (experimental/durable/tui.ts `#syncTranscript`/`#addEntry`/
// `#syncStreaming`) does it, but keyed by the shared display model's items (transcript.Item): an unchanged item keeps its component, a changed one
// updates it in place (a streaming answer becomes its committed entry under the same key), and a gone one is disposed. It also keeps the reading
// position: when not following the end, the item at the top of the viewport stays put while older history is prepended or items above it change
// height (p2-tui-port.md P0c).
//
// Go forced difference: upstream skips `update` for an item that is the same object as the view's; items here are values rebuilt on every sync, so
// every view is updated.
//
// pi: apps/p2/src/tui/components/transcript.ts

import (
	"regexp"
	"strconv"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
	"strings"
)

// TranscriptContext is what the transcript's components need.
type TranscriptContext interface {
	MessageContext
	// Cwd is the session cwd, for relative path display.
	Cwd() string
	// ToolRenderers are the renderers of the tool name; nil without any.
	ToolRenderers(name string) *tools.ToolRenderers
	// EntryRenderers are the plugins' components by entry kind.
	EntryRenderers() map[string]*EntryRenderer
}

// TranscriptAnchor is where the reader is: an item and the line offset within it at the top of the viewport.
type TranscriptAnchor struct {
	Key string
	// EntryID is nil when the item shows no entry.
	EntryID *int
	Offset  int
}

// ViewState is the transcript's display state.
type ViewState struct{ Expanded, ShowThinking bool }

// EntryItem is an entry a plugin's renderer shows in place of the transcript's own display of it.
type EntryItem struct {
	Key     string
	EntryID int
	Entry   durable.EntryRecord
}

// ItemKey is the item's key.
func (i EntryItem) ItemKey() string { return i.Key }

// ViewItem is a transcript.Item or an EntryItem.
type ViewItem interface{ ItemKey() string }

type itemView interface {
	component() tui.Component
	item() ViewItem
	update(ViewItem) bool
	setState(ViewState)
	dispose()
}

var leadingEntryID = regexp.MustCompile(`^(\d+)(?:-|$)`)

// ItemEntryID is the entry ID an item shows, also for failures and markers keyed by their entry.
func ItemEntryID(item ViewItem) (int, bool) {
	switch typed := item.(type) {
	case transcript.UserItem:
		if typed.EntryID != nil {
			return *typed.EntryID, true
		}
	case transcript.AssistantItem:
		if typed.EntryID != nil {
			return *typed.EntryID, true
		}
	case EntryItem:
		return typed.EntryID, true
	}
	match := leadingEntryID.FindStringSubmatch(item.ItemKey())
	if match == nil {
		return 0, false
	}
	id, err := strconv.Atoi(match[1])
	return id, err == nil
}

// streamingTools are the calls of a streaming answer as pending cards (DTUI `#syncStreaming`).
func streamingTools(message *ai.AssistantMessage) []transcript.ToolView {
	var views []transcript.ToolView
	for _, block := range message.Content {
		if call, ok := block.(ai.ToolCall); ok {
			views = append(views, transcript.ToolView{ID: call.ID, Name: call.Name, Args: jsvalue.ObjectFromDelta(call.Arguments), Status: transcript.ToolPending})
		}
	}
	return views
}

type assistantView struct {
	container *tui.Container
	it        transcript.AssistantItem
	message   *AssistantMessageComponent
	cards     map[string]*ToolExecutionComponent
	ctx       TranscriptContext
	state     ViewState
}

func newAssistantView(item transcript.AssistantItem, ctx TranscriptContext, state ViewState) *assistantView {
	v := &assistantView{container: tui.NewContainer(), it: item, ctx: ctx, state: state, cards: map[string]*ToolExecutionComponent{}}
	v.message = NewAssistantMessageComponent(ctx, nil, !state.ShowThinking, "")
	v.container.AddChild(v.message)
	v.sync()
	return v
}

func (v *assistantView) component() tui.Component { return v.container }
func (v *assistantView) item() ViewItem           { return v.it }

func (v *assistantView) update(item ViewItem) bool {
	typed, ok := item.(transcript.AssistantItem)
	if !ok {
		return false
	}
	v.it = typed
	v.sync()
	return true
}

func (v *assistantView) sync() {
	item := v.it
	v.message.UpdateContent(item.Message, item.Streaming)
	toolViews := item.Tools
	if item.Streaming {
		toolViews = streamingTools(item.Message)
	}
	next := map[string]*ToolExecutionComponent{}
	var order []*ToolExecutionComponent
	for _, tool := range toolViews {
		card := v.cards[tool.ID]
		if card != nil {
			card.Update(tool, !item.Streaming)
		} else {
			card = NewToolExecutionComponent(tool, v.ctx.ToolRenderers(tool.Name), v.ctx, !item.Streaming)
			card.SetExpanded(v.state.Expanded)
		}
		next[tool.ID] = card
		order = append(order, card)
	}
	for id, card := range v.cards {
		if _, ok := next[id]; !ok {
			card.Dispose()
		}
	}
	v.cards = next
	v.container.Clear()
	v.container.AddChild(v.message)
	for _, card := range order {
		v.container.AddChild(card)
	}
	v.container.Invalidate()
}

func (v *assistantView) setState(state ViewState) {
	v.state = state
	v.message.SetHideThinkingBlock(!state.ShowThinking)
	for _, card := range v.cards {
		card.SetExpanded(state.Expanded)
	}
}

func (v *assistantView) dispose() {
	for _, card := range v.cards {
		card.Dispose()
	}
}

type toolItemView struct {
	card *ToolExecutionComponent
	it   transcript.ToolItem
}

func newToolItemView(item transcript.ToolItem, ctx TranscriptContext, state ViewState) *toolItemView {
	v := &toolItemView{it: item, card: NewToolExecutionComponent(item.Tool, ctx.ToolRenderers(item.Tool.Name), ctx, true)}
	v.card.SetExpanded(state.Expanded)
	return v
}

func (v *toolItemView) component() tui.Component { return v.card }
func (v *toolItemView) item() ViewItem           { return v.it }
func (v *toolItemView) update(item ViewItem) bool {
	typed, ok := item.(transcript.ToolItem)
	if !ok || typed.Tool.ID != v.it.Tool.ID {
		return false
	}
	v.it = typed
	v.card.Update(typed.Tool, true)
	return true
}
func (v *toolItemView) setState(state ViewState) { v.card.SetExpanded(state.Expanded) }
func (v *toolItemView) dispose()                 { v.card.Dispose() }

type userView struct {
	container *tui.Container
	it        transcript.UserItem
	message   *UserMessageComponent
}

func newUserView(item transcript.UserItem, ctx TranscriptContext) *userView {
	v := &userView{it: item, message: NewUserMessageComponent(item, ctx), container: tui.NewContainer()}
	v.container.AddChild(tui.NewSpacer(1))
	v.container.AddChild(v.message)
	return v
}

func (v *userView) component() tui.Component { return v.container }
func (v *userView) item() ViewItem           { return v.it }
func (v *userView) update(item ViewItem) bool {
	typed, ok := item.(transcript.UserItem)
	if !ok {
		return false
	}
	v.it = typed
	v.message.Update(typed)
	return true
}

// setState: a skill block follows the tools toggle, as pi's (Ctrl+O).
func (v *userView) setState(state ViewState) { v.message.SetExpanded(state.Expanded) }
func (v *userView) dispose()                 {}

type noticeView struct {
	notice *NoticeComponent
	it     transcript.FailureItem
}

func newNoticeView(item transcript.FailureItem, ctx TranscriptContext) *noticeView {
	return &noticeView{it: item, notice: NewNoticeComponent(item.Text, "error", ctx)}
}

func (v *noticeView) component() tui.Component { return v.notice }
func (v *noticeView) item() ViewItem           { return v.it }
func (v *noticeView) update(item ViewItem) bool {
	typed, ok := item.(transcript.FailureItem)
	if !ok {
		return false
	}
	v.it = typed
	v.notice.Update(typed.Text)
	return true
}
func (v *noticeView) setState(ViewState) {}
func (v *noticeView) dispose()           {}

// gapView is a gap: entries between two loaded ones are missing; they load while the line is in view (`onVisibleGap`).
type gapView struct {
	notice *NoticeComponent
	it     transcript.GapItem
}

func gapText(item transcript.GapItem) string {
	if item.Loading {
		return "Loading…"
	}
	return "Load more"
}

func newGapView(item transcript.GapItem, ctx TranscriptContext) *gapView {
	return &gapView{it: item, notice: NewNoticeComponent(gapText(item), "muted", ctx)}
}

func (v *gapView) component() tui.Component { return v.notice }
func (v *gapView) item() ViewItem           { return v.it }
func (v *gapView) update(item ViewItem) bool {
	typed, ok := item.(transcript.GapItem)
	if !ok {
		return false
	}
	v.it = typed
	v.notice.Update(gapText(typed))
	return true
}
func (v *gapView) setState(ViewState) {}
func (v *gapView) dispose()           {}

type markerView struct {
	container *tui.Container
	it        transcript.MarkerItem
	summary   *CompactionSummaryComponent
	ctx       TranscriptContext
	state     ViewState
}

func newMarkerView(item transcript.MarkerItem, ctx TranscriptContext, state ViewState) *markerView {
	v := &markerView{container: tui.NewContainer(), it: item, ctx: ctx, state: state}
	v.build()
	return v
}

func (v *markerView) build() {
	v.container.Clear()
	v.summary = nil
	switch {
	case v.it.Label == "Context compacted":
		v.summary = NewCompactionSummaryComponent(v.it.Detail, v.state.Expanded, v.ctx, CompactionSummaryOptions{})
		v.container.AddChild(tui.NewSpacer(1))
		v.container.AddChild(v.summary)
	case v.it.Label == "New context" && strings.TrimFunc(v.it.Detail, widthx.IsJSSpace) != "":
		// A handoff's text belongs to its new-context marker, expandable like a compaction summary.
		v.summary = NewCompactionSummaryComponent(v.it.Detail, v.state.Expanded, v.ctx, CompactionSummaryOptions{Label: "[new context]", Collapsed: "Handoff note"})
		v.container.AddChild(tui.NewSpacer(1))
		v.container.AddChild(v.summary)
	default:
		text := "[" + v.it.Label + "]"
		if v.it.Label == "New context" {
			text = "[new context]"
		}
		v.container.AddChild(NewNoticeComponent(text, "muted", v.ctx))
	}
}

func (v *markerView) component() tui.Component { return v.container }
func (v *markerView) item() ViewItem           { return v.it }
func (v *markerView) update(item ViewItem) bool {
	typed, ok := item.(transcript.MarkerItem)
	if !ok {
		return false
	}
	v.it = typed
	v.build()
	return true
}
func (v *markerView) setState(state ViewState) {
	v.state = state
	if v.summary != nil {
		v.summary.SetExpanded(state.Expanded)
	}
}
func (v *markerView) dispose() {}

type entryContext struct {
	TranscriptContext
	expanded bool
}

func (c entryContext) Expanded() bool { return c.expanded }

// entryView is a plugin's component for an entry kind (`TuiEntryRenderers`); rebuilt when its entry or renderer changes.
type entryView struct {
	container *tui.Container
	it        EntryItem
	renderer  *EntryRenderer
	ctx       TranscriptContext
	state     ViewState
}

func newEntryView(item EntryItem, renderer *EntryRenderer, ctx TranscriptContext, state ViewState) *entryView {
	v := &entryView{container: tui.NewContainer(), it: item, renderer: renderer, ctx: ctx, state: state}
	v.build()
	return v
}

func (v *entryView) build() {
	v.container.Clear()
	defer func() {
		if recover() != nil {
			v.container.Clear()
			v.container.AddChild(NewNoticeComponent("["+v.it.Entry.Kind+": renderer failed]", "warning", v.ctx))
		}
	}()
	if component := v.renderer.Render(v.it.Entry, entryContext{TranscriptContext: v.ctx, expanded: v.state.Expanded}); component != nil {
		v.container.AddChild(component)
	}
}

func (v *entryView) component() tui.Component { return v.container }
func (v *entryView) item() ViewItem           { return v.it }
func (v *entryView) update(item ViewItem) bool {
	typed, ok := item.(EntryItem)
	if !ok || v.ctx.EntryRenderers()[typed.Entry.Kind] != v.renderer {
		return false
	}
	v.it = typed
	v.build()
	return true
}
func (v *entryView) setState(state ViewState) {
	if state.Expanded == v.state.Expanded {
		return
	}
	v.state = state
	v.build()
}
func (v *entryView) dispose() {}

func createView(item ViewItem, ctx TranscriptContext, state ViewState) itemView {
	switch typed := item.(type) {
	case transcript.AssistantItem:
		return newAssistantView(typed, ctx, state)
	case transcript.ToolItem:
		return newToolItemView(typed, ctx, state)
	case transcript.UserItem:
		return newUserView(typed, ctx)
	case transcript.FailureItem:
		return newNoticeView(typed, ctx)
	case transcript.GapItem:
		return newGapView(typed, ctx)
	case transcript.MarkerItem:
		return newMarkerView(typed, ctx, state)
	case EntryItem:
		return newEntryView(typed, ctx.EntryRenderers()[typed.Entry.Kind], ctx, state)
	}
	panic("components: unknown transcript item")
}

// viewItems are the items in order, with entries of kinds a plugin renders in place of what the transcript made of them.
func viewItems(items []transcript.Item, retained []durable.EntryRecord, renderers map[string]*EntryRenderer) []ViewItem {
	result := make([]ViewItem, 0, len(items))
	plain := func() []ViewItem {
		for _, item := range items {
			result = append(result, item)
		}
		return result
	}
	if len(renderers) == 0 {
		return plain()
	}
	var custom []durable.EntryRecord
	for _, entry := range retained {
		if _, ok := renderers[entry.Kind]; ok {
			custom = append(custom, entry)
		}
	}
	if len(custom) == 0 {
		return plain()
	}
	replaced := map[int]bool{}
	for _, entry := range custom {
		replaced[int(entry.Id)] = true
	}
	next := 0
	flush := func(below int, unbounded bool) {
		for next < len(custom) && (unbounded || int(custom[next].Id) < below) {
			entry := custom[next]
			next++
			result = append(result, EntryItem{Key: "entry-" + strconv.FormatInt(int64(entry.Id), 10), EntryID: int(entry.Id), Entry: entry})
		}
	}
	for _, item := range items {
		id, ok := ItemEntryID(item)
		flush(id, !ok)
		if !ok || !replaced[id] {
			result = append(result, item)
		}
	}
	flush(0, true)
	return result
}

type renderedItem struct {
	key     string
	start   int
	entryID *int
}

// TranscriptView is the transcript component.
type TranscriptView struct {
	ctx    TranscriptContext
	header tui.Component
	views  map[string]itemView
	order  []itemView
	keys   []string
	state  ViewState
	scroll *tui.ScrollView
	// rendered are the items of the last render with their first line, in order.
	rendered      []renderedItem
	pendingAnchor *TranscriptAnchor
	// OnTop is called when the reader reached the top: load older history.
	OnTop func()
	// OnVisibleGap is called for each gap shown in the viewport: load its entries.
	OnVisibleGap func(item transcript.GapItem)
	// OnMissingAnchor is called when a restored anchor is not loaded yet: older pages may hold it. It returns whether it is loading it.
	OnMissingAnchor func(anchor TranscriptAnchor) bool
	// Empty is shown when there are no items.
	Empty string
	// Tail is shown after the items: live content that is no entry yet (a running user shell command).
	Tail tui.Component
}

// NewTranscriptView is the constructor; header may be nil.
func NewTranscriptView(ctx TranscriptContext, state ViewState, header tui.Component) *TranscriptView {
	return &TranscriptView{ctx: ctx, state: state, header: header, views: map[string]itemView{}}
}

// Attach gives the scroll view showing this transcript; needed to keep the reading position.
func (t *TranscriptView) Attach(scroll *tui.ScrollView) { t.scroll = scroll }

// Sync shows transcript (nil: nothing); retained are the entries a plugin's renderer may replace.
func (t *TranscriptView) Sync(tr *transcript.Transcript, retained []durable.EntryRecord) {
	var items []ViewItem
	if tr != nil {
		items = viewItems(tr.Items, retained, t.ctx.EntryRenderers())
	}
	next := map[string]itemView{}
	order := make([]itemView, 0, len(items))
	keys := make([]string, 0, len(items))
	for _, item := range items {
		key := item.ItemKey()
		view := t.views[key]
		if _, taken := next[key]; taken {
			view = nil
		}
		if view != nil && !view.update(item) {
			view.dispose()
			view = nil
		}
		if view == nil {
			view = createView(item, t.ctx, t.state)
		}
		next[key] = view
		order = append(order, view)
		keys = append(keys, key)
	}
	for key, view := range t.views {
		if next[key] != view {
			view.dispose()
		}
	}
	t.views, t.order, t.keys = next, order, keys
}

// Reset drops every component, e.g. when renderers changed; the next Sync recreates them.
func (t *TranscriptView) Reset() {
	for _, view := range t.order {
		view.dispose()
	}
	t.views = map[string]itemView{}
	t.order, t.keys = nil, nil
}

// SetState changes the display state.
func (t *TranscriptView) SetState(state ViewState) {
	if state == t.state {
		return
	}
	t.state = state
	for _, view := range t.order {
		view.setState(state)
	}
}

// Anchor is the reading position, or nil while following the end.
func (t *TranscriptView) Anchor() *TranscriptAnchor {
	if t.scroll == nil || t.scroll.IsFollowingEnd() {
		return nil
	}
	return t.anchorAt(t.scroll.ScrollTop())
}

// Restore restores a reading position (after a reload or conversation switch) once its item is loaded.
func (t *TranscriptView) Restore(anchor *TranscriptAnchor) {
	t.pendingAnchor = anchor
	if anchor == nil && t.scroll != nil {
		t.scroll.ScrollToEnd()
	}
}

// anchorAt is the item at line top in the last render; above the first item (the header), the first one with a negative offset.
func (t *TranscriptView) anchorAt(top int) *TranscriptAnchor {
	var best *TranscriptAnchor
	for index, item := range t.rendered {
		if item.start > top && index > 0 {
			break
		}
		best = &TranscriptAnchor{Key: item.key, Offset: top - item.start, EntryID: item.entryID}
	}
	return best
}

// Invalidate invalidates every component.
func (t *TranscriptView) Invalidate() {
	if t.header != nil {
		t.header.Invalidate()
	}
	for _, view := range t.order {
		view.component().Invalidate()
	}
	if t.Tail != nil {
		t.Tail.Invalidate()
	}
}

// Render renders the transcript.
func (t *TranscriptView) Render(width int) []string {
	scroll := t.scroll
	// The item at the top of the viewport before this render, measured with the previous offsets.
	kept := t.pendingAnchor
	if kept == nil && scroll != nil && !scroll.IsFollowingEnd() {
		kept = t.anchorAt(scroll.ScrollTop())
	}
	var lines []string
	if t.header != nil {
		lines = append(lines, t.header.Render(width)...)
	}
	starts := map[string]int{}
	rendered := make([]renderedItem, 0, len(t.order))
	// The callbacks below may sync synchronously (store change -> runtime.changed()) and replace t.order: everything after this point works on the
	// views that were actually rendered.
	order, keys := t.order, t.keys
	for index, view := range order {
		key := keys[index]
		starts[key] = len(lines)
		item := renderedItem{key: key, start: len(lines)}
		if id, ok := ItemEntryID(view.item()); ok {
			item.entryID = &id
		}
		rendered = append(rendered, item)
		lines = append(lines, view.component().Render(width)...)
	}
	if len(order) == 0 && t.Empty != "" && t.Tail == nil {
		lines = append(lines, "", " "+t.Empty)
	}
	if t.Tail != nil {
		lines = append(lines, t.Tail.Render(width)...)
	}
	// One empty line between the transcript and the dock (DTUI).
	lines = append(lines, "")
	t.rendered = rendered
	if scroll != nil && kept != nil {
		if start, ok := starts[kept.Key]; ok {
			top := max(0, start+kept.Offset)
			if t.pendingAnchor != nil || top != scroll.ScrollTop() {
				// The layout clamps against the last content height: give it this one first.
				scroll.UpdateLayout(len(lines), scroll.ViewportHeight(), func() { t.ctx.RequestRender() })
				scroll.ScrollTo(top, tui.ScrollViewScrollToOptions{DisableFollow: true})
			}
			t.pendingAnchor = nil
		} else if t.pendingAnchor != nil || kept.EntryID != nil {
			// The anchored entry is gone, e.g. a reconnect's fresh snapshot dropped older pages: page back to it.
			restoring := t.pendingAnchor != nil
			if t.pendingAnchor == nil {
				t.pendingAnchor = kept
			}
			if t.OnMissingAnchor == nil || !t.OnMissingAnchor(*t.pendingAnchor) {
				// Not in older history either: a restore follows the end, a vanished item keeps the position.
				t.pendingAnchor = nil
				if restoring {
					scroll.ScrollToEnd()
				}
			}
		}
	}
	// At the top of a laid-out viewport (or everything fits): older history may continue above.
	if scroll != nil && scroll.ViewportHeight() > 0 && scroll.ScrollTop() == 0 && t.pendingAnchor == nil && t.OnTop != nil {
		t.OnTop()
	}
	if scroll != nil && scroll.ViewportHeight() > 0 && t.pendingAnchor == nil {
		for index, view := range order {
			start := rendered[index].start
			end := len(lines)
			if index+1 < len(rendered) {
				end = rendered[index+1].start
			}
			visible := end > scroll.ScrollTop() && start < scroll.ScrollTop()+scroll.ViewportHeight()
			if gap, ok := view.item().(transcript.GapItem); ok && visible && t.OnVisibleGap != nil {
				t.OnVisibleGap(gap)
			}
		}
	}
	return lines
}

// Dispose disposes every component.
func (t *TranscriptView) Dispose() {
	for _, view := range t.order {
		view.dispose()
	}
	t.views = map[string]itemView{}
	t.order = nil
}
