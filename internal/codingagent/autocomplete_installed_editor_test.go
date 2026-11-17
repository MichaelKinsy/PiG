package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's setupAutocompleteProvider hands the rebuilt provider to the installed editor on every rebuild, with no wrapper registered
// (interactive-mode.ts:848-850), and setCustomEditorComponent hands it the current one at install (interactive-mode.ts:2971-2973).
// The owner loop runs the editor install and the rebuild that follows session_start in either order (issue 497): the subprocess editor
// hears the rebuild when the install ran first, and when the rebuild ran first the editor's own install is what gives it the provider.
func TestInstalledSubprocessEditorHearsTheRebuildInEitherOrder(t *testing.T) {
	provider := func(t *testing.T) tui.AutocompleteProvider {
		return tui.NewCombinedProvider([]tui.SlashCommand{{Name: "keep"}}, t.TempDir(), "")
	}
	t.Run("install first", func(t *testing.T) {
		m := autocompleteModeProbe(t)
		bridge := &autocompleteBridgeProbe{captureUIBridge: &captureUIBridge{}}
		m.opts.SubprocessUIBridge = bridge
		m.setRemoteEditor(&fakeDelegatedEditor{})
		m.editor.SetAutocomplete(provider(t))
		drainAutocompleteWork(t, m)
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		if len(bridge.providers) != 1 || bridge.providers[0] == nil {
			t.Fatalf("the editor heard %d rebuilds with no wrapper registered; want 1", len(bridge.providers))
		}
	})
	t.Run("rebuild first", func(t *testing.T) {
		m := autocompleteModeProbe(t)
		bridge := &autocompleteBridgeProbe{captureUIBridge: &captureUIBridge{}}
		m.opts.SubprocessUIBridge = bridge
		m.editor.SetAutocomplete(provider(t))
		drainAutocompleteWork(t, m)
		m.setRemoteEditor(&fakeDelegatedEditor{})
		bridge.mu.Lock()
		heard := len(bridge.providers)
		bridge.mu.Unlock()
		if heard != 0 {
			t.Fatalf("the editor heard %d rebuilds before it was installed; want 0", heard)
		}
		m.editor.SetAutocomplete(provider(t))
		drainAutocompleteWork(t, m)
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		if len(bridge.providers) != 1 {
			t.Fatalf("the editor heard %d rebuilds after it was installed; want 1", len(bridge.providers))
		}
	})
}
