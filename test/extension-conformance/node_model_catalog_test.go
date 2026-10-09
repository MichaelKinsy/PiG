package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi's ctx.modelRegistry.find and getAll return the registry's stored model objects (model-registry.ts): repeated reads give the same object, and what an extension changes on it is what its next read returns. PiG replicates the host's catalog into each Node extension, and the members of a packed cell share one decoded publication (D20). This row proves that each extension still reads its own objects, in packed and isolated modes alike: one extension's change is invisible to the other, its own reads keep the change until the host publishes again, and a publication replaces its objects. The ready payload gives each member its own catalog, so the row also changes the models of a later publication, which a packed cell decodes once for all members, and a non-chat model (getModelsOfType, model-registry.ts:151-153), which only a publication carries.
const nodeModelCatalogSource = `let held;
export default function (pi) {
  pi.registerTool({
    name: %[1]q,
    label: %[1]q,
    description: "Model catalog reads",
    parameters: { type: "object", properties: { change: { type: "boolean" } } },
    async execute(_id, params, _signal, _onUpdate, ctx) {
      const found = ctx.modelRegistry.find("conformance", "declared");
      const all = ctx.modelRegistry.getAll();
      const typed = ctx.modelRegistry.getModelsOfType("image", "conformance")[0];
      const report = { pid: process.pid, name: found.name, input: found.cost.input, same: found === ctx.modelRegistry.find("conformance", "declared"), listed: all.includes(found), kept: found === held, models: all.length, typedInput: typed.cost.input, typedSame: typed === ctx.modelRegistry.getModelsOfType("image", "conformance")[0] };
      if (params.change) {
        found.name = "changed";
        found.cost.input = 99;
        typed.cost.input = 99;
      }
      held = found;
      return { content: [{ type: "text", text: JSON.stringify(report) }] };
    },
  });
}
`

type nodeModelCatalogRead struct {
	Pid    int     `json:"pid"`
	Name   string  `json:"name"`
	Input  float64 `json:"input"`
	Same   bool    `json:"same"`
	Listed bool    `json:"listed"`
	Kept   bool    `json:"kept"`
	Models int     `json:"models"`
	// TypedInput is the cost.input of the non-chat model; TypedSame says getModelsOfType returned the same object twice.
	TypedInput float64 `json:"typedInput"`
	TypedSame  bool    `json:"typedSame"`
}

func TestNodeModelCatalogReadsAreEachExtensionsOwn(t *testing.T) {
	// The catalog is large enough (about 250 KB of JSON) that a packed cell receives each publication as one shared frame.
	catalog := []map[string]any{conformanceModel("current", "Current"), conformanceModel("declared", "Declared"), conformanceModel("org/model/name", "Slash model")}
	for i := range 300 {
		catalog = append(catalog, conformanceModel(fmt.Sprintf("filler-%03d", i), fmt.Sprintf("Filler %03d", i)))
	}
	for _, mode := range []struct{ name, isolation string }{{"subprocess-node", "isolated"}, {"subprocess-node-packed", ""}} {
		t.Run(mode.name, func(t *testing.T) {
			dir := t.TempDir()
			var configs []subprocess.ExtConfig
			for _, name := range []string{"catalog-a", "catalog-b"} {
				entry := filepath.Join(dir, name+".mjs")
				if err := os.WriteFile(entry, fmt.Appendf(nil, nodeModelCatalogSource, name), 0o600); err != nil {
					t.Fatal(err)
				}
				configs = append(configs, subprocess.ExtConfig{Name: name, Source: entry, Enabled: true, Isolation: mode.isolation})
			}
			bridge := subprocess.NewUIBridge(func() {})
			typed := conformanceModel("typed", "Typed")
			typed["type"] = "image"
			bridge.SetActions(&subprocess.HostCallbacks{
				GetModels: func() []map[string]any { return catalog },
				GetModelRegistryState: func() map[string]any {
					return map[string]any{"typedModels": []map[string]any{typed}}
				},
			})
			h := subprocess.NewHost(t.TempDir())
			h.SetMode(conformanceMode)
			h.SetUIBridge(bridge)
			t.Cleanup(func() { h.Shutdown("test done") })
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			exts, errs := h.LoadAll(ctx, configs)
			if len(errs) != 0 || len(exts) != 2 {
				t.Fatalf("Node LoadAll: %d loaded, %v", len(exts), errs)
			}
			read := func(name string, change bool) nodeModelCatalogRead {
				t.Helper()
				for _, ext := range h.Extensions() {
					if ext.Name != name {
						continue
					}
					result, err := ext.Tools[name].Definition.Execute(ctx, "catalog", fmt.Appendf(nil, `{"change":%t}`, change), nil)
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					var got nodeModelCatalogRead
					if err := json.Unmarshal([]byte(result.Text()), &got); err != nil {
						t.Fatal(err)
					}
					return got
				}
				t.Fatalf("extension %s is not loaded", name)
				return nodeModelCatalogRead{}
			}
			want := func(step string, got nodeModelCatalogRead, name string, input float64, kept bool, typedInput float64) {
				t.Helper()
				if got.Name != name || got.Input != input || !got.Same || !got.Listed || got.Kept != kept || got.Models != len(catalog) || got.TypedInput != typedInput || !got.TypedSame {
					t.Errorf("%s: read %+v, want name %q, cost.input %v, the same object on every read and in getAll, kept %v, %d models, typed cost.input %v from the same typed object on every read", step, got, name, input, kept, len(catalog), typedInput)
				}
			}
			first := read("catalog-a", true)
			want("a changes its model", first, "Declared", 0, false, 0)
			other := read("catalog-b", false)
			want("b reads after a changed its own", other, "Declared", 0, false, 0)
			if packed := mode.isolation == ""; (first.Pid == other.Pid) != packed {
				t.Fatalf("%s: extension processes %d and %d, want one process %v", mode.name, first.Pid, other.Pid, packed)
			}
			want("a reads again", read("catalog-a", false), "changed", 99, true, 99)
			bridge.PublishModelCatalog()
			want("a reads the next publication and changes its models", read("catalog-a", true), "Declared", 0, false, 0)
			want("b reads the next publication after a changed its own", read("catalog-b", false), "Declared", 0, false, 0)
			want("a reads its change to the publication", read("catalog-a", false), "changed", 99, true, 99)
			want("b reads its own publication again", read("catalog-b", false), "Declared", 0, true, 0)
		})
	}
}
