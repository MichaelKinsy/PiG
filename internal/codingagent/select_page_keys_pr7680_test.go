package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Remapped selection page keys (Pi #7629) for the standalone selectors in this package that Pi does not flag with capturesViewportInput
// (D155). The selectors in package tui are in tui/select_page_keys_pr7680_test.go.

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

// The trust case of "pages through standalone selectors with remapped keys" and the first-time-setup and thinking cases of
// "pages through SelectList-backed selectors".
func TestPR7680PackageSelectorsPageWithRemappedKeys(t *testing.T) {
	useRemappedPageKeyManager(t)
	var trust *TrustSelection
	project := t.TempDir()
	trustSelector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: project, OnSelect: func(s TrustSelection) { trust = &s }, OnCancel: func() {}})
	trustSelector.HandleInput("\x16")
	trustSelector.HandleInput("\r")
	if trust == nil || trust.Trusted || len(trust.Updates) != 1 || trust.Updates[0].Path != CanonicalizePath(project) || trust.Updates[0].Decision == nil || *trust.Updates[0].Decision {
		t.Errorf("trust selection = %+v, want untrusted with %s decided false", trust, project)
	}

	var previews []string
	setup := NewFirstTimeSetupComponent(FirstTimeSetupOptions{OnThemePreview: func(name string) { previews = append(previews, name) }})
	setup.HandleInput("\x16")
	if len(previews) == 0 || previews[len(previews)-1] != "light" {
		t.Errorf("theme previews = %v, want light last", previews)
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
