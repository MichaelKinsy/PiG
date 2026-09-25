package tui

// ScopedModelsList: multi-select model toggle list for /scoped-models.
//
// Mirrors upstream scoped-models-selector.ts. Lets users enable/disable
// models for Ctrl+P cycling. enabledIDs == nil means all models are
// enabled (no filter).

import (
	"fmt"
	"slices"
	"strings"
)

// ModelItem describes one model in the scoped-models list.
type ModelItem struct {
	FullID   string // "provider/model-id"
	Name     string // display name
	Provider string
}

// ScopedModelsConfig configures the scoped-models list.
type ScopedModelsConfig struct {
	AllModels       []ModelItem
	EnabledModelIDs []string // nil = all enabled
	RefreshStatus   string
}

// RefreshStatusKind controls the refresh message emphasis.
type RefreshStatusKind string

const (
	RefreshStatusMuted   RefreshStatusKind = "muted"
	RefreshStatusSuccess RefreshStatusKind = "success"
	RefreshStatusWarning RefreshStatusKind = "warning"
)

// ScopedModelsResult describes the outcome after Done()==true.
type ScopedModelsResult struct {
	EnabledIDs []string // nil = all enabled
	Persisted  bool     // retained for compatibility; Ctrl+S no longer closes the selector
	Cancelled  bool
}

// ScopedModelsList is the /scoped-models selector component.
type ScopedModelsList struct {
	invalidatable

	allIDs     []string // ordered list of all model IDs
	models     map[string]ModelItem
	enabledIDs []string // nil = all enabled

	// View state.
	filteredItems []scopedModelEntry
	cursor        int
	scroll        int
	searchInput   *TextInput
	maxVisible    int
	dirty         bool // unsaved changes

	// Result.
	done   bool
	result ScopedModelsResult

	pendingSave    bool
	pendingSaveIDs []string
	refreshStatus  string
	refreshKind    RefreshStatusKind
}

type scopedModelEntry struct {
	fullID  string
	enabled bool
}

func scopedModelsActionMatches(kb *TUIKeybindingsManager, data, action, defaultKey string) bool {
	if kb.HasBinding(action) {
		return kb.Matches(data, action)
	}
	return matchesKeyID(data, defaultKey)
}

// NewScopedModelsList creates a new scoped-models selector.
func NewScopedModelsList(cfg ScopedModelsConfig) *ScopedModelsList {
	s := &ScopedModelsList{
		models:        make(map[string]ModelItem, len(cfg.AllModels)),
		maxVisible:    8,
		searchInput:   NewTextInput(""),
		refreshStatus: cfg.RefreshStatus,
		refreshKind:   RefreshStatusMuted,
	}
	for _, m := range cfg.AllModels {
		s.allIDs = append(s.allIDs, m.FullID)
		s.models[m.FullID] = m
	}
	if cfg.EnabledModelIDs != nil {
		s.enabledIDs = slices.Clone(cfg.EnabledModelIDs)
	}
	s.refresh()
	return s
}

// Done reports whether the user has confirmed, persisted, or cancelled.
func (s *ScopedModelsList) Done() bool { return s.done }

// Result returns the outcome after Done()==true.
func (s *ScopedModelsList) Result() ScopedModelsResult { return s.result }

// EnabledIDs returns the current enabled model IDs (nil = all).
func (s *ScopedModelsList) EnabledIDs() []string {
	if s.enabledIDs == nil {
		return nil
	}
	return slices.Clone(s.enabledIDs)
}

// SetRefreshStatus replaces the catalog refresh message rendered above the footer.
func (s *ScopedModelsList) SetRefreshStatus(message string, kind RefreshStatusKind) {
	s.refreshStatus = message
	s.refreshKind = kind
	s.Invalidate()
}

// UpdateModels publishes a refreshed catalog while preserving the selected model.
// Supplying enabledIDs also replaces the selection; an explicit nil means all.
func (s *ScopedModelsList) UpdateModels(models []ModelItem, enabledIDs ...[]string) {
	selectedID := ""
	if s.cursor >= 0 && s.cursor < len(s.filteredItems) {
		selectedID = s.filteredItems[s.cursor].fullID
	}
	if len(enabledIDs) > 0 {
		s.enabledIDs = slices.Clone(enabledIDs[0])
	}
	s.allIDs = s.allIDs[:0]
	clear(s.models)
	for _, model := range models {
		s.allIDs = append(s.allIDs, model.FullID)
		s.models[model.FullID] = model
	}
	s.refresh()
	if selectedID != "" {
		if index := slices.IndexFunc(s.filteredItems, func(item scopedModelEntry) bool {
			return item.fullID == selectedID
		}); index >= 0 {
			s.cursor = index
			s.fixScroll()
		}
	}
	s.Invalidate()
}

// ConsumeSave returns the most recent Ctrl+S save request and clears it.
func (s *ScopedModelsList) ConsumeSave() ([]string, bool) {
	if !s.pendingSave {
		return nil, false
	}
	s.pendingSave = false
	ids := slices.Clone(s.pendingSaveIDs)
	s.pendingSaveIDs = nil
	return ids, true
}

// --- Toggle logic (mirrors upstream scoped-models-selector.ts) ---

func (s *ScopedModelsList) isEnabled(id string) bool {
	return s.enabledIDs == nil || slices.Contains(s.enabledIDs, id)
}

// normalizeEnabled collapses an explicit list back to nil (= all enabled)
// when it covers every available model.
func (s *ScopedModelsList) normalizeEnabled(result []string) []string {
	if len(result) == len(s.allIDs) && !slices.ContainsFunc(result, func(id string) bool {
		return !slices.Contains(s.allIDs, id)
	}) {
		return nil
	}
	return result
}

func (s *ScopedModelsList) toggle(id string) {
	if s.enabledIDs == nil {
		// Toggling from "all enabled" disables only this model (0.87.1).
		s.enabledIDs = slices.DeleteFunc(slices.Clone(s.allIDs), func(modelID string) bool { return modelID == id })
		return
	}
	if idx := slices.Index(s.enabledIDs, id); idx >= 0 {
		s.enabledIDs = slices.Delete(s.enabledIDs, idx, idx+1)
	} else {
		s.enabledIDs = s.normalizeEnabled(append(s.enabledIDs, id))
	}
}

func (s *ScopedModelsList) enableAll(targetIDs []string) {
	if s.enabledIDs == nil {
		return // already all enabled
	}
	targets := targetIDs
	if targets == nil {
		targets = s.allIDs
	}
	for _, id := range targets {
		if !slices.Contains(s.enabledIDs, id) {
			s.enabledIDs = append(s.enabledIDs, id)
		}
	}
	s.enabledIDs = s.normalizeEnabled(s.enabledIDs)
}

func (s *ScopedModelsList) clearAll(targetIDs []string) {
	if s.enabledIDs == nil {
		if targetIDs != nil {
			// Switch from "all" to "all except targets".
			s.enabledIDs = nil
			var keep []string
			for _, id := range s.allIDs {
				if !slices.Contains(targetIDs, id) {
					keep = append(keep, id)
				}
			}
			s.enabledIDs = keep
		} else {
			s.enabledIDs = []string{}
		}
		return
	}
	targets := targetIDs
	if targets == nil {
		s.enabledIDs = []string{}
		return
	}
	s.enabledIDs = slices.DeleteFunc(s.enabledIDs, func(id string) bool {
		return slices.Contains(targets, id)
	})
}

func (s *ScopedModelsList) move(id string, delta int) {
	if s.enabledIDs == nil {
		return
	}
	idx := slices.Index(s.enabledIDs, id)
	if idx < 0 {
		return
	}
	newIdx := idx + delta
	if newIdx < 0 || newIdx >= len(s.enabledIDs) {
		return
	}
	s.enabledIDs[idx], s.enabledIDs[newIdx] = s.enabledIDs[newIdx], s.enabledIDs[idx]
}

// getSortedIDs returns IDs ordered: enabled first (in order), then disabled.
func (s *ScopedModelsList) getSortedIDs() []string {
	if s.enabledIDs == nil {
		return s.allIDs
	}
	enabledSet := make(map[string]bool, len(s.enabledIDs))
	for _, id := range s.enabledIDs {
		enabledSet[id] = true
	}
	result := slices.Clone(s.enabledIDs)
	for _, id := range s.allIDs {
		if !enabledSet[id] {
			result = append(result, id)
		}
	}
	return result
}

func (s *ScopedModelsList) refresh() {
	sorted := s.getSortedIDs()
	items := make([]scopedModelEntry, 0, len(sorted))
	for _, id := range sorted {
		items = append(items, scopedModelEntry{
			fullID:  id,
			enabled: s.isEnabled(id),
		})
	}
	s.filteredItems = FuzzyFilter(items, s.searchInput.Text(), func(item scopedModelEntry) string {
		model, ok := s.models[item.fullID]
		if !ok {
			return item.fullID
		}
		// Upstream searches item.model.id (the bare id); FullID carries it
		// either bare or provider-qualified depending on the caller.
		id := strings.TrimPrefix(model.FullID, model.Provider+"/")
		return GetModelSearchText(ModelSearchItem{ID: id, Provider: model.Provider, Name: model.Name})
	})
	s.cursor = min(s.cursor, max(0, len(s.filteredItems)-1))
	s.fixScroll()
}

func (s *ScopedModelsList) fixScroll() {
	if len(s.filteredItems) <= s.maxVisible {
		s.scroll = 0
		return
	}
	// Keep cursor centered-ish.
	half := s.maxVisible / 2
	ideal := s.cursor - half
	maxScroll := len(s.filteredItems) - s.maxVisible
	s.scroll = max(0, min(ideal, maxScroll))
}

// Render draws the component. Mirrors upstream ScopedModelsSelectorComponent:
// every text row is a Text(..., 0, 0) between two DynamicBorders.
func (s *ScopedModelsList) Render(width int) []string {
	th := ActiveTheme()
	border := NewDynamicBorder("")
	lines := append([]string{}, border.Render(width)...)
	text := func(content string) { lines = append(lines, NewPaddedText(content, 0, 0, nil).Render(width)...) }
	spacer := func() { lines = append(lines, padOrTrunc("", width)) }

	spacer()
	text(fg(th.Accent, "\x1b[1mModel Configuration"+SGRBoldDimReset))
	text(fg(th.Muted, "Session-only. "+ActionKeyDisplayTextOr("app.models.save", "ctrl+s")+" to save to settings."))
	spacer()
	lines = append(lines, s.searchInput.Render(width)...)
	spacer()

	if len(s.filteredItems) == 0 {
		text(fg(th.Muted, "  No matching models"))
	} else {
		end := min(s.scroll+s.maxVisible, len(s.filteredItems))
		for i := s.scroll; i < end; i++ {
			text(s.renderRow(i))
		}
		if s.scroll > 0 || end < len(s.filteredItems) {
			text(fg(th.Muted, fmt.Sprintf("  (%d/%d)", s.cursor+1, len(s.filteredItems))))
		}
		spacer()
		if m, available := s.models[s.filteredItems[s.cursor].fullID]; available {
			text(fg(th.Muted, "  Model Name: "+m.Name))
		} else {
			text(fg(th.Muted, "  Model unavailable"))
		}
	}

	spacer()
	if s.refreshStatus != "" {
		color := th.Muted
		switch s.refreshKind {
		case RefreshStatusSuccess:
			color = th.Success
		case RefreshStatusWarning:
			color = th.Warning
		}
		text(fg(color, "  "+s.refreshStatus))
	}
	footer := fg(th.Dim, "  "+strings.Join(s.footerParts(), " · "))
	if s.dirty {
		footer = fg(th.Dim, "  "+strings.Join(s.footerParts(), " · ")+" ") + fg(th.Warning, "(unsaved)")
	}
	text(footer)
	return append(lines, border.Render(width)...)
}

// renderRow draws one model row: cursor, the enabled "✓ " column, the model
// id (struck through when unavailable), and the provider badge.
func (s *ScopedModelsList) renderRow(i int) string {
	th := ActiveTheme()
	item := s.filteredItems[i]
	m, available := s.models[item.fullID]
	prefix := "  "
	id := item.fullID
	badge := fg(th.Muted, " [unavailable]")
	status := "  "
	if available {
		id = strings.TrimPrefix(m.FullID, m.Provider+"/")
		badge = fg(th.Muted, " ["+m.Provider+"]")
		if item.enabled {
			status = fg(th.Accent, "✓ ")
		}
	} else {
		id = "\x1b[9m" + id + SGRStrikeReset
	}
	if i == s.cursor {
		prefix = fg(th.Accent, "→ ")
		id = fg(th.Accent, id)
	}
	return prefix + status + id + badge
}

// footerParts mirrors upstream getFooterText's key hints and enabled count.
func (s *ScopedModelsList) footerParts() []string {
	countText := "all enabled"
	if s.enabledIDs != nil {
		enabledCount, unavailableCount := 0, 0
		for _, id := range s.enabledIDs {
			if _, ok := s.models[id]; ok {
				enabledCount++
			} else {
				unavailableCount++
			}
		}
		countText = fmt.Sprintf("%d/%d enabled", enabledCount, len(s.allIDs))
		if unavailableCount > 0 {
			countText += fmt.Sprintf(" · %d unavailable", unavailableCount)
		}
	}
	return []string{
		ActionKeyDisplayText(KBSelectConfirm) + " toggle",
		ActionKeyDisplayTextOr("app.models.enableAll", "ctrl+a") + " all",
		ActionKeyDisplayTextOr("app.models.clearAll", "ctrl+x") + " clear",
		ActionKeyDisplayTextOr("app.models.toggleProvider", "ctrl+p") + " provider",
		ActionKeyDisplayTextOr("app.models.reorderUp", "alt+up") + "/" + ActionKeyDisplayTextOr("app.models.reorderDown", "alt+down") + " reorder",
		ActionKeyDisplayTextOr("app.models.save", "ctrl+s") + " save",
		countText,
	}
}

// HandleInput processes a keystroke.
func (s *ScopedModelsList) HandleInput(data string) {
	if s.done {
		return
	}

	kb := GetTUIKeybindings()
	if scopedModelsActionMatches(kb, data, "app.models.save", "ctrl+s") {
		s.pendingSaveIDs = s.EnabledIDs()
		s.pendingSave = true
		s.dirty = false
		s.Invalidate()
		return
	}

	switch {
	// Navigation.
	case kb.Matches(data, KBSelectUp):
		if len(s.filteredItems) == 0 {
			return
		}
		if s.cursor == 0 {
			s.cursor = len(s.filteredItems) - 1
		} else {
			s.cursor--
		}
		s.fixScroll()
		s.Invalidate()

	case kb.Matches(data, KBSelectDown):
		if len(s.filteredItems) == 0 {
			return
		}
		if s.cursor == len(s.filteredItems)-1 {
			s.cursor = 0
		} else {
			s.cursor++
		}
		s.fixScroll()
		s.Invalidate()

	// Alt+Up: reorder up.
	case scopedModelsActionMatches(kb, data, "app.models.reorderUp", "alt+up"):
		s.handleReorder(-1)

	// Alt+Down: reorder down.
	case scopedModelsActionMatches(kb, data, "app.models.reorderDown", "alt+down"):
		s.handleReorder(1)

	// Enter/Space: toggle.
	case kb.Matches(data, KBSelectConfirm):
		if s.cursor >= 0 && s.cursor < len(s.filteredItems) {
			item := s.filteredItems[s.cursor]
			s.toggle(item.fullID)
			s.dirty = true
			s.refresh()
			s.Invalidate()
		}

	// Ctrl+A: enable all.
	case scopedModelsActionMatches(kb, data, "app.models.enableAll", "ctrl+a"):
		var targets []string
		if s.searchInput.Text() != "" {
			for _, item := range s.filteredItems {
				targets = append(targets, item.fullID)
			}
		}
		s.enableAll(targets)
		s.dirty = true
		s.refresh()
		s.Invalidate()

	// Ctrl+X: clear all.
	case scopedModelsActionMatches(kb, data, "app.models.clearAll", "ctrl+x"):
		var targets []string
		if s.searchInput.Text() != "" {
			for _, item := range s.filteredItems {
				targets = append(targets, item.fullID)
			}
		}
		s.clearAll(targets)
		s.dirty = true
		s.refresh()
		s.Invalidate()

	// Ctrl+P: toggle provider.
	case scopedModelsActionMatches(kb, data, "app.models.toggleProvider", "ctrl+p"):
		if s.cursor >= 0 && s.cursor < len(s.filteredItems) {
			item := s.filteredItems[s.cursor]
			m, available := s.models[item.fullID]
			if !available {
				return
			}
			provider := m.Provider
			var providerIDs []string
			for _, id := range s.allIDs {
				if s.models[id].Provider == provider {
					providerIDs = append(providerIDs, id)
				}
			}
			// If all provider models are enabled, clear them; otherwise enable them.
			allEnabled := true
			for _, id := range providerIDs {
				if !s.isEnabled(id) {
					allEnabled = false
					break
				}
			}
			if allEnabled {
				s.clearAll(providerIDs)
			} else {
				s.enableAll(providerIDs)
			}
			s.dirty = true
			s.refresh()
			s.Invalidate()
		}

	// Ctrl+C: clear search or cancel.
	case matchesKeyID(data, "ctrl+c"):
		if s.searchInput.Text() != "" {
			s.searchInput.SetText("")
			s.refresh()
			s.Invalidate()
		} else {
			s.result = ScopedModelsResult{Cancelled: true}
			s.done = true
			s.Invalidate()
		}

	// Esc: cancel.
	case kb.Matches(data, KBSelectCancel):
		s.result = ScopedModelsResult{Cancelled: true}
		s.done = true
		s.Invalidate()

	// Everything else belongs to the shared Input contract (cursor movement,
	// deletion, paste, Kitty/modifyOtherKeys printable decoding, and typing).
	default:
		before := s.searchInput.Text()
		s.searchInput.HandleInput(data)
		if s.searchInput.Text() != before {
			s.refresh()
			s.Invalidate()
		}
	}
}

func (s *ScopedModelsList) handleReorder(delta int) {
	if s.enabledIDs == nil || s.cursor < 0 || s.cursor >= len(s.filteredItems) {
		return
	}
	item := s.filteredItems[s.cursor]
	if !s.isEnabled(item.fullID) {
		return
	}
	idx := slices.Index(s.enabledIDs, item.fullID)
	newIdx := idx + delta
	if newIdx < 0 || newIdx >= len(s.enabledIDs) {
		return
	}
	s.move(item.fullID, delta)
	s.cursor += delta
	s.dirty = true
	s.refresh()
	s.Invalidate()
}
