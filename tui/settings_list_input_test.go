package tui

import (
	"strings"
	"testing"
)

func settingsInputItems() []SettingItem {
	return []SettingItem{
		{ID: "a", Label: "Alpha mode", CurrentValue: "off", Values: []string{"off", "on"}},
		{ID: "b", Label: "Beta mode", CurrentValue: "off", Values: []string{"off", "on"}},
		{ID: "c", Label: "Gamma mode", CurrentValue: "off", Values: []string{"off", "on"}},
	}
}

func withBindings(t *testing.T, overrides map[string][]string) {
	t.Helper()
	previous := GetKeybindings()
	SetKeybindings(NewTUIKeybindingsManager(overrides))
	t.Cleanup(func() { SetKeybindings(previous) })
}

// settings-list.ts handleInput delegates every key the list does not own to the search Input and then refilters, so the Input's
// editing keys work and the selection returns to the first match.
func TestSettingsListSearchDelegatesEditingKeysToTheInput(t *testing.T) {
	list := NewSettingsList(settingsInputItems(), 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
	for _, key := range []string{"m", "o", "d", "e"} {
		list.HandleInput(key)
	}
	list.HandleInput("\x1b[D") // left
	list.HandleInput("\x1b[D")
	list.HandleInput("X")
	if got := list.searchInput.GetValue(); got != "moXde" {
		t.Fatalf("cursor movement then insert gave %q, want moXde", got)
	}
	list.HandleInput("\x17") // ctrl+w deletes the word before the cursor
	if got := list.searchInput.GetValue(); got != "de" {
		t.Fatalf("ctrl+w gave %q, want de", got)
	}
	list.HandleInput("\x1b[200~ma\r\nx\x1b[201~") // bracketed paste strips line breaks
	if got := list.searchInput.GetValue(); got != "max"+"de" {
		t.Fatalf("paste gave %q, want maxde", got)
	}
}

func TestSettingsListAnyDelegatedKeyResetsTheSelectionToTheFirstMatch(t *testing.T) {
	list := NewSettingsList(settingsInputItems(), 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
	list.HandleInput("m")
	list.HandleInput("\x1b[B")
	list.HandleInput("\x1b[B")
	if list.cursor != 2 {
		t.Fatalf("down twice put the cursor at %d, want 2", list.cursor)
	}
	list.HandleInput("\x1b[D") // an Input key that edits nothing still refilters
	if list.cursor != 0 {
		t.Fatalf("a delegated key left the cursor at %d, want 0 (applyFilter)", list.cursor)
	}
}

// Up and down are tested before confirm/space, and confirm before cancel (settings-list.ts handleInput order), so a key bound to
// two of them runs the earlier one.
func TestSettingsListBoundActionsRunInUpstreamOrder(t *testing.T) {
	t.Run("confirm before cancel", func(t *testing.T) {
		withBindings(t, map[string][]string{KBSelectCancel: {"ctrl+s"}, KBSelectConfirm: {"ctrl+s"}})
		cancels := 0
		list := NewSettingsList(settingsInputItems(), 10, GetSettingsListTheme(), func(string, string) {}, func() { cancels++ }, SettingsListOptions{EnableSearch: true})
		list.HandleInput("\x13")
		if cancels != 0 || list.Items()[0].CurrentValue != "on" {
			t.Fatalf("ctrl+s bound to confirm and cancel: cancels %d, value %q (confirm must win)", cancels, list.Items()[0].CurrentValue)
		}
	})
	t.Run("down before confirm", func(t *testing.T) {
		withBindings(t, map[string][]string{KBSelectDown: {"ctrl+s"}, KBSelectConfirm: {"ctrl+s"}})
		list := NewSettingsList(settingsInputItems(), 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
		list.HandleInput("\x13")
		if list.cursor != 1 || list.Items()[0].CurrentValue != "off" {
			t.Fatalf("ctrl+s bound to down and confirm: cursor %d, value %q (down must win)", list.cursor, list.Items()[0].CurrentValue)
		}
	})
	t.Run("up before cancel", func(t *testing.T) {
		withBindings(t, map[string][]string{KBSelectUp: {"ctrl+s"}, KBSelectCancel: {"ctrl+s"}})
		list := NewSettingsList(settingsInputItems(), 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
		list.HandleInput("\x13")
		if list.Cancelled() || list.cursor != 2 {
			t.Fatalf("ctrl+s bound to up and cancel: cancelled %v cursor %d (up must win and wrap)", list.Cancelled(), list.cursor)
		}
	})
}

func TestSettingsListEmptyAndSingletonNavigationBoundaries(t *testing.T) {
	empty := NewSettingsList(nil, 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
	empty.HandleInput("\x1b[A")
	empty.HandleInput("\x1b[B")
	empty.HandleInput("\r")
	empty.HandleInput(" ")
	if empty.Done() || empty.cursor != 0 {
		t.Fatalf("empty list moved or finished: done %v cursor %d", empty.Done(), empty.cursor)
	}
	single := NewSettingsList(settingsInputItems()[:1], 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
	single.HandleInput("\x1b[A")
	single.HandleInput("\x1b[B")
	if single.cursor != 0 {
		t.Fatalf("singleton wrapped to %d", single.cursor)
	}
	list := NewSettingsList(settingsInputItems(), 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: true})
	list.HandleInput("\x1b[A")
	if list.cursor != 2 {
		t.Fatalf("up from the first row went to %d, want the last (2)", list.cursor)
	}
	list.HandleInput("\x1b[B")
	if list.cursor != 0 {
		t.Fatalf("down from the last row went to %d, want the first", list.cursor)
	}
}

func TestSettingsListWithoutSearchIgnoresOtherKeysAndKeepsSelection(t *testing.T) {
	list := NewSettingsList(settingsInputItems(), 10, GetSettingsListTheme(), nil, nil, SettingsListOptions{EnableSearch: false})
	list.HandleInput("\x1b[B")
	list.HandleInput("x")
	list.HandleInput("\x1b[D")
	if list.cursor != 1 || list.searchInput != nil || strings.Contains(strings.Join(list.Render(60), "\n"), "> ") {
		t.Fatalf("a non-search list reacted to unowned keys: cursor %d", list.cursor)
	}
}

func TestSettingsListSubmenuReceivesEveryKeyIncludingCancel(t *testing.T) {
	var received []string
	var done func(*string, *SubmenuDoneOptions)
	items := []SettingItem{{ID: "s", Label: "Sub", CurrentValue: "v", Submenu: func(_ string, d func(*string, *SubmenuDoneOptions)) Component {
		done = d
		return &recordingInputComponent{keys: &received}
	}}}
	cancels := 0
	list := NewSettingsList(items, 10, GetSettingsListTheme(), func(string, string) {}, func() { cancels++ }, SettingsListOptions{EnableSearch: true})
	list.HandleInput("\r")
	list.HandleInput("\x1b")
	list.HandleInput("j")
	if cancels != 0 || len(received) != 2 || received[0] != "\x1b" {
		t.Fatalf("submenu got %q, list cancels %d (every key, escape included, belongs to the submenu)", received, cancels)
	}
	done(nil, nil)
	list.HandleInput("\x1b")
	if cancels != 1 {
		t.Fatalf("escape after the submenu closed cancelled %d times, want 1", cancels)
	}
}

type recordingInputComponent struct {
	Container
	keys *[]string
}

func (r *recordingInputComponent) HandleInput(data string) { *r.keys = append(*r.keys, data) }
