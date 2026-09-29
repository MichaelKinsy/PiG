package main

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestPackageMultiFileExtensionDiscoveryUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, pkg       string
		files           map[string]string
		enabled, absent []string
		count           int
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2159
		{name: "should only load index.ts from subdirectories not helper modules", pkg: "multifile-pkg", files: map[string]string{"extensions/subagent/index.ts": "import { helper } from \"./agents.ts\";\nexport default function(api) { api.registerTool({ name: \"test\", description: \"test\", execute: async () => helper() }); }", "extensions/subagent/agents.ts": "export function helper() { return \"helper\"; }", "extensions/standalone.ts": "export default function(api) {}"}, enabled: []string{"extensions/subagent/index.ts", "extensions/standalone.ts"}, absent: []string{"extensions/subagent/agents.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2189
		{name: "should respect package.json pi.extensions manifest in subdirectories", pkg: "manifest-subdir-pkg", files: map[string]string{"extensions/custom/package.json": `{"pi":{"extensions":["./main.ts"]}}`, "extensions/custom/main.ts": "export default function(api) {}", "extensions/custom/utils.ts": "export const util = 1;"}, enabled: []string{"extensions/custom/main.ts"}, absent: []string{"extensions/custom/utils.ts"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2214
		{name: "should handle mixed top-level files and subdirectories", pkg: "mixed-pkg", files: map[string]string{"extensions/simple.ts": "export default function(api) {}", "extensions/complex/index.ts": "import { a } from './a.ts'; export default function(api) {}", "extensions/complex/a.ts": "export const a = 1;", "extensions/complex/b.ts": "export const b = 2;"}, enabled: []string{"extensions/simple.ts", "extensions/complex/index.ts"}, absent: []string{"extensions/complex/a.ts", "extensions/complex/b.ts"}, count: 2},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2243
		{name: "should skip subdirectories without index.ts or manifest", pkg: "no-entry-pkg", files: map[string]string{"extensions/broken/helper.ts": "export const x = 1;", "extensions/broken/another.ts": "export const y = 2;", "extensions/valid.ts": "export default function(api) {}"}, enabled: []string{"extensions/valid.ts"}, count: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageResourceFixture(t)
			pkg := filepath.Join(f.cwd, tc.pkg)
			for path, text := range tc.files {
				writePackageResource(t, filepath.Join(pkg, filepath.FromSlash(path)), text)
			}
			if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
				t.Fatal(err)
			}
			items := f.items(t)
			for _, path := range tc.enabled {
				requirePackageResource(t, items, filepath.Join(pkg, filepath.FromSlash(path)), tui.ResourceExtensions, true)
			}
			for _, path := range tc.absent {
				for _, item := range items {
					if item.Path == filepath.Join(pkg, filepath.FromSlash(path)) {
						t.Errorf("helper module leaked: %+v", item)
					}
				}
			}
			if tc.count > 0 {
				count := 0
				for _, item := range items {
					if item.Enabled && item.ResourceType == string(tui.ResourceExtensions) {
						count++
					}
				}
				if count != tc.count {
					t.Fatalf("enabled count=%d, want%d", count, tc.count)
				}
			}
			configs := collectExtensionConfigs(f.cwd, f.agent, f.settings, CLIFlags{Extensions: []string{pkg}, NoExtensions: true}, nil)
			for _, config := range configs {
				for _, path := range tc.absent {
					if config.Source == filepath.Join(pkg, filepath.FromSlash(path)) {
						t.Errorf("runtime loaded helper module: %+v", config)
					}
				}
			}
		})
	}
}
