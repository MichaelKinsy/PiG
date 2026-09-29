package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type autocompleteSyncUI struct {
	*testUIContext
	bridge    *UIBridge
	ctx       context.Context
	installed chan struct{}
}

func (u *autocompleteSyncUI) SetEditorComponent(value any) {
	u.testUIContext.SetEditorComponent(value)
	if value != nil {
		close(u.installed)
	}
}
func (u *autocompleteSyncUI) AddAutocompleteProvider(factory extension.AutocompleteProviderFactory) error {
	if err := u.testUIContext.AddAutocompleteProvider(factory); err != nil {
		return err
	}
	return u.bridge.SyncAutocomplete(u.ctx, u.autocompleteProvider())
}

// A synchronous provider change can call an editor in a different member of the same Node cell. The socket pump must service that member before the initiating setter returns.
func TestNodeAutocompleteChangeReentersPackedEditor(t *testing.T) {
	shortSockDir(t)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			root := t.TempDir()
			scripts := map[string]string{
				"editor":  `export default function(pi){pi.on("session_start",(_,ctx)=>{ctx.ui.setEditorComponent(()=>({render(){return ["editor"]},handleInput(){},getText(){return ""},setText(){},setAutocompleteProvider(provider){ctx.ui.notify("provider:"+(provider.triggerCharacters??[]).join(","),"info")}}))})}`,
				"wrapper": `export default function(pi){pi.registerCommand("wrap",{handler:(_,ctx)=>{ctx.ui.addAutocompleteProvider(current=>({triggerCharacters:["$"],getSuggestions:(...args)=>current.getSuggestions(...args),applyCompletion:(...args)=>current.applyCompletion(...args)}))}})}`,
			}
			for name, source := range scripts {
				if err := os.WriteFile(filepath.Join(root, name+".mjs"), []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			notices := make(chan string, 4)
			bridge := NewUIBridge(func() {})
			ui := &autocompleteSyncUI{testUIContext: newTestUIContext(), bridge: bridge, ctx: t.Context(), installed: make(chan struct{})}
			ui.onNotify = func(message, _ string) { notices <- message }
			bridge.SetUIContext(ui)
			host := newTestHost(t)
			host.SetUIBridge(bridge)
			defer host.Shutdown("test done")
			extensions, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "editor", Source: filepath.Join(root, "editor.mjs"), Enabled: true, Isolation: isolation}, {Name: "wrapper", Source: filepath.Join(root, "wrapper.mjs"), Enabled: true, Isolation: isolation}})
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
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := extensions[1].Commands["wrap"].Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			for {
				select {
				case message := <-notices:
					if strings.HasSuffix(message, "$") {
						return
					}
				case <-ctx.Done():
					t.Fatal("custom editor did not receive the new provider")
				}
			}
		})
	}
}
