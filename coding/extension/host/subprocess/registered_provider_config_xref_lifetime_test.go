//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The unregistering process reads no root at once. A reader that retains a superseded or unregistered root keeps using it, as a retained object in Pi's heap stays live; once the reader drops it, the Host lease and the author's export are released.
func TestRegisteredProviderConfigXrefLifetime(t *testing.T) {
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
  const config = { name: "Author", api: "xref-life-api", baseUrl: "https://author.invalid", apiKey: "author-key", models: [{ id: "m", name: "M", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }] };
  pi.registerProvider("xref-life", config);
  pi.registerCommand("author-reregister", { handler: async () => pi.registerProvider("xref-life", { name: "Second" }) });
  // Pi's unregisterProvider removes the registration synchronously (model-runtime.ts:438-445,791-797).
  pi.registerCommand("author-unregister", { handler: async (_args, ctx) => {
    pi.unregisterProvider("xref-life");
    const after = ctx.modelRegistry.getRegisteredProviderConfig("xref-life");
    if (after !== undefined) throw new Error("the unregistering process still reads the root " + after.name);
    if (ctx.modelRegistry.getRegisteredProviderIds().includes("xref-life")) throw new Error("the unregistering process still lists the provider");
  } });
}
`)
	reader := write("reader.mjs", xrefForceGC+`
let saved, weak;
export default function (pi) {
  pi.registerCommand("reader-retain", { handler: async (_args, ctx) => {
    saved = ctx.modelRegistry.getRegisteredProviderConfig("xref-life");
    weak = new WeakRef(saved);
    if (saved?.name !== "Author") throw new Error("no author root");
  } });
  pi.registerCommand("reader-check", { handler: async (_args, ctx) => {
    if (saved.apiKey !== "author-key" || saved.name !== "Author" || saved.models.length !== 1) throw new Error("the retained root is no longer the author's");
    const current = ctx.modelRegistry.getRegisteredProviderConfig("xref-life");
    if (current === saved) throw new Error("a superseded root is still current");
  } });
  pi.registerCommand("reader-drop", { handler: async () => {
    saved = undefined;
    let collected = false;
    for (let i = 0; i < 300 && !collected; i++) { await new Promise(resolve => setTimeout(resolve, 10)); gc(); await new Promise(resolve => setTimeout(resolve, 10)); collected = weak.deref() === undefined; }
    weak = undefined;
    if (!collected) throw new Error("the retained root proxy was not collected");
  } });
}
`)
	h := newTestHost(t)
	h.SetUIBridge(NewUIBridge(func() {}))
	t.Cleanup(func() { h.Shutdown("test done") })
	exts, errs := h.LoadAll(t.Context(), []ExtConfig{
		{Name: "life-author", Source: author, Enabled: true, Isolation: "isolated"},
		{Name: "life-reader", Source: reader, Enabled: true, Isolation: "isolated"},
	})
	if len(errs) != 0 || len(exts) != 2 {
		t.Fatalf("LoadAll = %d extensions, %v", len(exts), errs)
	}
	run := func(index int, command string) {
		t.Helper()
		if err := exts[index].Commands[command].Handler(t.Context(), ""); err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	run(1, "reader-retain")
	run(0, "author-reregister")
	run(1, "reader-check")
	run(0, "author-unregister")
	run(1, "reader-check")
	if stats := h.xref.stats(); stats.Leases == 0 {
		t.Fatalf("a retained root has no Host lease: %+v", stats)
	}
	run(1, "reader-drop")
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats := h.xref.stats()
		if stats.Leases == 0 && stats.Holds == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Host xref ledger retained %+v after the reader dropped the root and the provider was unregistered", stats)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
