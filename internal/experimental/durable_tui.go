package experimental

// Ports packages/coding-agent/src/experimental/durable/tui.ts and packages/coding-agent/src/experimental/vacation/tui.ts
//
// The vacation planner's TUI is the same file as the coding agent's; one implementation serves both.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// durableSelectItem is one row of a durableListSelector.
type durableSelectItem struct {
	Value       string
	Label       string
	Description string
}

// durableListSelector is a filterable list in place of the editor: a title, a filter input and the rows that match it, fuzzily, on their label and value.
type durableListSelector struct {
	*tui.Container
	input    *tui.TextInput
	list     *tui.FilterableList
	rows     *tui.Container
	items    []durableSelectItem
	shown    []durableSelectItem
	onSelect func(value string)
	onCancel func()
	focused  bool
}

func newDurableListSelector(title string, items []durableSelectItem, onSelect func(value string), onCancel func()) *durableListSelector {
	selector := &durableListSelector{Container: tui.NewContainer(), input: tui.NewInput(tui.InputOptions{}), rows: tui.NewContainer(), items: items, onSelect: onSelect, onCancel: onCancel}
	selector.rebuild(items)
	theme := tui.ActiveTheme()
	selector.Add(tui.NewDynamicBorderToken("border"))
	selector.Add(tui.NewSpacer(1))
	selector.Add(tui.NewPaddedText(theme.FgText("accent", boldText(title)), 1, 0, nil))
	selector.Add(selector.input)
	selector.Add(tui.NewSpacer(1))
	selector.Add(selector.rows)
	selector.Add(tui.NewDynamicBorderToken("border"))
	return selector
}

// boldText is theme.bold.
func boldText(text string) string { return "\x1b[1m" + text + "\x1b[22m" }

// SetFocused moves the focus to the filter input.
func (selector *durableListSelector) SetFocused(focused bool) {
	selector.focused = focused
	selector.input.SetFocused(focused)
}

// Focused reports whether the selector has the focus.
func (selector *durableListSelector) Focused() bool { return selector.focused }

func (selector *durableListSelector) HandleInput(data string) {
	keys := tui.GetTUIKeybindings()
	for _, action := range []tui.TUIKeybinding{tui.KBSelectUp, tui.KBSelectDown, tui.KBSelectConfirm, tui.KBSelectCancel} {
		if keys.Matches(data, action) {
			selector.list.HandleInput(data)
			selector.finish()
			return
		}
	}
	selector.input.HandleInput(data)
	query := selector.input.GetValue()
	filtered := selector.items
	if query != "" {
		filtered = tui.FuzzyFilter(selector.items, query, func(item durableSelectItem) string { return item.Label + " " + item.Value })
	}
	selector.rebuild(filtered)
}

// finish reports the list's confirmation or cancellation once.
func (selector *durableListSelector) finish() {
	if !selector.list.Done() {
		return
	}
	if selector.list.Cancelled() {
		selector.onCancel()
		return
	}
	if index := selector.list.SelectedIndex(); index >= 0 && index < len(selector.shown) {
		selector.onSelect(selector.shown[index].Value)
	}
}

func (selector *durableListSelector) rebuild(items []durableSelectItem) {
	selector.shown = items
	labels := make([]string, len(items))
	descriptions := make([]string, len(items))
	for index, item := range items {
		labels[index], descriptions[index] = item.Label, item.Description
	}
	list := tui.NewFilterableList("", labels)
	list.EnableSearch = false
	list.Descriptions = descriptions
	list.MaxVisible = 10
	selector.list = list
	selector.rows.Clear()
	selector.rows.Add(list)
}

// durableCompaction is the summary that replaced earlier context: collapsed to one line until expanded.
type durableCompaction struct {
	*tui.Box
	summary string
	hint    string
}

func newDurableCompaction(summary, expandKey string, expanded bool) *durableCompaction {
	theme := tui.ActiveTheme()
	component := &durableCompaction{Box: tui.NewPaddedBox(1, 1, func(text string) string { return theme.BgText("customMessageBg", text) }), summary: summary, hint: expandKey}
	component.SetExpanded(expanded)
	return component
}

func (component *durableCompaction) SetExpanded(expanded bool) {
	theme := tui.ActiveTheme()
	component.Clear()
	component.AddChild(tui.NewPaddedText(theme.FgText("customMessageLabel", boldText("[compaction]")), 0, 0, nil))
	component.AddChild(tui.NewSpacer(1))
	if expanded {
		markdown := tui.NewMarkdown(component.summary)
		markdown.SetDefaultColor(theme.CustomMessageText)
		component.AddChild(markdown)
		return
	}
	component.AddChild(tui.NewPaddedText(theme.FgText("customMessageText", "Earlier context summarized (")+theme.FgText("dim", component.hint)+theme.FgText("customMessageText", " to expand)"), 0, 0, nil))
}

// durableTuiHandlers are what the TUI asks of the application.
type durableTuiHandlers struct {
	submit        func(text string)
	followUp      func(text string)
	abort         func()
	exit          func()
	selectModel   func()
	cycleThinking func()
}

// durableTui renders a DurableView with pi's interactive components. Every method runs on the owner loop.
type durableTui struct {
	ctx           context.Context
	ui            durableRenderer
	requestRender func()
	runOnMain     func(context.Context, func()) error
	keybindings   *codingagent.KeybindingsManager
	handlers      durableTuiHandlers
	cwd           string

	chat           *tui.Container
	tasks          *tui.Container
	queue          *tui.Container
	notices        *tui.Container
	editorSlot     *tui.Container
	footer         *tui.Container
	footerStats    *tui.Text
	footerHints    *tui.Text
	editor         *tui.Editor
	selector       *durableListSelector
	transcript     *tui.ScrollView
	layoutRoot     tui.Component
	tools          map[string]*codingagent.ToolRendererCard
	cards          []*codingagent.ToolRendererCard
	streamingCalls map[string]struct{}
	summaries      []*durableCompaction

	expanded         bool
	renderedEntryIds []durable.EntryId
	streaming        *tui.AssistantMessageBlock
	indicator        *tui.StatusIndicator
	stopIndicator    context.CancelFunc
	statusText       string
	rebuilt          bool

	background sync.WaitGroup
}

// durableRenderer is what a durableTui asks of the renderer: a repaint of the whole screen after the transcript was rebuilt.
type durableRenderer interface{ ForceFullRender() }

// durableTuiOptions bind a durableTui to its renderer and owner loop.
type durableTuiOptions struct {
	CWD           string
	UI            durableRenderer
	Keybindings   *codingagent.KeybindingsManager
	Handlers      durableTuiHandlers
	RequestRender func()
	RunOnMain     func(context.Context, func()) error
}

var durableToolRenderers = codingagent.CreateAllToolRenderers()

func newDurableTui(ctx context.Context, options durableTuiOptions) *durableTui {
	view := &durableTui{
		ctx: ctx, ui: options.UI, requestRender: options.RequestRender, runOnMain: options.RunOnMain, keybindings: options.Keybindings,
		handlers: options.Handlers, cwd: options.CWD,
		chat: tui.NewContainer(), tasks: tui.NewContainer(), queue: tui.NewContainer(), notices: tui.NewContainer(), editorSlot: tui.NewContainer(), footer: tui.NewContainer(),
		footerStats: tui.NewPaddedText("", 1, 0, nil), footerHints: tui.NewPaddedText("", 1, 0, nil),
		tools: map[string]*codingagent.ToolRendererCard{}, streamingCalls: map[string]struct{}{},
	}
	editor := tui.NewEditor()
	editor.SetPaddingX(1)
	editor.EmbedWorkingStatus = true
	editor.OnSubmit = options.Handlers.submit
	editor.SetFocused(true)
	view.editor = editor
	view.editorSlot.Add(editor)
	view.footer.Add(view.footerStats)
	view.footer.Add(view.footerHints)

	// One empty line between the transcript and everything below it.
	content := tui.NewContainer()
	content.Add(view.chat)
	content.Add(tui.NewSpacer(1))
	theme := tui.ActiveTheme()
	view.transcript = tui.NewScrollView(content, tui.ScrollViewOptions{
		Follow: "end", Primary: true, Overscroll: "chain",
		ScrollbarTrackStyle: func(text string) string { return theme.FgText("scrollbarTrack", text) },
		ScrollbarThumbStyle: func(text string) string { return theme.FgText("scrollbarThumb", text) },
	})
	shrinking := func(component tui.Component, minSize int) tui.StackChild {
		return tui.StackChild{Component: component, StackEntryOptions: tui.StackEntryOptions{Shrink: new(1), MinSize: new(minSize)}}
	}
	dock := tui.NewVStack([]tui.StackChild{
		shrinking(view.tasks, 0), shrinking(view.queue, 0), shrinking(view.notices, 0), shrinking(view.editorSlot, 3), shrinking(view.footer, 0),
	}, tui.StackOptions{})
	view.layoutRoot = tui.NewVStack([]tui.StackChild{
		{Component: view.transcript, StackEntryOptions: tui.StackEntryOptions{Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)}},
		{Component: dock, StackEntryOptions: tui.StackEntryOptions{Grow: new(0), Shrink: new(1), MinSize: new(1)}},
	}, tui.StackOptions{})
	return view
}

// LayoutRoot is the fullscreen layout: the scrolling transcript over the dock of panels, editor and footer.
func (view *durableTui) LayoutRoot() tui.Component { return view.layoutRoot }

// Render renders the document in the order of its parts.
func (view *durableTui) Render(width int) []string {
	var lines []string
	for _, child := range []tui.Component{view.chat, view.tasks, view.queue, view.notices, view.editorSlot, view.footer} {
		lines = append(lines, child.Render(width)...)
	}
	return lines
}

func (view *durableTui) Invalidate() { view.layoutRoot.Invalidate() }

// Mount shows component in place of the editor and gives it the focus.
func (view *durableTui) Mount(selector *durableListSelector) {
	view.editorSlot.Clear()
	view.editorSlot.Add(selector)
	view.selector = selector
	selector.SetFocused(true)
	view.editor.SetFocused(false)
	view.requestRender()
}

// RestoreEditor shows the editor again.
func (view *durableTui) RestoreEditor() {
	view.editorSlot.Clear()
	view.editorSlot.Add(view.editor)
	view.selector = nil
	view.editor.SetFocused(true)
	view.requestRender()
}

// HandleInput routes a key to the shown selector or, as pi's CustomEditor does, to the app actions and then the editor.
func (view *durableTui) HandleInput(data string) {
	if view.selector != nil {
		view.selector.HandleInput(data)
		view.requestRender()
		return
	}
	view.handleEditorInput(data)
	view.requestRender()
}

// handleEditorInput is CustomEditor.handleInput: the interrupt (not while the autocomplete is open), exit (only with an empty editor), the history keys, the registered app actions in registration order, then the editor itself.
func (view *durableTui) handleEditorInput(data string) {
	keys := view.keybindings
	switch {
	case keys.Matches(data, "app.clipboard.pasteImage"):
		return
	case keys.Matches(data, "app.interrupt"):
		if !view.editor.AutocompleteOpen() {
			view.handlers.abort()
			return
		}
		view.editor.HandleInput(data)
		return
	case keys.Matches(data, "app.exit") && view.editor.Text() == "":
		view.handlers.exit()
		return
	case keys.MatchesEditorHistory(data):
		view.editor.HandleInput(data)
		return
	}
	for _, action := range []struct {
		name string
		run  func()
	}{
		{"app.clear", view.handlers.exit},
		{"app.model.select", view.handlers.selectModel},
		{"app.thinking.cycle", view.handlers.cycleThinking},
		{"app.tools.expand", view.toggleExpanded},
		{"app.message.followUp", view.followUp},
	} {
		if keys.Matches(data, action.name) {
			action.run()
			return
		}
	}
	view.editor.HandleInput(data)
}

// toggleExpanded shows tool output and compaction summaries in full, or collapsed again, as pi's expand key does.
func (view *durableTui) toggleExpanded() {
	view.expanded = !view.expanded
	for _, card := range view.cards {
		card.Component.SetExpanded(view.expanded)
	}
	for _, summary := range view.summaries {
		summary.SetExpanded(view.expanded)
	}
	view.requestRender()
}

func (view *durableTui) followUp() {
	text := strings.TrimFunc(view.editor.Text(), widthx.IsJSSpace)
	if text == "" {
		return
	}
	view.editor.SetText("")
	view.handlers.followUp(text)
}

// Stop finishes the cards and the status indicator and joins the work behind them.
func (view *durableTui) Stop() {
	view.discardTools()
	view.stopStatusIndicator()
	view.background.Wait()
}

// discardTools finishes every card: a running bash card keeps a timer until it gets a final result.
func (view *durableTui) discardTools() {
	cards := view.cards
	view.cards = nil
	view.tools = map[string]*codingagent.ToolRendererCard{}
	clear(view.streamingCalls)
	for _, card := range cards {
		card.Component.SetResultValue(agent.AgentToolResult{})
		card.Component.ImageBlocks = nil
		card.Component.SetResult("", false, 0)
	}
	view.background.Go(func() {
		for _, card := range cards {
			card.Dispose()
		}
	})
}

// Apply shows view: new entries, the streaming answer, running tools, the panels, the status, and the footer. A transcript that was rebuilt repaints the screen and shows its end.
func (view *durableTui) Apply(state durableagent.DurableView) error {
	live, err := LiveOf(state.Conversation)
	if err != nil {
		return err
	}
	if err := view.syncTranscript(state.Conversation.Entries); err != nil {
		return err
	}
	var partial *agent.AssistantMessage
	if generation := live.Generation; generation != nil && len(generation.Message) != 0 {
		message, err := agentMessageOf(generation.Message)
		if err != nil {
			return err
		}
		partial = message.Assistant
	}
	// A partial without its entry was dropped, for example by a retry: render the transcript again.
	if partial == nil && view.streaming != nil {
		if err := view.rebuild(state.Conversation.Entries); err != nil {
			return err
		}
	}
	if partial != nil {
		if err := view.syncStreaming(partial); err != nil {
			return err
		}
	}
	for _, slot := range live.Tools {
		if slot.Status == harness.ToolSlotPending {
			continue
		}
		card, err := view.tool(slot.Name, slot.CallId, nil, false, false)
		if err != nil {
			return err
		}
		card.Component.SetArgsComplete()
		if slot.Status != harness.ToolSlotRunning {
			continue
		}
		card.Component.MarkExecutionStarted()
		var details any
		if slot.Details != nil {
			details = *slot.Details
		}
		child := subagentOf(details)
		switch {
		case slot.Output == nil && child != nil:
			view.updateResult(slot.CallId, card, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Subagent " + strconv.FormatInt(int64(*child), 10) + " is working. /agents switches to it."}}, Details: details}, true)
		case slot.Output != nil:
			view.updateResult(slot.CallId, card, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: *slot.Output}}, Details: details}, true)
		}
	}
	view.syncTasks(state.Tasks)
	var inbox harness.InboxState
	if err := decodeDocument(state.Conversation, services.InboxDocKind, &inbox); err != nil {
		return err
	}
	if err := view.syncQueue(inbox); err != nil {
		return err
	}
	view.syncNotices(state)
	agentState := durableagent.AgentOf(state.Conversation)
	if level := string(thinkingOrOff(agentState)); view.editor.ThinkingLevel != level {
		view.editor.ThinkingLevel = level
		view.editor.Invalidate()
	}
	view.syncStatus(live)
	if err := view.syncFooter(state, agentState); err != nil {
		return err
	}
	if view.rebuilt {
		view.transcript.ScrollToEnd()
		view.ui.ForceFullRender()
	}
	view.requestRender()
	view.rebuilt = false
	return nil
}

func thinkingOrOff(agentState harness.AgentState) ai.ModelThinkingLevel {
	if agentState.ThinkingLevel != "" {
		return agentState.ThinkingLevel
	}
	return ai.ModelThinkingLevel(ai.ThinkingOff)
}

// subagentOf is the conversation a tool call's details name, when they do.
func subagentOf(details any) *durable.ConversationId {
	object, ok := details.(map[string]any)
	if !ok {
		return nil
	}
	number, ok := object["conversationId"].(float64)
	if !ok {
		return nil
	}
	return new(durable.ConversationId(number))
}

// describeTask is the task panel's line of one live task.
func describeTask(node harness.TaskGraphNode) string {
	state := node.State
	var status string
	switch state.Status {
	case durable.TaskWaiting:
		ids := make([]string, len(state.On))
		for index, id := range state.On {
			ids[index] = strconv.FormatInt(int64(id), 10)
		}
		status = "waiting on " + strings.Join(ids, ", ")
	case durable.TaskCompleting:
		status = "completing (" + string(state.Outcome) + ")"
	default:
		status = string(state.Status) + " " + state.Phase
	}
	var flags []string
	if node.Background {
		flags = append(flags, "background")
	}
	if node.AbortRequested {
		flags = append(flags, "aborting")
	}
	flagText := ""
	if len(flags) > 0 {
		flagText = " [" + strings.Join(flags, ", ") + "]"
	}
	owned := ""
	if len(node.Conversations) > 0 {
		ids := make([]string, len(node.Conversations))
		for index, id := range node.Conversations {
			ids[index] = strconv.FormatInt(int64(id), 10)
		}
		owned = " owns conversation " + strings.Join(ids, ", ")
	}
	return node.Kind + " #" + strconv.FormatInt(int64(node.Id), 10) + ": " + status + flagText + owned
}

func (view *durableTui) syncTasks(graph *harness.TaskGraph) {
	view.tasks.Clear()
	if graph == nil {
		return
	}
	nodes := make([]harness.TaskGraphNode, 0, len(graph.Tasks))
	for _, node := range graph.Tasks {
		nodes = append(nodes, node)
	}
	// Object.values of integer keys runs in ascending order.
	slices.SortFunc(nodes, func(left, right harness.TaskGraphNode) int { return int(left.Id) - int(right.Id) })
	theme := tui.ActiveTheme()
	lines := []string{theme.FgText("accent", "Tasks ("+strconv.Itoa(len(nodes))+" live, /tasks to hide)")}
	// A conversation-owned task sits under the task that owns its conversation, when that task is live.
	owned := map[durable.ConversationId]bool{}
	for _, node := range nodes {
		for _, id := range node.Conversations {
			owned[id] = true
		}
	}
	children := func(node harness.TaskGraphNode) []harness.TaskGraphNode {
		var found []harness.TaskGraphNode
		for _, candidate := range nodes {
			if (candidate.Owner != nil && *candidate.Owner == node.Id) || (candidate.Owner == nil && slices.Contains(node.Conversations, candidate.ConversationId)) {
				found = append(found, candidate)
			}
		}
		return found
	}
	var visit func(node harness.TaskGraphNode, depth int)
	visit = func(node harness.TaskGraphNode, depth int) {
		lines = append(lines, strings.Repeat("  ", depth+1)+describeTask(node))
		for _, child := range children(node) {
			visit(child, depth+1)
		}
	}
	for _, node := range nodes {
		if node.Owner == nil && !owned[node.ConversationId] {
			visit(node, 0)
		}
	}
	for _, line := range lines {
		view.tasks.Add(tui.NewPaddedTruncatedText(theme.FgText("muted", line), 1, 0))
	}
}

func (view *durableTui) syncQueue(inbox harness.InboxState) error {
	view.queue.Clear()
	for _, item := range inbox.Items {
		var text string
		if item.Mode == harness.InboxWrite {
			kind, _ := item.Entry["kind"].(string)
			text = "<" + kind + ">"
		} else {
			content, err := decodeInboxContent(item.Content)
			if err != nil {
				return err
			}
			text = userContentText(content)
		}
		view.queue.Add(tui.NewPaddedTruncatedText(tui.ActiveTheme().FgText("muted", "["+string(item.Mode)+"] "+text), 1, 0))
	}
	return nil
}

func (view *durableTui) syncNotices(state durableagent.DurableView) {
	view.notices.Clear()
	items := state.Notices
	if len(items) > 4 {
		items = items[len(items)-4:]
	}
	theme := tui.ActiveTheme()
	for _, item := range items {
		color := "muted"
		switch item.Level {
		case durableagent.NoticeError:
			color = "error"
		case durableagent.NoticeWarning:
			color = "warning"
		}
		view.notices.Add(tui.NewPaddedTruncatedText(theme.FgText(color, item.Message), 1, 0))
	}
}

// syncStatus shows what the conversation waits for in the editor's border: a retry, a deferred response, a compaction, a running tool, or a plain working run.
func (view *durableTui) syncStatus(live harness.LiveState) {
	text := ""
	switch {
	case live.Generation != nil && live.Generation.Retry != nil:
		text = "Retrying (attempt " + strconv.Itoa(live.Generation.Attempt+1) + "): " + live.Generation.Retry.Error
	case live.Generation != nil && live.Generation.Deferred != nil:
		text = "Waiting for deferred response..."
	case len(live.Compactions) > 0:
		compaction := live.Compactions[0]
		if compaction.Retry != nil {
			text = "Retrying " + string(compaction.Reason) + " compaction (attempt " + strconv.Itoa(compaction.Attempt+1) + ")..."
		} else {
			text = "Compacting (" + string(compaction.Reason) + ")..."
		}
	case runningToolName(live) != "":
		text = "Running " + runningToolName(live) + "... (esc to abort)"
	case live.Run != nil:
		text = "Working... (esc to abort)"
	}
	if text == view.statusText {
		return
	}
	view.statusText = text
	view.stopStatusIndicator()
	if text == "" {
		view.editor.SetWorkingStatusIndicator(nil)
		return
	}
	theme := tui.ActiveTheme()
	indicator := &tui.StatusIndicator{Kind: "working", Loader: tui.NewStyledLoader(theme.Accent, theme.Muted, text, nil)}
	view.indicator = indicator
	view.editor.SetWorkingStatusIndicator(indicator)
	view.startIndicator(indicator)
}

func (view *durableTui) stopStatusIndicator() {
	if view.stopIndicator != nil {
		view.stopIndicator()
		view.stopIndicator = nil
	}
	view.indicator = nil
}

// startIndicator ticks the spinner until the indicator is replaced.
func (view *durableTui) startIndicator(indicator *tui.StatusIndicator) {
	ctx, cancel := context.WithCancel(view.ctx)
	view.stopIndicator = cancel
	view.background.Go(func() {
		// upstream: packages/tui/src/components/loader.ts:DEFAULT_INTERVAL_MS
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := view.runOnMain(ctx, func() {
					if view.indicator == indicator {
						indicator.Tick()
						view.requestRender()
					}
				}); err != nil {
					return
				}
			}
		}
	})
}

func totalUsage(state harness.UsageState) ai.Usage {
	var total ai.Usage
	for _, group := range []map[string]ai.Usage{state.Models, state.Tools} {
		for _, usage := range group {
			total.Input += usage.Input
			total.Output += usage.Output
			total.CacheRead += usage.CacheRead
			total.CacheWrite += usage.CacheWrite
			total.Cost.Total += usage.Cost.Total
		}
	}
	return total
}

// contextTokens is the context size from the newest successful answer after the newest compaction; unknown before one. Kept entries follow the summary in the view but are older than it; only later answers measure the new context.
func contextTokens(entries []durable.EntryRecord) (int, bool) {
	var compacted durable.EntryId
	for _, entry := range entries {
		if entry.Kind == "pi.compaction" {
			compacted = max(compacted, entry.Id)
		}
	}
	for _, entry := range slices.Backward(entries) {

		if entry.Id < compacted || entry.Kind != "pi.assistant" || len(entry.Model) == 0 {
			continue
		}
		message, ok := entry.Model[0].(ai.AssistantMessage)
		if !ok || message.StopReason == ai.StopReasonAborted || message.StopReason == ai.StopReasonError {
			continue
		}
		usage := message.Usage
		if usage.TotalTokens != 0 {
			return usage.TotalTokens, true
		}
		return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite, true
	}
	return 0, false
}

func (view *durableTui) syncFooter(state durableagent.DurableView, agentState harness.AgentState) error {
	theme := tui.ActiveTheme()
	var usage harness.UsageState
	if err := decodeDocument(state.Conversation, "pi.usage", &usage); err != nil {
		return err
	}
	total := totalUsage(usage)
	var stats []string
	if total.Input != 0 {
		stats = append(stats, "↑"+codingagent.FormatTokens(total.Input))
	}
	if total.Output != 0 {
		stats = append(stats, "↓"+codingagent.FormatTokens(total.Output))
	}
	if total.CacheRead != 0 {
		stats = append(stats, "R"+codingagent.FormatTokens(total.CacheRead))
	}
	if total.CacheWrite != 0 {
		stats = append(stats, "W"+codingagent.FormatTokens(total.CacheWrite))
	}
	stats = append(stats, "$"+tui.JSToFixed(total.Cost.Total, 3))
	contextWindow := 0
	for _, model := range state.Models {
		if agentState.Model != nil && model.Provider == agentState.Model.Provider && model.ModelId == agentState.Model.ModelId {
			contextWindow = model.ContextWindow
			break
		}
	}
	if contextWindow > 0 {
		tokens, known := contextTokens(state.Conversation.Entries)
		percent := "?"
		if known {
			percent = tui.JSToFixed(float64(tokens)/float64(contextWindow)*100, 1)
		}
		text := percent + "%/" + codingagent.FormatTokens(contextWindow)
		if known && float64(tokens)/float64(contextWindow)*100 > 90 {
			text = theme.FgText("error", text)
		}
		stats = append(stats, text)
	}
	view.footerStats.SetText(theme.FgText("dim", strings.Join(stats, " ")+"  "+state.Session.CWD))
	model := "no model"
	if agentState.Model != nil {
		model = agentState.Model.Provider + "/" + agentState.Model.ModelId
	}
	label := "conversation " + strconv.FormatInt(int64(state.Conversation.Conversation.Id), 10)
	for _, candidate := range state.Conversations {
		if candidate.ID == state.Conversation.Conversation.Id {
			label = candidate.Label
		}
	}
	color := "accent"
	if label == "main" {
		color = "dim"
	}
	view.footerHints.SetText(theme.FgText(color, label) + theme.FgText("dim",
		fmt.Sprintf(" · %s · thinking:%s (%s) · %s or /model · /agents · /compact · /tasks · %s follow-up · %s exit",
			model, thinkingOrOff(agentState), view.keybindings.KeyText("app.thinking.cycle"), view.keybindings.KeyText("app.model.select"),
			view.keybindings.KeyText("app.message.followUp"), view.keybindings.KeyText("app.clear"))))
	return nil
}

func (view *durableTui) syncTranscript(entries []durable.EntryRecord) error {
	// Compaction and resets replace the head of the active transcript.
	for index, id := range view.renderedEntryIds {
		if index >= len(entries) || entries[index].Id != id {
			return view.rebuild(entries)
		}
	}
	for _, entry := range entries[len(view.renderedEntryIds):] {
		if err := view.ctx.Err(); err != nil {
			return err
		}
		if err := view.addEntry(entry); err != nil {
			return err
		}
		view.renderedEntryIds = append(view.renderedEntryIds, entry.Id)
	}
	return nil
}

func (view *durableTui) rebuild(entries []durable.EntryRecord) error {
	view.chat.Clear()
	view.discardTools()
	view.summaries = nil
	view.rebuilt = true
	view.renderedEntryIds = nil
	view.streaming = nil
	for _, entry := range entries {
		if err := view.ctx.Err(); err != nil {
			return err
		}
		if err := view.addEntry(entry); err != nil {
			return err
		}
		view.renderedEntryIds = append(view.renderedEntryIds, entry.Id)
	}
	return nil
}

func (view *durableTui) addEntry(entry durable.EntryRecord) error {
	message, err := decodeEntryMessage(entry)
	if err != nil {
		return err
	}
	switch {
	case entry.Kind == "pi.user" && message != nil && message.User != nil:
		view.chat.Add(tui.NewSpacer(1))
		view.chat.Add(tui.NewUserMessageBlock(userMessageText(*message)))
	case entry.Kind == "pi.assistant" && message != nil && message.Assistant != nil:
		component := view.streaming
		if component == nil {
			component = tui.NewAssistantMessageBlock(false)
			view.chat.Add(component)
		}
		view.streaming = nil
		applyClientAssistant(component, message.Assistant)
		// Only a tool-calling answer runs its calls; an aborted, failed, or truncated one never does.
		ran := message.Assistant.StopReason == ai.StopReasonToolUse
		for _, block := range message.Assistant.Content {
			call, ok := block.(ai.ToolCall)
			if !ok {
				continue
			}
			_, streamed := view.streamingCalls[call.ID]
			// Only cards the stream already showed are kept for calls that never run.
			if !ran && !streamed {
				continue
			}
			card, err := view.tool(call.Name, call.ID, call.Arguments, true, !streamed)
			if err != nil {
				return err
			}
			card.Component.SetArgsComplete()
			if !ran {
				view.updateResult(call.ID, card, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Not run: the answer was interrupted."}}, IsError: true}, false)
			}
		}
		clear(view.streamingCalls)
	case entry.Kind == "pi.tool-result" && message != nil && message.ToolResult != nil:
		result := message.ToolResult
		card, err := view.tool(result.ToolName, result.ToolCallID, nil, false, false)
		if err != nil {
			return err
		}
		view.updateResult(result.ToolCallID, card, result.Result(), false)
	case entry.Kind == "pi.compaction":
		summary := ""
		if message != nil && message.User != nil {
			summary = userMessageText(*message)
		}
		component := newDurableCompaction(summary, view.keybindings.KeyText("app.tools.expand"), view.expanded)
		view.summaries = append(view.summaries, component)
		view.chat.Add(tui.NewSpacer(1))
		view.chat.Add(component)
	case entry.Kind == "pi.reset":
		view.addText("[new context]")
	}
	return nil
}

func (view *durableTui) syncStreaming(message *agent.AssistantMessage) error {
	if view.streaming == nil {
		view.streaming = tui.NewAssistantMessageBlock(false)
		view.chat.Add(view.streaming)
	}
	applyClientAssistant(view.streaming, message)
	for _, block := range message.Content {
		if call, ok := block.(ai.ToolCall); ok {
			_, streamed := view.streamingCalls[call.ID]
			if _, err := view.tool(call.Name, call.ID, call.Arguments, true, !streamed); err != nil {
				return err
			}
			view.streamingCalls[call.ID] = struct{}{}
		}
	}
	return nil
}

func (view *durableTui) addText(text string) {
	view.chat.Add(tui.NewSpacer(1))
	view.chat.Add(tui.NewPaddedText(tui.ActiveTheme().FgText("muted", text), 1, 0, nil))
}

// tool is the card of a call. hasArgs updates an existing card's arguments; fresh starts a new card for a call ID an earlier turn used.
func (view *durableTui) tool(name, id string, args any, hasArgs, fresh bool) (*codingagent.ToolRendererCard, error) {
	if existing := view.tools[id]; existing != nil && !fresh {
		if hasArgs {
			encoded, err := json.Marshal(args)
			if err != nil {
				return nil, err
			}
			existing.Component.UpdateArgs(name, string(encoded))
		}
		return existing, nil
	}
	if args == nil {
		args = map[string]any{}
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	card := codingagent.NewToolRendererCard(view.ctx, name, id, view.cwd, encoded, durableToolRenderers[name], view.requestRender)
	card.Component.SetExpanded(view.expanded)
	view.chat.Add(card.Component)
	view.cards = append(view.cards, card)
	view.tools[id] = card
	return card, nil
}

func (view *durableTui) updateResult(id string, card *codingagent.ToolRendererCard, result agent.AgentToolResult, partial bool) {
	component := card.Component
	component.SetResultValue(result)
	component.ImageBlocks = nil
	for _, block := range result.Content {
		if image, ok := block.(ai.ImageContent); ok {
			component.ImageBlocks = append(component.ImageBlocks, tui.ImageBlock{Data: image.Data, MIMEType: image.MimeType})
		}
	}
	if partial {
		component.SetStreaming(result.Text())
	} else {
		component.SetResult(result.Text(), result.IsError, 0)
	}
	for _, request := range component.PendingKittyImageConversions() {
		view.background.Go(func() {
			if view.ctx.Err() != nil {
				return
			}
			converted := codingagent.ConvertToPng(request.Data, request.MimeType)
			_ = view.runOnMain(view.ctx, func() {
				if view.tools[id] == card && card.Component.ApplyConvertedImage(request, converted) {
					view.requestRender()
				}
			})
		})
	}
}
