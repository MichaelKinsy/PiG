//go:build !pig_strip_node_extensions

package coding

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// A subprocess handler's selectedTools edit crosses the extension wire into the same Session loadout as a native edit.
func TestSubprocessBeforeAgentStartSelectedToolsControlLoadout(t *testing.T) {
	t.Run("fused-go", func(t *testing.T) {
		ext := sdk.New("selected-tools")
		ext.OnEvent("before_agent_start", func(_ sdk.Context, data map[string]any) (any, error) {
			if data["prompt"] == "two" {
				data["systemPromptOptions"].(map[string]any)["selectedTools"] = []any{"second"}
			}
			return nil, nil
		})
		host := subprocess.NewHost(t.TempDir())
		t.Cleanup(func() { host.Shutdown("test complete") })
		loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "selected-tools", Enabled: true}, ext.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		assertSelectedToolsLoadout(t, *loaded, nil)
	})
	t.Run("node", func(t *testing.T) {
		if _, err := exec.LookPath("node"); err != nil {
			t.Fatalf("node is required: %v", err)
		}
		source := filepath.Join(t.TempDir(), "index.mjs")
		if err := os.WriteFile(source, []byte(`export default function (pi) { pi.on("before_agent_start", async (event) => { await new Promise((resolve) => setTimeout(resolve, 20)); if (event.prompt === "two") event.systemPromptOptions.selectedTools.splice(0, 1, "second"); }); }`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		host := subprocess.NewHost(t.TempDir())
		t.Cleanup(func() { host.Shutdown("test complete") })
		loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "selected-tools", Source: source, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		assertSelectedToolsLoadout(t, *loaded, nil)
	})
}
