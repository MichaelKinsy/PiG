package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestPackageLocalSourceResolutionUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:572
	t.Run("should resolve local paths", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		path := filepath.Join(f.cwd, "ext.ts")
		writePackageResource(t, path, "export default function() {}")
		configs := collectExtensionConfigs(f.cwd, f.agent, f.settings, Args{Extensions: []string{path}, NoExtensions: true}, nil)
		found := false
		for _, config := range configs {
			if config.Source == path && config.Enabled {
				found = true
			}
		}
		if !found {
			t.Fatalf("local extension absent: %+v", configs)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:580
	t.Run("should handle directories with pi manifest", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "my-package")
		writePackageResource(t, filepath.Join(pkg, "package.json"), `{"name":"my-package","pi":{"extensions":["./src/index.ts"],"skills":["./skills"]}}`)
		extension, skill := filepath.Join(pkg, "src/index.ts"), filepath.Join(pkg, "skills/my-skill/SKILL.md")
		writePackageResource(t, extension, "export default function() {}")
		writePackageResource(t, skill, "---\nname: my-skill\ndescription: Test\n---\nContent")
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		requirePackageResource(t, items, extension, tui.ResourceExtensions, true)
		requirePackageResource(t, items, skill, tui.ResourceSkills, true)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:609
	t.Run("should keep pi manifest entries with leading tilde package-relative", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "tilde-manifest-package")
		extensions := []string{filepath.Join(pkg, "~extensions/main.ts"), filepath.Join(pkg, "~/extensions/alt.ts")}
		skills := []string{filepath.Join(pkg, "~skills/direct-skill/SKILL.md"), filepath.Join(pkg, "~/skills/slash-skill/SKILL.md")}
		for _, path := range extensions {
			writePackageResource(t, path, "export default function() {}")
		}
		writePackageResource(t, skills[0], "---\nname: direct-skill\ndescription: Direct\n---\nContent")
		writePackageResource(t, skills[1], "---\nname: slash-skill\ndescription: Slash\n---\nContent")
		writePackageResource(t, filepath.Join(pkg, "package.json"), `{"name":"tilde-manifest-package","pi":{"extensions":["~extensions/main.ts","~/extensions/alt.ts"],"skills":["~skills","~/skills"]}}`)
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		for _, path := range extensions {
			requirePackageResource(t, items, path, tui.ResourceExtensions, true)
		}
		for _, path := range skills {
			requirePackageResource(t, items, path, tui.ResourceSkills, true)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:643
	t.Run("should handle directories with auto-discovery layout", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "auto-pkg")
		extension, theme := filepath.Join(pkg, "extensions/main.ts"), filepath.Join(pkg, "themes/dark.json")
		writePackageResource(t, extension, "export default function() {}")
		writePackageResource(t, theme, "{}")
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		requirePackageResource(t, items, theme, tui.ResourceThemes, true)
		entry := requirePackageResource(t, items, extension, tui.ResourceExtensions, true)
		rel, err := filepath.Rel(pkg, entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("AUTO_EXTENSION_ENTRY %s\n", filepath.ToSlash(rel))
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:655
	t.Run("should stop recursing when a package skill directory contains SKILL.md", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "skill-root-pkg")
		root, nested := filepath.Join(pkg, "skills/root-skill/SKILL.md"), filepath.Join(pkg, "skills/root-skill/nested-skill/SKILL.md")
		writePackageResource(t, root, "---\nname: root-skill\ndescription: Root skill\n---\n")
		writePackageResource(t, nested, "---\nname: nested-skill\ndescription: Nested skill\n---\n")
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: pkg}}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		requirePackageResource(t, items, root, tui.ResourceSkills, true)
		for _, item := range items {
			if item.Path == nested {
				t.Fatalf("nested package skill leaked: %+v", item)
			}
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:686
func TestPackageCommandPreservesSpacedArgvUpstream(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	const value = `C:\Users\A B\.pi\npm`
	got, err := packagemanager.RunCmd(node, "-e", "console.log(process.argv[1])", value)
	if err != nil || got != value {
		t.Fatalf("output=%q err=%v", got, err)
	}
}

func TestPackageIdentityDeduplicationUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2047
	t.Run("should dedupe same local package in global and project project wins", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		pkg := filepath.Join(f.cwd, "shared-pkg")
		path := filepath.Join(pkg, "extensions/shared.ts")
		writePackageResource(t, path, "export default function() {}")
		sources := []codingagent.PackageSource{{Source: pkg}}
		if err := f.settings.SetPackages(sources); err != nil {
			t.Fatal(err)
		}
		if err := f.settings.SetProjectPackages(sources); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.settings.GetGlobalSettings().Packages, sources) || !reflect.DeepEqual(f.settings.GetProjectSettings().Packages, sources) {
			t.Fatal("settings did not retain original declarations")
		}
		items := f.items(t)
		if len(items) != 1 || items[0].Path != path || items[0].Scope != "project" {
			t.Fatalf("resolved=%+v", items)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2069
	t.Run("should keep both if different packages", func(t *testing.T) {
		f := newPackageResourceFixture(t)
		first, second := filepath.Join(f.cwd, "pkg1"), filepath.Join(f.cwd, "pkg2")
		a, b := filepath.Join(first, "extensions/from-pkg1.ts"), filepath.Join(second, "extensions/from-pkg2.ts")
		writePackageResource(t, a, "export default function() {}")
		writePackageResource(t, b, "export default function() {}")
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: first}}); err != nil {
			t.Fatal(err)
		}
		if err := f.settings.SetProjectPackages([]codingagent.PackageSource{{Source: second}}); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		requirePackageResource(t, items, a, tui.ResourceExtensions, true)
		requirePackageResource(t, items, b, tui.ResourceExtensions, true)
	})
	for _, tc := range []struct {
		name string
		urls []string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2085
		{"should dedupe SSH and HTTPS URLs for same repo", []string{"https://github.com/user/repo", "git:git@github.com:user/repo"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2099
		{"should dedupe SSH and HTTPS with refs", []string{"https://github.com/user/repo@v1.0.0", "git:git@github.com:user/repo@v1.0.0"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2112
		{"should dedupe SSH URL with ssh protocol and git at format", []string{"ssh://git@github.com/user/repo", "git:git@github.com:user/repo"}},
		// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2125
		{"should dedupe all supported URL formats for same repo", []string{"https://github.com/user/repo", "https://github.com/user/repo.git", "ssh://git@github.com/user/repo", "git:https://github.com/user/repo", "git:github.com/user/repo", "git:git@github.com:user/repo", "git:git@github.com:user/repo.git"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, url := range tc.urls {
				if got := packagemanager.PackageSourceIdentity(t.TempDir(), url); got != "git:github.com/user/repo" {
					t.Fatalf("identity(%s)=%s", url, got)
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:2144
	t.Run("should keep different repos separate HTTPS vs SSH", func(t *testing.T) {
		cwd := t.TempDir()
		a, b := packagemanager.PackageSourceIdentity(cwd, "https://github.com/user/repo1"), packagemanager.PackageSourceIdentity(cwd, "git:git@github.com:user/repo2")
		if a != "git:github.com/user/repo1" || b != "git:github.com/user/repo2" || a == b {
			t.Fatalf("identities=%q/%q", a, b)
		}
	})
}
