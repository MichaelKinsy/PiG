package extensionconformance

import (
	"context"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's setupAutocompleteProvider hands the rebuilt provider to the installed editor (interactive-mode.ts:848-850
// `this.editor.setAutocompleteProvider?.(provider)`), and the host reports every rebuild to the editor's runtime as
// autocomplete.sync {operation: "changed"}. An editor an SDK installed must accept it in every SDK and placement: the editor
// is a proxy of the host's editor, which already holds the rebuilt provider, so the notice is acknowledged and the
// descriptor's host reference is released. An SDK that answers an error here reaches InteractiveMode.reportAutocompleteError,
// which ends the session as an uncaught exception (issue 497). The provider's collection after the notice shows the SDK
// released its reference; one that kept it, or never answered, leaves the host's table holding it.
func TestAutocompleteChangedReachesInstalledNativeEditor(t *testing.T) {
	t.Parallel()
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			if h.bridge == nil {
				t.Fatal("the harness has no UI bridge")
			}
			if !h.runner.ExecuteCommand(context.Background(), "editor-install", "") {
				t.Fatal("fixture has no editor-install command")
			}
			editor := waitInstalledEditor(t, h)
			editor.Bind(&baseEditorHost{inputs: make(chan struct{}, 16)})
			editor.Configure(extension.RemoteEditorConfig{Focused: true})
			// The provider lives only in this closure: its collection below is the SDK's release, and nothing here holds it.
			observed := func() weak.Pointer[extension.AutocompleteProvider] {
				provider := autocompleteBase()
				for range 2 {
					ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
					err := h.bridge.SyncAutocomplete(ctx, provider)
					cancel()
					if err != nil {
						t.Fatalf("a rebuild while the extension's editor is installed failed: %v", err)
					}
				}
				return weak.Make(provider)
			}()
			deadline := time.Now().Add(20 * time.Second)
			for observed.Value() != nil {
				if time.Now().After(deadline) {
					t.Fatal("the host still holds the provider the SDK was told about: the SDK did not release its reference")
				}
				runtime.GC()
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
