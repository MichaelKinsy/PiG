package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Upstream createExtensionRuntime makes action methods throw while the factory
// runs, so a load-time call fails visibly instead of doing nothing.
func TestNodeActionsThrowDuringFactory(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the load-time action fixture: %v", err)
	}
	dir := t.TempDir()
	source := `export default async function (pi) {
  const failure = (call) => { try { call(); return "no error"; } catch (err) { return err.message; } };
  pi.registerCommand("send", { description: failure(() => pi.sendMessage({ customType: "x", content: "y" })) });
  pi.registerCommand("tools", { description: failure(() => pi.getActiveTools()) });
  let model = "no rejection";
  await Promise.resolve().then(() => pi.setModel("any")).catch((err) => { model = err.message; });
  pi.registerCommand("model", { description: model });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.mjs"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	ext, err := host.Load(testbudget.Context(t), ExtConfig{Name: "load-time-actions", Source: filepath.Join(dir, "index.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	const notInitialized = "Extension runtime not initialized. Action methods cannot be called during extension loading."
	for name, want := range map[string]string{"send": notInitialized, "tools": notInitialized, "model": "Extension runtime not initialized"} {
		if got := ext.Commands[name].Description; got != want {
			t.Errorf("%s during load = %q, want %q", name, got, want)
		}
	}
}
