package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestPackageManifestPatternsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1574
	t.Run("should support glob patterns in manifest extensions", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "manifest-pkg")
		for _, file := range []string{"extensions/local.ts", "node_modules/dep/extensions/remote.ts", "node_modules/dep/extensions/skip.ts"} {
			writePackageResource(t, filepath.Join(pkg, filepath.FromSlash(file)), "export default function() {}")
		}
		writePackageResource(t, filepath.Join(pkg, "package.json"), `{"name":"manifest-pkg","pi":{"extensions":["extensions","node_modules/dep/extensions","!**/skip.ts"]}}`)
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		requirePackageResource(t, items, filepath.Join(pkg, "extensions/local.ts"), tui.ResourceExtensions, true)
		requirePackageResource(t, items, filepath.Join(pkg, "node_modules/dep/extensions/remote.ts"), tui.ResourceExtensions, true)
		for _, item := range items {
			if filepath.Base(item.Path) == "skip.ts" {
				t.Fatalf("manifest excluded entry=%+v", item)
			}
		}
		var paths []string
		for _, item := range items {
			rel, err := filepath.Rel(pkg, item.Path)
			if err != nil {
				t.Fatal(err)
			}
			paths = append(paths, filepath.ToSlash(rel))
		}
		slices.Sort(paths)
		raw, err := json.Marshal(paths)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("MANIFEST_GLOBSTAR %s\n", raw)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1597
	t.Run("should support glob patterns in manifest skills", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "skill-manifest-pkg")
		writePackageResource(t, filepath.Join(pkg, "skills/good-skill/SKILL.md"), "---\nname: good-skill\ndescription: Good\n---\nContent")
		writePackageResource(t, filepath.Join(pkg, "skills/bad-skill/SKILL.md"), "---\nname: bad-skill\ndescription: Bad\n---\nContent")
		writePackageResource(t, filepath.Join(pkg, "package.json"), `{"name":"skill-manifest-pkg","pi":{"skills":["skills","!**/bad-skill"]}}`)
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		requirePackageResource(t, items, filepath.Join(pkg, "skills/good-skill/SKILL.md"), tui.ResourceSkills, true)
		for _, item := range items {
			if filepath.Base(filepath.Dir(item.Path)) == "bad-skill" {
				t.Fatalf("excluded skill=%+v", item)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1624
	t.Run("should expand positive glob manifest entries before collecting skills", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "skill-manifest-glob-pkg")
		first := filepath.Join(pkg, "plugins/pdf-to-markdown/skills/pdf-to-markdown/SKILL.md")
		second := filepath.Join(pkg, "plugins/nutrient-dws/skills/document-processor-api/SKILL.md")
		writePackageResource(t, first, "---\nname: pdf-to-markdown\ndescription: PDF to Markdown\n---\nContent")
		writePackageResource(t, second, "---\nname: document-processor-api\ndescription: DWS\n---\nContent")
		writePackageResource(t, filepath.Join(pkg, "package.json"), `{"name":"skill-manifest-glob-pkg","pi":{"skills":["./plugins/*/skills"]}}`)
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		requirePackageResource(t, items, first, tui.ResourceSkills, true)
		requirePackageResource(t, items, second, tui.ResourceSkills, true)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1651
	t.Run("should sort manifest glob matches and use exact entries for dot paths and symlink traversal", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "manifest-glob-semantics-pkg")
		for _, file := range []string{"extension-files/z.ts", "extension-files/a.ts", "extension-files/.ignored.ts", "extension-files/nested/.hidden.ts", "extension-groups/group/index.ts"} {
			writePackageResource(t, filepath.Join(pkg, filepath.FromSlash(file)), "export default function() {}")
		}
		local := filepath.Join(pkg, "plugins/local/skills/local-skill/SKILL.md")
		linked := filepath.Join(pkg, "linked-plugin-source/skills/linked-skill/SKILL.md")
		writePackageResource(t, local, "---\nname: local-skill\ndescription: Local\n---\n")
		writePackageResource(t, linked, "---\nname: linked-skill\ndescription: Linked\n---\n")
		if err := os.MkdirAll(filepath.Join(pkg, "plugins"), 0o755); err != nil {
			t.Fatal(err)
		}
		requirePackageDirectoryLink(t, filepath.Join(pkg, "linked-plugin-source"), filepath.Join(pkg, "plugins/linked"))
		manifest := map[string]any{"name": "manifest-glob-semantics-pkg", "pi": map[string]any{"extensions": []string{"./extension-files/*.ts", "./extension-files/**/.ignored.ts", "./extension-files/nested/.hidden.ts", "./extension-groups/*/"}, "skills": []string{"./plugins/*/skills", "./plugins/linked/skills"}}}
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		writePackageResource(t, filepath.Join(pkg, "package.json"), string(raw))
		configs := collectExtensionConfigs(f.cwd, f.agent, f.settings, CLIFlags{Extensions: []string{pkg}, NoExtensions: true}, nil)
		var paths []string
		for _, config := range configs {
			relative, err := filepath.Rel(pkg, config.Source)
			if err != nil {
				t.Fatal(err)
			}
			paths = append(paths, relative)
		}
		want := []string{filepath.Join("extension-files", "a.ts"), filepath.Join("extension-files", "z.ts"), filepath.Join("extension-files", "nested", ".hidden.ts"), filepath.Join("extension-groups", "group", "index.ts")}
		if !reflect.DeepEqual(paths, want) {
			t.Fatalf("extension order=%q, want %q", paths, want)
		}
		for i := range paths {
			paths[i] = filepath.ToSlash(paths[i])
		}
		raw, err = json.Marshal(paths)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("MANIFEST_ORDER %s\n", raw)
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		requirePackageResource(t, items, local, tui.ResourceSkills, true)
		requirePackageResource(t, items, filepath.Join(pkg, "plugins/linked/skills/linked-skill/SKILL.md"), tui.ResourceSkills, true)
	})
}
