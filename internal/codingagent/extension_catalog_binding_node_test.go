//go:build !pig_strip_node_extensions

package codingagent

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// pig additive (D19): a Node member answers ctx.modelRegistry from the snapshot
// the host replicates into it. A Node cell defers each member's ready until
// every factory has finished, so the member receives RegisterExtConn's
// model_registry_update first; the deferred ready payload then replaces the
// replica with its models and must carry the catalog, or the member reads an
// empty registry until the next publication (a reload re-activates a cell with
// model operations already wired).
func TestNodeModelRegistryReplicaSurvivesDeferredReady(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required: %v", err)
	}
	for _, count := range []int{1, 2} {
		t.Run(map[int]string{1: "one-member", 2: "two-members"}[count], func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			_, bindings, _ := replicaRegistry(t)
			bridge := subprocess.NewUIBridge(func() {})
			detach := WireModelOperations(bridge, bindings)
			defer detach()
			root := t.TempDir()
			var configs []subprocess.ExtConfig
			for i := range count {
				name := fmt.Sprintf("replica%d", i)
				entry := filepath.Join(root, name+".mjs")
				source := fmt.Sprintf(`export default function(pi) {
 pi.registerTool({name:%q,label:"probe",description:"probe",parameters:{type:"object",properties:{}},execute:async(_id,_args,_signal,_update,ctx)=>{
  const model=ctx.modelRegistry.find("replica-provider","replica-model");
  return {content:[{type:"text",text:JSON.stringify({pid:process.pid,model:model?model.provider+"/"+model.id:null})}]};
 }});
}`, name)
				if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				configs = append(configs, subprocess.ExtConfig{Name: name, Source: entry, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"})
			}
			host := subprocess.NewHost(t.TempDir())
			host.SetUIBridge(bridge)
			t.Cleanup(func() { host.Shutdown("test done") })
			exts, errs := host.LoadAll(t.Context(), configs)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			pids := map[float64]bool{}
			tools := map[string]extension.RegisteredTool{}
			for _, ext := range exts {
				maps.Copy(tools, ext.Tools)
			}
			for i := range count {
				name := fmt.Sprintf("replica%d", i)
				tool, ok := tools[name]
				if !ok {
					t.Fatalf("%s did not register its probe tool", name)
				}
				result, err := tool.Definition.Execute(t.Context(), "probe", json.RawMessage(`{}`), nil)
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					PID   float64 `json:"pid"`
					Model *string `json:"model"`
				}
				if err := json.Unmarshal([]byte(result.Text()), &got); err != nil {
					t.Fatal(err)
				}
				pids[got.PID] = true
				if got.Model == nil || *got.Model != "replica-provider/replica-model" {
					t.Fatalf("%s ctx.modelRegistry.find(replica-provider, replica-model) = %v, want the host's registered model", name, got.Model)
				}
			}
			if len(exts) != count || len(pids) != 1 {
				t.Fatalf("%d members ran in %d Node processes, want %d in 1", len(exts), len(pids), count)
			}
		})
	}
}
