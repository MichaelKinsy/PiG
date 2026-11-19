package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's setupAutocompleteProvider hands the rebuilt provider to the installed editor on every rebuild, with no wrapper registered
// (interactive-mode.ts:861-867), and setCustomEditorComponent hands the editor it installs the current one whenever there is one
// (interactive-mode.ts:3015-3017). The owner loop runs the editor install and the provider the bind's setup builds in either order (issue 497). An editor
// installed first hears the provider the setup builds. An editor installed after it was built by the extension's process, which asked for the provider
// while there was none (the host answered {}), so the host hands it the provider that exists when it installs the editor, once; an editor whose process
// was handed the provider when it built it has had its one call, and the host adds none (scenario 19 counts them against Pi's).
func TestInstalledSubprocessEditorGetsTheProviderInEitherOrder(t *testing.T) {
	provider := func(t *testing.T) tui.AutocompleteProvider {
		return tui.NewCombinedProvider([]tui.SlashCommand{{Name: "keep"}}, t.TempDir(), "")
	}
	heard := func(bridge *autocompleteBridgeProbe) int {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return len(bridge.providers)
	}
	newMode := func(t *testing.T, answeredPending bool) (*InteractiveMode, *autocompleteBridgeProbe) {
		m := autocompleteModeProbe(t)
		bridge := &autocompleteBridgeProbe{captureUIBridge: &captureUIBridge{}, pending: answeredPending}
		m.opts.SubprocessUIBridge = bridge
		return m, bridge
	}
	t.Run("install first", func(t *testing.T) {
		m, bridge := newMode(t, true)
		m.setRemoteEditor(&fakeDelegatedEditor{})
		drainAutocompleteWork(t, m)
		if got := heard(bridge); got != 0 {
			t.Fatalf("the editor was given %d providers before the setup built one; want 0", got)
		}
		m.editor.SetAutocomplete(provider(t))
		drainAutocompleteWork(t, m)
		if got := heard(bridge); got != 1 {
			t.Fatalf("the editor was given %d providers after the setup built one; want 1", got)
		}
	})
	t.Run("provider first, editor built without one", func(t *testing.T) {
		m, bridge := newMode(t, true)
		m.autocompleteSetUp = true
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
	t.Run("provider first, editor built with it", func(t *testing.T) {
		m, bridge := newMode(t, false)
		m.autocompleteSetUp = true
		m.editor.SetAutocomplete(provider(t))
		drainAutocompleteWork(t, m)
		m.setRemoteEditor(&fakeDelegatedEditor{})
		drainAutocompleteWork(t, m)
		if got := heard(bridge); got != 0 {
			t.Fatalf("the host gave %d providers to an editor whose process had been handed one at its build; want 0", got)
		}
		m.editor.SetAutocomplete(provider(t))
		drainAutocompleteWork(t, m)
		if got := heard(bridge); got != 1 {
			t.Fatalf("the editor heard %d providers after the next rebuild; want 1", got)
		}
	})
}
