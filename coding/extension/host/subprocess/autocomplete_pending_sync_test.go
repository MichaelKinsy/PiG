//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pendingAutocompleteUI answers ui.autocomplete.current as the interactive mode does until its first setupAutocompleteProvider has run: no provider yet.
type pendingAutocompleteUI struct {
	*autocompleteSyncUI
	pending bool
}

func (u *pendingAutocompleteUI) AutocompleteProvider(ctx context.Context) (*extension.AutocompleteProvider, error) {
	if u.pending {
		return nil, nil
	}
	return u.autocompleteSyncUI.AutocompleteProvider(ctx)
}

func (u *pendingAutocompleteUI) AutocompletePending(context.Context) (bool, error) {
	return u.pending, nil
}

// Pi hands the provider to an editor at its install (setCustomEditorComponent, interactive-mode.ts:3015-3017), once. An extension's process builds its editor
// when the host answers ui.autocomplete.current: with {} while there is no provider yet, and the host installs the editor later, when a provider may exist. The
// host's hand-over reaches only an editor whose process was answered {} and that no sync has reached since: an editor handed the provider at its build has had
// its call, and a second hand-over would be a call Pi does not make. The editor of this fixture refuses a provider with the trigger "!", so a refusal shows that
// the sync reached it.
func TestSyncPendingAutocompleteReachesOnlyAnEditorBuiltWithoutAProvider(t *testing.T) {
	shortSockDir(t)
	for _, tc := range []struct {
		name         string
		pending      bool
		syncedBefore bool
		wantSync     bool
	}{
		{"answered {} and not synced since", true, false, true},
		{"answered the provider at its build", false, false, false},
		{"answered {} and synced since", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "refusing-editor.mjs")
			script := `export default function(pi){pi.on("session_start",(_,ctx)=>{ctx.ui.setEditorComponent(()=>({render(){return ["editor"]},handleInput(){},getText(){return ""},setText(){},setAutocompleteProvider(provider){if((provider.triggerCharacters??[]).includes("!"))throw new Error("EDITOR_SYNC_REFUSED")}}))})}`
			if err := os.WriteFile(source, []byte(script), 0o600); err != nil {
				t.Fatal(err)
			}
			bridge := NewUIBridge(func() {})
			ui := &pendingAutocompleteUI{autocompleteSyncUI: &autocompleteSyncUI{testUIContext: newTestUIContext(), bridge: bridge, ctx: t.Context(), installed: make(chan struct{})}, pending: tc.pending}
			bridge.SetUIContext(ui)
			host := newTestHost(t)
			host.SetUIBridge(bridge)
			defer host.Shutdown("test done")
			extensions, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "refusing", Source: source, Enabled: true}})
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
			if tc.syncedBefore {
				if err := bridge.SyncAutocomplete(ctx, &extension.AutocompleteProvider{}); err != nil {
					t.Fatalf("SyncAutocomplete of a provider the editor takes returned %v", err)
				}
			}
			refused := &extension.AutocompleteProvider{TriggerCharacters: []string{"!"}}
			err := bridge.SyncPendingAutocomplete(ctx, refused)
			got, synced := errors.AsType[*extension.EditorSyncError](err)
			switch {
			case tc.wantSync && !synced:
				t.Fatalf("SyncPendingAutocomplete returned %v; the editor was built without a provider and must be handed one, and it refuses this one", err)
			case !tc.wantSync && err != nil:
				t.Fatalf("SyncPendingAutocomplete returned %v; the editor needs no hand-over", err)
			case synced && got.Error() != "EDITOR_SYNC_REFUSED":
				t.Fatalf("the editor's refusal came back as %q, want its own EDITOR_SYNC_REFUSED", got.Error())
			}
			if err := bridge.SyncPendingAutocomplete(ctx, refused); err != nil {
				t.Fatalf("a second SyncPendingAutocomplete returned %v; the hand-over is once", err)
			}
		})
	}
}
