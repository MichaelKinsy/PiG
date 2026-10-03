package subprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The module/factory cache belongs to the Node loader; exercise its shipped SDK graph through an actual source extension, not a second Go implementation of module evaluation.
func TestUpstreamExtensionFactoryCache(t *testing.T) {
	nodeCellRequireNode(t)
	entry := filepath.Join(findModuleRoot(t), "test/parity/scenarios/extensions-runtime/testdata/factory-cache/probe.mjs")
	for _, tc := range []struct {
		name, mode             string
		moduleLoads, factories int
		fresh                  bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/extension-factory-cache.test.ts:69
		{"caches extension modules for cached same-cwd loads but reruns factories", "same-cwd", 1, 2, true},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/extension-factory-cache.test.ts:83
		{"does not cache direct loadExtensions calls", "direct", 2, 2, false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/extension-factory-cache.test.ts:95
		{"clears the cache on resource loader reload", "reload", 2, 2, false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/extension-factory-cache.test.ts:115
		{"keeps the cache scoped to one cwd", "cross-cwd", 2, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, isolation := range []string{"", "isolated"} {
				t.Run("isolation="+isolation, func(t *testing.T) {
					root := t.TempDir()
					t.Setenv("PIG_FACTORY_CACHE_ROOT", root)
					t.Setenv("PIG_FACTORY_CACHE_CASE", tc.mode)
					t.Setenv("PIG_FACTORY_CACHE_DIST", "")
					host := NewHost(root)
					t.Cleanup(func() { host.Shutdown("test done") })
					loaded, failures := host.LoadAll(t.Context(), []ExtConfig{{
						Name: "cache-probe", Source: entry, Enabled: true, Isolation: isolation,
					}})
					if len(failures) != 0 || len(loaded) != 1 {
						t.Fatalf("loaded=%v failures=%v", loaded, failures)
					}
					var result struct {
						ModuleLoads    int   `json:"moduleLoads"`
						FactoryRuns    int   `json:"factoryRuns"`
						FreshExtension *bool `json:"freshExtension"`
						FreshRuntime   *bool `json:"freshRuntime"`
					}
					raw := loaded[0].Commands["cache-report"].Description
					if err := json.Unmarshal([]byte(raw), &result); err != nil {
						t.Fatal(err)
					}
					if result.ModuleLoads != tc.moduleLoads || result.FactoryRuns != tc.factories {
						t.Fatalf("cache=%s; want %d module loads and %d factory runs", raw, tc.moduleLoads, tc.factories)
					}
					if tc.fresh && (result.FreshExtension == nil || !*result.FreshExtension || result.FreshRuntime == nil || !*result.FreshRuntime) {
						t.Fatalf("cached factory reused extension/runtime identity: %s", raw)
					}
				})
			}
		})
	}
}

// BenchmarkFactoryCacheThroughNodeHost includes source-host startup, the inner loader case and joined shutdown with a warm artifact cache.
func BenchmarkFactoryCacheThroughNodeHost(b *testing.B) {
	nodeCellRequireNode(b)
	entry := filepath.Join(findModuleRoot(b), "test/parity/scenarios/extensions-runtime/testdata/factory-cache/probe.mjs")
	for _, mode := range []string{"same-cwd", "direct", "reload", "cross-cwd"} {
		b.Run(mode, func(b *testing.B) {
			root, cache := b.TempDir(), b.TempDir()
			b.Setenv("PIG_FACTORY_CACHE_ROOT", root)
			b.Setenv("PIG_FACTORY_CACHE_CASE", mode)
			b.Setenv("PIG_FACTORY_CACHE_DIST", "")
			run := func() {
				host := NewHostWithConfigRoot(root, cache)
				loaded, failures := host.LoadAll(b.Context(), []ExtConfig{{Name: "cache-probe", Source: entry, Enabled: true}})
				host.Shutdown("benchmark done")
				if len(failures) != 0 || len(loaded) != 1 {
					b.Fatalf("loaded=%v failures=%v", loaded, failures)
				}
			}
			run()
			b.ReportAllocs()
			for b.Loop() {
				run()
			}
		})
	}
}

// The direct-load and reload cases also exercise the main Go Host, using an append-only evaluation trace because separate runtime processes do not share globalThis.
func TestHostUncachedFactoryModuleEvaluation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"direct", "reload"} {
		t.Run(mode, func(t *testing.T) {
			nodeCellRequireNode(t)
			root := t.TempDir()
			trace := filepath.Join(root, "evaluations.log")
			entry := filepath.Join(root, "counting.ts")
			write(t, entry, fmt.Sprintf(`import { appendFileSync } from "node:fs";
appendFileSync(%q, "module\n");
export default function() { appendFileSync(%q, "factory\n"); }
`, trace, trace))
			host := NewHost(root)
			t.Cleanup(func() { host.Shutdown("test done") })
			config := ExtConfig{Name: "counting", Source: entry, Enabled: true}
			first, err := host.Load(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "direct" {
				// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/extension-factory-cache.test.ts:83
				second, err := host.Load(t.Context(), config)
				if err != nil {
					t.Fatal(err)
				}
				if first == second {
					t.Fatal("direct load reused the extension instance")
				}
			} else {
				// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/extension-factory-cache.test.ts:95
				host.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{config}, nil })
				if loaded, err := host.Reload(t.Context()); err != nil || len(loaded) != 1 {
					t.Fatalf("reload=%v error=%v", loaded, err)
				}
			}
			got, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "module\nfactory\nmodule\nfactory\n" {
				t.Fatalf("module/factory evaluation order=%q", got)
			}
		})
	}
}
