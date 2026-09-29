package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi deletes a registration synchronously (model-runtime.ts:438-445,791-797), so a member of the same process reads no configuration and lists no id in the tick after another member unregisters it, before the Host's registry update arrives.
func TestRegisteredProviderConfigSameProcessMemberSeesUnregister(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	dir := t.TempDir()
	write := func(name, source string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	author := write("author.mjs", `export default function (pi) {
  pi.registerProvider("cell-unreg", { name: "Author", api: "cell-unreg-api", baseUrl: "https://author.invalid", apiKey: "author-key", models: [{ id: "m", name: "M", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }] });
  pi.registerCommand("author-unregister", { handler: async () => {
    const before = globalThis.__cellReader();
    if (before.config?.name !== "Author" || !before.ids.includes("cell-unreg")) throw new Error("the member does not read the registration first: " + JSON.stringify(before.ids));
    pi.unregisterProvider("cell-unreg");
    const after = globalThis.__cellReader();
    if (after.config !== undefined) throw new Error("the member still reads the root " + after.config.name);
    if (after.ids.includes("cell-unreg")) throw new Error("the member still lists the provider");
  } });
}
`)
	reader := write("reader.mjs", `export default function (pi) {
  let modelRegistry;
  pi.on("session_start", async (_event, ctx) => { modelRegistry = ctx.modelRegistry; });
  pi.registerCommand("reader-ready", { handler: async (_args, ctx) => { modelRegistry = ctx.modelRegistry; } });
  globalThis.__cellReader = () => ({ config: modelRegistry.getRegisteredProviderConfig("cell-unreg"), ids: modelRegistry.getRegisteredProviderIds() });
}
`)
	h := newTestHost(t)
	h.SetUIBridge(NewUIBridge(func() {}))
	t.Cleanup(func() { h.Shutdown("test done") })
	exts, errs := h.LoadAll(t.Context(), []ExtConfig{
		{Name: "cell-author", Source: author, Enabled: true},
		{Name: "cell-reader", Source: reader, Enabled: true},
	})
	if len(errs) != 0 || len(exts) != 2 {
		t.Fatalf("LoadAll = %d extensions, %v", len(exts), errs)
	}
	if err := exts[1].Commands["reader-ready"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if err := exts[0].Commands["author-unregister"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
}
