package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestPackagePatternCasesUpstream(t *testing.T) {
	type patternCase struct {
		name, kind, pkg    string
		files              map[string]string
		patterns, manifest []string
		states             map[string]string
	}
	const ext = "export default function() {}"
	for _, tc := range []patternCase{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1499
		{name: "should exclude extensions with ! pattern", kind: "extensions", files: map[string]string{"keep.ts": ext, "remove.ts": ext}, patterns: []string{"extensions", "!**/remove.ts"}, states: map[string]string{"keep.ts": "enabled", "remove.ts": "disabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1512
		{name: "should filter themes with glob patterns", kind: "themes", files: map[string]string{"dark.json": "{}", "light.json": "{}", "funky.json": "{}"}, patterns: []string{"themes", "!funky.json"}, states: map[string]string{"dark.json": "enabled", "light.json": "enabled", "funky.json": "disabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1527
		{name: "should filter prompts with exclusion pattern", kind: "prompts", files: map[string]string{"review.md": "Review code", "explain.md": "Explain code"}, patterns: []string{"prompts", "!explain.md"}, states: map[string]string{"review.md": "enabled", "explain.md": "disabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1540
		{name: "should filter skills with exclusion pattern", kind: "skills", files: map[string]string{"good-skill/SKILL.md": "---\nname: good-skill\ndescription: Good\n---\nContent", "bad-skill/SKILL.md": "---\nname: bad-skill\ndescription: Bad\n---\nContent"}, patterns: []string{"skills", "!**/bad-skill"}, states: map[string]string{"good-skill/SKILL.md": "enabled", "bad-skill/SKILL.md": "disabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1560
		{name: "should work without patterns backward compatible", kind: "extensions", files: map[string]string{"my-ext.ts": ext}, patterns: []string{"extensions/my-ext.ts"}, states: map[string]string{"my-ext.ts": "enabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1707
		{name: "should apply user filters on top of manifest filters not replace", kind: "extensions", pkg: "layered-pkg", files: map[string]string{"foo.ts": ext, "bar.ts": ext, "baz.ts": ext}, manifest: []string{"extensions", "!**/baz.ts"}, patterns: []string{"!**/bar.ts"}, states: map[string]string{"foo.ts": "enabled", "bar.ts": "disabled", "baz.ts": "absent"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1745
		{name: "should exclude extensions from package with ! pattern", kind: "extensions", pkg: "pattern-pkg", files: map[string]string{"foo.ts": ext, "bar.ts": ext, "baz.ts": ext}, patterns: []string{"!**/baz.ts"}, states: map[string]string{"foo.ts": "enabled", "bar.ts": "enabled", "baz.ts": "disabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1768
		{name: "should filter themes from package", kind: "themes", pkg: "theme-pkg", files: map[string]string{"nice.json": "{}", "ugly.json": "{}"}, patterns: []string{"!ugly.json"}, states: map[string]string{"nice.json": "enabled", "ugly.json": "disabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1789
		{name: "should combine include and exclude patterns", kind: "extensions", pkg: "combo-pkg", files: map[string]string{"alpha.ts": ext, "beta.ts": ext, "gamma.ts": ext}, patterns: []string{"**/alpha.ts", "**/beta.ts", "!**/beta.ts"}, states: map[string]string{"alpha.ts": "enabled", "beta.ts": "disabled", "gamma.ts": "disabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1812
		{name: "should work with direct paths no patterns", kind: "extensions", pkg: "direct-pkg", files: map[string]string{"one.ts": ext, "two.ts": ext}, patterns: []string{"extensions/one.ts"}, states: map[string]string{"one.ts": "enabled", "two.ts": "disabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1879
		{name: "should force-include extensions with + pattern after exclusion", kind: "extensions", files: map[string]string{"keep.ts": ext, "excluded.ts": ext, "force-back.ts": ext}, patterns: []string{"extensions", "!extensions/*.ts", "+extensions/force-back.ts"}, states: map[string]string{"keep.ts": "disabled", "excluded.ts": "disabled", "force-back.ts": "enabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1895
		{name: "should force-include overrides exclude in package filters", kind: "extensions", pkg: "force-pkg", files: map[string]string{"alpha.ts": ext, "beta.ts": ext, "gamma.ts": ext}, patterns: []string{"!**/*.ts", "+extensions/beta.ts"}, states: map[string]string{"alpha.ts": "disabled", "beta.ts": "enabled", "gamma.ts": "disabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1918
		{name: "should force-include multiple resources", kind: "skills", pkg: "multi-force-pkg", files: map[string]string{"skill-a/SKILL.md": "---\nname: skill-a\ndescription: A\n---\nContent", "skill-b/SKILL.md": "---\nname: skill-b\ndescription: B\n---\nContent", "skill-c/SKILL.md": "---\nname: skill-c\ndescription: C\n---\nContent"}, patterns: []string{"!**/*", "+skills/skill-a", "+skills/skill-c"}, states: map[string]string{"skill-a/SKILL.md": "enabled", "skill-b/SKILL.md": "disabled", "skill-c/SKILL.md": "enabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1943
		{name: "should force-include after specific exclusion", kind: "extensions", files: map[string]string{"a.ts": ext, "b.ts": ext}, patterns: []string{"extensions", "!extensions/b.ts", "+extensions/b.ts"}, states: map[string]string{"a.ts": "enabled", "b.ts": "enabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1957
		{name: "should handle force-include in manifest patterns", kind: "extensions", pkg: "manifest-force-pkg", files: map[string]string{"one.ts": ext, "two.ts": ext, "three.ts": ext}, manifest: []string{"extensions", "!**/two.ts", "+extensions/two.ts"}, states: map[string]string{"one.ts": "enabled", "two.ts": "enabled", "three.ts": "enabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1979
		{name: "should force-include themes", kind: "themes", files: map[string]string{"dark.json": "{}", "light.json": "{}", "special.json": "{}"}, patterns: []string{"themes", "!themes/*.json", "+themes/special.json"}, states: map[string]string{"dark.json": "disabled", "light.json": "disabled", "special.json": "enabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1994
		{name: "should force-include prompts", kind: "prompts", files: map[string]string{"review.md": "Review", "explain.md": "Explain", "debug.md": "Debug"}, patterns: []string{"prompts", "!prompts/*.md", "+prompts/debug.md"}, states: map[string]string{"review.md": "disabled", "explain.md": "disabled", "debug.md": "enabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2011
		{name: "should force-exclude top-level resources", kind: "extensions", files: map[string]string{"alpha.ts": ext, "beta.ts": ext}, patterns: []string{"extensions", "+extensions/alpha.ts", "-extensions/alpha.ts"}, states: map[string]string{"alpha.ts": "disabled", "beta.ts": "enabled"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2024
		{name: "should force-exclude in package filters", kind: "extensions", pkg: "force-exclude-pkg", files: map[string]string{"alpha.ts": ext, "beta.ts": ext}, patterns: []string{"extensions/*.ts", "+extensions/alpha.ts", "-extensions/alpha.ts"}, states: map[string]string{"alpha.ts": "disabled", "beta.ts": "enabled"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageResourceFixture(t)
			root := f.agent
			if tc.pkg != "" {
				root = filepath.Join(f.cwd, tc.pkg)
			}
			for path, contents := range tc.files {
				writePackageResource(t, filepath.Join(root, tc.kind, filepath.FromSlash(path)), contents)
			}
			if tc.manifest != nil {
				data, err := json.Marshal(map[string]any{"name": tc.pkg, "pi": map[string]any{tc.kind: tc.manifest}})
				if err != nil {
					t.Fatal(err)
				}
				writePackageResource(t, filepath.Join(root, "package.json"), string(data))
			}
			if tc.pkg != "" {
				pkg := codingagent.PackageSource{Source: root}
				if tc.patterns != nil {
					pkg.Extensions = []string{}
					pkg.Skills = []string{}
					pkg.Prompts = []string{}
					pkg.Themes = []string{}
					switch tc.kind {
					case "extensions":
						pkg.Extensions = tc.patterns
					case "skills":
						pkg.Skills = tc.patterns
					case "prompts":
						pkg.Prompts = tc.patterns
					case "themes":
						pkg.Themes = tc.patterns
					}
				}
				if err := f.settings.SetPackages([]codingagent.PackageSource{pkg}); err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				switch tc.kind {
				case "extensions":
					err = f.settings.SetExtensionPaths(tc.patterns)
				case "skills":
					err = f.settings.SetSkillPaths(tc.patterns)
				case "prompts":
					err = f.settings.SetPromptTemplatePaths(tc.patterns)
				case "themes":
					err = f.settings.SetThemePaths(tc.patterns)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			items := f.items(t)
			if tc.pkg == "layered-pkg" || tc.pkg == "multi-force-pkg" {
				rows := make([]string, 0, len(items))
				for _, item := range items {
					rel, err := filepath.Rel(root, item.Path)
					if err != nil {
						t.Fatal(err)
					}
					rows = append(rows, fmt.Sprintf("%s=%t", filepath.ToSlash(rel), item.Enabled))
				}
				slices.Sort(rows)
				raw, err := json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Printf("PATTERN_STATES %s %s\n", tc.pkg, raw)
			}
			for path, state := range tc.states {
				full := filepath.Join(root, tc.kind, filepath.FromSlash(path))
				if state == "absent" {
					for _, item := range items {
						if item.Path == full {
							t.Errorf("manifest-excluded path present: %+v", item)
						}
					}
					continue
				}
				requirePackageResource(t, items, full, tui.ResourceType(tc.kind), state == "enabled")
			}
		})
	}
}
