package tui

import (
	"strings"
	"testing"
)

func TestSettingsListRendersTwoColumns(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "Auto-compact", Description: "Compact context automatically", CurrentValue: "true", Values: []string{"true", "false"}},
		{ID: "b", Label: "Theme", Description: "Color theme", CurrentValue: "dark", Values: []string{"auto", "dark", "light"}},
	}
	sl := NewSettingsList(items)
	lines := sl.Render(80)

	// Should have the upstream-style search input line.
	if !strings.HasPrefix(stripANSI(lines[0]), "> ") {
		t.Errorf("first line should be search input, got: %q", lines[0])
	}

	// Should show both labels.
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Auto-compact") {
		t.Error("missing 'Auto-compact' label")
	}
	if !strings.Contains(joined, "Theme") {
		t.Error("missing 'Theme' label")
	}

	// Should show values in separate column (not [bracketed]).
	if strings.Contains(joined, "[true]") {
		t.Error("value should not be [bracketed]: should be plain 'true' in right column")
	}
	if !strings.Contains(joined, "true") {
		t.Error("missing 'true' value")
	}
	if !strings.Contains(joined, "dark") {
		t.Error("missing 'dark' value")
	}
}

// Upstream settings-list.ts aligns values after min(36, widest label).
func TestSettingsListUsesThirtySixCellLabelCap(t *testing.T) {
	valueColumn := func(label string) int {
		items := []SettingItem{
			{ID: "a", Label: label, CurrentValue: "long", Values: []string{"long"}},
			{ID: "b", Label: "Short", CurrentValue: "value", Values: []string{"value"}},
		}
		for _, line := range NewSettingsList(items).Render(100) {
			if plain := stripANSI(line); strings.Contains(plain, "Short") {
				return strings.Index(plain, "value")
			}
		}
		t.Fatal("short settings row not rendered")
		return -1
	}
	// "Default thinking level per model" (32 cells) sets the 0.87.1 column.
	if got := valueColumn("Default thinking level per model"); got != 36 {
		t.Fatalf("value column = %d, want 36 for a 32-cell label", got)
	}
	if got := valueColumn(strings.Repeat("x", 40)); got != 40 {
		t.Fatalf("value column = %d, want 40 for the 36-cell label cap", got)
	}
}

// Nested upstream settings menus (warnings, automatic theme) use
// SettingsList without enableSearch: no input row and the short hint.
func TestSettingsListWithoutSearch(t *testing.T) {
	items := []SettingItem{
		{ID: "light", Label: "Light theme", CurrentValue: "light", Values: []string{"light", "dark"}},
		{ID: "apply", Label: "Apply", CurrentValue: "save and go back", Values: []string{"save and go back"}},
	}
	sl := NewSettingsListWithOptions(items, 2, false)
	var plain []string
	for _, line := range sl.Render(80) {
		plain = append(plain, strings.TrimRight(stripANSI(line), " "))
	}
	want := []string{
		"→ Light theme  light",
		"  Apply        save and go back",
		"",
		"  Enter/Space to change · Esc to cancel",
	}
	if strings.Join(plain, "\n") != strings.Join(want, "\n") {
		t.Fatalf("render =\n%s\nwant\n%s", strings.Join(plain, "\n"), strings.Join(want, "\n"))
	}
	sl.HandleInput("x")
	sl.HandleInput(" ")
	if !sl.Done() || sl.ChangedID != "light" || sl.ChangedValue != "dark" {
		t.Fatalf("space without search should change the value: done=%v %s=%s", sl.Done(), sl.ChangedID, sl.ChangedValue)
	}
}

// Upstream sends Space to a non-empty search and resets the selection to
// the first match whenever the query changes.
func TestSettingsListSpaceTypesIntoNonEmptySearch(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "Editor padding", CurrentValue: "0", Values: []string{"0", "1"}},
		{ID: "b", Label: "Output padding", CurrentValue: "1", Values: []string{"0", "1"}},
	}
	sl := NewSettingsList(items)
	sl.HandleInput("\x1b[B")
	sl.HandleInput("p")
	sl.HandleInput(" ")
	if sl.Done() {
		t.Fatal("space in a non-empty search changed a value")
	}
	joined := stripANSI(strings.Join(sl.Render(80), "\n"))
	if !strings.Contains(joined, "> p ") || !strings.Contains(joined, "→ Editor padding") {
		t.Fatalf("search did not take the space or keep the first match selected:\n%s", joined)
	}
}

func TestSettingsListShowsDescription(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "Auto-compact", Description: "Compact context automatically", CurrentValue: "true", Values: []string{"true", "false"}},
	}
	sl := NewSettingsList(items)
	lines := sl.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Compact context automatically") {
		t.Errorf("expected description in output, got:\n%s", joined)
	}
}

func TestSettingsListShowsHintLine(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "Foo", CurrentValue: "bar", Values: []string{"bar", "baz"}},
	}
	sl := NewSettingsList(items)
	lines := sl.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Type to search") {
		t.Error("missing hint line")
	}
	if !strings.Contains(joined, "Enter/Space to change") {
		t.Error("missing Enter/Space hint")
	}
	if !strings.Contains(joined, "Esc to cancel") {
		t.Error("missing Esc hint")
	}
}

func TestSettingsListCyclesValue(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "Toggle", CurrentValue: "off", Values: []string{"off", "on"}},
	}
	sl := NewSettingsList(items)

	// Press Enter to cycle.
	sl.HandleInput("\r")
	if !sl.Done() {
		t.Fatal("expected Done after Enter")
	}
	if sl.Cancelled() {
		t.Fatal("should not be cancelled")
	}
	if sl.ChangedID != "a" {
		t.Errorf("expected ChangedID='a', got %q", sl.ChangedID)
	}
	if sl.ChangedValue != "on" {
		t.Errorf("expected ChangedValue='on', got %q", sl.ChangedValue)
	}
}

func TestSettingsListEscCancels(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "Foo", CurrentValue: "bar", Values: []string{"bar"}},
	}
	sl := NewSettingsList(items)
	sl.HandleInput("\x1b")
	if !sl.Done() {
		t.Fatal("expected Done after Esc")
	}
	if !sl.Cancelled() {
		t.Fatal("expected Cancelled after Esc")
	}
}

func TestSettingsListSpaceCyclesValue(t *testing.T) {
	items := []SettingItem{
		{ID: "x", Label: "Mode", CurrentValue: "a", Values: []string{"a", "b", "c"}},
	}
	sl := NewSettingsList(items)
	sl.HandleInput(" ") // Space
	if sl.ChangedValue != "b" {
		t.Errorf("expected 'b' after Space, got %q", sl.ChangedValue)
	}
}

func TestSettingsListFilterReducesItems(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "Auto-compact", CurrentValue: "true", Values: []string{"true", "false"}},
		{ID: "b", Label: "Theme", CurrentValue: "dark", Values: []string{"dark", "light"}},
		{ID: "c", Label: "Thinking", CurrentValue: "off", Values: []string{"off", "high"}},
	}
	sl := NewSettingsList(items)
	// Type "the" to filter.
	sl.HandleInput("t")
	sl.HandleInput("h")
	sl.HandleInput("e")

	lines := sl.Render(80)
	joined := strings.Join(lines, "\n")
	// Should show Theme (contains "the"), not Auto-compact or Thinking.
	if !strings.Contains(joined, "Theme") {
		t.Error("expected Theme in filtered results")
	}
	if strings.Contains(joined, "Auto-compact") {
		t.Error("Auto-compact should be filtered out")
	}
}

func TestSettingsListShowsScrollCounter(t *testing.T) {
	// Create more items than maxRows (10).
	items := make([]SettingItem, 15)
	for i := range items {
		items[i] = SettingItem{
			ID: strings.Repeat("x", i+1), Label: strings.Repeat("Item", i+1),
			CurrentValue: "v", Values: []string{"v"},
		}
	}
	sl := NewSettingsList(items)
	lines := sl.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "(1/15)") {
		t.Errorf("expected scroll counter (1/15), got:\n%s", joined)
	}
}

func TestSettingsListCursorOnFirstItem(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "First", CurrentValue: "v1", Values: []string{"v1"}},
		{ID: "b", Label: "Second", CurrentValue: "v2", Values: []string{"v2"}},
	}
	sl := NewSettingsList(items)
	lines := sl.Render(80)
	// The first item row should contain the upstream-style cursor marker "→".
	found := false
	for _, l := range lines {
		if strings.Contains(l, "First") && strings.Contains(l, "→") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected cursor (→) on first item, got:\n%s", strings.Join(lines, "\n"))
	}
}

func TestSettingsListUnselectedValuesUseMutedColor(t *testing.T) {
	items := []SettingItem{
		{ID: "a", Label: "Selected", CurrentValue: "on", Values: []string{"on", "off"}},
		{ID: "b", Label: "Theme", CurrentValue: "dark", Values: []string{"dark", "light"}},
	}
	sl := NewSettingsList(items)
	lines := sl.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, ActiveTheme().Muted+"dark") {
		t.Fatalf("expected unselected value to use muted color %q, got:\n%s", ActiveTheme().Muted, joined)
	}
	if strings.Contains(joined, ActiveTheme().Dim+"dark") {
		t.Fatalf("unselected value should not use dim color %q, got:\n%s", ActiveTheme().Dim, joined)
	}
}
