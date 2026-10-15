package packagemanager

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's applyPackageFilter matches each member's absolute path with the Package root as the base (package-manager.ts:2280-2306, 661-685), so a configured filter can name a member by its absolute path.
func TestPackageFilterMatchesAbsolutePaths(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	_, agentDir, sm, pm := resolveFixture(t)
	root := filepath.Join(agentDir, "pkg")
	for _, name := range []string{"a", "b"} {
		writeResolveFile(t, filepath.Join(root, "prompts", name+".md"), "x\n")
	}
	absolute := filepath.ToSlash(filepath.Join(root, "prompts", "a.md"))
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "./pkg", Prompts: []string{absolute}}}); err != nil {
		t.Fatal(err)
	}
	resolved, err := pm.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var enabled []string
	for _, r := range resolved.Prompts {
		if r.Enabled {
			enabled = append(enabled, filepath.Base(r.Path))
		}
	}
	if !slices.Equal(enabled, []string{"a.md"}) {
		t.Fatalf("enabled prompts %q, want a.md", enabled)
	}
}

// Pi's resolve() rejects when a settings array's pattern, an auto-discovered resource's override or a built-in extension's override cannot compile (package-manager.ts:933-986, 2398-2411, 2413-2575), each only once a path reaches it.
func TestResolveRejectsSettingsPatternErrors(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	const syntax = `Invalid regular expression: /^(?!\.)[\p{L}\p{Nl}]\-`
	for _, tc := range []struct {
		name, settings string
		prompt         bool
		want           string
	}{
		{"a settings array pattern", `{"prompts":["prompts","[[:alpha:]]-*"]}`, true, syntax + `[^/]*?$/u: Invalid escape`},
		{"an auto-discovered resource's override", `{"prompts":["![[:alpha:]]-x"]}`, true, syntax + `x$/u: Invalid escape`},
		{"no auto-discovered resource", `{"prompts":["![[:alpha:]]-x"]}`, false, ""},
		{"a built-in extension's override", `{"extensions":["![[:alpha:]]-x"]}`, false, syntax + `x$/u: Invalid escape`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, agentDir, _, _ := resolveFixture(t)
			if tc.prompt {
				writeResolveFile(t, filepath.Join(agentDir, "prompts", "a.md"), "x\n")
			}
			writeResolveFile(t, filepath.Join(agentDir, "settings.json"), tc.settings)
			cwd := t.TempDir()
			sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
			pm := NewPackageManager(PackageManagerOptions{CWD: cwd, SettingsManager: sm, BuiltinExtensions: []string{"probe"}})
			_, err := pm.Resolve(nil)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.HasSuffix(err.Error(), tc.want)) {
				t.Fatalf("Resolve = %v, want %q", err, tc.want)
			}
		})
	}
}

// A filter that throws ends a local Package's collection at its kind (collectPackageResources; resolveLocalExtensionSource's catch, package-manager.ts:1372-1389), so the inventory lists neither the members nor the missing members of that kind and the later ones.
func TestPackageInventoryStopsAtAFilterThrow(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	root := t.TempDir()
	writeResolveFile(t, filepath.Join(root, "package.json"), `{"name":"p","pi":{"extensions":["extensions/missing.js"],"skills":["skills"],"prompts":["prompts/a.md","prompts/missing.md"]}}`)
	writeResolveFile(t, filepath.Join(root, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nBody\n")
	writeResolveFile(t, filepath.Join(root, "prompts", "a.md"), "x\n")
	pkg := ConfiguredPackage{Source: codingagent.PackageSource{Source: root, Skills: []string{"[[:alpha:]]-*"}}, Scope: "user"}
	items, err := CollectPackageResourceItems(root, pkg, true)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range items {
		got = append(got, string(item.ResourceType)+":"+item.Health+":"+filepath.Base(item.Path))
	}
	if want := []string{string(tui.ResourceExtensions) + ":missing:missing.js"}; !slices.Equal(got, want) {
		t.Fatalf("items %q, want %q", got, want)
	}
}

// A settings array applies its patterns to the files its plain entries collect (resolveLocalEntries, package-manager.ts:2398-2411), so a "!" pattern that cannot compile throws for an entry outside the auto-discovery directory too. Probed with Pi 1.1.0 (r26): {"prompts":["../other","![[:alpha:]]-x"]} with other/b.md and no auto-discovered prompt exits 1 with the SyntaxError.
func TestResolveRejectsAnExcludeOverAnEntry(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	cwd, agentDir := t.TempDir(), t.TempDir()
	other := filepath.Join(t.TempDir(), "other")
	writeResolveFile(t, filepath.Join(other, "b.md"), "x\n")
	settings, err := json.Marshal(map[string][]string{"prompts": {other, "![[:alpha:]]-x"}})
	if err != nil {
		t.Fatal(err)
	}
	writeResolveFile(t, filepath.Join(agentDir, "settings.json"), string(settings))
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	_, err = NewPackageManager(PackageManagerOptions{CWD: cwd, SettingsManager: sm}).Resolve(nil)
	if want := `Invalid regular expression: /^(?!\.)[\p{L}\p{Nl}]\-x$/u: Invalid escape`; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("Resolve = %v, want %q", err, want)
	}
}

// A missing member is no file Pi's filter sees, so a filter that only a missing member would reach throws nothing: the inventory lists that member as disabled and goes on to the later kinds.
func TestPackageInventoryMissingMemberOfAThrowingFilter(t *testing.T) {
	root := t.TempDir()
	writeResolveFile(t, filepath.Join(root, "package.json"), `{"name":"p","pi":{"prompts":["prompts/missing.md"],"themes":["themes/missing.json"]}}`)
	pkg := ConfiguredPackage{Source: codingagent.PackageSource{Source: root, Prompts: []string{"[[:alpha:]]-*"}}, Scope: "user"}
	items, err := CollectPackageResourceItems(root, pkg, true)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range items {
		got = append(got, fmt.Sprintf("%s:%s:%t", item.ResourceType, item.Health, item.Enabled))
	}
	if want := []string{"prompts:missing:false", "themes:missing:true"}; !slices.Equal(got, want) {
		t.Fatalf("items %q, want %q", got, want)
	}
}

// An npm Package's manifest overrides go through applyPatterns when its kind is collected (collectManifestFiles, package-manager.ts:2258-2278), outside any catch, so one that cannot compile rejects resolve() once a file reaches it.
func TestResolveRejectsAManifestOverrideThrow(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	cwd, agentDir, sm, pm := resolveFixture(t)
	root := filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, false), "node_modules", "overrideprobe")
	writeResolveFile(t, filepath.Join(root, "package.json"), `{"name":"overrideprobe","version":"1.0.0","pi":{"prompts":["prompts/*.md","![[:alpha:]]-x"]}}`)
	writeResolveFile(t, filepath.Join(root, "prompts", "a.md"), "x\n")
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:overrideprobe"}}); err != nil {
		t.Fatal(err)
	}
	_, err := pm.Resolve(nil)
	if want := `[\p{L}\p{Nl}]\-x$/u: Invalid escape`; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("Resolve = %v, want the override's SyntaxError", err)
	}
}

// An autoload-disabled Project delta applies its patterns with applyAutoloadDisabledPatterns (applyPackageDeltaFilter, package-manager.ts:2316-2333, 793-810), outside any catch for an npm Package, so a pattern that cannot compile rejects resolve() once a file reaches it. Probed with Pi 1.1.0 (r27): the delta exits 1 with the SyntaxError.
func TestResolveRejectsADeltaPatternThrow(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	cwd, agentDir, sm, pm := resolveFixture(t)
	root := filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, false), "node_modules", "deltaprobe")
	writeResolveFile(t, filepath.Join(root, "package.json"), `{"name":"deltaprobe","version":"1.0.0","pi":{"prompts":["prompts/*.md"]}}`)
	writeResolveFile(t, filepath.Join(root, "prompts", "a.md"), "x\n")
	disabled := false
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:deltaprobe"}}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:deltaprobe", Autoload: &disabled, Prompts: []string{"[[:alpha:]]-*"}}}); err != nil {
		t.Fatal(err)
	}
	_, err := pm.Resolve(nil)
	if want := `[\p{L}\p{Nl}]\-[^/]*?$/u: Invalid escape`; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("Resolve = %v, want the delta pattern's SyntaxError", err)
	}
}
