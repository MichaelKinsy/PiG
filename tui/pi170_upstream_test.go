package tui

import (
	"strings"
	"testing"
)

// Ports packages/tui/test/terminal-image.test.ts "allocates IDs that fit a signed 32-bit integer" (Pi ed0f27d62).
func TestAllocateImageID_Signed32BitRange(t *testing.T) {
	for range 1000 {
		id := AllocateImageID()
		if id < 1 || id > 0x7fffffff {
			t.Fatalf("AllocateImageID() = %d, want in [1, 0x7fffffff]", id)
		}
	}
}

// Ports packages/tui/test/settings-list.test.ts "names the configured select keys in the hint" (Pi b882d08f3).
func TestSettingsList_NamesConfiguredSelectKeysInHint(t *testing.T) {
	prev := GetKeybindings()
	t.Cleanup(func() { SetKeybindings(prev) })
	SetKeybindings(NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{
		KBSelectConfirm: {"ctrl+y"},
		KBSelectCancel:  {"ctrl+q"},
	}))

	list := NewSettingsList(
		[]SettingItem{{ID: "a", Label: "Setting 1", CurrentValue: "on", Values: []string{"on", "off"}}},
		10,
		SettingsListTheme{
			Label: func(text string, _ bool) string { return text },
			Value: func(text string, _ bool) string { return text },
			Hint:  func(text string) string { return text },
		},
		func(string, string) {},
		func() {},
	)
	joined := strings.Join(list.Render(80), "\n")
	if !strings.Contains(joined, "ctrl+y/space to change · ctrl+q to cancel") {
		t.Fatalf("hint = %q, want ctrl+y/space to change · ctrl+q to cancel", joined)
	}
}

// Ports packages/coding-agent/test/settings-submenu.test.ts "names the configured select keys" (Pi b882d08f3).
func TestSelectSubmenu_NamesConfiguredSelectKeys(t *testing.T) {
	prev := GetKeybindings()
	t.Cleanup(func() { SetKeybindings(prev) })
	SetKeybindings(NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{
		KBSelectConfirm: {"ctrl+y"},
		KBSelectCancel:  {"ctrl+q"},
	}))

	submenu := NewSelectSubmenu("Theme", "", []SelectItem{{Value: "dark", Label: "dark"}}, "dark")
	joined := strings.Join(submenu.Render(120), "\n")
	if !strings.Contains(joined, "ctrl+y to select · ctrl+q to go back") {
		t.Fatalf("submenu render = %q, want ctrl+y to select · ctrl+q to go back", joined)
	}
}

// Ports packages/coding-agent/test/bash-execution-width.test.ts "BashExecutionComponent cancel hint" (Pi 745338811).
func TestBashExecutionComponent_CancelHintNamesInterruptKey(t *testing.T) {
	prev := GetKeybindings()
	t.Cleanup(func() { SetKeybindings(prev) })

	SetKeybindings(NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{"app.interrupt": {"escape"}}))
	comp := NewBashExecutionComponent("sleep 30", nil, false, 0)
	if joined := strings.Join(comp.Render(120), "\n"); !strings.Contains(joined, "Running... (escape to cancel)") {
		t.Fatalf("default hint = %q, want Running... (escape to cancel)", joined)
	}

	SetKeybindings(NewKeybindingsManager(TUIKeybindings, KeybindingsConfig{"app.interrupt": {"ctrl+g"}}))
	comp = NewBashExecutionComponent("sleep 30", nil, false, 0)
	if joined := strings.Join(comp.Render(120), "\n"); !strings.Contains(joined, "Running... (ctrl+g to cancel)") {
		t.Fatalf("remapped hint = %q, want Running... (ctrl+g to cancel)", joined)
	}
}
