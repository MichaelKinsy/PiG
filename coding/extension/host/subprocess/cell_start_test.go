package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// writeOrderFixture writes a Node extension that registers one provider,
// command, tool, and flag named after itself. before runs first in the factory.
func writeOrderFixture(t *testing.T, dir, name, before string) string {
	t.Helper()
	source := fmt.Sprintf(`import { existsSync, writeFileSync } from "node:fs";
export default async function (pi) {
  %s
  pi.registerProvider(%[2]q, { baseUrl: "https://%[2]s.invalid/v1", api: "openai-completions", apiKey: "k", models: [] });
  pi.registerCommand(%[3]q, { description: "command", handler: async () => {} });
  pi.registerTool({ name: %[4]q, label: "tool", description: "tool", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [] }) });
  pi.registerFlag(%[5]q, { description: "flag", type: "boolean" });
}
`, before, name+"-provider", name+"-command", name+"_tool", name+"-flag")
	path := filepath.Join(dir, name+".mjs")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Upstream loader.ts awaits each extension factory before loading the next,
// so factories run one at a time in plan order. PiG prepares cells (discovery
// and build checks) concurrently but starts each process, which runs its
// factory, only after earlier cells committed. alpha's factory waits until
// beta's build check has published beta's launcher, which serial preparation
// never allows, then confirms beta's factory has not started. beta and gamma
// require the previous factory's completed side effect. Providers, commands,
// tools, flags, and the loaded order match one-at-a-time loading.
func TestLoadAllPreparesConcurrentlyAndRunsFactoriesInPlanOrder(t *testing.T) {
	shortSockDir(t)
	dir := t.TempDir()
	configRoot := t.TempDir()
	cacheDir := filepath.Join(configRoot, "cache", "ext")
	marker := func(name string) string { return filepath.Join(dir, name) }
	alpha := fmt.Sprintf(`const { readdirSync } = await import("node:fs");
  const prepared = () => { try { return readdirSync(%q).some((entry) => entry.startsWith("beta-") && existsSync(%q + "/" + entry + "/ready.json")); } catch { return false; } };
  const deadline = Date.now() + 120000;
  while (!prepared()) {
    if (Date.now() > deadline) throw new Error("beta was not prepared while alpha loaded");
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  const settle = Date.now() + 500;
  while (Date.now() < settle) {
    if (existsSync(%q)) throw new Error("beta's factory ran before alpha's finished");
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  writeFileSync(%q, "");`, cacheDir, cacheDir, marker("beta-started"), marker("alpha-done"))
	beta := fmt.Sprintf(`writeFileSync(%q, "");
  if (!existsSync(%q)) throw new Error("beta's factory ran before alpha's finished");
  writeFileSync(%q, "");`, marker("beta-started"), marker("alpha-done"), marker("beta-done"))
	gamma := fmt.Sprintf(`if (!existsSync(%q)) throw new Error("gamma's factory ran before beta's finished");`, marker("beta-done"))
	// Isolation: "isolated" keeps these three in three separate cells. This
	// test's own signal for "beta is prepared" is the existence of beta's
	// isolated build cache directory (ext/beta-<hash>/), which only exists
	// per extension in isolated mode; a Node cell (N8) builds one shared
	// runtime cache for every member instead, so a bare (default) config
	// here would pack all three into one cell and this cache-directory
	// signal would never appear, hanging until the test's own deadline.
	// That packing is otherwise correct and desired (it is what N8 is for);
	// this test is specifically about stageCellsInOrder's concurrent-prepare,
	// sequential-start cell contract, so it forces the separate-cells shape
	// that contract needs.
	configs := []ExtConfig{
		{Name: "alpha", Source: writeOrderFixture(t, dir, "alpha", alpha), Enabled: true, Isolation: "isolated"},
		{Name: "beta", Source: writeOrderFixture(t, dir, "beta", beta), Enabled: true, Isolation: "isolated"},
		{Name: "gamma", Source: writeOrderFixture(t, dir, "gamma", gamma), Enabled: true, Isolation: "isolated"},
	}

	host := NewHostWithConfigRoot(t.TempDir(), configRoot)
	defer host.Shutdown("test done")
	var mu sync.Mutex
	var providers []string
	host.SetProviderCallbacks(func(name string, _ extension.ProviderConfig) {
		mu.Lock()
		defer mu.Unlock()
		providers = append(providers, name)
	}, func(string) {})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	loaded, errs := host.LoadAll(ctx, configs)
	if len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}

	var planned []string
	for _, cell := range PlanCells(configs, nil) {
		planned = append(planned, cellExtNames(cell)...)
	}
	var names []string
	for _, ext := range loaded {
		names = append(names, ext.Name)
	}
	if !slices.Equal(names, planned) {
		t.Fatalf("loaded order = %v, want plan order %v", names, planned)
	}
	var wantProviders []string
	for _, name := range planned {
		wantProviders = append(wantProviders, name+"-provider")
	}
	mu.Lock()
	gotProviders := slices.Clone(providers)
	mu.Unlock()
	if !slices.Equal(gotProviders, wantProviders) {
		t.Fatalf("provider registration order = %v, want %v", gotProviders, wantProviders)
	}
	for _, ext := range loaded {
		if _, ok := ext.Commands[ext.Name+"-command"]; !ok || len(ext.Commands) != 1 {
			t.Errorf("%s commands = %v", ext.Name, ext.Commands)
		}
		if _, ok := ext.Tools[ext.Name+"_tool"]; !ok || len(ext.Tools) != 1 {
			t.Errorf("%s tools = %v", ext.Name, ext.Tools)
		}
		if _, ok := ext.Flags[ext.Name+"-flag"]; !ok || len(ext.Flags) != 1 {
			t.Errorf("%s flags = %v", ext.Name, ext.Flags)
		}
	}
}

// A failed cell does not stop later cells from committing, and errors keep
// plan order.
func TestLoadAllConcurrentStartReportsFailuresInPlanOrder(t *testing.T) {
	shortSockDir(t)
	dir := t.TempDir()
	configs := []ExtConfig{
		{Name: "first-bad", Source: writeOrderFixture(t, dir, "first-bad", `throw new Error("first");`), Enabled: true},
		{Name: "good", Source: writeOrderFixture(t, dir, "good", ""), Enabled: true},
		{Name: "second-bad", Source: writeOrderFixture(t, dir, "second-bad", `throw new Error("second");`), Enabled: true},
	}
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	loaded, errs := host.LoadAll(ctx, configs)
	if len(loaded) != 1 || loaded[0].Name != "good" {
		t.Fatalf("loaded = %v, want only good", loaded)
	}
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want two", errs)
	}
	var planned []string
	for _, cell := range PlanCells(configs, nil) {
		for _, name := range cellExtNames(cell) {
			if name != "good" {
				planned = append(planned, name)
			}
		}
	}
	for i, err := range errs {
		if want := fmt.Sprintf("extension %q:", planned[i]); len(err.Error()) < len(want) || err.Error()[:len(want)] != want {
			t.Fatalf("error %d = %v, want prefix %s", i, err, want)
		}
	}
}
