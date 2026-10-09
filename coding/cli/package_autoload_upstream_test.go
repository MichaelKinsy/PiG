package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestPackageAutoloadRuntimeDeltaOrder(t *testing.T) {
	// package-manager.ts resolves project delta entries before inherited user entries, including enabled entries' metadata and extension load order.
	f := newPackageProcessFixture(t, `throw new Error('unexpected process');`)
	pkg := filepath.Join(f.agent, "npm", "node_modules", "pi-tools")
	writePackageResource(t, filepath.Join(pkg, "package.json"), `{"name":"pi-tools","version":"1.0.0"}`)
	for _, name := range []string{"foo", "bar"} {
		writePackageResource(t, filepath.Join(pkg, "extensions", name+".ts"), "export default function() {}")
	}
	if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:pi-tools"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:pi-tools", Autoload: new(false), Extensions: []string{"+extensions/foo.ts"}}}); err != nil {
		t.Fatal(err)
	}
	configs := collectExtensionConfigs(f.cwd, f.agent, f.settings, Args{}, nil)
	want := []struct{ name, scope string }{{"foo.ts", "project"}, {"bar.ts", "user"}}
	if len(configs) != len(want) {
		t.Fatalf("configs=%+v", configs)
	}
	for i, expected := range want {
		info := configs[i].SourceInfo
		if filepath.Base(configs[i].Source) != expected.name || info.Scope != expected.scope {
			t.Fatalf("config%d=%+v sourceInfo=%+v want=%+v", i, configs[i], info, expected)
		}
	}
	fmt.Println("AUTOLOAD_RUNTIME foo.ts/project bar.ts/user")
}

func TestPackageAutoloadDefaultsDoNotInferDeltas(t *testing.T) {
	for _, setting := range []string{"", `,"autoload":true`} {
		t.Run(setting, func(t *testing.T) {
			f := newPackageProcessFixture(t, `throw new Error('unexpected process');`)
			user := filepath.Join(f.agent, "npm", "node_modules", "pi-tools")
			project := filepath.Join(codingagent.ProjectConfigDir(f.cwd), "npm", "node_modules", "pi-tools")
			for _, root := range []string{user, project} {
				writePackageResource(t, filepath.Join(root, "package.json"), `{"name":"pi-tools","version":"1.0.0"}`)
				writePackageResource(t, filepath.Join(root, "extensions/foo.ts"), "export default function() {}")
				writePackageResource(t, filepath.Join(root, "extensions/bar.ts"), "export default function() {}")
			}
			if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:pi-tools"}}); err != nil {
				t.Fatal(err)
			}
			var sources []codingagent.PackageSource
			if err := json.Unmarshal([]byte(`[{"source":"npm:pi-tools","extensions":["-extensions/foo.ts"]`+setting+`}]`), &sources); err != nil {
				t.Fatal(err)
			}
			if err := f.settings.SetProjectPackages(sources); err != nil {
				t.Fatal(err)
			}
			items := f.items(t)
			for _, item := range items {
				if item.Scope != "project" {
					t.Fatalf("autoload inferred a delta: %+v", items)
				}
			}
			requirePackageResource(t, items, filepath.Join(project, "extensions/foo.ts"), tui.ResourceExtensions, false)
			requirePackageResource(t, items, filepath.Join(project, "extensions/bar.ts"), tui.ResourceExtensions, true)
		})
	}
}

func TestPackageAutoloadDisabledOriginal(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1833
	t.Run("should resolve autoload-disabled project package entries as deltas over global packages", func(t *testing.T) {
		f := newPackageProcessFixture(t, `throw new Error('unexpected install');`)
		pkg := filepath.Join(f.agent, "npm", "node_modules", "pi-tools")
		writePackageResource(t, filepath.Join(pkg, "package.json"), `{"name":"pi-tools","version":"1.0.0"}`)
		for _, name := range []string{"foo", "bar"} {
			writePackageResource(t, filepath.Join(pkg, "extensions", name+".ts"), "export default function() {}")
		}
		if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:pi-tools"}}); err != nil {
			t.Fatal(err)
		}
		var sources []codingagent.PackageSource
		if err := json.Unmarshal([]byte(`[{"source":"npm:pi-tools","autoload":false,"extensions":["-extensions/foo.ts"]}]`), &sources); err != nil {
			t.Fatal(err)
		}
		if err := f.settings.SetProjectPackages(sources); err != nil {
			t.Fatal(err)
		}
		_, missing := packagemanager.EnsureConfiguredPackagesInstalled(f.cwd, f.settings.AgentDir(), f.settings)
		if len(missing) > 0 {
			t.Fatal(missing)
		}
		items := f.items(t)
		foo := requirePackageResource(t, items, filepath.Join(pkg, "extensions/foo.ts"), tui.ResourceExtensions, false)
		bar := requirePackageResource(t, items, filepath.Join(pkg, "extensions/bar.ts"), tui.ResourceExtensions, true)
		if foo.Scope != "project" || bar.Scope != "user" {
			t.Fatalf("scopes foo=%s bar=%s", foo.Scope, bar.Scope)
		}
		if calls := f.calls(t); len(calls) != 0 {
			t.Fatalf("unexpected installation: %+v", calls)
		}
		fmt.Printf("AUTOLOAD_DELTA foo=%t/%s bar=%t/%s\n", foo.Enabled, foo.Scope, bar.Enabled, bar.Scope)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/package-manager.test.ts:1859
	t.Run("should resolve autoload-disabled package entries as positive-only without a global package", func(t *testing.T) {
		f := newPackageProcessFixture(t, `throw new Error('unexpected install');`)
		pkg := filepath.Join(f.cwd, "positive-only-pkg")
		for _, name := range []string{"foo", "bar"} {
			writePackageResource(t, filepath.Join(pkg, "extensions", name+".ts"), "export default function() {}")
		}
		writePackageResource(t, filepath.Join(pkg, "skills/foo/SKILL.md"), "# Foo\n")
		rel, err := filepath.Rel(codingagent.ProjectConfigDir(f.cwd), pkg)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal([]any{map[string]any{"source": rel, "autoload": false, "extensions": []string{"+extensions/foo.ts"}}})
		if err != nil {
			t.Fatal(err)
		}
		var sources []codingagent.PackageSource
		if err := json.Unmarshal(raw, &sources); err != nil {
			t.Fatal(err)
		}
		if err := f.settings.SetProjectPackages(sources); err != nil {
			t.Fatal(err)
		}
		items := f.items(t)
		if len(items) != 1 || items[0].Path != filepath.Join(pkg, "extensions/foo.ts") || !items[0].Enabled {
			t.Fatalf("positive-only=%+v", items)
		}
		fmt.Println("AUTOLOAD_ONLY extensions/foo.ts; skills=[]")
	})
}
