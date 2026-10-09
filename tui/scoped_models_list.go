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

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
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
	Container
	listContainer     *Container
	refreshStatusText *Text
	footerText        *Text

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

		listContainer: NewContainer(),
	}
	for _, m := range cfg.AllModels {
		s.allIDs = append(s.allIDs, m.FullID)
		s.models[m.FullID] = m
	}
	if cfg.EnabledModelIDs != nil {
		s.enabledIDs = slices.Clone(cfg.EnabledModelIDs)
	}
	s.refresh()
	// upstream: scoped-models-selector.ts constructor children.
	th := ActiveTheme()
	s.refreshStatusText = NewPaddedText("", 0, 0, nil)
	s.footerText = NewPaddedText("", 0, 0, nil)
	s.Add(NewDynamicBorder())
	s.Add(NewSpacer(1))
	s.Add(NewPaddedText(fg(th.Accent, "\x1b[1mModel Configuration"+SGRBoldDimReset), 0, 0, nil))
	s.Add(NewPaddedText(fg(th.Muted, "Session-only. "+ActionKeyDisplayTextOr("app.models.save", "ctrl+s")+" to save to settings."), 0, 0, nil))
	s.Add(NewSpacer(1))
	s.Add(s.searchInput)
	s.Add(NewSpacer(1))
	s.Add(s.listContainer)
	s.Add(NewSpacer(1))
	s.Add(s.refreshStatusText)
	s.Add(s.footerText)
	s.Add(NewDynamicBorder())
	s.updateList()
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
	s.updateList()
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
	s.updateList()
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

// enableAll is upstream enableAll(enabledIds, allIds, targetIds?): restricted is "targetIds is defined", so an empty target list
// (a search with no matches) enables nothing instead of everything.
func (s *ScopedModelsList) enableAll(targetIDs []string, restricted bool) {
	if s.enabledIDs == nil {
		return // already all enabled
	}
	targets := targetIDs
	if !restricted {
		targets = s.allIDs
	}
	for _, id := range targets {
		if !slices.Contains(s.enabledIDs, id) {
			s.enabledIDs = append(s.enabledIDs, id)
		}
	}
	s.enabledIDs = s.normalizeEnabled(s.enabledIDs)
}

// clearAll is upstream clearAll(enabledIds, allIds, targetIds?); restricted is "targetIds is defined".
func (s *ScopedModelsList) clearAll(targetIDs []string, restricted bool) {
	if s.enabledIDs == nil {
		if restricted {
			// Switch from "all" to "all except targets"; the result is an explicit list even when it is empty.
			keep := []string{}
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
	if !restricted {
		// upstream: with no target list the targets are the enabled ids themselves.
		s.enabledIDs = []string{}
		return
	}
	s.enabledIDs = slices.DeleteFunc(s.enabledIDs, func(id string) bool {
		return slices.Contains(targetIDs, id)
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

// updateList rebuilds the list rows, the refresh status and the footer from the current state (scoped-models-selector.ts updateList).
func (s *ScopedModelsList) updateList() {
	th := ActiveTheme()
	s.listContainer.Clear()
	text := func(content string) { s.listContainer.Add(NewPaddedText(content, 0, 0, nil)) }
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
		s.listContainer.Add(NewSpacer(1))
		if m, available := s.models[s.filteredItems[s.cursor].fullID]; available {
			text(fg(th.Muted, "  Model Name: "+m.Name))
		} else {
			text(fg(th.Muted, "  Model unavailable"))
		}
	}

	if s.refreshStatus != "" {
		color := th.Muted
		switch s.refreshKind {
		case RefreshStatusSuccess:
			color = th.Success
		case RefreshStatusWarning:
			color = th.Warning
		}
		s.refreshStatusText.SetText(fg(color, "  "+s.refreshStatus))
	} else {
		s.refreshStatusText.SetText("")
	}
	footer := fg(th.Dim, "  "+strings.Join(s.footerParts(), " · "))
	if s.dirty {
		footer = fg(th.Dim, "  "+strings.Join(s.footerParts(), " · ")+" ") + fg(th.Warning, "(unsaved)")
	}
	s.footerText.SetText(footer)
}

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
	return []string{
		ActionKeyDisplayText(KBSelectConfirm) + " toggle",
		ActionKeyDisplayTextOr("app.models.enableAll", "ctrl+a") + " all",
		ActionKeyDisplayTextOr("app.models.clearAll", "ctrl+x") + " clear",
		ActionKeyDisplayTextOr("app.models.toggleProvider", "ctrl+p") + " provider",
		ActionKeyDisplayTextOr("app.models.reorderUp", "alt+up") + "/" + ActionKeyDisplayTextOr("app.models.reorderDown", "alt+down") + " reorder",
		ActionKeyDisplayTextOr("app.models.save", "ctrl+s") + " save",
		s.enabledCountText(),
	}
}

// enabledCountText is the footer's enabled count, "all enabled" when every
// model is.
func (s *ScopedModelsList) enabledCountText() string {
	if s.enabledIDs == nil {
		return "all enabled"
	}
	enabledCount, unavailableCount := 0, 0
	for _, id := range s.enabledIDs {
		if _, ok := s.models[id]; ok {
			enabledCount++
		} else {
			unavailableCount++
		}
	}
	countText := fmt.Sprintf("%d/%d enabled", enabledCount, len(s.allIDs))
	if unavailableCount > 0 {
		countText += fmt.Sprintf(" · %d unavailable", unavailableCount)
	}
	return countText
}

// NativeNode reports the list as a selector whose item ids are the models'
// full ids and whose checks are the enabled models. pig additive (D91): the
// status carries the refresh message, then the footer's enabled count and
// unsaved marker without its key hints.
func (s *ScopedModelsList) NativeNode() (frontend.Node, bool) {
	status := s.enabledCountText()
	if s.dirty {
		status += " (unsaved)"
	}
	if s.refreshStatus != "" {
		status = s.refreshStatus + "\n" + status
	}
	node := frontend.Selector{
		Title:       "Model Configuration",
		Description: "Session-only. " + ActionKeyDisplayTextOr("app.models.save", "ctrl+s") + " to save to settings.",
		Searchable:  true,
		Query:       s.searchInput.Text(),
		Items:       make([]frontend.SelectorItem, len(s.filteredItems)),
		Status:      status,
	}
	for i, entry := range s.filteredItems {
		item := frontend.SelectorItem{ID: entry.fullID, Label: entry.fullID, Detail: "unavailable"}
		if m, available := s.models[entry.fullID]; available {
			item.Label = strings.TrimPrefix(m.FullID, m.Provider+"/")
			item.Detail = m.Provider
			item.Checked = entry.enabled
		}
		node.Items[i] = item
	}
	if s.cursor >= 0 && s.cursor < len(node.Items) {
		node.Selected = node.Items[s.cursor].ID
	}
	return node, true
}

// HandleInput processes a keystroke.
func (s *ScopedModelsList) HandleInput(data string) {
	if s.done {
		return
	}
	defer s.updateList()

	kb := GetTUIKeybindings()
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
		targets := []string{}
		restricted := s.searchInput.Text() != ""
		if restricted {
			for _, item := range s.filteredItems {
				targets = append(targets, item.fullID)
			}
		}
		s.enableAll(targets, restricted)
		s.dirty = true
		s.refresh()
		s.Invalidate()

	// Ctrl+X: clear all.
	case scopedModelsActionMatches(kb, data, "app.models.clearAll", "ctrl+x"):
		targets := []string{}
		restricted := s.searchInput.Text() != ""
		if restricted {
			for _, item := range s.filteredItems {
				targets = append(targets, item.fullID)
			}
		}
		s.clearAll(targets, restricted)
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
				s.clearAll(providerIDs, true)
			} else {
				s.enableAll(providerIDs, true)
			}
			s.dirty = true
			s.refresh()
			s.Invalidate()
		}

	// Ctrl+S: save (checked after the other actions, as upstream orders them).
	case scopedModelsActionMatches(kb, data, "app.models.save", "ctrl+s"):
		s.pendingSaveIDs = s.EnabledIDs()
		s.pendingSave = true
		s.dirty = false
		s.Invalidate()

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
