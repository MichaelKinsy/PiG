//go:build !pig_strip_node_extensions

package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The complete path with a real extension: Pi's TypeScript API on the Node
// runtime, the subprocess host and bridge, and the production input loop. The
// extension remaps j to x and consumes q, as an upstream listener does in
// process, and every keystroke reaches the editor in the order it was typed.
func TestNodeTerminalInputListenerRewritesKeysInOrder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "pig-home"))
	source := filepath.Join(root, "remap")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"type":"module","main":"main.js"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	const extensionSource = `export default function register(pi) {
  pi.registerCommand("remap", {
    description: "Remap j to x and consume q",
    handler: async (_args, ctx) => {
      ctx.ui.onTerminalInput((data) => {
        if (data === "j") return { data: "x" };
        if (data === "q") return { consume: true };
        return undefined;
      });
    },
  });
}
`
	if err := os.WriteFile(filepath.Join(source, "main.js"), []byte(extensionSource), 0o644); err != nil {
		t.Fatal(err)
	}

	q := newInputQueueMode(t)
	host := subprocess.NewHost(root)
	host.SetMode("tui")
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: q.InteractiveMode})
	host.SetUIBridge(bridge)
	ctx := testbudget.Context(t)
	ext, err := host.Load(ctx, subprocess.ExtConfig{Name: "remap", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Shutdown("test complete")
	if err := ext.Commands["remap"].Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// The command returns once the extension sent ui.onTerminalInput, which
	// the host handles on its own goroutine.
	waitTerminalInputListeners(t, q.InteractiveMode, 1)
	seen := watchTerminalInput(q.InteractiveMode)
	q.start(t)

	q.typeKeys(t, "ajbqc")
	for _, want := range []string{"a", "x", "b", "c"} {
		expectSeen(t, seen, want)
	}
	if got := runOnQueueLoop(t, q.InteractiveMode, func() string { return q.editor.Text() }); got != "axbc" {
		t.Fatalf("editor = %q, want %q", got, "axbc")
	}
}
