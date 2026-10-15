package cli

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The pinned Pi probe uses reversed project/global package orders. addResource keeps disabled project entries as well as enabled entries before inherited resources for every resource type.
func TestPackageAutoloadResourceOrderAcrossPackages(t *testing.T) {
	f := newPackageProcessFixture(t, `throw new Error('unexpected process');`)
	for _, pkg := range []string{"one", "two"} {
		root := filepath.Join(f.agent, "npm", "node_modules", pkg)
		writePackageResource(t, filepath.Join(root, "package.json"), fmt.Sprintf(`{"name":%q,"version":"1.0.0"}`, pkg))
		for _, name := range []string{"foo", "bar"} {
			writePackageResource(t, filepath.Join(root, "extensions", name+".ts"), "export default function() {}")
			writePackageResource(t, filepath.Join(root, "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: test\n---\n")
			writePackageResource(t, filepath.Join(root, "prompts", name+".md"), name)
			writePackageResource(t, filepath.Join(root, "themes", name+".json"), `{}`)
		}
	}
	if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:one"}, {Source: "npm:two"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.settings.SetProjectPackages([]codingagent.PackageSource{
		{Source: "npm:two", Autoload: new(false), Extensions: []string{"+extensions/foo.ts"}, Skills: []string{"+skills/foo"}, Prompts: []string{"+prompts/foo.md"}, Themes: []string{"+themes/foo.json"}},
		{Source: "npm:one", Autoload: new(false), Extensions: []string{"-extensions/foo.ts"}, Skills: []string{"-skills/foo"}, Prompts: []string{"-prompts/foo.md"}, Themes: []string{"-themes/foo.json"}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []packagecontent.Kind{packagecontent.Extensions, packagecontent.Skills, packagecontent.Prompts, packagecontent.Themes} {
		t.Run(string(kind), func(t *testing.T) {
			items, err := collectConfigResourceItems(f.cwd, f.agent, f.settings)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			var wantPaths []string
			for _, item := range items {
				if packagecontent.Kind(item.ResourceType) == kind {
					got = append(got, fmt.Sprintf("%s/%s/%t", item.Source, item.Scope, item.Enabled))
					if item.Enabled {
						wantPaths = append(wantPaths, item.Path)
					}
				}
			}
			want := []string{"npm:two/project/true", "npm:one/project/false", "npm:one/user/true", "npm:two/user/true"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("states=%v want=%v", got, want)
			}
			var paths []string
			switch kind {
			case packagecontent.Extensions:
				for _, config := range collectExtensionConfigs(f.cwd, f.agent, f.settings, Args{}, nil) {
					paths = append(paths, config.Source)
				}
			case packagecontent.Skills:
				paths = collectSkillInputs(f.cwd, f.agent, f.settings, Args{}, nil)
			case packagecontent.Prompts:
				paths = collectPromptPaths(f.cwd, f.agent, f.settings, Args{}, true)
			case packagecontent.Themes:
				paths = collectThemePaths(f.cwd, f.agent, f.settings, Args{}, true)
			}
			if !reflect.DeepEqual(paths, wantPaths) {
				t.Fatalf("runtime paths=%v want=%v", paths, wantPaths)
			}
			if kind == packagecontent.Extensions {
				configs, diagnostics, err := authExtensionConfigs(f.cwd, f.agent, f.settings)
				if err != nil || len(diagnostics) != 0 {
					t.Fatalf("auth diagnostics=%+v error=%v", diagnostics, err)
				}
				var authPaths []string
				for _, config := range configs {
					authPaths = append(authPaths, config.Source)
				}
				if !reflect.DeepEqual(authPaths, wantPaths) {
					t.Fatalf("auth paths=%v want=%v", authPaths, wantPaths)
				}
			}
		})
	}
}

func TestPackageAutoloadAuthExcludesProjectDisabledResource(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			f := newPackageProcessFixture(t, `throw new Error('unexpected process');`)
			root := filepath.Join(f.agent, "npm", "node_modules", "one")
			writePackageResource(t, filepath.Join(root, "package.json"), `{"name":"one","version":"1.0.0","pi":{"extensions":["extensions/foo.ts","extensions/bar.ts"]}}`)
			for _, name := range []string{"foo", "bar"} {
				if name != "foo" || !missing {
					writePackageResource(t, filepath.Join(root, "extensions", name+".ts"), "export default function() {}")
				}
			}
			if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:one"}}); err != nil {
				t.Fatal(err)
			}
			if err := f.settings.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:one", Autoload: new(false), Extensions: []string{"-extensions/foo.ts"}}}); err != nil {
				t.Fatal(err)
			}
			configs, diagnostics, err := authExtensionConfigs(f.cwd, f.agent, f.settings)
			if err != nil || len(diagnostics) != 0 {
				t.Fatalf("diagnostics=%+v error=%v", diagnostics, err)
			}
			if len(configs) != 1 || configs[0].Source != filepath.Join(root, "extensions/bar.ts") {
				t.Fatalf("auth loaded a project-disabled extension: %+v", configs)
			}
		})
	}
}

func TestPackageAutoloadAuthDiagnosticsKeepMissingFirst(t *testing.T) {
	f := newPackageProcessFixture(t, `throw new Error('unexpected process');`)
	root := filepath.Join(f.agent, "npm", "node_modules", "one")
	writePackageResource(t, filepath.Join(root, "package.json"), `{"name":"one","version":"1.0.0","pi":{"extensions":["extensions/missing.ts","extensions/invalid"]}}`)
	writePackageResource(t, filepath.Join(root, "extensions/invalid/go.mod"), "module example.com/invalid\n\ngo 1.26\n")
	if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:one"}}); err != nil {
		t.Fatal(err)
	}
	_, diagnostics, err := authExtensionConfigs(f.cwd, f.agent, f.settings)
	if err != nil || len(diagnostics) != 2 || diagnostics[0].Extension != "extensions/missing.ts" {
		t.Fatalf("missing declaration must precede invalid-source diagnostics: %+v error=%v", diagnostics, err)
	}
}

func TestPackageAutoloadStartupThemes(t *testing.T) {
	f := newPackageProcessFixture(t, `throw new Error('unexpected process');`)
	root := filepath.Join(f.agent, "npm", "node_modules", "one")
	writePackageResource(t, filepath.Join(root, "package.json"), `{"name":"one","version":"1.0.0"}`)
	for _, name := range []string{"foo", "bar"} {
		writePackageResource(t, filepath.Join(root, "themes", name+".json"), `{}`)
	}
	if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:one", Autoload: new(false), Themes: []string{"+themes/foo.json"}}}); err != nil {
		t.Fatal(err)
	}
	got := collectStartupThemePaths(f.cwd, f.agent, f.settings)
	want := []string{filepath.Join(root, "themes/foo.json")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("startup themes=%v want=%v", got, want)
	}
}

func TestPackageAutoloadUserEntriesRetainTouchedDisabledPaths(t *testing.T) {
	f := newPackageProcessFixture(t, `throw new Error('unexpected process');`)
	root := filepath.Join(f.agent, "npm", "node_modules", "one")
	writePackageResource(t, filepath.Join(root, "package.json"), `{"name":"one","version":"1.0.0"}`)
	for _, name := range []string{"foo", "bar", "untouched"} {
		writePackageResource(t, filepath.Join(root, "extensions", name+".ts"), "export default function() {}")
	}
	if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: "npm:one", Autoload: new(false), Extensions: []string{"+extensions/foo.ts", "-extensions/foo.ts", "extensions/bar.ts"}}}); err != nil {
		t.Fatal(err)
	}
	items, err := collectConfigResourceItems(f.cwd, f.agent, f.settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Path != filepath.Join(root, "extensions/foo.ts") || items[0].Enabled || items[1].Path != filepath.Join(root, "extensions/bar.ts") || !items[1].Enabled {
		t.Fatalf("items=%+v", items)
	}
	configs := collectExtensionConfigs(f.cwd, f.agent, f.settings, Args{}, nil)
	if len(configs) != 1 || configs[0].Source != filepath.Join(root, "extensions/bar.ts") {
		t.Fatalf("configs=%+v", configs)
	}
}
