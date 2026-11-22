package tui

// Ports the cases Pi 37b74c546 (#12246) and 5bcf370a5 (#12334) add, both fixes for issue #12245: a non-empty query selects the
// best match even after the cursor moved, and a key that leaves the query unchanged (Left, Right, Home, End) keeps the selection.
//
// pi: packages/coding-agent/test/oauth-selector.test.ts, scoped-models-selector.test.ts, config-selector.test.ts,
// model-selector.test.ts, settings-submenu.test.ts and packages/tui/test/settings-list.test.ts

import (
	"slices"
	"strings"
	"testing"
)

const (
	selectorUpKey    = "\x1b[A"
	selectorDownKey  = "\x1b[B"
	selectorRightKey = "\x1b[C"
	selectorLeftKey  = "\x1b[D"
	selectorHomeKey  = "\x1b[H"
	selectorEndKey   = "\x1b[F"
	selectorBsKey    = "\x7f"
)

func typeSelectorQuery(h interface{ HandleInput(string) }, query string) {
	for _, r := range query {
		h.HandleInput(string(r))
	}
}

func oauthSelectorProviders(names ...string) []OAuthProvider {
	providers := make([]OAuthProvider, 0, len(names))
	for _, name := range names {
		providers = append(providers, OAuthProvider{ID: strings.ReplaceAll(strings.ToLower(name), " ", "-"), Name: name, AuthType: "api_key"})
	}
	return providers
}

// oauth-selector.test.ts "selects the best match after typing a query, even after scrolling".
func TestOAuthSelectorSelectsTheBestMatchAfterTypingAQueryEvenAfterScrolling(t *testing.T) {
	providers := oauthSelectorProviders("Anthropic", "Google", "Groq", "Mistral", "OpenAI", "OpenCode Go", "OpenCode Zen", "xAI")
	var selected []string
	selector := NewOAuthSelectorComponent("login", providers, func(id, _ string) { selected = append(selected, id) }, func() {})
	for range 5 {
		selector.HandleInput(selectorDownKey)
	}
	typeSelectorQuery(selector, "openai")
	if got := selector.SelectedID(); got != "openai" {
		t.Fatalf("selected %q after the query, want openai", got)
	}
	selector.HandleInput("\r")
	if !slices.Equal(selected, []string{"openai"}) {
		t.Fatalf("Enter selected %v, want [openai]", selected)
	}
}

// PostPinFixCheck probe: 12 providers, 5 Down, the query typed as one write.
func TestOAuthSelectorSelectsTheBestMatchForAQueryWrittenAtOnce(t *testing.T) {
	providers := oauthSelectorProviders("Anthropic", "Azure", "Bedrock", "Cerebras", "Deepseek", "Google", "Groq", "OpenAI", "OpenRouter", "xAI", "Mistral", "Moonshot")
	selector := NewOAuthSelectorComponent("login", providers, nil, nil)
	for range 5 {
		selector.HandleInput(selectorDownKey)
	}
	selector.HandleInput("openai")
	if got := selector.SelectedID(); got != "openai" {
		t.Fatalf("selected %q, want openai", got)
	}
}

// Clearing the query keeps the clamp: the highlight stays on its row number, as Pi's `query ? 0 : Math.min(...)`.
func TestOAuthSelectorClearingTheQueryKeepsTheRowNumber(t *testing.T) {
	selector := NewOAuthSelectorComponent("login", oauthSelectorProviders("Anthropic", "Google", "Groq", "OpenAI"), nil, nil)
	selector.HandleInput("g")
	selector.HandleInput(selectorDownKey)
	if got := selector.SelectedID(); got != "groq" {
		t.Fatalf("selected %q after Down, want groq", got)
	}
	selector.HandleInput(selectorBsKey)
	if got := selector.SelectedID(); got != "google" {
		t.Fatalf("selected %q after clearing the query, want row 1 (google)", got)
	}
}

// oauth-selector.test.ts "keeps the selection when a cursor key leaves the query unchanged".
func TestOAuthSelectorKeepsTheSelectionWhenACursorKeyLeavesTheQueryUnchanged(t *testing.T) {
	selector := NewOAuthSelectorComponent("login", oauthSelectorProviders("OpenAI", "OpenCode Go", "OpenCode Zen"), nil, nil)
	typeSelectorQuery(selector, "open")
	selector.HandleInput(selectorDownKey)
	selector.HandleInput(selectorDownKey)
	before := selector.SelectedID()
	selector.HandleInput(selectorLeftKey)
	selector.HandleInput(selectorHomeKey)
	if before == "openai" {
		t.Fatalf("two Down keys left the selection on openai")
	}
	if got := selector.SelectedID(); got != before {
		t.Fatalf("selection moved from %q to %q", before, got)
	}
}

func scopedSelectorList() (*ScopedModelsList, []ModelItem) {
	models := []ModelItem{
		{FullID: "openai/gpt-4o", Name: "GPT-4o", Provider: "openai"},
		{FullID: "openai/gpt-4o-mini", Name: "GPT-4o mini", Provider: "openai"},
		{FullID: "anthropic/claude", Name: "Claude", Provider: "anthropic"},
		{FullID: "google/gemini", Name: "Gemini", Provider: "google"},
	}
	return NewScopedModelsList(ScopedModelsConfig{AllModels: models}), models
}

func scopedEnabledFlags(list *ScopedModelsList, models []ModelItem) []bool {
	enabled := list.EnabledIDs()
	flags := make([]bool, len(models))
	for i, model := range models {
		flags[i] = enabled == nil || slices.Contains(enabled, model.FullID)
	}
	return flags
}

// scoped-models-selector.test.ts "selects the best match after typing a query, even after scrolling".
func TestScopedModelsSelectsTheBestMatchAfterTypingAQueryEvenAfterScrolling(t *testing.T) {
	list, models := scopedSelectorList()
	for range 3 {
		list.HandleInput(selectorDownKey)
	}
	typeSelectorQuery(list, "gpt-4o")
	list.HandleInput("\r")
	if got, want := scopedEnabledFlags(list, models), []bool{false, true, true, true}; !slices.Equal(got, want) {
		t.Fatalf("enabled = %v, want %v", got, want)
	}
}

// scoped-models-selector.test.ts "keeps the selection when a cursor key leaves the query unchanged".
func TestScopedModelsKeepsTheSelectionWhenACursorKeyLeavesTheQueryUnchanged(t *testing.T) {
	list, models := scopedSelectorList()
	typeSelectorQuery(list, "gpt")
	list.HandleInput(selectorDownKey)
	list.HandleInput(selectorLeftKey)
	list.HandleInput("\r")
	if got, want := scopedEnabledFlags(list, models), []bool{true, false, true, true}; !slices.Equal(got, want) {
		t.Fatalf("enabled = %v, want %v", got, want)
	}
}

func configSelectorToolItems() *ConfigSelectorComponent {
	var items []ResourceItem
	for _, name := range []string{"tool-a.ts", "tool-b.ts", "other.ts"} {
		items = append(items, ResourceItem{Path: "/pkg/extensions/" + name, ResourceType: ResourceExtensions, DisplayName: name, Enabled: true, Scope: "user", Origin: "package", Source: "npm:pi-tools"})
	}
	return NewConfigSelector(BuildResourceGroups(items), 0)
}

func configSelectorSelectedName(cs *ConfigSelectorComponent) string {
	if cs.cursor < 0 || cs.cursor >= len(cs.filtered) || cs.filtered[cs.cursor].item == nil {
		return ""
	}
	return cs.filtered[cs.cursor].item.DisplayName
}

// config-selector.test.ts "keeps the selection when a cursor key leaves the query unchanged".
func TestConfigSelectorKeepsTheSelectionWhenACursorKeyLeavesTheQueryUnchanged(t *testing.T) {
	cs := configSelectorToolItems()
	typeSelectorQuery(cs, "tool")
	cs.HandleInput(selectorDownKey)
	if got := configSelectorSelectedName(cs); got != "tool-b.ts" {
		t.Fatalf("Down selected %q, want tool-b.ts", got)
	}
	cs.HandleInput(selectorLeftKey)
	cs.HandleInput(selectorHomeKey)
	if got := configSelectorSelectedName(cs); got != "tool-b.ts" {
		t.Fatalf("selection moved to %q, want tool-b.ts", got)
	}
}

// model-selector.test.ts "keeps the selection when a cursor key leaves the query unchanged".
func TestModelSelectorKeepsTheSelectionWhenACursorKeyLeavesTheQueryUnchanged(t *testing.T) {
	items := mkItems("fixture/alpha-1", "fixture/alpha-2", "fixture/alpha-3")
	selector := NewStaticModelSelectorComponent("x", nil, items, "")
	typeSelectorQuery(selector, "alpha")
	selector.HandleInput(selectorDownKey)
	selector.HandleInput(selectorDownKey)
	browsed := selector.cursor
	if browsed != 2 {
		t.Fatalf("two Down keys left cursor %d, want 2", browsed)
	}
	selector.HandleInput(selectorLeftKey)
	selector.HandleInput(selectorHomeKey)
	if selector.cursor != browsed {
		t.Fatalf("cursor moved from %d to %d", browsed, selector.cursor)
	}
}

// settings-submenu.test.ts "keeps the selection when a cursor key leaves the query unchanged".
func TestSelectSubmenuKeepsTheSelectionWhenACursorKeyLeavesTheQueryUnchanged(t *testing.T) {
	items := []SelectItem{{Value: "dark-blue", Label: "dark-blue"}, {Value: "dark-green", Label: "dark-green"}, {Value: "light", Label: "light"}}
	submenu := NewSelectSubmenu("Theme", "", items, "light", SelectSubmenuOptions{Searchable: true})
	typeSelectorQuery(submenu, "dark")
	submenu.HandleInput(selectorDownKey)
	submenu.HandleInput(selectorLeftKey)
	submenu.HandleInput(selectorHomeKey)
	if got := submenu.CurrentValue(); got != "dark-green" {
		t.Fatalf("selection = %q, want dark-green", got)
	}
}

// settings-list.test.ts "keeps the selection when a cursor key leaves the query unchanged".
func TestSettingsListKeepsTheSelectionWhenACursorKeyLeavesTheQueryUnchanged(t *testing.T) {
	var changes []string
	var items []SettingItem
	for _, label := range []string{"Alpha mode", "Beta mode", "Other"} {
		items = append(items, SettingItem{ID: label, Label: label, CurrentValue: "off", Values: []string{"off", "on"}})
	}
	list := NewSettingsList(items, 10, GetSettingsListTheme(), func(id, _ string) { changes = append(changes, id) }, nil, SettingsListOptions{EnableSearch: true})
	typeSelectorQuery(list, "mode")
	list.HandleInput(selectorDownKey)
	browsed := list.cursor
	if browsed != 1 {
		t.Fatalf("Down left cursor %d, want 1", browsed)
	}
	list.HandleInput(selectorLeftKey)
	list.HandleInput(selectorHomeKey)
	if list.cursor != browsed {
		t.Fatalf("cursor moved from %d to %d", browsed, list.cursor)
	}
	list.HandleInput("\r")
	if want := items[list.filtered[browsed]].ID; !slices.Equal(changes, []string{want}) {
		t.Fatalf("Enter changed %v, want the browsed setting [%s]", changes, want)
	}
}

// Boundary cases required by AGENTS.md done criterion 12: empty list, one row, first/last wraparound,
// a query that matches nothing, cursor keys at start/end of input, and typing after a cursor move.
func TestSelectorSearchBoundaryTransitions(t *testing.T) {
	t.Run("oauth-empty-and-singleton-and-no-match", func(t *testing.T) {
		empty := NewOAuthSelectorComponent("login", nil, nil, nil)
		empty.HandleInput(selectorDownKey)
		empty.HandleInput(selectorUpKey)
		empty.HandleInput("a")
		empty.HandleInput(selectorLeftKey)
		empty.HandleInput(selectorRightKey)
		if got := empty.SelectedID(); got != "" {
			t.Fatalf("empty oauth SelectedID = %q, want empty", got)
		}

		one := NewOAuthSelectorComponent("login", oauthSelectorProviders("OpenAI"), nil, nil)
		one.HandleInput(selectorDownKey)
		one.HandleInput(selectorUpKey)
		typeSelectorQuery(one, "zzz")
		if got := one.SelectedID(); got != "" {
			t.Fatalf("no-match oauth SelectedID = %q, want empty", got)
		}
		one.HandleInput(selectorLeftKey)
		one.HandleInput(selectorHomeKey)
		one.HandleInput(selectorEndKey)
		one.HandleInput(selectorRightKey)
		if got := one.SelectedID(); got != "" {
			t.Fatalf("cursor keys on no-match oauth SelectedID = %q, want empty", got)
		}
	})

	t.Run("oauth-wraparound-and-cursor-bounds-and-type-after-move", func(t *testing.T) {
		sel := NewOAuthSelectorComponent("login", oauthSelectorProviders("Alpha One", "Alpha Two", "Beta"), nil, nil)
		// Wraparound: Up from 0 goes to last; Down from last goes to 0.
		sel.HandleInput(selectorUpKey)
		if got := sel.SelectedID(); got != "beta" {
			t.Fatalf("Up from first = %q, want beta", got)
		}
		sel.HandleInput(selectorDownKey)
		if got := sel.SelectedID(); got != "alpha-one" {
			t.Fatalf("Down from last = %q, want alpha-one", got)
		}
		// Cursor keys at start and end of query keep selection, then typing resets to row 0.
		typeSelectorQuery(sel, "al")
		sel.HandleInput(selectorDownKey)
		if got := sel.SelectedID(); got != "alpha-two" {
			t.Fatalf("Down after query = %q, want alpha-two", got)
		}
		// At end of input: Right and End leave query unchanged.
		sel.HandleInput(selectorRightKey)
		sel.HandleInput(selectorEndKey)
		if got := sel.SelectedID(); got != "alpha-two" {
			t.Fatalf("Right/End at end of input moved selection to %q", got)
		}
		// Move to start and press Left/Home at start of input.
		sel.HandleInput(selectorHomeKey)
		sel.HandleInput(selectorLeftKey)
		if got := sel.SelectedID(); got != "alpha-two" {
			t.Fatalf("Home/Left at start of input moved selection to %q", got)
		}
		// Move back to end and type: query changes so selection resets to best match (row 0).
		sel.HandleInput(selectorEndKey)
		sel.HandleInput("p")
		if got := sel.SelectedID(); got != "alpha-one" {
			t.Fatalf("typing after cursor move = %q, want alpha-one", got)
		}
	})

	t.Run("model-cursor-bounds-and-type-after-move", func(t *testing.T) {
		items := mkItems("fixture/alpha-1", "fixture/alpha-2", "fixture/beta-1")
		sel := NewStaticModelSelectorComponent("x", nil, items, "")
		typeSelectorQuery(sel, "al")
		sel.HandleInput(selectorDownKey)
		if sel.cursor != 1 {
			t.Fatalf("Down selected %d, want 1", sel.cursor)
		}
		sel.HandleInput(selectorRightKey)
		sel.HandleInput(selectorEndKey)
		sel.HandleInput(selectorHomeKey)
		sel.HandleInput(selectorLeftKey)
		if sel.cursor != 1 {
			t.Fatalf("cursor keys at start/end moved selection to %d, want 1", sel.cursor)
		}
		sel.HandleInput(selectorEndKey)
		sel.HandleInput("p")
		if sel.cursor != 0 {
			t.Fatalf("typing after cursor move selected %d, want 0", sel.cursor)
		}
		typeSelectorQuery(sel, "zzzz")
		sel.HandleInput(selectorLeftKey)
		if len(sel.filtered) != 0 {
			t.Fatalf("no-match filtered len = %d, want 0", len(sel.filtered))
		}
	})

	t.Run("settings-list-cursor-bounds-and-type-after-move", func(t *testing.T) {
		var items []SettingItem
		for _, label := range []string{"Alpha mode", "Beta mode", "Other"} {
			items = append(items, SettingItem{ID: label, Label: label, CurrentValue: "off", Values: []string{"off", "on"}})
		}
		list := NewSettingsList(items, 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
		typeSelectorQuery(list, "mo")
		list.HandleInput(selectorDownKey)
		if list.cursor != 1 {
			t.Fatalf("Down selected %d, want 1", list.cursor)
		}
		list.HandleInput(selectorRightKey)
		list.HandleInput(selectorEndKey)
		list.HandleInput(selectorHomeKey)
		list.HandleInput(selectorLeftKey)
		if list.cursor != 1 {
			t.Fatalf("cursor keys at start/end moved cursor to %d, want 1", list.cursor)
		}
		list.HandleInput(selectorEndKey)
		list.HandleInput("d")
		if list.cursor != 0 {
			t.Fatalf("typing after cursor move selected %d, want 0", list.cursor)
		}
		typeSelectorQuery(list, "zzzz")
		list.HandleInput(selectorLeftKey)
		if len(list.filtered) != 0 {
			t.Fatalf("no-match filtered len = %d, want 0", len(list.filtered))
		}
	})
}
