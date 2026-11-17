//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// An installed editor that throws from setAutocompleteProvider answers the host's autocomplete.sync with its own error. Pi's setupAutocompleteProvider
// throws it to whichever caller rebuilt the provider (interactive-mode.ts:848-850), so the sync names the extension and keeps the editor's text and
// stack, in a shared cell and in an isolated one.
func TestSyncAutocompleteNamesTheExtensionWhoseEditorRefused(t *testing.T) {
	shortSockDir(t)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "refusing-editor.mjs")
			script := `export default function(pi){pi.on("session_start",(_,ctx)=>{ctx.ui.setEditorComponent(()=>({render(){return ["editor"]},handleInput(){},getText(){return ""},setText(){},setAutocompleteProvider(provider){if((provider.triggerCharacters??[]).includes("!"))throw new Error("EDITOR_SYNC_REFUSED")}}))})}`
			if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			bridge := NewUIBridge(func() {})
			ui := &autocompleteSyncUI{testUIContext: newTestUIContext(), bridge: bridge, ctx: t.Context(), installed: make(chan struct{})}
			bridge.SetUIContext(ui)
			host := newTestHost(t)
			host.SetUIBridge(bridge)
			defer host.Shutdown("test done")
			extensions, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "refusing", Source: source, Enabled: true, Isolation: isolation}})
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			if _, err := extensions[0].Handlers["session_start"][0](map[string]any{"type": "session_start"}, t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ui.installed:
			case <-t.Context().Done():
				t.Fatal("editor was not installed")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			refused := &extension.AutocompleteProvider{TriggerCharacters: []string{"!"}}
			err := bridge.SyncAutocomplete(ctx, refused)
			got, ok := errors.AsType[*extension.EditorSyncError](err)
			if !ok {
				t.Fatalf("SyncAutocomplete returned %v, want an *extension.EditorSyncError", err)
			}
			if got.Error() != "EDITOR_SYNC_REFUSED" || got.ExtensionPath != source {
				t.Errorf("sync error = %q from %q, want EDITOR_SYNC_REFUSED from %q", got.Error(), got.ExtensionPath, source)
			}
			if stack := extension.ErrorStack(err); !strings.Contains(stack, "refusing-editor.mjs") {
				t.Errorf("sync error stack %q does not name the extension's source", stack)
			}
			// A provider the editor takes is no error: the refusal is the editor's, not the sync's.
			if err := bridge.SyncAutocomplete(ctx, &extension.AutocompleteProvider{}); err != nil {
				t.Errorf("SyncAutocomplete of a provider the editor takes returned %v", err)
			}
		})
	}
}
