package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's setupAutocompleteProvider hands the rebuilt provider to the installed editor on every rebuild, with no wrapper registered
// (interactive-mode.ts:861-863), and setCustomEditorComponent hands the editor it installs the current one whenever there is one
// (interactive-mode.ts:3015-3017). The owner loop runs the editor install and the provider install that ends the bind in either order
// (issue 497): the subprocess editor hears the provider install when the editor was installed first, and when the provider was built first
// the install of the editor is what gives it the provider. An extension's process builds its editor while session_start runs, when the
// host answers that it has no provider, so nothing but these two paths ever reaches it.
func TestInstalledSubprocessEditorGetsTheProviderInEitherOrder(t *testing.T) {
	provider := func(t *testing.T) tui.AutocompleteProvider {
		return tui.NewCombinedProvider([]tui.SlashCommand{{Name: "keep"}}, t.TempDir(), "")
	}
	heard := func(bridge *autocompleteBridgeProbe) int {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return len(bridge.providers)
	}
	t.Run("install first", func(t *testing.T) {
		m := autocompleteModeProbe(t)
		bridge := &autocompleteBridgeProbe{captureUIBridge: &captureUIBridge{}}
		m.opts.SubprocessUIBridge = bridge
		m.editor.SetAutocomplete(nil)
		m.setRemoteEditor(&fakeDelegatedEditor{})
		drainAutocompleteWork(t, m)
		if got := heard(bridge); got != 0 {
			t.Fatalf("the editor was given %d providers before there was one; want 0", got)
		}
		m.editor.SetAutocomplete(provider(t))
		drainAutocompleteWork(t, m)
		if got := heard(bridge); got != 1 {
			t.Fatalf("the editor was given %d providers; want 1: the one built after it", got)
		}
	})
	t.Run("provider first", func(t *testing.T) {
		m := autocompleteModeProbe(t)
		bridge := &autocompleteBridgeProbe{captureUIBridge: &captureUIBridge{}}
		m.opts.SubprocessUIBridge = bridge
		m.editor.SetAutocomplete(provider(t))
		drainAutocompleteWork(t, m)
		if got := heard(bridge); got != 0 {
			t.Fatalf("the editor heard %d providers before it was installed; want 0", got)
		}
		m.setRemoteEditor(&fakeDelegatedEditor{})
		drainAutocompleteWork(t, m)
		if got := heard(bridge); got != 1 {
			t.Fatalf("the editor was given %d providers when it was installed; want 1", got)
		}
		m.editor.SetAutocomplete(provider(t))
		drainAutocompleteWork(t, m)
		if got := heard(bridge); got != 2 {
			t.Fatalf("the editor was given %d providers after the next rebuild; want 2", got)
		}
	})
}
