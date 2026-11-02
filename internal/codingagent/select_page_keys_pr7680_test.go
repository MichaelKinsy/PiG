package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Remapped selection page keys (Pi #7629) for the standalone selectors in this package: the SelectList-backed thinking selector pages,
// the trust selector and first-time setup have no page keys in Pi and ignore them. The selectors in package tui are in
// tui/select_page_keys_upstream_test.go.

var remappedPageKeys = map[string][]KeyID{
	"tui.editor.pageUp":   {"alt+v"},
	"tui.editor.pageDown": {"ctrl+v"},
	"tui.select.pageUp":   {"alt+v"},
	"tui.select.pageDown": {"ctrl+v"},
}

func useRemappedPageKeyManager(t *testing.T) *KeybindingsManager {
	t.Helper()
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	return NewKeybindingsManagerFromBindings(remappedPageKeys, "")
}

// The "pages through SelectList-backed selectors" thinking case, and the trust and first-time-setup components, which Pi does not page.
func TestPR7680PackageSelectorsPageWithRemappedKeys(t *testing.T) {
	useRemappedPageKeyManager(t)
	var trust *TrustSelection
	project := t.TempDir()
	trustSelector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: project, OnSelect: func(s TrustSelection) { trust = &s }, OnCancel: func() {}})
	trustSelector.HandleInput("\x16")
	trustSelector.HandleInput("\r")
	first := trustSelector.trustOptions[0]
	if trust == nil || trust.Trusted != first.Trusted || len(trust.Updates) != len(first.Updates) {
		t.Errorf("trust selection = %+v, want the first option %+v: the trust selector has no page keys", trust, first)
	}

	var previews []string
	setup := NewFirstTimeSetupComponent(FirstTimeSetupOptions{OnThemePreview: func(name string) { previews = append(previews, name) }})
	setup.HandleInput("\x16")
	if len(previews) != 0 || setup.themeIndex != 0 {
		t.Errorf("theme previews = %v, index %d, want none and 0: first-time setup has no page keys", previews, setup.themeIndex)
	}

	var thinking ai.ThinkingLevel
	levels := []ai.ThinkingLevel{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	thinkingSelector := NewThinkingSelectorComponent("off", levels, func(level ai.ThinkingLevel) { thinking = level }, func() {}, nil, "")
	thinkingSelector.GetSelectList().HandleInput("\x16")
	thinkingSelector.GetSelectList().HandleInput("\r")
	if thinking != "max" {
		t.Errorf("thinking selector selected %q, want max", thinking)
	}
}
