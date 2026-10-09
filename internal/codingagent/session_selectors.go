// Modal selector plumbing for interactive mode.
//
// runModalSelector pushes a tui overlay, takes over input from
// inputLoop's perspective (we're called synchronously from
// dispatchSlash, which itself is called from within dispatchKey, so
// inputLoop is paused), reads keystrokes via tui.ReadInput, and
// dispatches them to the overlay component until Done() returns true.
//
// Two flavours: FilterableList (returns int index) and TreeSelectorComponent
// (returns string id).

package codingagent

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// forkAndRebuild moves the session leaf to entryID and: critically -
// rebuilds the agent's in-memory message history from the new
// path-to-leaf. Without the SetMessages step, /fork would change the
// on-disk parent chain but leave the LLM seeing the abandoned tail,
// so the next prompt would mix both branches.
//
// The agent argument may be nil (e.g. unit tests that don't need to
// exercise the LLM bridge); in that case only the session leaf moves.
func forkAndRebuild(sess *Session, agent *agent.Agent, entryID string) error {
	if sess == nil {
		return nil
	}
	if err := sess.Branch(entryID); err != nil {
		return err
	}
	if agent != nil {
		agent.SetMessages(sess.BuildContext(nil))
	}
	return nil
}

// CheckSavedForFork rejects a file-backed Session that has not been saved yet: no user or assistant message has created its file. In-memory Sessions do not require a file.
// Ports packages/coding-agent/src/core/agent-session-runtime.ts (fork).
func (s *Session) CheckSavedForFork() error {
	if path := s.Path(); path != "" {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("This session has not been saved yet. Send a message before cloning or forking it.")
		}
	}
	return nil
}

// ForkToNewSession creates a NEW session file branched at the PARENT of
// userMsgEntryID (so the selected user message itself is excluded) and
// switches sm's current session to it. It returns the new session and the
// selected message's text, which the caller prefills into the editor for
// modification. Mirrors upstream AgentSessionRuntime.fork() with the default
// position "before": createBranchedSession(selectedEntry.parentId), or a fresh
// session carrying `parentSession` when the selected message is the root.
func (sm *SessionManager) ForkToNewSession(source *Session, userMsgEntryID string) (*Session, string, error) {
	if source == nil {
		return nil, "", fmt.Errorf("fork: nil source session")
	}
	entry, ok := source.GetEntry(userMsgEntryID)
	if !ok {
		return nil, "", fmt.Errorf("Invalid entry ID for forking")
	}
	message, ok := source.messageFor(entry)
	if !ok || message.Message.User == nil {
		return nil, "", fmt.Errorf("Invalid entry ID for forking")
	}
	selectedText := extractMessageText(message)
	if entry.Base().ParentID == nil {
		// Selected message is the root: upstream forks into a fresh empty
		// session that only records parentSession (newSession fallback).
		id, err := generateSessionID()
		if err != nil {
			return nil, "", err
		}
		newSess := NewSession(id, source.CWD())
		if source.IsPersisted() {
			newSess, err = sm.Create(id, source.Path())
			if err != nil {
				return nil, "", err
			}
		} else {
			sm.mu.Lock()
			sm.current = newSess
			sm.mu.Unlock()
		}
		return newSess, selectedText, nil
	}
	if err := source.CheckSavedForFork(); err != nil {
		return nil, "", err
	}
	newSess, err := sm.Clone(source, *entry.Base().ParentID)
	if err != nil {
		return nil, "", err
	}
	return newSess, selectedText, nil
}

// runModalModelSelector runs the typed-FQ ModelSelectorComponent.
func (m *InteractiveMode) runModalModelSelector(ctx context.Context, ms *tui.ModelSelectorComponent, opts tui.OverlayOptions) (string, bool) {
	h := m.tuiInst.ShowOverlay(ms, opts)
	defer h.Close()
	return m.runModelSelectorInput(ctx, ms)
}

// runEditorSlotModelSelector replaces the editor with the model
// selector in the layout's editor slot, handles input until done, then
// restores the editor. Mirrors upstream showSelector pattern
// (interactive-mode.ts:3655-3666) which does
//
//	this.editorContainer.clear();
//	this.editorContainer.addChild(component);
//	... input loop ...
//	restore editor.
//
// No floating overlay; chat stays fully visible above.
func (m *InteractiveMode) runEditorSlotModelSelector(ctx context.Context, ms *tui.ModelSelectorComponent) (string, bool) {
	if m.layout == nil {
		// Fallback: modal overlay (only happens in tests / non-TTY).
		return m.runModalModelSelector(ctx, ms, tui.OverlayOptions{Title: "Select model", WidthFraction: 0.85, HeightFraction: 0.7})
	}
	m.editorContainer.SetChildren(ms)
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()
	return m.runModelSelectorInput(ctx, ms)
}

func (m *InteractiveMode) runModelSelectorInput(ctx context.Context, ms *tui.ModelSelectorComponent) (string, bool) {
	m.tuiInst.Render()
	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	for !ms.Done() {
		if m.modalStopped() {
			return "", false
		}
		select {
		case <-ctx.Done():
			return "", false
		case err := <-m.inputErrCh:
			m.inputLoopErr = err
			return "", false
		case task := <-m.uiTaskCh:
			task()
		case buf := <-inputCh:
			for _, chunk := range m.modalInputChunks(ms, []string{string(buf)}) {
				ms.HandleInput(chunk)
				if ms.Done() {
					break
				}
			}
		}
		m.tuiInst.Render()
	}
	if ms.Cancelled() {
		return "", false
	}
	return ms.SelectedFQ(), true
}

func (m *InteractiveMode) runModalTreeSelector(ts *tui.TreeSelectorComponent, opts tui.OverlayOptions) (string, bool) {
	h := m.tuiInst.ShowOverlay(ts, opts)
	defer h.Close()
	m.tuiInst.Render()
	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	for !ts.Done() {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return "", false
		}
		for _, chunk := range dropKeyReleases(ts, []string{string(buf)}) {
			ts.HandleInput(chunk)
			if ts.Done() {
				break
			}
		}
		m.tuiInst.Render()
	}
	if ts.Cancelled() {
		return "", false
	}
	return ts.SelectedID(), true
}

// settingsFrame mirrors upstream SettingsSelectorComponent, which draws a
// DynamicBorder above and below the settings list and every submenu it opens.
func settingsFrame(content tui.Component) tui.Component {
	return tui.NewContainer(tui.NewDynamicBorder(), content, tui.NewDynamicBorder())
}

// runEditorSlotSelectSubmenu replaces the editor with a submenu-style selector
// and blocks until the user confirms or cancels. Mirrors upstream
// SettingsSelectorComponent submenus (settings-selector.ts SelectSubmenu).
func (m *InteractiveMode) runEditorSlotSelectSubmenu(sel *tui.SelectSubmenuComponent) (string, bool) {
	if !m.runEditorSlotComponent(settingsFrame(sel), sel.HandleInput, sel.Done) || sel.Cancelled() {
		return "", false
	}
	return sel.SelectedValue(), true
}

// runEditorSlotComponent shows component in the editor slot and feeds it
// input until done reports true. It returns false when input fails first.
func (m *InteractiveMode) runEditorSlotComponent(component tui.Component, handleInput func(string), done func() bool) bool {
	m.editorContainer.SetChildren(component)
	m.tuiInst.Render()

	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	for !done() {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return false
		}
		for _, chunk := range dropKeyReleases(component, []string{string(buf)}) {
			handleInput(chunk)
			if done() {
				break
			}
		}
		m.tuiInst.Render()
	}
	return true
}

// runEditorSlotCustom shows component in the editor slot, feeding it input,
// repainting on request, and running queued loop tasks until done closes.
func (m *InteractiveMode) runEditorSlotCustom(component interface {
	tui.Component
	HandleInput(data string)
}, renderNotify <-chan struct{}, tasks <-chan func(), done <-chan struct{}) {
	m.editorContainer.SetChildren(component)
	m.tuiInst.Render()
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()
	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	for {
		if m.modalStopped() {
			return
		}
		select {
		case <-m.modalContextDone():
			return
		case err := <-m.inputErrCh:
			m.inputLoopErr = err
			return
		case task := <-m.uiTaskCh:
			task()
		case buf := <-inputCh:
			for _, chunk := range m.modalInputChunks(component, []string{string(buf)}) {
				component.HandleInput(chunk)
			}
		case <-renderNotify:
		case task := <-tasks:
			task()
		case <-done:
			for {
				select {
				case task := <-tasks:
					task()
				default:
					return
				}
			}
		}
		m.tuiInst.Render()
	}
}

const automaticThemeValue = "/"

// themeSelectItems mirrors upstream themeItems (settings-selector.ts): the
// current theme carries a "✓ " prefix column and the others keep it blank.
func themeSelectItems(names []string, currentTheme string) []tui.SelectItem {
	items := make([]tui.SelectItem, len(names))
	for i, name := range names {
		marker := "  "
		if name == currentTheme {
			marker = "✓ "
		}
		items[i] = tui.SelectItem{Value: name, Label: marker + name}
		if name == tui.SystemThemeName {
			items[i].Description = "Theme created from your terminal's colors"
		}
	}
	return items
}

// themeSelectItemsWithAutomatic mirrors upstream singleModeThemeItems: the system theme comes first, then automatic mode, then the remaining themes.
func themeSelectItemsWithAutomatic(names []string, currentTheme string) []tui.SelectItem {
	themes := themeSelectItems(names, currentTheme)
	items := make([]tui.SelectItem, 0, len(themes)+1)
	if systemIndex := slices.IndexFunc(themes, func(item tui.SelectItem) bool { return item.Value == tui.SystemThemeName }); systemIndex != -1 {
		items = append(items, themes[systemIndex])
		themes = slices.Delete(themes, systemIndex, systemIndex+1)
	}
	items = append(items, tui.SelectItem{
		Value:       automaticThemeValue,
		Label:       "  automatic",
		Description: "Use separate themes for light and dark terminal appearance",
	})
	return append(items, themes...)
}

func preferredThemeName(names []string, preferred, fallback string) string {
	if preferred != "" {
		for _, name := range names {
			if name == preferred {
				return name
			}
		}
	}
	if slices.Contains(names, fallback) {
		return fallback
	}
	if len(names) > 0 {
		return names[0]
	}
	return fallback
}

func defaultAutomaticThemeNames(themeSetting string, names []string) (lightTheme, darkTheme string) {
	if light, dark, ok := tui.ParseAutoThemeSetting(themeSetting); ok {
		return light, dark
	}
	fixedTheme := themeSetting
	if strings.Contains(themeSetting, "/") {
		fixedTheme = ""
	}
	themeName := preferredThemeName(names, fixedTheme, tui.SystemThemeName)
	return themeName, themeName
}

// runEditorSlotTreeSelector replaces the editor with the tree selector in the
// layout's editor slot, handles input until done, then restores the editor.
// Mirrors upstream showSelector (interactive-mode.ts:3655-3666) which does:
//
//	this.editorContainer.clear(); this.editorContainer.addChild(component);
//	... input loop ... restore editor.
//
// No floating overlay: chat stays fully visible above.
// sessionAge formats a relative age string matching upstream's formatSessionDate.
// Mirrors session-selector.ts:formatSessionDate.
func sessionAge(t time.Time) string {
	diff := time.Since(t)
	min := int(diff.Minutes())
	hrs := int(diff.Hours())
	days := int(diff.Hours() / 24)
	switch {
	case min < 1:
		return "now"
	case min < 60: // upstream: packages/coding-agent/src/modes/interactive/components/session-selector.ts:formatSessionDate
		return fmt.Sprintf("%dm", min)
	case hrs < 24:
		return fmt.Sprintf("%dh", hrs)
	case days < 7:
		return fmt.Sprintf("%dd", days)
	case days < 30:
		return fmt.Sprintf("%dw", days/7)
	case days < 365:
		return fmt.Sprintf("%dmo", days/30)
	default:
		return fmt.Sprintf("%dy", days/365)
	}
}

// runEditorSlotExtensionSelector shows a bordered ExtensionSelector in
// the editor slot. Mirrors upstream's showExtensionSelector path
// (interactive-mode.ts:1935-1969 → editorContainer.addChild + setFocus),
// which uses the standalone ExtensionSelectorComponent (NO filter input,
// fixed title row, fixed hint row). Used for `/tree → "Summarize
// branch?"` and any extension UI dialog routed through
// ExtUIContext.Select / Confirm.
//
// Uses the modal input channel to avoid racing with the main input
// goroutine for stdin reads (same pattern as runEditorSlotTreeSelector).
func (m *InteractiveMode) runEditorSlotExtensionSelector(sel *tui.ExtensionSelectorComponent) (int, bool) {
	return m.runEditorSlotExtensionSelectorAs(sel, sel)
}

// runEditorSlotExtensionSelectorAs runs sel with view, a component that draws it, in the editor slot.
func (m *InteractiveMode) runEditorSlotExtensionSelectorAs(sel *tui.ExtensionSelectorComponent, view tui.Component) (int, bool) {
	if m.layout == nil {
		return -1, false
	}
	m.editorContainer.SetChildren(view)
	m.tuiInst.Render()
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

	for !sel.Done() {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return -1, false
		}
		for _, chunk := range dropKeyReleases(sel, []string{string(buf)}) {
			sel.HandleInput(chunk)
			if sel.Done() {
				break
			}
		}
		m.tuiInst.Render()
	}
	if sel.Cancelled() {
		return -1, false
	}
	return sel.SelectedIndex(), true
}

// runEditorSlotExtensionEditor shows a bordered ExtensionEditorComponent in
// the editor slot. Mirrors upstream showExtensionEditor
// (interactive-mode.ts:2065-2090 → editorContainer.clear + addChild +
// setFocus). Used for /tree "Custom summarization instructions" and
// any extension ctx.ui.editor() calls.
//
// Uses the modal input channel to avoid racing with the main input
// goroutine for stdin reads (same pattern as runEditorSlotTreeSelector).
func (m *InteractiveMode) runEditorSlotExtensionEditor(ed *ExtensionEditorComponent, result func() (value string, done, cancelled bool)) (string, bool) {
	ed.SetExternalEditor(func(command, content string, apply func(string)) {
		m.openExternalEditorBuffer(m.runCtx, command, content, apply)
	})
	if m.layout == nil {
		return "", false
	}
	m.editorContainer.SetChildren(ed)
	m.tuiInst.Render()
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

	ctx := m.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if _, done, _ := result(); done {
			break
		}
		if m.modalStopped() {
			return "", false
		}
		select {
		case err := <-m.inputErrCh:
			m.inputLoopErr = err
			return "", false
		case <-ctx.Done():
			return "", false
		case fn := <-m.uiTaskCh:
			fn()
		case buf := <-inputCh:
			if m.consumeModalHostInput(string(buf)) {
				continue
			}
			for _, chunk := range dropKeyReleases(ed, []string{string(buf)}) {
				ed.HandleInput(chunk)
				if _, done, _ := result(); done {
					break
				}
			}
			m.tuiInst.Render()
		}
	}
	value, _, cancelled := result()
	if cancelled {
		return "", false
	}
	return value, true
}

// runEditorSlotUserMessageSelector shows the fork picker in the editor slot and returns the selected entry ID.
func (m *InteractiveMode) runEditorSlotUserMessageSelector(items []tui.UserMessageItem) (string, bool) {
	var selectedID string
	var done, cancelled atomic.Bool
	sel := tui.NewUserMessageSelectorComponent(items,
		func(entryID string) { selectedID = entryID; done.Store(true) },
		func() { cancelled.Store(true); done.Store(true) },
		"")
	var overlay *tui.OverlayHandle
	if m.layout == nil {
		overlay = m.tuiInst.ShowOverlay(sel, tui.OverlayOptions{WidthFraction: 0.85, HeightFraction: 0.7})
		defer overlay.Close()
	} else {
		m.editorContainer.SetChildren(sel)
		defer func() {
			m.editorContainer.SetChildren(m.editor)
			m.tuiInst.RequestRender()
		}()
	}
	m.tuiInst.Render()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

	// upstream: interactive-mode.ts:5534 focuses selector.getMessageList(), so the list takes the input. A parent Container reuses the
	// selector's lines until it is invalidated, so a list change invalidates the selector.
	list := sel.GetMessageList()
	handle := func(data string) {
		list.HandleInput(data)
		sel.Invalidate()
	}
	for !done.Load() {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return "", false
		}
		dispatchModalInput(sel, []string{string(buf)}, handle, done.Load)
		m.tuiInst.Render()
	}
	if cancelled.Load() {
		return "", false
	}
	return selectedID, true
}

// sessionSelectorOutcome records which of the selector's onSelect and onCancel callbacks ran (session-selector.ts:757-763), so a host reads the
// result from the callbacks rather than from the component's polled state.
type sessionSelectorOutcome struct {
	path      string
	selected  bool
	cancelled bool
}

func (o *sessionSelectorOutcome) onSelect(sessionPath string) { o.path, o.selected = sessionPath, true }
func (o *sessionSelectorOutcome) onCancel()                   { o.cancelled = true }
func (o *sessionSelectorOutcome) finished() bool              { return o.selected || o.cancelled }

func (m *InteractiveMode) runEditorSlotSessionSelector(sel *SessionSelectorComponent, outcome *sessionSelectorOutcome) (string, bool) {
	defer sel.close()
	sel.drainLoadUpdates()
	if m.layout == nil {
		return "", false
	}
	m.editorContainer.SetChildren(sel)
	m.tuiInst.Render()

	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	finished := func() bool { return outcome.finished() || sel.operationError != nil }
	for !finished() {
		if m.modalStopped() {
			return "", false
		}
		select {
		case <-m.modalContextDone():
			return "", false
		case err := <-m.inputErrCh:
			m.inputLoopErr = err
			return "", false
		case task := <-m.uiTaskCh:
			task()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case <-sel.work.ready:
			sel.drainLoadUpdates()
		case result := <-sel.loadResult(sessionScopeCurrent):
			sel.finishLoad(sessionScopeCurrent, result)
		case result := <-sel.loadResult(sessionScopeAll):
			sel.finishLoad(sessionScopeAll, result)
		case update := <-sel.work.updates:
			update()
		case <-sel.statusTimeout():
			sel.clearStatusMessage()
		case buf, ok := <-inputCh:
			if !ok {
				return "", false
			}
			m.dispatchModalInput(sel, []string{string(buf)}, sel.HandleInput, finished)
		}
		m.tuiInst.Render()
	}
	if sel.operationError != nil {
		// Pi's void confirmRename leaves a rename rejection unhandled, so Node raises it to the uncaughtException handler (interactive-mode.ts:4266) that Run's recover mirrors. The deferred cleanups above run first.
		panic(uncaughtError{sel.operationError})
	}
	if outcome.cancelled {
		return "", false
	}
	return outcome.path, outcome.path != ""
}

func (m *InteractiveMode) runEditorSlotTreeSelector(ts *tui.TreeSelectorComponent) (string, bool) {
	if m.layout == nil {
		// Fallback: overlay (shouldn't happen in normal flow).
		return m.runModalTreeSelector(ts, tui.OverlayOptions{
			Title: "Session tree", WidthFraction: 0.9, HeightFraction: 0.8,
		})
	}

	// Swap editor → tree selector in layout; the selector takes focus so its label input shows the cursor (upstream showSelector setFocus).
	previousFocus := m.tuiInst.GetFocusedComponent()
	m.editorContainer.SetChildren(ts)
	m.tuiInst.SetFocus(ts)
	m.tuiInst.Render()

	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.SetFocus(previousFocus)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

	for !ts.Done() {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return "", false
		}
		dispatchModalInput(ts, []string{string(buf)}, ts.HandleInput, ts.Done)
		// Coalesce a burst of buffered keystrokes (key-repeat, paste,
		// spamming) into a single render so the loop never queues one
		// render per byte and falls behind input.
	drain:
		for !ts.Done() {
			select {
			case more := <-inputCh:
				m.dispatchModalInput(ts, []string{string(more)}, ts.HandleInput, ts.Done)
			default:
				break drain
			}
		}
		m.tuiInst.Render()
	}
	if ts.Cancelled() {
		return "", false
	}
	return ts.SelectedID(), true
}

// userMessageSelectorItems extracts the user-role messages from the current
// path-to-leaf in chronological order (oldest to newest), matching upstream's
// session.getUserMessagesForForking() contract.
func userMessageSelectorItems(s *Session) []tui.UserMessageItem {
	leaf := s.GetLeafID()
	if leaf == nil {
		return nil
	}
	var items []tui.UserMessageItem
	for _, e := range s.GetBranch(*leaf) {
		me, ok := s.messageFor(e)
		if !ok || me.Message.User == nil {
			continue
		}
		txt := extractMessageText(me)
		if txt == "" {
			continue
		}
		items = append(items, tui.UserMessageItem{ID: me.ID, Text: txt})
	}
	return items
}

// treeNodeAdapter bridges *SessionTreeNode (codingagent) →
// tui.TreeNode (TUI-package interface). Label rendering is delegated
// to treeRowFormatter so the on-screen output mirrors
// upstream tree-selector.ts:708-808 (per-type labels) and :849-895
// (per-tool argument summaries). The formatter is shared across all
// adapters in a single /tree open via the `f` pointer: both for
// efficiency (one toolCallMap pre-walk per session) and so siblings
// agree on home-path shortening + tool-name resolution.
type treeNodeAdapter struct {
	n *SessionTreeNode
	f *treeRowFormatter
}

func (a *treeNodeAdapter) NodeID() string { return a.n.Entry.Base().ID }

func (a *treeNodeAdapter) NodeLabelTimestamp() string { return a.n.LabelTimestamp }

func (a *treeNodeAdapter) NodeBranchLabel() string { return a.n.Label }

// SetNodeBranchLabel updates the displayed tree snapshot before its label-change entry is persisted.
func (a *treeNodeAdapter) SetNodeBranchLabel(label, timestamp string) {
	a.n.Label, a.n.LabelTimestamp = label, timestamp
}

// NodeFilterTags classifies the entry for /tree's filter-mode
// skip-set. Mirrors upstream's `isSettingsEntry`
// derivation at `tree-selector.ts:358-364`: `label`, `context_edit`,
// `custom`, `model_change`, `thinking_level_change`, `session_info` are all
// tagged `"settings"` and hidden in the default filter mode.
// Adds `"tool_result"` (no-tools mode),
// `"user"` (user-only mode), and `"labeled"` (labeled-only mode).
func (a *treeNodeAdapter) NodeFilterTags() []string {
	var tags []string
	switch a.n.Entry.Base().Type {
	case "label", "context_edit", "custom", "model_change", "thinking_level_change", "session_info":
		tags = append(tags, "settings")
	case "usage":
		tags = append(tags, "usage")
	case "message":
		if me, ok := a.f.asMessage(a.n.Entry); ok {
			switch me.Message.Role() {
			case "user":
				tags = append(tags, "user")
			case "toolResult":
				tags = append(tags, "tool_result")
			}
		}
	}
	if a.n.Label != "" {
		tags = append(tags, "labeled")
	}
	return tags
}

func (a *treeNodeAdapter) NodeSearchableText() string {
	// Mirrors upstream getSearchableText (tree-selector.ts:559-600):
	// the branch label plus role, content, and per-type fields, joined
	// as a plain string the tui lowercases and substring-matches.
	e := a.n.Entry
	parts := []string{}
	if a.n.Label != "" {
		parts = append(parts, a.n.Label)
	}
	switch e.Base().Type {
	case "message":
		if a.f != nil {
			if me, ok := a.f.asMessage(e); ok {
				parts = append(parts, me.Message.Role())
				parts = append(parts, extractMessageText(me))
			}
		}
	case "custom_message":
		var ent CustomMessageEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		parts = append(parts, ent.CustomType, extractCustomMessageText(ent))
	case "compaction":
		parts = append(parts, "compaction", e.Base().Type)
	case "branch_summary":
		var ent BranchSummaryEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		parts = append(parts, "branch", "summary", ent.Summary)
	case "session_info":
		var ent SessionInfoEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		parts = append(parts, "title", ent.Name)
	case "model_change":
		var ent ModelChangeEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		parts = append(parts, "model", ent.ModelID)
	case "thinking_level_change":
		var ent ThinkingLevelEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		parts = append(parts, "thinking", ent.ThinkingLevel)
	case "custom":
		var ent CustomEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		parts = append(parts, "custom", ent.CustomType)
	case "context_edit":
		mode, targetID := contextEditSummary(e)
		parts = append(parts, "context edit", mode, targetID)
	case "label":
		var ent LabelEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		label := ""
		if ent.Label != nil {
			label = *ent.Label
		}
		parts = append(parts, "label", label)
	}
	return strings.Join(parts, " ")
}

// NodeCopyText returns complete entry text without display truncation or whitespace normalization.
func (a *treeNodeAdapter) NodeCopyText() *string {
	e := a.n.Entry
	text := ""
	switch e.Base().Type {
	case "message":
		var message MessageEntry
		var ok bool
		if a.f != nil {
			message, ok = a.f.asMessage(e)
		} else {
			message, ok = e.(MessageEntry)
		}
		if !ok {
			return nil
		}
		if message.Message.Role() == agent.RoleBashExecution {
			text, _ = message.Message.Custom["command"].(string)
		} else {
			var content strings.Builder
			for _, block := range message.Message.ContentBlocks() {
				if block, ok := block.(ai.TextContent); ok {
					content.WriteString(block.Text)
				}
			}
			text = content.String()
			if text == "" && message.Message.Assistant != nil {
				text = message.Message.Assistant.ErrorMessage
			}
		}
	case "custom_message":
		var entry CustomMessageEntry
		if json.Unmarshal(e.Raw(), &entry) != nil {
			return nil
		}
		text = extractCustomMessageText(entry)
	case "compaction", "branch_summary":
		var entry struct {
			Summary string `json:"summary"`
		}
		if json.Unmarshal(e.Raw(), &entry) != nil {
			return nil
		}
		text = entry.Summary
	}
	if widthx.JSTrim(text) == "" {
		return nil
	}
	return &text
}

func (a *treeNodeAdapter) NodeLabel() string {
	f := a.f
	if f == nil {
		// Adapter built without a formatter (e.g. older callers /
		// unit tests). Build a one-shot formatter with no toolCallMap;
		// tool_result rows will fall through to "[tool]" but every
		// other type renders identically.
		f = newTreeRowFormatter(nil)
	}
	return f.FormatTreeRow(a.n.Entry)
}

func (a *treeNodeAdapter) NodeChildren() []tui.TreeNode {
	// Filter out tool-call-only assistant messages
	// (mirror upstream tree-selector.ts:287-296 unconditional
	// pre-filter). Suppressed nodes are not reparented: their
	// visible children are pulled up to take their slot. Recursive
	// because a chain of suppressed nodes (asst-tool-only →
	// tool_result_user → asst-tool-only → …) collapses to its
	// non-suppressed leaves.
	out := make([]tui.TreeNode, 0, len(a.n.Children))
	for _, c := range a.n.Children {
		child := &treeNodeAdapter{n: c, f: a.f}
		if a.f != nil && a.f.shouldSuppressInTree(c.Entry) {
			out = append(out, child.NodeChildren()...)
			continue
		}
		out = append(out, child)
	}
	return out
}

type scopedModelsRefresh struct {
	models []tui.ModelItem
	status string
	kind   tui.RefreshStatusKind
}

// runModalScopedModels applies selection changes to the available session scope and persists configured IDs only on save.
func (m *InteractiveMode) runModalScopedModels(sl *tui.ScopedModelsList, refresh <-chan scopedModelsRefresh, selection *scopedModelsSelection) tui.ScopedModelsResult {
	m.editorContainer.SetChildren(sl)
	m.tuiInst.Render()

	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()
	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

	for !sl.Done() {
		if m.modalStopped() {
			return tui.ScopedModelsResult{Cancelled: true}
		}
		select {
		case <-m.modalContextDone():
			return tui.ScopedModelsResult{Cancelled: true}
		case err := <-m.inputErrCh:
			m.inputLoopErr = err
			return tui.ScopedModelsResult{Cancelled: true}
		case refreshed := <-refresh:
			selection.updateAvailable(refreshed.models)
			if !selection.changed && !selection.sessionScoped {
				sl.UpdateModels(refreshed.models, selection.configuredIDs())
			} else {
				sl.UpdateModels(refreshed.models)
			}
			if enabled := sl.EnabledIDs(); enabled != nil {
				selection.apply(m, enabled)
			}
			sl.SetRefreshStatus(refreshed.status, refreshed.kind)
			m.tuiInst.Render()
			refresh = nil
			continue
		case task := <-m.uiTaskCh:
			task()
		case buf := <-inputCh:
			for _, chunk := range m.modalInputChunks(sl, []string{string(buf)}) {
				previousIDs := sl.EnabledIDs()
				sl.HandleInput(chunk)
				currentIDs := sl.EnabledIDs()
				if !scopedModelIDsEqual(previousIDs, currentIDs) {
					selection.changed = true
					selection.apply(m, currentIDs)
				}
				if enabledIDs, ok := sl.ConsumeSave(); ok {
					m.persistScopedModelIDs(selection.persistedIDs(enabledIDs))
				}
				if sl.Done() {
					break
				}
			}
			m.tuiInst.Render()
		}
	}
	return sl.Result()
}

func scopedModelIDsEqual(a, b []string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return slices.Equal(a, b)
}

// runEditorSlotOAuthSelector replaces the editor with the OAuth provider
// picker and blocks until the user selects a provider or cancels.
// Mirrors upstream showOAuthSelector pattern (interactive-mode.ts:2452).
func (m *InteractiveMode) runEditorSlotOAuthSelector(sel *tui.OAuthSelectorComponent) (string, bool) {
	if m.layout == nil {
		return "", false
	}
	previousFocus := m.tuiInst.GetFocusedComponent()
	m.editorContainer.SetChildren(sel)
	m.tuiInst.SetFocus(sel)
	m.tuiInst.Render()
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.SetFocus(previousFocus)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

	for !sel.Done() {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return "", false
		}
		for _, chunk := range dropKeyReleases(sel, []string{string(buf)}) {
			sel.HandleInput(chunk)
			if sel.Done() {
				break
			}
		}
		// Coalesce a burst of buffered keystrokes (key-repeat, paste) into a
		// single render so the loop never queues one render per input message
		// and falls behind, replaying keys late. End state is identical; only
		// transient frames are skipped (same pattern as runEditorSlotTreeSelector).
		m.drainModalInput(sel, inputCh, func(chunk string) bool {
			sel.HandleInput(chunk)
			return sel.Done()
		})
		m.tuiInst.Render()
	}
	if sel.Cancelled() {
		return "", false
	}
	return sel.SelectedID(), true
}

// runEditorSlotLoginDialog replaces the editor with the login dialog.
// The caller drives device-flow I/O from a goroutine; state updates
// arrive on renderNotify, causing a re-render. Blocks until dlg.Done().
func (m *InteractiveMode) runEditorSlotLoginDialog(dlg *tui.LoginDialogComponent, renderNotify <-chan struct{}) bool {
	return m.runEditorSlotLoginDialogPrompts(dlg, renderNotify, nil, nil)
}

// runEditorSlotLoginDialogPrompts is runEditorSlotLoginDialog for a flow that can also ask select prompts and end the
// dialog itself. A select prompt received on selects replaces the dialog with a selector until the user picks an option
// or cancels it; either way the dialog returns (interactive-mode.ts:6243-6276 showAuthSelect). Closing ended returns
// false without marking the dialog cancelled.
func (m *InteractiveMode) runEditorSlotLoginDialogPrompts(dlg *tui.LoginDialogComponent, renderNotify <-chan struct{}, selects <-chan authPromptRequest, ended <-chan struct{}) bool {
	if m.layout == nil {
		return false
	}
	previousFocus := m.tuiInst.GetFocusedComponent()
	m.editorContainer.SetChildren(dlg)
	m.tuiInst.SetFocus(dlg)
	m.tuiInst.Render()
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.SetFocus(previousFocus)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

	var request *authPromptRequest
	var selector *tui.ExtensionSelectorComponent
	var promptDone <-chan struct{}
	restoreDialog := func() {
		request, selector, promptDone = nil, nil, nil
		m.editorContainer.SetChildren(dlg)
		m.tuiInst.SetFocus(dlg)
	}
	for !dlg.Done() {
		if m.modalStopped() {
			return false
		}
		select {
		case <-m.modalContextDone():
			return false
		case err := <-m.inputErrCh:
			m.inputLoopErr = err
			return false
		case task := <-m.uiTaskCh:
			task()
		case next := <-selects:
			prompt := next.prompt.(ai.AuthSelectPrompt)
			labels := make([]string, len(prompt.Options))
			for i, option := range prompt.Options {
				labels[i] = option.Label
			}
			request, promptDone = &next, next.ctx.Done()
			selector = tui.NewExtensionSelectorComponent(prompt.Message, labels, nil, nil)
			m.editorContainer.SetChildren(selector)
			m.tuiInst.SetFocus(selector)
			m.tuiInst.Render()
			extension.CallInitiated(next.ctx)
		case <-promptDone:
			restoreDialog()
		case <-ended:
			return false
		case buf := <-inputCh:
			if selector != nil {
				for _, chunk := range m.modalInputChunks(selector, []string{string(buf)}) {
					selector.HandleInput(chunk)
					if !selector.Done() {
						continue
					}
					reply := authPromptReply{err: errLoginCancelled}
					if !selector.Cancelled() {
						reply = authPromptReply{value: request.prompt.(ai.AuthSelectPrompt).Options[selector.SelectedIndex()].ID}
					}
					request.reply <- reply
					restoreDialog()
					break
				}
				break
			}
			for _, chunk := range m.modalInputChunks(dlg, []string{string(buf)}) {
				dlg.HandleInput(chunk)
				if dlg.Done() {
					break
				}
			}
			// Coalesce queued keystrokes into one render (see OAuth selector).
			m.drainModalInput(dlg, inputCh, func(chunk string) bool {
				dlg.HandleInput(chunk)
				return dlg.Done()
			})
		case <-renderNotify:
		}
		m.tuiInst.Render()
	}
	return !dlg.Cancelled()
}
