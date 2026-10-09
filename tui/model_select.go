package tui

// ModelSelectorComponent implements the upstream scoped/all model picker state machine:
// current-model pinning, filtering, scope changes, wrapped arrow navigation,
// selection, and cancellation.

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// ModelScope discriminates the two views of the picker.
type ModelScope int

const (
	ModelScopeScoped ModelScope = iota
	ModelScopeAll
)

// ModelSelectorItem is one row in the picker.
type ModelSelectorItem struct {
	Provider string
	ID       string // bare model id (e.g. "gpt-4o")
	// Name is the raw model display name (upstream model.name), empty when
	// the source has none. Search text omits an empty name; the selected-model
	// footer falls back to ID.
	Name string
}

// FQ returns the "<provider>/<id>" form used for switch dispatch.
func (m ModelSelectorItem) FQ() string { return m.Provider + "/" + m.ID }

// ModelSelectorComponent renders a fuzzy-filtered list of models with a
// scope-toggle header.
type ModelSelectorComponent struct {
	Container
	Title string

	scoped       []ModelSelectorItem // session-scoped models, in configured order
	all          []ModelSelectorItem // all models with configured auth
	current      string              // current FQ spec; ✓-marked + sorted-first
	defaultModel string              // settings default FQ spec; "· default" badge + sorted second
	scope        ModelScope
	noAuth       bool // when true, scoped is empty and we show the warning

	searchInput   *TextInput
	scopeText     *Text // upstream scopeText; nil when there are no scoped models
	scopeHintText *Text
	listContainer *Container
	active        []ModelSelectorItem // = scoped or all per scope
	filtered      []int               // indices into active
	cursor        int                 // within filtered

	done              bool
	cancelled         bool
	selectedFQ        string
	selectedAsDefault bool
	errorText         string
	statusText        string
	statusSuccess     bool

	// refreshCancel stops the catalog refresh the selector started; Dispose calls it once.
	refreshCancel context.CancelFunc
	closed        bool

	// saveHint is the action hint row, which a picker without a "set as default" action drops.
	saveHint Component
	// noSaveDefault drops the app.models.save action and its hint (no onSelectAsDefault callback, model-selector.ts:139, 413).
	noSaveDefault bool
	// callbacks are the Pi constructor's models and callbacks; nil for the host-driven picker, which the driver polls.
	callbacks *modelSelectorCallbacks
}

// NewStaticModelSelectorComponent constructs the picker from item lists the caller owns; NewModelSelectorComponent builds on it. scoped contains the session's scoped models;
// allReachable contains all models with configured auth. current is the current
// provider-qualified model ID, or empty when none is selected.
// With no scoped models, the picker starts in all scope and shows the provider hint.
func NewStaticModelSelectorComponent(title string, scoped, allReachable []ModelSelectorItem, current string) *ModelSelectorComponent {
	ms := &ModelSelectorComponent{
		Title:       title,
		scoped:      slices.Clone(scoped),
		all:         sortModelItems(allReachable, current, ""),
		current:     current,
		noAuth:      len(scoped) == 0,
		searchInput: NewInput(InputOptions{}),

		listContainer: NewContainer(),
	}
	if len(scoped) > 0 {
		ms.scope = ModelScopeScoped
	} else {
		ms.scope = ModelScopeAll
	}
	// upstream: model-selector.ts constructor children.
	ms.Add(NewDynamicBorder())
	ms.Add(NewSpacer(1))
	if !ms.noAuth {
		ms.scopeText = NewPaddedText("", 0, 0, nil)
		ms.scopeHintText = NewPaddedText("", 0, 0, nil)
		ms.Add(ms.scopeText)
		ms.Add(ms.scopeHintText)
	} else {
		ms.Add(NewPaddedText(ms.warningLine(), 0, 0, nil))
	}
	ms.Add(NewSpacer(1))
	// model-selector.ts: Enter on the search Input (tui.input.submit, or LF when tui.select.confirm is rebound) selects the highlighted model.
	ms.searchInput.Focused = true
	ms.searchInput.OnSubmit = func(string) { ms.selectHighlighted() }
	// The terminal cursor is managed outside the captured text, so the search input does not embed a block cursor glyph.
	ms.Add(ms.searchInput)
	ms.Add(NewSpacer(1))
	ms.Add(ms.listContainer)
	ms.Add(NewSpacer(1))
	ms.saveHint = NewPaddedText(fg(ActiveTheme().Dim, "  "+ActionKeyDisplayText(KBSelectConfirm)+" to select · "+ActionKeyDisplayTextOr("app.models.save", "ctrl+s")+" to set as default · "+ActionKeyDisplayText(KBSelectCancel)+" to cancel"), 0, 0, nil)
	ms.Add(ms.saveHint)
	ms.Add(NewDynamicBorder())
	ms.refreshActive()
	return ms
}

// sortModelItems puts the current model first and the default model second,
// then stable-sorts by provider. Models within one provider preserve the
// caller's order. Mirrors upstream's sortModels() (model-selector.ts), which
// sorts the all-models list only; scoped models keep their configured order.
func sortModelItems(items []ModelSelectorItem, current, defaultModel string) []ModelSelectorItem {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b ModelSelectorItem) int {
		aCurrent := a.FQ() == current
		bCurrent := b.FQ() == current
		if aCurrent != bCurrent {
			if aCurrent {
				return -1
			}
			return 1
		}
		aDefault := defaultModel != "" && a.FQ() == defaultModel
		bDefault := defaultModel != "" && b.FQ() == defaultModel
		if aDefault != bDefault {
			if aDefault {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Provider, b.Provider)
	})
	return out
}

// SetDefaultModel marks the settings default model ("<provider>/<id>"), which
// upstream badges with "· default", sorts after the current model, and
// matches for a "default" search.
func (m *ModelSelectorComponent) SetDefaultModel(fq string) {
	m.defaultModel = fq
	m.all = sortModelItems(m.all, m.current, fq)
	m.refreshActive()
	m.Invalidate()
}

// UpdateModels replaces the available snapshot and refreshes scoped model metadata without dropping unavailable scoped entries. It retains the scope and query, reanchors on the current model (or clamps the cursor), then selects the best match when a query is active, like upstream loadModelsFromSnapshot followed by filterModels.
func (m *ModelSelectorComponent) UpdateModels(models []ModelSelectorItem) {
	if m.done {
		return
	}
	m.all = sortModelItems(models, m.current, m.defaultModel)
	byID := make(map[string]ModelSelectorItem, len(models))
	for _, item := range models {
		byID[item.FQ()] = item
	}
	for i, item := range m.scoped {
		if refreshed, ok := byID[item.FQ()]; ok {
			m.scoped[i] = refreshed
		}
	}
	if m.scope == ModelScopeScoped {
		m.active = m.scoped
	} else {
		m.active = m.all
	}
	currentIndex := slices.IndexFunc(m.active, func(item ModelSelectorItem) bool { return item.FQ() == m.current })
	if currentIndex >= 0 {
		m.cursor = currentIndex
	} else {
		m.cursor = min(m.cursor, max(0, len(m.active)-1))
	}
	m.applyFilter()
	m.Invalidate()
}

func (m *ModelSelectorComponent) isDefaultModel(item ModelSelectorItem) bool {
	return m.defaultModel != "" && item.FQ() == m.defaultModel
}

// isDefaultSearch mirrors upstream isDefaultSearch: a non-empty prefix of "default".
func isDefaultSearch(query string) bool {
	normalized := strings.ToLower(strings.TrimSpace(query))
	const defaultWord = "default"
	return normalized != "" && strings.HasPrefix(defaultWord, normalized)
}

func (m *ModelSelectorComponent) refreshActive() {
	if m.scope == ModelScopeScoped {
		m.active = m.scoped
	} else {
		m.active = m.all
	}
	// Try to preserve current selection in new view.
	// Best-effort: the prior m.active may be different; re-anchored on current when applyFilter runs.
	m.applyFilter()
	// Pin cursor to the current model if it's in the new filtered view.
	if m.current != "" {
		for vi, idx := range m.filtered {
			if m.active[idx].FQ() == m.current {
				m.cursor = vi
				break
			}
		}
	}
	m.updateList()
}

// scopeLine returns the upstream "Scope: all | scoped" header with the active
// scope accented and the inactive scope muted.
func (m *ModelSelectorComponent) scopeLine() string {
	th := ActiveTheme()
	allTxt, scopedTxt := fg(th.Muted, "all"), fg(th.Accent, "scoped")
	if m.scope == ModelScopeAll {
		allTxt, scopedTxt = fg(th.Accent, "all"), fg(th.Muted, "scoped")
	}
	return fg(th.Muted, "Scope: ") + allTxt + fg(th.Muted, " | ") + scopedTxt
}

// hintLine is the second header row. Mirrors upstream getScopeHintText
// (model-selector.ts): keyHint("tui.input.tab", "scope") + " (all/scoped)".
func (m *ModelSelectorComponent) hintLine() string {
	key := FormatKeyText(strings.Join(GetTUIKeybindings().GetKeys(KBInputTab), "/"), false)
	return KeyHint(key, "scope") + fg(ActiveTheme().Muted, " (all/scoped)")
}

// warningLine is the header shown instead of the scope rows when no scoped
// models exist. Mirrors upstream model-selector.ts constructor hint text.
func (m *ModelSelectorComponent) warningLine() string {
	return fg(ActiveTheme().Warning, "Only showing models from configured providers. Use /login to add providers.")
}

// Done / Cancelled / SelectedFQ: modal contract.
func (m *ModelSelectorComponent) Done() bool         { return m.done }
func (m *ModelSelectorComponent) Cancelled() bool    { return m.cancelled }
func (m *ModelSelectorComponent) SelectedFQ() string { return m.selectedFQ }

// SelectedAsDefault reports whether the selection requested persistence via app.models.save.
func (m *ModelSelectorComponent) SelectedAsDefault() bool { return m.selectedAsDefault }

func (m *ModelSelectorComponent) SetFilter(query string) {
	m.searchInput.SetText(query)
	m.applyFilter()
}
func (m *ModelSelectorComponent) SetError(text string) {
	m.errorText = text
	m.statusText = ""
	m.updateList()
}
func (m *ModelSelectorComponent) SetStatus(text string) {
	m.statusText = text
	m.statusSuccess = false
	m.errorText = ""
	m.updateList()
}

// SetRefreshSuccess shows the upstream "Model catalogs refreshed." status in
// the success color.
func (m *ModelSelectorComponent) SetRefreshSuccess(text string) {
	m.SetStatus(text)
	m.statusSuccess = true
	m.updateList()
}

// updateList rebuilds the scope header and the list container (model-selector.ts updateList): the model rows, the scroll position, the error or selected-model name and the refresh status.
func (m *ModelSelectorComponent) updateList() {
	if m.scopeText != nil {
		m.scopeText.SetText(m.scopeLine())
		m.scopeHintText.SetText(m.hintLine())
	}
	th := ActiveTheme()
	m.listContainer.Clear()
	text := func(content string) { m.listContainer.Add(NewPaddedText(content, 0, 0, nil)) }
	spacer := func() { m.listContainer.Add(NewSpacer(1)) }
	// Visible window: upstream maxVisible=10 with the cursor centered.
	const maxVisible = 10
	count := len(m.filtered)
	startIdx := max(0, min(m.cursor-maxVisible/2, count-maxVisible))
	endIdx := min(startIdx+maxVisible, count)
	for i := startIdx; i < endIdx; i++ {
		item := m.active[m.filtered[i]]
		cursor, modelText := "  ", item.ID
		if i == m.cursor {
			cursor, modelText = fg(th.Accent, "→ "), fg(th.Accent, item.ID)
		}
		currentMarker := "  "
		if item.FQ() == m.current {
			currentMarker = fg(th.Accent, "✓ ")
		}
		defaultBadge := ""
		if m.isDefaultModel(item) {
			defaultBadge = fg(th.Muted, " · default")
		}
		text(cursor + currentMarker + modelText + " " + fg(th.Muted, "["+item.Provider+"]") + defaultBadge)
	}
	if startIdx > 0 || endIdx < count {
		text(fg(th.Muted, fmt.Sprintf("  (%d/%d)", m.cursor+1, count)))
	}
	switch {
	case m.errorText != "":
		for line := range strings.SplitSeq(m.errorText, "\n") {
			text(fg(th.Error, line))
		}
	case count == 0:
		text(fg(th.Muted, "  No matching models"))
	default:
		spacer()
		sel := m.active[m.filtered[m.cursor]]
		name := sel.Name
		if name == "" {
			name = sel.ID
		}
		text(fg(th.Muted, "  Model Name: "+name))
	}
	if m.statusText != "" {
		spacer()
		color := th.Muted
		if m.statusSuccess {
			color = th.Success
		}
		text(fg(color, "  "+m.statusText))
	}
}

func (m *ModelSelectorComponent) selectHighlighted() {
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		m.selectedFQ = m.active[m.filtered[m.cursor]].FQ()
		m.Dispose()
		m.done = true
		m.callbacks.selected(m.selectedFQ, false)
	}
}

// SetRefreshCancel registers the cancel function of the catalog refresh this selector started (model-selector.ts refreshAbortController and refreshTimeout).
func (m *ModelSelectorComponent) SetRefreshCancel(cancel context.CancelFunc) {
	m.refreshCancel = cancel
}

// Dispose stops the catalog refresh. The first call cancels it; later calls do nothing (model-selector.ts:224-229).
func (m *ModelSelectorComponent) Dispose() {
	if m.closed {
		return
	}
	m.closed = true
	if m.refreshCancel != nil {
		m.refreshCancel()
	}
}

// HandleInput mirrors upstream model-selector.ts handleInput: it checks tui.input.tab, tui.select.up, tui.select.down, tui.select.confirm, tui.select.cancel and app.models.save in that order, and passes every other key to the search input before it reapplies the query.
func (m *ModelSelectorComponent) HandleInput(data string) {
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBInputTab):
		if len(m.scoped) > 0 {
			if m.scope == ModelScopeAll {
				m.setScope(ModelScopeScoped)
			} else {
				m.setScope(ModelScopeAll)
			}
		}
	case kb.Matches(data, KBSelectUp):
		m.moveCursor(-1, true)
	case kb.Matches(data, KBSelectDown):
		m.moveCursor(1, true)
	case kb.Matches(data, KBSelectConfirm):
		m.selectHighlighted()
	case kb.Matches(data, KBSelectCancel):
		m.Dispose()
		m.cancelled = true
		m.done = true
		m.callbacks.cancel()
	case !m.noSaveDefault && scopedModelsActionMatches(kb, data, "app.models.save", "ctrl+s"):
		if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
			m.selectedFQ = m.active[m.filtered[m.cursor]].FQ()
			m.selectedAsDefault = true
			m.Dispose()
			m.done = true
			m.callbacks.selected(m.selectedFQ, true)
		}
	default:
		m.searchInput.HandleInput(data)
		m.applyFilter()
	}
	m.updateList()
	m.Invalidate()
}

// setScope mirrors upstream setScope: it selects the current model in the new scope's unfiltered list, or the first row, and then reapplies the query.
func (m *ModelSelectorComponent) setScope(scope ModelScope) {
	if m.scope == scope {
		return
	}
	m.scope = scope
	if scope == ModelScopeScoped {
		m.active = m.scoped
	} else {
		m.active = m.all
	}
	m.cursor = max(0, slices.IndexFunc(m.active, func(item ModelSelectorItem) bool { return item.FQ() == m.current }))
	m.applyFilter()
}

func (m *ModelSelectorComponent) applyFilter() {
	indices := make([]int, len(m.active))
	for i := range m.active {
		indices[i] = i
	}
	query := m.searchInput.Text()
	m.filtered = FuzzyFilter(indices, query, func(index int) string {
		item := m.active[index]
		defaultText := ""
		if m.isDefaultModel(item) {
			defaultText = " default"
		}
		return GetModelSelectorSearchText(ModelSearchItem{ID: item.ID, Provider: item.Provider, Name: item.Name}) + defaultText
	})
	if isDefaultSearch(query) {
		// Upstream lists the default model first for a "default" prefix query.
		defaults := []int{}
		for index, item := range m.active {
			if m.isDefaultModel(item) {
				defaults = append(defaults, index)
			}
		}
		rest := slices.DeleteFunc(m.filtered, func(index int) bool { return m.isDefaultModel(m.active[index]) })
		m.filtered = slices.Concat(defaults, rest)
	}
	if query != "" {
		m.cursor = 0
	} else if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.updateList()
}

func (m *ModelSelectorComponent) moveCursor(delta int, wrap bool) {
	count := len(m.filtered)
	if count == 0 {
		return
	}
	if wrap {
		m.cursor = (m.cursor + delta + count) % count
	} else {
		m.cursor = max(0, min(m.cursor+delta, count-1))
	}
	m.updateList()
}

// modelScopeTabs are the scope line's views in the order tui.input.tab
// toggles them.
var modelScopeTabs = []frontend.SelectorTab{{ID: "all", Label: "all"}, {ID: "scoped", Label: "scoped"}}

// NativeNode reports the picker as a selector whose item ids are the models'
// provider/id. pig additive (D91): the scope line becomes the all/scoped tabs
// and the no-auth warning the description.
func (m *ModelSelectorComponent) NativeNode() (frontend.Node, bool) {
	node := frontend.Selector{
		Title:      plainText(m.Title),
		Searchable: true,
		Query:      m.searchInput.Text(),
		Items:      make([]frontend.SelectorItem, len(m.filtered)),
		Status:     cmp.Or(m.errorText, m.statusText),
	}
	if m.noAuth {
		node.Description = plainText(m.warningLine())
	} else {
		node.Tabs, node.Tab = modelScopeTabs, "scoped"
		if m.scope == ModelScopeAll {
			node.Tab = "all"
		}
	}
	for i, index := range m.filtered {
		item := m.active[index]
		detail := item.Provider
		if m.isDefaultModel(item) {
			detail += " · default"
		}
		fq := item.FQ()
		node.Items[i] = frontend.SelectorItem{ID: fq, Label: item.ID, Detail: detail, Checked: fq == m.current}
	}
	if m.cursor >= 0 && m.cursor < len(node.Items) {
		node.Selected = node.Items[m.cursor].ID
	}
	return node, true
}

// Scope returns the current scope (exposed for tests).
func (m *ModelSelectorComponent) Scope() ModelScope { return m.scope }

// VisibleCount returns the number of items in the filtered view.
func (m *ModelSelectorComponent) VisibleCount() int { return len(m.filtered) }

// GetSearchInput returns the search input (model-selector.ts:getSearchInput).
func (m *ModelSelectorComponent) GetSearchInput() *TextInput { return m.searchInput }

// SetFocused propagates TUI focus to the search input, which emits the hardware-cursor marker only while focused, so the IME candidate window follows the query (upstream `set focused`, model-selector.ts:44-51).
func (m *ModelSelectorComponent) SetFocused(focused bool) { m.searchInput.SetFocused(focused) }

// Focused reports whether the search input holds the TUI focus (upstream `get focused`).
func (m *ModelSelectorComponent) Focused() bool { return m.searchInput.Focused }
