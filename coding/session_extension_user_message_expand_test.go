package coding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// sendExpandExtension is a Node extension whose command sends "/greet Alice" as a user message: with the options its argument names
// ("true" or "false" for expandPromptTemplates), or with no options at all for "default".
const sendExpandExtension = `export default function (pi) {
  pi.registerCommand("send-expand", {
    description: "send /greet Alice as a user message",
    handler: async (args) => {
      if (args === "default") pi.sendUserMessage("/greet Alice");
      else pi.sendUserMessage("/greet Alice", { expandPromptTemplates: args === "true" });
    },
  });
}
`

func loadSendExpandExtension(t *testing.T, bridge *subprocess.UIBridge) extension.Extension {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.mjs")
	if err := os.WriteFile(path, []byte(sendExpandExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("send-expand test complete") })
	host.SetUIBridge(bridge)
	loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "send-expand", Source: path, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return *loaded
}
