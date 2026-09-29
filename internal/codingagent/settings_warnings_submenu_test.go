package codingagent

import (
	"strings"
	"testing"
	"time"
)

// Upstream's Warnings row opens WarningSettingsSubmenu, a SettingsList with
// Math.min(items.length, 10) rows and no search (settings-selector.ts), so
// its hint is "Enter/Space to change · Esc to cancel" and it has no search
// line. Changing its row saves at once; Esc returns to the main list.
func TestSettingsWarningsSubmenuHasNoSearch(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	m.opts.SettingsManager = NewSettingsManager(t.TempDir(), t.TempDir())
	sc := m.buildSlashContext(t.Context())
	done := make(chan error, 1)
	go func() { done <- settingsHandler(sc) }()
	for _, key := range []string{"W", "a", "r", "n", "i", "n", "g", "s", "\r", "\x1b[C"} {
		sendModalKey(t, m, key)
	}
	submenu := stripANSI(strings.Join(m.editorContainer.Render(100), "\n"))
	if !strings.Contains(submenu, "Anthropic extra usage") {
		t.Fatalf("the Warnings submenu did not open:\n%s", submenu)
	}
	if strings.Contains(submenu, "Type to search") || strings.Contains(submenu, "\n> ") || strings.HasPrefix(strings.TrimLeft(submenu, "─\n"), "> ") {
		t.Fatalf("the Warnings submenu has a search input:\n%s", submenu)
	}
	if !strings.Contains(submenu, "Enter/Space to change · Esc to cancel") {
		t.Fatalf("the Warnings submenu lacks upstream's hint:\n%s", submenu)
	}
	before := m.opts.SettingsManager.GetWarnings().AnthropicExtraUsage
	sendModalKey(t, m, "\r")
	sendModalKey(t, m, "\x1b[C")
	after := m.opts.SettingsManager.GetWarnings().AnthropicExtraUsage
	if after == before {
		t.Fatalf("the Warnings change was not saved: still %v", after)
	}
	if row := stripANSI(strings.Join(m.editorContainer.Render(100), "\n")); !strings.Contains(row, "Anthropic extra usage  "+warningBoolString(after)) {
		t.Fatalf("the Warnings row does not show the saved value %v:\n%s", after, row)
	}
	sendModalKey(t, m, "\x1b")
	sendModalKey(t, m, "\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Esc did not close /settings")
	}
}
