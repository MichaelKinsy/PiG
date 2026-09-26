package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

const argumentCompletionExtension = `export default function (pi) {
  pi.registerCommand("mcp", {
    description: "Show MCP server status",
    getArgumentCompletions: async (prefix) => {
      const items = [
        { value: "reconnect", label: "reconnect — Reconnect servers" },
        { value: "tools", label: "tools — List all tools" },
      ].filter(({ value }) => value.startsWith(prefix.trimStart()));
      return items.length > 0 ? items : null;
    },
    handler: async () => {},
  });
  pi.registerCommand("plain", { handler: async () => {} });
}
`

// A Node command's getArgumentCompletions runs in the extension process, as
// upstream's editor awaits it for "/<command> <prefix>"; null means none.
func TestNodeCommandArgumentCompletionsRunInTheExtensionProcess(t *testing.T) {
	nodeCellRequireNode(t)
	entry := filepath.Join(t.TempDir(), "commands.mjs")
	if err := os.WriteFile(entry, []byte(argumentCompletionExtension), 0o644); err != nil {
		t.Fatal(err)
	}
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	loaded, errs := host.LoadAll(ctx, []ExtConfig{{Name: "commands", Source: entry, Enabled: true}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", errs)
	}
	if loaded[0].Commands["plain"].GetArgumentCompletions != nil {
		t.Fatal("a command without getArgumentCompletions got argument completions")
	}
	complete := loaded[0].Commands["mcp"].GetArgumentCompletions
	if complete == nil {
		t.Fatal("mcp has no argument completions")
	}
	items, err := complete(" ")
	if err != nil {
		t.Fatal(err)
	}
	var values []string
	for _, item := range items {
		values = append(values, item.Value+"|"+item.Label)
	}
	if want := []string{"reconnect|reconnect — Reconnect servers", "tools|tools — List all tools"}; !slices.Equal(values, want) {
		t.Fatalf("completions = %q, want %q", values, want)
	}
	if items, err := complete("zzz"); err != nil || items != nil {
		t.Fatalf("completions for zzz = %v, %v; want none", items, err)
	}
}
