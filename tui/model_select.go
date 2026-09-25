package tui

// ModelSelector implements the upstream scoped/all model picker state machine:
// current-model pinning, filtering, scope changes, wrapped arrow navigation,
// selection, and cancellation.

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
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

// ModelSelector renders a fuzzy-filtered list of models with a
// scope-toggle header.
type ModelSelector struct {
	invalidatable
	Title string

	scoped       []ModelSelectorItem // session-scoped models, in configured order
	all          []ModelSelectorItem // all models with configured auth
	current      string              // current FQ spec; ✓-marked + sorted-first
	defaultModel string              // settings default FQ spec; "· default" badge + sorted second
	scope        ModelScope
	noAuth       bool // when true, scoped is empty and we show the warning

	searchInput *TextInput
	active      []ModelSelectorItem // = scoped or all per scope
	filtered    []int               // indices into active
	cursor      int                 // within filtered

	done              bool
	cancelled         bool
	selectedFQ        string
	selectedAsDefault bool
	errorText         string
	statusText        string
	statusSuccess     bool
}

// NewModelSelector constructs a picker. scoped contains the session's scoped models;
// allReachable contains all models with configured auth. current is the current
// provider-qualified model ID, or empty when none is selected.
// With no scoped models, the picker starts in all scope and shows the provider hint.
func NewModelSelector(title string, scoped, allReachable []ModelSelectorItem, current string) *ModelSelector {
	ms := &ModelSelector{
		Title:       title,
		scoped:      slices.Clone(scoped),
		all:         sortModelItems(allReachable, current, ""),
		current:     current,
		noAuth:      len(scoped) == 0,
		searchInput: NewTextInput(""),
	}
	if len(scoped) > 0 {
		ms.scope = ModelScopeScoped
	} else {
		ms.scope = ModelScopeAll
	}
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
func (m *ModelSelector) SetDefaultModel(fq string) {
	m.defaultModel = fq
	m.all = sortModelItems(m.all, m.current, fq)
	m.refreshActive()
	m.Invalidate()
}

// UpdateModels replaces the available snapshot and refreshes scoped model metadata without dropping unavailable scoped entries. It retains the scope and query, reanchors on the current model (or clamps the cursor), then selects the best match when a query is active, like upstream loadModelsFromSnapshot followed by filterModels.
func (m *ModelSelector) UpdateModels(models []ModelSelectorItem) {
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

func (m *ModelSelector) isDefaultModel(item ModelSelectorItem) bool {
	return m.defaultModel != "" && item.FQ() == m.defaultModel
}

// isDefaultSearch mirrors upstream isDefaultSearch: a non-empty prefix of "default".
func isDefaultSearch(query string) bool {
	normalized := strings.ToLower(strings.TrimSpace(query))
	const defaultWord = "default"
	return normalized != "" && strings.HasPrefix(defaultWord, normalized)
}

func (m *ModelSelector) refreshActive() {
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
}

// scopeLine returns the upstream "Scope: all | scoped" header with the active
// scope accented and the inactive scope muted.
func (m *ModelSelector) scopeLine() string {
	th := ActiveTheme()
	allTxt, scopedTxt := fg(th.Muted, "all"), fg(th.Accent, "scoped")
	if m.scope == ModelScopeAll {
		allTxt, scopedTxt = fg(th.Accent, "all"), fg(th.Muted, "scoped")
	}
	return fg(th.Muted, "Scope: ") + allTxt + fg(th.Muted, " | ") + scopedTxt
}

// hintLine is the second header row. Mirrors upstream getScopeHintText
// (model-selector.ts): keyHint("tui.input.tab", "scope") + " (all/scoped)".
func (m *ModelSelector) hintLine() string {
	key := FormatKeyText(strings.Join(GetTUIKeybindings().GetKeys(KBInputTab), "/"), false)
	return KeyHint(key, "scope") + fg(ActiveTheme().Muted, " (all/scoped)")
}

// warningLine is the header shown instead of the scope rows when no scoped
// models exist. Mirrors upstream model-selector.ts constructor hint text.
func (m *ModelSelector) warningLine() string {
	return fg(ActiveTheme().Warning, "Only showing models from configured providers. Use /login to add providers.")
}

// Done / Cancelled / SelectedFQ: modal contract.
func (m *ModelSelector) Done() bool         { return m.done }
func (m *ModelSelector) Cancelled() bool    { return m.cancelled }
func (m *ModelSelector) SelectedFQ() string { return m.selectedFQ }

// SelectedAsDefault reports whether the selection requested persistence via app.models.save.
func (m *ModelSelector) SelectedAsDefault() bool { return m.selectedAsDefault }

func (m *ModelSelector) SetFilter(query string) {
	m.searchInput.SetText(query)
	m.applyFilter()
}
func (m *ModelSelector) SetError(text string) {
	m.errorText = text
	m.statusText = ""
}
func (m *ModelSelector) SetStatus(text string) {
	m.statusText = text
	m.statusSuccess = false
	m.errorText = ""
}

// SetRefreshSuccess shows the upstream "Model catalogs refreshed." status in
// the success color.
func (m *ModelSelector) SetRefreshSuccess(text string) {
	m.SetStatus(text)
	m.statusSuccess = true
}

// Render lays out the upstream selector rows (model-selector.ts): top border,
// scope and hint (or the no-auth warning), search input, model list, scroll
// position, selected-model name, refresh status, selection hint, and bottom border. Every text
// row is a Text(..., 0, 0), so long rows wrap to the width.
func (m *ModelSelector) Render(width int) []string {
	th := ActiveTheme()
	border := NewDynamicBorder("")
	out := append([]string{}, border.Render(width)...)
	text := func(content string) { out = append(out, NewPaddedText(content, 0, 0, nil).Render(width)...) }
	spacer := func() { out = append(out, padOrTrunc("", width)) }
	spacer()
	if !m.noAuth {
		text(m.scopeLine())
		text(m.hintLine())
	} else {
		text(m.warningLine())
	}
	spacer()
	// The terminal cursor is managed outside the captured text, so the
	// search input does not embed a block cursor glyph here.
	out = append(out, m.searchInput.Render(width)...)
	spacer()

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
	spacer()
	text(fg(th.Dim, "  "+ActionKeyDisplayText(KBSelectConfirm)+" to select · "+ActionKeyDisplayTextOr("app.models.save", "ctrl+s")+" to set as default · "+ActionKeyDisplayText(KBSelectCancel)+" to cancel"))
	return append(out, border.Render(width)...)
}

func (m *ModelSelector) HandleInput(data string) {
	// Route through TUI keybinding registry. Mirrors upstream
	// model-select.ts handleInput dispatch.
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectCancel):
		m.cancelled = true
		m.done = true
	case kb.Matches(data, KBSelectConfirm):
		if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
			item := m.active[m.filtered[m.cursor]]
			m.selectedFQ = item.FQ()
			m.done = true
		}
	case data == "\t" || kb.Matches(data, KBInputTab) || data == "\x1b[Z": // Tab / Shift+Tab: toggle scope
		// Only toggle if there's a meaningful difference between the
		// two lists (mirrors upstream's `if scopedModelItems.length > 0`
		// guard).
		if len(m.scoped) > 0 {
			if m.scope == ModelScopeAll {
				m.scope = ModelScopeScoped
			} else {
				m.scope = ModelScopeAll
			}
			m.refreshActive()
		}
	case kb.Matches(data, KBSelectUp):
		m.moveCursor(-1, true)
	case kb.Matches(data, KBSelectDown):
		m.moveCursor(1, true)
	case kb.Matches(data, KBSelectPageUp):
		m.moveCursor(-10, false)
	case kb.Matches(data, KBSelectPageDown):
		m.moveCursor(10, false)
	case scopedModelsActionMatches(kb, data, "app.models.save", "ctrl+s"):
		if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
			m.selectedFQ = m.active[m.filtered[m.cursor]].FQ()
			m.selectedAsDefault = true
			m.done = true
		}
	default:
		before := m.searchInput.Text()
		m.searchInput.HandleInput(data)
		if m.searchInput.Text() != before {
			m.applyFilter()
		}
	}
	m.Invalidate()
}

func (m *ModelSelector) applyFilter() {
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
}

func (m *ModelSelector) moveCursor(delta int, wrap bool) {
	count := len(m.filtered)
	if count == 0 {
		return
	}
	if wrap {
		m.cursor = (m.cursor + delta + count) % count
	} else {
		m.cursor = max(0, min(m.cursor+delta, count-1))
	}
}

// Scope returns the current scope (exposed for tests).
func (m *ModelSelector) Scope() ModelScope { return m.scope }

// VisibleCount returns the number of items in the filtered view.
func (m *ModelSelector) VisibleCount() int { return len(m.filtered) }
