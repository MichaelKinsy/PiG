//go:build !pig_strip_node_extensions

package coding

import (
	"path/filepath"
	"slices"
	"testing"
)

// upstream: packages/coding-agent/test/extensions-discovery.test.ts:43 'discovers direct .ts files in extensions/' (discoverAndLoadExtensions([], tempDir, tempDir)), and loader.ts:696-703 a failing path is an entry of errors, not a failure of the call.
func TestDiscoverAndLoadExtensionsLoadsWhatItDiscoversAndReportsFailures(t *testing.T) {
	dir := t.TempDir()
	writeExtensionTree(t, dir, map[string]string{
		"extensions/foo.ts": discoverCommandExtension,
		"extensions/bar.ts": discoverCommandExtension,
	})
	missing := filepath.Join(dir, "missing.ts")
	load, err := DiscoverAndLoadExtensions(t.Context(), []string{missing}, dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer load.Close()
	var loaded []string
	for _, e := range load.Extensions {
		loaded = append(loaded, filepath.Base(e.Path))
	}
	slices.Sort(loaded)
	if !slices.Equal(loaded, []string{"bar.ts", "foo.ts"}) {
		t.Fatalf("loaded extensions = %q, want bar.ts and foo.ts", loaded)
	}
	if len(load.Errors) != 1 || load.Errors[0].Path != missing {
		t.Fatalf("errors = %+v, want one entry for %s", load.Errors, missing)
	}
	if load.Runtime == nil {
		t.Fatal("the result carries no runtime")
	}
}

// upstream: packages/coding-agent/src/core/extensions/loader.ts:181-221,695-715: loadExtensionsInternal creates one runtime, every loaded extension's pi.registerProvider queues on runtime.pendingProviderRegistrations until a runner binds, and the result's runtime is that runtime (agent-session-services.ts:157-193 drains it). A fresh runtime would report no registration.
func TestDiscoverAndLoadExtensionsReturnsTheRuntimeTheExtensionsShare(t *testing.T) {
	dir := t.TempDir()
	writeExtensionTree(t, dir, map[string]string{
		"extensions/provider.ts": `export default function(pi) { pi.registerProvider("discovered-provider", { api: "openai-completions", baseUrl: "https://discovered.invalid/v1", apiKey: "discovered-key", models: [{ id: "m", name: "M", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }] }); }`,
	})
	load, err := DiscoverAndLoadExtensions(t.Context(), nil, dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer load.Close()
	if len(load.Errors) != 0 {
		t.Fatalf("errors = %+v", load.Errors)
	}
	var names []string
	for _, pending := range load.Runtime.PendingProviderRegistrations() {
		names = append(names, pending.Name)
	}
	if !slices.Equal(names, []string{"discovered-provider"}) {
		t.Fatalf("pending provider registrations of the result's runtime = %q, want the one the loaded extension made", names)
	}
}
