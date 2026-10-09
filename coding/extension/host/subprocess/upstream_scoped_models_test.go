//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestNodeScopedModelsFollowHostState(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(map[bool]string{false: "isolated", true: "packed"}[packed], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "scope.mjs")
			write(t, path, `export default pi => { pi.registerCommand("probe", {handler: async (_,ctx) => ctx.ui.notify(JSON.stringify(ctx.scopedModels ?? null), "info")}); };`)
			h := NewHost(root)
			t.Cleanup(func() { h.Shutdown("test done") })
			b := NewUIBridge(func() {})
			b.SetUIContext(&struct{ extension.UIContext }{extension.NoopUIContext})
			messages := make(chan string, 1)
			b.SetNotifyFunc(func(message, _ string) { messages <- message })
			h.SetUIBridge(b)
			var models []extension.ScopedModel
			b.SetHostAction("getScopedModels", func() []extension.ScopedModel { return models })
			configs := []ExtConfig{{Name: "scope", Source: path, Enabled: true}}
			if packed {
				configs[0].Isolation = "shared-ok"
				sibling := filepath.Join(root, "sibling.mjs")
				write(t, sibling, `export default pi => { pi.on("session_start", () => {}); };`)
				configs = append(configs, ExtConfig{Name: "sibling", Source: sibling, Enabled: true, Isolation: "shared-ok"})
			} else {
				configs[0].Isolation = "isolated"
			}
			extensions, failures := h.LoadAll(t.Context(), configs)
			if len(failures) != 0 || len(extensions) != len(configs) {
				t.Fatalf("loaded=%d failures=%v", len(extensions), failures)
			}
			for _, state := range [][]extension.ScopedModel{
				{},
				{{Model: &ai.Model{ID: "scoped-test"}, ThinkingLevel: "high"}},
				{},
			} {
				models = state
				if err := extensions[0].Commands["probe"].Handler(context.Background(), ""); err != nil {
					t.Fatal(err)
				}
				want, err := json.Marshal(snapshotScopedModels(state))
				if err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-messages:
					if got != string(want) {
						t.Fatalf("scope=%s; want %s", got, want)
					}
				case <-t.Context().Done():
					t.Fatal(t.Context().Err())
				}
			}
		})
	}
}
