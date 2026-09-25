package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func testModels() []ModelItem {
	return []ModelItem{
		{FullID: "openai/gpt-4o", Name: "GPT-4o", Provider: "openai"},
		{FullID: "openai/gpt-4o-mini", Name: "GPT-4o Mini", Provider: "openai"},
		{FullID: "anthropic/claude-sonnet", Name: "Claude Sonnet", Provider: "anthropic"},
		{FullID: "anthropic/claude-haiku", Name: "Claude Haiku", Provider: "anthropic"},
		{FullID: "google/gemini-pro", Name: "Gemini Pro", Provider: "google"},
	}
}

func TestScopedModels_NilMeansAllEnabled(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       testModels(),
		EnabledModelIDs: nil,
	})

	lines := sl.Render(80)
	joined := strings.Join(lines, "\n")

	// 0.87.1: all enabled (nil) marks every available model with "✓ ".
	if got := strings.Count(stripANSI(joined), "✓ "); got != len(testModels()) {
		t.Errorf("nil enabledIDs rendered %d checkmarks, want %d", got, len(testModels()))
	}

	// Footer should say "all enabled".
	if !strings.Contains(joined, "all enabled") {
		t.Error("footer should say 'all enabled'")
	}
}

func TestScopedModels_RendersAndRemovesUnavailableEnabledModel(t *testing.T) {
	models := testModels()[:1]
	unavailable := "openai/unavailable"
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       models,
		EnabledModelIDs: []string{unavailable, models[0].FullID},
	})
	joined := stripANSI(strings.Join(sl.Render(100), "\n"))
	if !strings.Contains(joined, "→   "+unavailable+" [unavailable]") {
		t.Fatalf("unavailable model missing:\n%s", joined)
	}
	sl.HandleInput("\r")
	if slices.Contains(sl.EnabledIDs(), unavailable) {
		t.Fatalf("unavailable model was not removed: %v", sl.EnabledIDs())
	}
}

func TestScopedModels_ToggleSingle(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       testModels(),
		EnabledModelIDs: nil,
	})

	// Toggle first item (Enter).
	sl.HandleInput("\r")

	ids := sl.EnabledIDs()
	if ids == nil {
		t.Fatal("after toggle, enabledIDs should not be nil")
	}
	// 0.87.1: the first toggle from nil disables only the toggled model.
	if strings.Join(ids, ",") != "openai/gpt-4o-mini,anthropic/claude-sonnet,anthropic/claude-haiku,google/gemini-pro" {
		t.Errorf("expected every model except openai/gpt-4o, got %v", ids)
	}

	// Now checkmarks should appear.
	lines := sl.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "✓") {
		t.Error("after toggle, ✓ should appear for enabled model")
	}
}

func TestScopedModels_ArrowNavigationWraps(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{AllModels: testModels()})
	sl.HandleInput("\x1b[A")
	if sl.cursor != len(testModels())-1 {
		t.Fatalf("up from first selected %d", sl.cursor)
	}
	sl.HandleInput("\x1b[B")
	if sl.cursor != 0 {
		t.Fatalf("down from last selected %d", sl.cursor)
	}
}

func TestScopedModels_ToggleProvider(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels: testModels(),
		EnabledModelIDs: []string{
			"openai/gpt-4o", "openai/gpt-4o-mini",
			"anthropic/claude-sonnet", "anthropic/claude-haiku",
			"google/gemini-pro",
		},
	})

	// Move cursor to an anthropic model (index 2).
	sl.HandleInput("\x1b[B") // down
	sl.HandleInput("\x1b[B") // down: now on anthropic/claude-sonnet

	// Press Ctrl+P to toggle provider: all anthropic models should be cleared.
	sl.HandleInput("\x10")

	ids := sl.EnabledIDs()
	for _, id := range ids {
		if strings.HasPrefix(id, "anthropic/") {
			t.Errorf("anthropic model %s should be disabled after provider toggle", id)
		}
	}
	if len(ids) != 3 { // openai/2 + google/1
		t.Errorf("expected 3 enabled, got %d: %v", len(ids), ids)
	}
}

func TestScopedModels_EnableAll(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       testModels(),
		EnabledModelIDs: []string{"openai/gpt-4o"},
	})

	sl.HandleInput("\x01") // Ctrl+A: enable all

	ids := sl.EnabledIDs()
	if ids != nil {
		t.Errorf("enable all should return nil (all enabled), got %v", ids)
	}
}

func TestScopedModels_EnableAllPreservesUnavailableConfiguredIDs(t *testing.T) {
	models := testModels()[:1]
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       models,
		EnabledModelIDs: []string{"openai/unavailable"},
	})
	sl.HandleInput("\x01")
	ids := sl.EnabledIDs()
	want := []string{"openai/unavailable", models[0].FullID}
	if !slices.Equal(ids, want) {
		t.Fatalf("enable all with unavailable IDs = %v, want %v", ids, want)
	}
}

func TestScopedModels_ClearAll(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       testModels(),
		EnabledModelIDs: nil,
	})

	sl.HandleInput("\x18") // Ctrl+X: clear all

	ids := sl.EnabledIDs()
	if ids == nil {
		t.Fatal("clear all should return non-nil")
	}
	if len(ids) != 0 {
		t.Errorf("clear all should produce empty list, got %v", ids)
	}
}

func TestScopedModels_FilteredClearAndEnableTargetOnlyMatches(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{AllModels: testModels()})
	for _, ch := range "openai/" {
		sl.HandleInput(string(ch))
	}
	// Pi 0.87.1 fuzzyFilter splits "openai/" to the token "openai", an ordered
	// subsequence of both openai and both anthropic search texts. Ctrl+X clears
	// exactly those four matches, so only google/gemini-pro stays enabled.
	sl.HandleInput("\x18")
	if got, want := sl.EnabledIDs(), []string{"google/gemini-pro"}; !slices.Equal(got, want) {
		t.Fatalf("filtered clear = %v, want %v", got, want)
	}
	sl.HandleInput("\x01")
	if got := sl.EnabledIDs(); got != nil {
		t.Fatalf("filtered enable did not restore all-enabled nil: %v", got)
	}
}

func TestScopedModels_SearchFilter(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       testModels(),
		EnabledModelIDs: nil,
	})

	// Type "gemini" to filter.
	for _, ch := range "gemini" {
		sl.HandleInput(string(ch))
	}

	// Upstream refresh() fuzzy-filters on getModelSearchText, whose trailing
	// name makes "gpt-4o-mini openai ... GPT-4o Mini" an ordered g-e-m-i-n-i
	// subsequence too. Pi 0.87.1 fuzzyFilter over the same five models returns
	// exactly [google/gemini-pro, openai/gpt-4o-mini].
	got := make([]string, 0, len(sl.filteredItems))
	for _, item := range sl.filteredItems {
		got = append(got, item.fullID)
	}
	want := []string{"google/gemini-pro", "openai/gpt-4o-mini"}
	if !slices.Equal(got, want) {
		t.Fatalf("filter 'gemini' = %v, want %v", got, want)
	}
}

func TestScopedModels_SearchUsesFuzzySubsequenceOrdering(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{AllModels: testModels()})
	for _, ch := range "gmpr" {
		sl.HandleInput(string(ch))
	}
	joined := stripANSI(strings.Join(sl.Render(80), "\n"))
	if !strings.Contains(joined, "gemini-pro") || strings.Contains(joined, "gpt-4o") {
		t.Fatalf("fuzzy model search result:\n%s", joined)
	}
}

func TestScopedModels_ReorderUpDown(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels: testModels(),
		EnabledModelIDs: []string{
			"openai/gpt-4o", "openai/gpt-4o-mini", "anthropic/claude-sonnet",
		},
	})

	// Cursor is at index 0 (openai/gpt-4o). Move it down in the enabled order.
	sl.HandleInput("\x1b[1;3B") // Alt+Down

	ids := sl.EnabledIDs()
	if len(ids) < 2 {
		t.Fatal("expected at least 2 enabled models")
	}
	if ids[0] != "openai/gpt-4o-mini" || ids[1] != "openai/gpt-4o" {
		t.Errorf("after reorder down, expected [gpt-4o-mini, gpt-4o, ...], got %v", ids)
	}

	// Now move it back up.
	sl.HandleInput("\x1b[1;3A") // Alt+Up

	ids = sl.EnabledIDs()
	if ids[0] != "openai/gpt-4o" || ids[1] != "openai/gpt-4o-mini" {
		t.Errorf("after reorder up, expected [gpt-4o, gpt-4o-mini, ...], got %v", ids)
	}
}

func TestScopedModels_RefreshStatusAndModelUpdate(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       testModels()[:2],
		EnabledModelIDs: []string{testModels()[0].FullID},
		RefreshStatus:   "Refreshing model catalogs…",
	})
	// Rows are Text(..., 0, 0) as upstream, so they pad to the render width.
	var trimmed []string
	for _, line := range sl.Render(100) {
		trimmed = append(trimmed, strings.TrimRight(stripANSI(line), " "))
	}
	initial := strings.Join(trimmed, "\n")
	if !strings.Contains(initial, "Refreshing model catalogs…\n  Enter toggle") {
		t.Fatalf("initial refresh status is not immediately before footer:\n%s", initial)
	}

	sl.HandleInput("\x1b[B")
	selectedID := sl.filteredItems[sl.cursor].fullID
	sl.UpdateModels(append([]ModelItem{{FullID: "new/model", Name: "New", Provider: "new"}}, testModels()[:2]...), nil)
	if got := sl.filteredItems[sl.cursor].fullID; got != selectedID {
		t.Fatalf("selected model after update = %q, want %q", got, selectedID)
	}
	if sl.EnabledIDs() != nil {
		t.Fatalf("explicit nil enabled IDs did not restore all-enabled state: %v", sl.EnabledIDs())
	}
	sl.SetRefreshStatus("Model catalogs refreshed.", RefreshStatusSuccess)
	updated := stripANSI(strings.Join(sl.Render(100), "\n"))
	if !strings.Contains(updated, "model [new]") || !strings.Contains(updated, "Model catalogs refreshed.") {
		t.Fatalf("refreshed selector did not publish models and status:\n%s", updated)
	}
}

func TestScopedModels_Render(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels: testModels(),
		EnabledModelIDs: []string{
			"openai/gpt-4o", "anthropic/claude-sonnet",
		},
	})

	lines := sl.Render(80)
	joined := strings.Join(lines, "\n")

	// Should contain title.
	if !strings.Contains(joined, "Model Configuration") {
		t.Error("missing title")
	}

	// Should contain input.
	if !strings.Contains(joined, ">") {
		t.Error("missing input")
	}

	// Should show enabled count.
	if !strings.Contains(joined, "2/5 enabled") {
		t.Errorf("expected '2/5 enabled' in footer, got:\n%s", joined)
	}

	// Should show the selected model name.
	if !strings.Contains(joined, "Model Name:") {
		t.Error("missing model name detail")
	}

	// 0.87.1 marks enabled models with a "✓ " prefix column; disabled rows
	// keep the column blank.
	plain := stripANSI(joined)
	if !strings.Contains(plain, "→ ✓ gpt-4o [openai]") || !strings.Contains(plain, "  ✓ claude-sonnet [anthropic]") {
		t.Errorf("missing ✓ prefix for enabled models:\n%s", plain)
	}
	if !strings.Contains(plain, "    gpt-4o-mini [openai]") || strings.Contains(plain, "✗") {
		t.Errorf("disabled rows should have a blank marker column:\n%s", plain)
	}
}

func TestScopedModels_SaveDoesNotCloseAndClearsDirty(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       testModels(),
		EnabledModelIDs: []string{"openai/gpt-4o"},
	})

	sl.HandleInput("\r")
	if !strings.Contains(strings.Join(sl.Render(80), "\n"), "(unsaved)") {
		t.Fatal("toggle should mark selector dirty before save")
	}

	sl.HandleInput("\x13")

	if sl.Done() {
		t.Fatal("Ctrl+S should save without closing the selector")
	}
	ids, ok := sl.ConsumeSave()
	if !ok {
		t.Fatal("Ctrl+S did not emit a save event")
	}
	if !slices.Equal(ids, []string{}) {
		t.Errorf("unexpected saved enabledIDs: %v", ids)
	}
	if strings.Contains(strings.Join(sl.Render(80), "\n"), "(unsaved)") {
		t.Fatal("save should clear dirty marker")
	}
	if _, ok := sl.ConsumeSave(); ok {
		t.Fatal("save event should be consumed once")
	}
}

func TestScopedModels_SaveRecognizesCtrlSEncodings(t *testing.T) {
	for _, input := range []string{"\x13", "\x1b[115;5u", "\x1b[27;5;115~"} {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			sl := NewScopedModelsList(ScopedModelsConfig{
				AllModels:       testModels(),
				EnabledModelIDs: []string{"openai/gpt-4o"},
			})

			sl.HandleInput(input)

			if sl.Done() {
				t.Fatal("save should not close selector")
			}
			ids, ok := sl.ConsumeSave()
			if !ok {
				t.Fatal("save encoding did not emit save event")
			}
			if !slices.Equal(ids, []string{"openai/gpt-4o"}) {
				t.Errorf("saved ids = %v", ids)
			}
		})
	}
}

func TestScopedModels_UsesConfiguredActionBindings(t *testing.T) {
	previous := GetTUIKeybindings()
	t.Cleanup(func() { SetTUIKeybindings(previous) })
	SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{
		"app.models.clearAll": {"ctrl+r"},
	}))
	sl := NewScopedModelsList(ScopedModelsConfig{AllModels: testModels()})
	sl.HandleInput("\x18")
	if ids := sl.EnabledIDs(); ids != nil {
		t.Fatalf("default clear binding remained active after override: %v", ids)
	}
	sl.HandleInput("\x12")
	if ids := sl.EnabledIDs(); ids == nil || len(ids) != 0 {
		t.Fatalf("configured clear binding left enabled IDs %v", ids)
	}
}

func TestScopedModels_SearchAcceptsKittyAndPasteInput(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{AllModels: testModels()})
	sl.HandleInput("\x1b[103u")
	sl.HandleInput("\x1b[200~emini\x1b[201~")
	joined := stripANSI(strings.Join(sl.Render(80), "\n"))
	if !strings.Contains(joined, "> gemini") || !strings.Contains(joined, "gemini-pro") {
		t.Fatalf("encoded search input:\n%s", joined)
	}
}

func TestScopedModels_CancelEsc(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels:       testModels(),
		EnabledModelIDs: nil,
	})

	sl.HandleInput("\x1b") // Esc

	if !sl.Done() {
		t.Fatal("should be done after Esc")
	}
	if !sl.Result().Cancelled {
		t.Error("result should be cancelled")
	}
}

// scopedMarkerStates ports getMarkerStates from scoped-models-selector.test.ts.
func scopedMarkerStates(t *testing.T, sl *ScopedModelsList, ids []string) []bool {
	t.Helper()
	lines := strings.Split(stripANSI(strings.Join(sl.Render(120), "\n")), "\n")
	out := make([]bool, len(ids))
	for i, id := range ids {
		found := false
		for _, line := range lines {
			if strings.Contains(line, id+" [") {
				out[i] = strings.HasPrefix(string([]rune(line)[2:]), "✓ ")
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected rendered row for %s", id)
		}
	}
	return out
}

func scopedFixture(enabled ...string) *ScopedModelsList {
	models := []ModelItem{
		{FullID: "p/model-a", Name: "Model A", Provider: "p"},
		{FullID: "p/model-b", Name: "Model B", Provider: "p"},
		{FullID: "p/model-c", Name: "Model C", Provider: "p"},
	}
	return NewScopedModelsList(ScopedModelsConfig{AllModels: models, EnabledModelIDs: enabled})
}

func enabledStates(sl *ScopedModelsList) []bool {
	out := []bool{}
	for _, id := range []string{"p/model-a", "p/model-b", "p/model-c"} {
		out = append(out, sl.isEnabled(id))
	}
	return out
}

// Ports scoped-models-selector.test.ts (Pi 0.87.1).
func TestScopedModels_UpstreamToggleCases(t *testing.T) {
	ids := []string{"model-a", "model-b", "model-c"}
	check := func(t *testing.T, sl *ScopedModelsList, want []bool) {
		t.Helper()
		if got := enabledStates(sl); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("enabled = %v, want %v", got, want)
		}
		if got := scopedMarkerStates(t, sl, ids); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("markers = %v, want %v", got, want)
		}
	}
	t.Run("marks every model after enabling all", func(t *testing.T) {
		sl := scopedFixture("p/model-a")
		sl.HandleInput("\x01")
		check(t, sl, []bool{true, true, true})
		if !strings.Contains(stripANSI(strings.Join(sl.Render(120), "\n")), "all enabled") {
			t.Fatal("footer does not say all enabled")
		}
	})
	t.Run("disables only the selected model after enabling all", func(t *testing.T) {
		sl := scopedFixture("p/model-a")
		sl.HandleInput("\x01")
		sl.HandleInput("\r")
		check(t, sl, []bool{false, true, true})
	})
	t.Run("enables only the selected model after clearing all", func(t *testing.T) {
		sl := scopedFixture("p/model-a", "p/model-b", "p/model-c")
		sl.HandleInput("\x18")
		check(t, sl, []bool{false, false, false})
		sl.HandleInput("\r")
		check(t, sl, []bool{true, false, false})
	})
	t.Run("restores the all-enabled state after re-enabling the last disabled model", func(t *testing.T) {
		sl := scopedFixture("p/model-a")
		sl.HandleInput("\x01")
		sl.HandleInput("\r")
		sl.HandleInput("\x1b[B")
		sl.HandleInput("\x1b[B")
		sl.HandleInput("\r")
		check(t, sl, []bool{true, true, true})
		if sl.EnabledIDs() != nil || !strings.Contains(stripANSI(strings.Join(sl.Render(120), "\n")), "all enabled") {
			t.Fatalf("re-enabling the last model did not restore all enabled: %v", sl.EnabledIDs())
		}
	})
}

// Pi 0.87.1 renders the header and footer keys with keyDisplayText
// (capitalized) and frames the selector with DynamicBorders
// (model-resolver-selector/08 probe).
func TestScopedModels_HeaderAndFrameMatchUpstream(t *testing.T) {
	sl := NewScopedModelsList(ScopedModelsConfig{
		AllModels: []ModelItem{
			{FullID: "fixture/model-two", Name: "Model Two", Provider: "fixture"},
			{FullID: "fixture/model-one", Name: "Model One", Provider: "fixture"},
		},
		RefreshStatus: "Model catalogs refreshed.",
	})
	var plain []string
	for _, line := range sl.Render(100) {
		plain = append(plain, strings.TrimRight(stripANSI(line), " "))
	}
	want := []string{
		strings.Repeat("─", 100),
		"",
		"Model Configuration",
		"Session-only. Ctrl+S to save to settings.",
		"",
		">",
		"",
		"→ ✓ model-two [fixture]",
		"  ✓ model-one [fixture]",
		"",
		"  Model Name: Model Two",
		"",
		"  Model catalogs refreshed.",
	}
	if got := strings.Join(plain[:len(want)], "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("render =\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
	footer := strings.Join(plain[len(want):len(plain)-1], " ")
	for _, part := range []string{"Enter toggle", "Ctrl+A all", "Ctrl+X clear", "Ctrl+P provider", "Ctrl+S save", "all enabled"} {
		if !strings.Contains(footer, part) {
			t.Fatalf("footer %q lacks %q", footer, part)
		}
	}
	if plain[len(plain)-1] != strings.Repeat("─", 100) {
		t.Fatalf("last row = %q, want the bottom border", plain[len(plain)-1])
	}
}
