// Modal selector plumbing for interactive mode.
//
// runModalSelector pushes a tui overlay, takes over input from
// inputLoop's perspective (we're called synchronously from
// dispatchSlash, which itself is called from within dispatchKey, so
// inputLoop is paused), reads keystrokes via tui.ReadInput, and
// dispatches them to the overlay component until Done() returns true.
//
// Two flavours: FilterableList (returns int index) and TreeSelect
// (returns string id).

package codingagent

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
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
	if err := sess.Fork(entryID); err != nil {
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
	entry, ok := source.EntryByID(userMsgEntryID)
	if !ok {
		return nil, "", fmt.Errorf("Invalid entry ID for forking")
	}
	message, ok := source.messageFor(entry)
	if !ok || message.Message.User == nil {
		return nil, "", fmt.Errorf("Invalid entry ID for forking")
	}
	selectedText := extractMessageText(message)
	if entry.Base.ParentID == nil {
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
	newSess, err := sm.Clone(source, *entry.Base.ParentID)
	if err != nil {
		return nil, "", err
	}
	return newSess, selectedText, nil
}

func (m *InteractiveMode) runModalSelector(list *tui.FilterableList, opts tui.OverlayOptions) (int, bool) {
	if opts.Title == "Fork from message" {
		selector := tui.NewUserMessageSelector(list.Labels)
		return m.runEditorSlotUserMessageSelector(selector)
	}

	h := m.tuiInst.OpenOverlay(list, opts)
	defer h.Close()
	m.tuiInst.Render()
	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	for !list.Done() {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return -1, false
		}
		for _, chunk := range dropKeyReleases(list, []string{string(buf)}) {
			list.HandleInput(chunk)
			if list.Done() {
				break
			}
		}
		m.tuiInst.Render()
	}
	if list.Cancelled() {
		return -1, false
	}
	return list.SelectedIndex(), true
}

// runModalModelSelector: counterpart to runModalSelector
// for the typed-FQ ModelSelector component.
func (m *InteractiveMode) runModalModelSelector(ctx context.Context, ms *tui.ModelSelector, opts tui.OverlayOptions, refresh <-chan modelSelectorRefresh) (string, bool) {
	h := m.tuiInst.OpenOverlay(ms, opts)
	defer h.Close()
	return m.runModelSelectorInput(ctx, ms, refresh)
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
func (m *InteractiveMode) runEditorSlotModelSelector(ctx context.Context, ms *tui.ModelSelector, refresh <-chan modelSelectorRefresh) (string, bool) {
	if m.layout == nil {
		// Fallback: modal overlay (only happens in tests / non-TTY).
		return m.runModalModelSelector(ctx, ms, tui.OverlayOptions{Title: "Select model", WidthFraction: 0.85, HeightFraction: 0.7}, refresh)
	}
	m.editorContainer.SetChildren(ms)
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()
	return m.runModelSelectorInput(ctx, ms, refresh)
}

func (m *InteractiveMode) runModelSelectorInput(ctx context.Context, ms *tui.ModelSelector, refresh <-chan modelSelectorRefresh) (string, bool) {
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
		case refreshed := <-refresh:
			if refreshed.updateModels {
				ms.UpdateModels(refreshed.models)
			}
			if refreshed.errorText != "" {
				ms.SetError(refreshed.errorText)
			} else {
				ms.SetRefreshSuccess("Model catalogs refreshed.")
			}
			m.updateProviderInfo()
			refresh = nil
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

func (m *InteractiveMode) runModalTreeSelector(ts *tui.TreeSelect, opts tui.OverlayOptions) (string, bool) {
	h := m.tuiInst.OpenOverlay(ts, opts)
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
	return tui.NewContainer(tui.NewDynamicBorder(""), content, tui.NewDynamicBorder(""))
}

// runModalSettingsList opens a SettingsList in the editor slot (no overlay)
// and blocks until the user cancels. Each change calls onChange while the
// list stays open with its selection and search, and the row then shows the
// value onChange returns, as upstream SettingsList's onChange does
// (settings-list.ts:264-291). Mirrors upstream showSelector pattern
// (interactive-mode.ts:3655-3666).
func (m *InteractiveMode) runModalSettingsList(sl *tui.SettingsList, onChange func(id, value string) string) {
	// Swap editor → framed settings list in the layout's editor slot.
	frame := settingsFrame(sl)
	m.editorContainer.SetChildren(frame)
	m.tuiInst.Render()

	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	for {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return
		}
		for _, chunk := range dropKeyReleases(sl, []string{string(buf)}) {
			sl.HandleInput(chunk)
			if sl.Cancelled() {
				return
			}
			if sl.Done() {
				// A change that opens its own selector takes the editor slot
				// meanwhile; the list returns to it afterwards.
				id, value := sl.ChangedID, sl.ChangedValue
				sl.Reset()
				sl.UpdateValue(id, onChange(id, value))
				m.editorContainer.SetChildren(frame)
			}
		}
		m.tuiInst.Render()
	}
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

type automaticThemeMenu struct {
	tui.BaseComponent
	list    *tui.SettingsList
	submenu *tui.SelectSubmenuComponent
}

func newAutomaticThemeMenu(lightTheme, darkTheme string) *automaticThemeMenu {
	items := []tui.SettingItem{
		{
			ID:           "light-theme",
			Label:        "Light theme",
			Description:  "Theme to use in automatic mode when the terminal is light",
			CurrentValue: lightTheme,
			Values:       []string{lightTheme},
		},
		{
			ID:           "dark-theme",
			Label:        "Dark theme",
			Description:  "Theme to use in automatic mode when the terminal is dark",
			CurrentValue: darkTheme,
			Values:       []string{darkTheme},
		},
		{
			ID:           "apply",
			Label:        "Apply",
			Description:  "Save and go back",
			CurrentValue: "save and go back",
			Values:       []string{"save and go back"},
		},
		{
			ID:           "single-mode",
			Label:        "Change mode",
			Description:  "Switch to one theme for light and dark",
			CurrentValue: "switch to single theme",
			Values:       []string{"switch to single theme"},
		},
	}
	// Upstream showAutomaticMenu: SettingsList(items, min(items.length, 10))
	// without search.
	return &automaticThemeMenu{list: tui.NewSettingsListWithOptions(items, min(len(items), 10), false)}
}

func (m *automaticThemeMenu) Render(width int) []string {
	th := tui.ActiveTheme()
	accent := th.Accent
	if accent == "" {
		accent = "\x1b[38;2;138;190;183m"
	}
	muted := th.Muted
	if muted == "" {
		muted = "\x1b[38;2;128;128;128m"
	}
	// Upstream showAutomaticMenu adds the heading rows as Text(..., 0, 0), so
	// they wrap to the render width.
	text := func(content string) []string { return tui.NewPaddedText(content, 0, 0, nil).Render(width) }
	lines := text(accent + "\x1b[1mAutomatic Theme" + th.Reset)
	lines = append(lines, "")
	lines = append(lines, text(muted+"Choose themes for terminal light and dark appearance."+th.Reset)...)
	lines = append(lines, text(muted+"Light/dark detection requires terminal support."+th.Reset)...)
	lines = append(lines, "")
	if m.submenu != nil {
		lines = append(lines, m.submenu.Render(width)...)
	} else {
		lines = append(lines, m.list.Render(width)...)
	}
	return lines
}

func (m *automaticThemeMenu) HandleInput(data string) { m.list.HandleInput(data) }
func (m *automaticThemeMenu) Done() bool              { return m.list.Done() }
func (m *automaticThemeMenu) Cancelled() bool         { return m.list.Cancelled() }
func (m *automaticThemeMenu) Reset()                  { m.list.Reset() }
func (m *automaticThemeMenu) ChangedID() string       { return m.list.ChangedID }
func (m *automaticThemeMenu) UpdateValue(id, value string) {
	m.list.UpdateValue(id, value)
	m.Invalidate()
}

// runEditorSlotThemeSubmenu mirrors upstream theme submenu behavior closely:
// selection changes preview the theme live; Esc restores the original theme;
// Enter commits the selected theme. The first screen offers Automatic mode,
// which stores settings as "lightTheme/darkTheme".
func (m *InteractiveMode) runEditorSlotThemeSubmenu(currentTheme string) (string, bool) {
	reg := tui.ActiveThemeRegistry()
	names := reg.Names()
	if light, _, ok := tui.ParseAutoThemeSetting(currentTheme); ok {
		return m.runEditorSlotAutomaticThemeSubmenu(currentTheme, names, light)
	}

	currentValue := preferredThemeName(names, currentTheme, tui.SystemThemeName)
	items := themeSelectItemsWithAutomatic(names, currentValue)
	sel := tui.NewSelectSubmenu("Theme", "Select a theme, or choose automatic to follow terminal appearance.", items, currentValue)
	m.editorContainer.SetChildren(settingsFrame(sel))
	m.tuiInst.Render()
	original := currentTheme
	lastPreview := currentValue
	restored := false
	restore := func() {
		if restored {
			return
		}
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.Render()
		restored = true
	}
	defer restore()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	for !sel.Done() {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return "", false
		}
		for _, chunk := range dropKeyReleases(sel, []string{string(buf)}) {
			sel.HandleInput(chunk)
			preview := sel.CurrentValue()
			if preview != "" && preview != lastPreview {
				if preview == automaticThemeValue {
					light, dark := defaultAutomaticThemeNames(original, names)
					preview = light + "/" + dark
				}
				m.previewTheme(preview)
				lastPreview = sel.CurrentValue()
			}
			if sel.Done() {
				break
			}
		}
		m.tuiInst.Render()
	}
	if sel.Cancelled() {
		m.previewTheme(original)
		return "", false
	}
	selected := sel.SelectedValue()
	if selected == automaticThemeValue {
		restore()
		light, _, _ := tui.ParseAutoThemeSetting(original)
		return m.runEditorSlotAutomaticThemeSubmenu(original, names, light)
	}
	return selected, true
}

func (m *InteractiveMode) runEditorSlotAutomaticThemeSubmenu(currentTheme string, names []string, preferredSingle string) (string, bool) {
	lightTheme, darkTheme := defaultAutomaticThemeNames(currentTheme, names)
	menu := newAutomaticThemeMenu(lightTheme, darkTheme)
	m.editorContainer.SetChildren(settingsFrame(menu))
	m.tuiInst.Render()
	original := currentTheme
	restored := false
	restore := func() {
		if restored {
			return
		}
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.Render()
		restored = true
	}
	defer restore()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	for {
		for !menu.Done() {
			buf, ok := m.readModalInput(inputCh)
			if !ok {
				return "", false
			}
			for _, chunk := range dropKeyReleases(menu, []string{string(buf)}) {
				menu.HandleInput(chunk)
				if menu.Done() {
					break
				}
			}
			m.tuiInst.Render()
		}
		if menu.Cancelled() {
			m.previewTheme(original)
			return "", false
		}

		switch menu.ChangedID() {
		case "light-theme":
			if chosen, ok := m.runThemeChoiceSubmenu(menu, "Light Theme", "Select the theme to use for light terminal appearance", themeSelectItems(names, lightTheme), lightTheme, lightTheme+"/"+darkTheme); ok {
				lightTheme = chosen
				menu.UpdateValue("light-theme", chosen)
				m.previewTheme(lightTheme + "/" + darkTheme)
			}
		case "dark-theme":
			if chosen, ok := m.runThemeChoiceSubmenu(menu, "Dark Theme", "Select the theme to use for dark terminal appearance", themeSelectItems(names, darkTheme), darkTheme, lightTheme+"/"+darkTheme); ok {
				darkTheme = chosen
				menu.UpdateValue("dark-theme", chosen)
				m.previewTheme(lightTheme + "/" + darkTheme)
			}
		case "apply":
			return lightTheme + "/" + darkTheme, true
		case "single-mode":
			restore()
			if preferredSingle == "" {
				preferredSingle = darkTheme
			}
			return m.runEditorSlotThemeSubmenu(preferredSingle)
		}
		menu.Reset()
	}
}

// runThemeChoiceSubmenu replaces only the parent's list content and restores its pending automatic selection when the child is canceled.
// upstream: packages/coding-agent/src/modes/interactive/components/settings-selector.ts:createThemeSelect
func (m *InteractiveMode) runThemeChoiceSubmenu(parent *automaticThemeMenu, title, description string, items []tui.SelectItem, currentValue, parentTheme string) (string, bool) {
	sel := tui.NewSelectSubmenu(title, description, items, currentValue)
	parent.submenu = sel
	m.editorContainer.SetChildren(settingsFrame(parent))
	m.tuiInst.Render()
	lastPreview := currentValue
	defer func() {
		parent.submenu = nil
		m.editorContainer.SetChildren(settingsFrame(parent))
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
			preview := sel.CurrentValue()
			if preview != "" && preview != lastPreview {
				m.previewTheme(preview)
				lastPreview = preview
			}
			if sel.Done() {
				break
			}
		}
		m.tuiInst.Render()
	}
	if sel.Cancelled() {
		m.previewTheme(parentTheme)
		return "", false
	}
	return sel.SelectedValue(), true
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
	case min < 60:
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
	if m.layout == nil {
		return -1, false
	}
	m.editorContainer.SetChildren(sel)
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
func (m *InteractiveMode) runEditorSlotExtensionEditor(ed *tui.ExtensionEditorComponent) (string, bool) {
	ed.SetExternalEditor(func(content string, apply func(string)) {
		m.openExternalEditorBuffer(m.runCtx, content, apply)
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
	for !ed.Done() {
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
			if m.consumeTerminalThemeInput(string(buf)) {
				continue
			}
			for _, chunk := range dropKeyReleases(ed, []string{string(buf)}) {
				ed.HandleInput(chunk)
				if ed.Done() {
					break
				}
			}
			m.tuiInst.Render()
		}
	}
	if ed.Cancelled() {
		return "", false
	}
	return ed.Value(), true
}

func (m *InteractiveMode) runEditorSlotUserMessageSelector(sel *tui.UserMessageSelector) (int, bool) {
	var overlay *tui.OverlayHandle
	if m.layout == nil {
		overlay = m.tuiInst.OpenOverlay(sel, tui.OverlayOptions{WidthFraction: 0.85, HeightFraction: 0.7})
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

	for !sel.Done() {
		buf, ok := m.readModalInput(inputCh)
		if !ok {
			return -1, false
		}
		dispatchModalInput(sel, []string{string(buf)}, sel.HandleInput, sel.Done)
		m.tuiInst.Render()
	}
	if sel.Cancelled() {
		return -1, false
	}
	return sel.SelectedIndex(), true
}

func (m *InteractiveMode) runEditorSlotSessionSelector(sel *sessionSelector) (string, bool) {
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
	for !sel.Done() {
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
			m.dispatchModalInput(sel, []string{string(buf)}, sel.HandleInput, sel.Done)
		}
		m.tuiInst.Render()
	}
	if sel.operationError != nil {
		// Pi's void confirmRename leaves a rename rejection unhandled, so Node raises it to the uncaughtException handler (interactive-mode.ts:4266) that Run's recover mirrors. The deferred cleanups above run first.
		panic(uncaughtError{sel.operationError})
	}
	if sel.Cancelled() {
		return "", false
	}
	return sel.SelectedPath(), sel.SelectedPath() != ""
}

func (m *InteractiveMode) runEditorSlotTreeSelector(ts *tui.TreeSelect) (string, bool) {
	if m.layout == nil {
		// Fallback: overlay (shouldn't happen in normal flow).
		return m.runModalTreeSelector(ts, tui.OverlayOptions{
			Title: "Session tree", WidthFraction: 0.9, HeightFraction: 0.8,
		})
	}

	// Swap editor → tree selector in layout.
	m.editorContainer.SetChildren(ts)
	m.tuiInst.Render()

	defer func() {
		m.editorContainer.SetChildren(m.editor)
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
// path-to-leaf and returns parallel slices of entry IDs and message texts in
// chronological order (oldest to newest), matching upstream's
// session.getUserMessagesForForking() contract.
func userMessageSelectorItems(s *Session) (ids []string, labels []string) {
	leaf := s.LeafID()
	if leaf == nil {
		return nil, nil
	}
	path := s.Branch(*leaf)
	for _, e := range path {
		me, ok := s.messageFor(e)
		if !ok || me.Message.User == nil {
			continue
		}
		txt := extractMessageText(me)
		if txt == "" {
			continue
		}
		ids = append(ids, me.ID)
		labels = append(labels, txt)
	}
	return ids, labels
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

func (a *treeNodeAdapter) NodeID() string { return a.n.Entry.Base.ID }

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
	switch a.n.Entry.Base.Type {
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
	switch e.Base.Type {
	case "message":
		if a.f != nil {
			if me, ok := a.f.asMessage(e); ok {
				parts = append(parts, me.Message.Role())
				parts = append(parts, extractMessageText(me))
			}
		}
	case "custom_message":
		var ent CustomMessageEntry
		_ = json.Unmarshal(e.raw, &ent)
		parts = append(parts, ent.CustomType, extractCustomMessageText(ent))
	case "compaction":
		parts = append(parts, "compaction", e.Base.Type)
	case "branch_summary":
		var ent BranchSummaryEntry
		_ = json.Unmarshal(e.raw, &ent)
		parts = append(parts, "branch", "summary", ent.Summary)
	case "session_info":
		var ent SessionInfoEntry
		_ = json.Unmarshal(e.raw, &ent)
		parts = append(parts, "title", ent.Name)
	case "model_change":
		var ent ModelChangeEntry
		_ = json.Unmarshal(e.raw, &ent)
		parts = append(parts, "model", ent.ModelID)
	case "thinking_level_change":
		var ent ThinkingLevelEntry
		_ = json.Unmarshal(e.raw, &ent)
		parts = append(parts, "thinking", ent.ThinkingLevel)
	case "custom":
		var ent CustomEntry
		_ = json.Unmarshal(e.raw, &ent)
		parts = append(parts, "custom", ent.CustomType)
	case "context_edit":
		mode, targetID := contextEditSummary(e)
		parts = append(parts, "context edit", mode, targetID)
	case "label":
		var ent LabelEntry
		_ = json.Unmarshal(e.raw, &ent)
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
	switch e.Base.Type {
	case "message":
		var message MessageEntry
		var ok bool
		if a.f != nil {
			message, ok = a.f.asMessage(e)
		} else {
			message, ok = e.AsMessage()
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
		if json.Unmarshal(e.raw, &entry) != nil {
			return nil
		}
		text = extractCustomMessageText(entry)
	case "compaction", "branch_summary":
		var entry struct {
			Summary string `json:"summary"`
		}
		if json.Unmarshal(e.raw, &entry) != nil {
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
func (m *InteractiveMode) runEditorSlotOAuthSelector(sel *tui.OAuthSelector) (string, bool) {
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
func (m *InteractiveMode) runEditorSlotLoginDialog(dlg *tui.LoginDialog, renderNotify <-chan struct{}) bool {
	if m.layout == nil {
		return false
	}
	m.editorContainer.SetChildren(dlg)
	m.tuiInst.Render()
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

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
		case buf := <-inputCh:
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
