package extensionconformance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's setupAutocompleteProvider ends with `this.editor.setAutocompleteProvider?.(provider)` (interactive-mode.ts:848-850), so an extension's installed
// editor that throws there fails the rebuild of whichever caller made it, and the extension runner shows it as that extension's error. The host's sync
// of a rebuilt provider carries the editor's own error text, with the extension it came from, as an [extension.EditorSyncError]: the session's
// callers report it as an extension error instead of the crash of a provider that failed. Only the Node SDK can make its editor refuse: the editor of
// the Go, Rust, Python and Lisp SDKs is a proxy of the host's editor and has no member of the extension's own that sees the provider, so no native
// extension can throw there.
func TestInstalledEditorSyncErrorIsAnExtensionError(t *testing.T) {
	t.Parallel()
	for _, tc := range allHarnessCases() {
		if tc.name != "subprocess-node" && tc.name != "subprocess-node-packed" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			// Node's editor host installs a component only once the host answers its current provider.
			h.bridge.SetUIContext(editorGetterUI{h.ui})
			if !h.runner.ExecuteCommand(context.Background(), "editor-install-refusing", "") {
				t.Fatal("fixture has no editor-install-refusing command")
			}
			editor := waitInstalledEditor(t, h)
			editor.Bind(&baseEditorHost{inputs: make(chan struct{}, 16)})
			editor.Configure(extension.RemoteEditorConfig{Focused: true})
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			provider := autocompleteBase()
			provider.TriggerCharacters = []string{"!"}
			err := h.bridge.SyncAutocomplete(ctx, provider)
			refused, ok := errors.AsType[*extension.EditorSyncError](err)
			if !ok {
				t.Fatalf("a rebuild the installed editor refuses returned %v, want an *extension.EditorSyncError", err)
			}
			if refused.Error() != "EDITOR_SYNC_REFUSED" {
				t.Errorf("the sync error reads %q, want the extension's own text EDITOR_SYNC_REFUSED", refused.Error())
			}
			if !strings.HasSuffix(refused.ExtensionPath, "main.mjs") {
				t.Errorf("the sync error names extension %q, want the path of the fixture's main.mjs", refused.ExtensionPath)
			}
			if stack := extension.ErrorStack(err); !strings.Contains(stack, "main.mjs") {
				t.Errorf("the sync error's stack %q does not name the extension's source", stack)
			}
		})
	}
}
