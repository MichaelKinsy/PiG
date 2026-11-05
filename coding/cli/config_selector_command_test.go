package cli

// pi: packages/coding-agent/src/modes/interactive/components/config-selector.ts

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestDriveConfigSelectorParksCursorAfterClose(t *testing.T) {
	for _, key := range []string{"\x1b", "\x03"} {
		t.Run(key, func(t *testing.T) {
			var output bytes.Buffer
			selector := tui.NewConfigSelector(nil, 0)
			ui := tui.NewWithOutput(&output, 80, 24)
			ui.Add(selector)
			if err := driveConfigSelector(ui, selector, strings.NewReader(key)); err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(output.String(), "\r\n\x1b[?25h") {
				t.Fatalf("config returned without parking the shell cursor below the selector: %q", output.String())
			}
		})
	}
}

func TestUpdateResourcePatterns_ReplacesExistingRule(t *testing.T) {
	got := updateResourcePatterns([]string{"+prompts/a.md", "-themes/dark.json"}, "prompts/a.md", false)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0] != "-themes/dark.json" && got[1] != "-themes/dark.json" {
		t.Fatalf("expected unrelated rule preserved: %v", got)
	}
	found := false
	for _, v := range got {
		if v == "-prompts/a.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing replacement rule: %v", got)
	}
}

func TestCollectConfigResourceItems_IncludesAutoAndExplicitResolvedEntries(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	agentDir := filepath.Join(root, "agent")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// The home directory is HOME on Unix and USERPROFILE on Windows, as for
	// upstream's os.homedir().
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	mustWriteFile := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	projectAuto := filepath.Join(cwd, ".pig", "prompts", "project-auto.md")
	projectLocal := filepath.Join(cwd, ".pig", "prompts", "project-local.md")
	userAuto := filepath.Join(agentDir, "prompts", "user-auto.md")
	userLocal := filepath.Join(agentDir, "prompts", "user-local.md")
	projectAgentSkill := filepath.Join(cwd, ".agents", "skills", "project-skill", "SKILL.md")
	userAgentSkill := filepath.Join(home, ".agents", "skills", "user-skill", "SKILL.md")
	mustWriteFile(projectAuto, "# auto\n")
	mustWriteFile(projectLocal, "# local\n")
	mustWriteFile(userAuto, "# auto\n")
	mustWriteFile(userLocal, "# local\n")
	mustWriteFile(projectAgentSkill, "# project skill\n")
	mustWriteFile(userAgentSkill, "# user skill\n")

	mustWriteFile(filepath.Join(agentDir, "settings.json"), `{
  "prompts": ["prompts/user-local.md", "-prompts/user-local.md", "-prompts/user-auto.md"]
}`)
	mustWriteFile(filepath.Join(cwd, ".pig", "settings.json"), `{
  "prompts": ["prompts/project-local.md", "-prompts/project-local.md"]
}`)

	sm := codingagent.NewSettingsManager(cwd, agentDir)
	items, err := collectConfigResourceItems(cwd, agentDir, sm)
	if err != nil {
		t.Fatal(err)
	}

	find := func(path string, resourceType tui.ResourceType) *tui.ResourceItem {
		t.Helper()
		for i := range items {
			if items[i].Path == path && items[i].ResourceType == resourceType {
				return &items[i]
			}
		}
		return nil
	}
	count := func(path string, resourceType tui.ResourceType) int {
		t.Helper()
		n := 0
		for i := range items {
			if items[i].Path == path && items[i].ResourceType == resourceType {
				n++
			}
		}
		return n
	}

	if item := find(projectAuto, tui.ResourcePrompts); item == nil || item.Source != "auto" || item.Scope != "project" || !item.Enabled {
		t.Fatalf("project auto prompt = %+v", item)
	}
	if item := find(projectLocal, tui.ResourcePrompts); item == nil || item.Source != "local" || item.Scope != "project" || item.Enabled {
		t.Fatalf("project local prompt = %+v, want disabled local entry", item)
	}
	if item := find(userAuto, tui.ResourcePrompts); item == nil || item.Source != "auto" || item.Scope != "user" || item.Enabled {
		t.Fatalf("user auto prompt = %+v, want disabled auto entry", item)
	}
	if item := find(userLocal, tui.ResourcePrompts); item == nil || item.Source != "local" || item.Scope != "user" || item.Enabled {
		t.Fatalf("user local prompt = %+v, want disabled local entry", item)
	}
	if got := count(userLocal, tui.ResourcePrompts); got != 1 {
		t.Fatalf("user local prompt count = %d, want 1", got)
	}
	if item := find(filepath.Dir(projectAgentSkill), tui.ResourceSkills); item == nil || item.Source != "auto" || item.Scope != "project" || !item.Enabled {
		t.Fatalf("project .agents skill = %+v", item)
	} else if want := filepath.Join(cwd, ".agents"); item.BaseDir != want {
		t.Fatalf("project .agents skill BaseDir = %q, want %q", item.BaseDir, want)
	}
	if item := find(filepath.Dir(userAgentSkill), tui.ResourceSkills); item == nil || item.Source != "auto" || item.Scope != "user" || !item.Enabled {
		t.Fatalf("user .agents skill = %+v", item)
	} else if want := filepath.Join(home, ".agents"); item.BaseDir != want {
		t.Fatalf("user .agents skill BaseDir = %q, want %q", item.BaseDir, want)
	}
}

func TestConfigSelectorDisablesEnabledMissingPackageMember(t *testing.T) {
	cwd, agentDir, packageRoot := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"pkg","pi":{"extensions":["extensions/missing-extension"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
		t.Fatal(err)
	}

	// Upstream skips a declared member that matches nothing, so startup
	// proceeds; pig config still lists it so it can be disabled.
	if err := startupPackageValidationError(cwd, settings); err != nil {
		t.Fatalf("missing member blocked startup: %v", err)
	}

	selector, err := newConfigSelector(cwd, agentDir, settings)
	if err != nil {
		t.Fatalf("pig config refused recovery inventory: %v", err)
	}
	if rendered := strings.Join(selector.Render(100), "\n"); !strings.Contains(rendered, "missing-extension") || !strings.Contains(rendered, "missing") {
		t.Fatalf("selector did not show enabled missing extension:\n%s", rendered)
	}

	selector.HandleInput(" ")
	settings.Reload()
	if err := startupPackageValidationError(cwd, settings); err != nil {
		t.Fatalf("disabled missing member blocked startup: %v", err)
	}
	filters := settings.GetGlobalSettings().Packages[0].Extensions
	if len(filters) != 1 || filters[0] != "-extensions/missing-extension" {
		t.Fatalf("extension filters = %v, want [-extensions/missing-extension]", filters)
	}
}

func TestProjectConfigPackageOverrideIsDeltaOverInheritedGlobalPackage(t *testing.T) {
	cwd, agentDir, packageRoot := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	for path, content := range map[string]string{
		"package.json":                  `{"name":"pkg","pi":{"extensions":["extensions/*"],"prompts":["prompts/*.md"]}}`,
		"extensions/alpha/go.mod":       "module example.com/alpha\n\ngo 1.26\n",
		"extensions/alpha/extension.go": "package alpha\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n",
		"extensions/beta/go.mod":        "module example.com/beta\n\ngo 1.26\n",
		"extensions/beta/extension.go":  "package beta\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n",
		"prompts/review.md":             "review",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(packageRoot, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packageRoot, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
		t.Fatal(err)
	}
	item := &tui.ResourceItem{
		Path: filepath.Join(packageRoot, "extensions", "alpha"), ResourceType: tui.ResourceExtensions,
		Scope: "user", Origin: "package", Source: packageRoot, BaseDir: packageRoot, Inherited: true, InheritedEnabled: true,
	}

	if err := applyProjectConfigOverride(cwd, settings, item, "unload"); err != nil {
		t.Fatal(err)
	}
	settings.Reload()
	// Pi persists path.relative(baseDir, item.path), which uses the platform
	// separator (config-selector.ts getPackageResourcePattern).
	project := settings.GetProjectSettings().Packages
	if len(project) != 1 || len(project[0].Extensions) != 1 || project[0].Extensions[0] != "-"+filepath.Join("extensions", "alpha") {
		t.Fatalf("project delta = %#v", project)
	}
	assertEffective := func(targetEnabled bool) {
		t.Helper()
		packages := packagemanager.ConfiguredPackagesForResolution(cwd, settings.AgentDir(), settings)
		if len(packages) != 1 {
			t.Fatalf("effective packages = %#v, want one inherited Package", packages)
		}
		if packages[0].Scope != "user" {
			t.Fatalf("effective Package scope = %q, want user", packages[0].Scope)
		}
		items, err := packagemanager.CollectResolvedPackageResourceItems(cwd, settings.AgentDir(), settings, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		for path, wantEnabled := range map[string]bool{"extensions/alpha": targetEnabled, "extensions/beta": true, "prompts/review.md": true} {
			index := slices.IndexFunc(items, func(item tui.ResourceItem) bool { return item.Path == filepath.Join(packageRoot, path) })
			if index < 0 || items[index].Enabled != wantEnabled {
				t.Fatalf("resource %s enabled != %t: %#v", path, wantEnabled, items)
			}
		}
		listed := packageManagerFor(cwd, settings, nil).ListConfiguredPackages()
		wantListed := 1 + len(settings.GetProjectSettings().Packages)
		if len(listed) != wantListed || listed[0].Scope != "user" || wantListed == 2 && listed[1].Scope != "project" {
			t.Fatalf("configured Package list = %#v, want %d Pi-compatible settings entries", listed, wantListed)
		}
	}
	assertEffective(false)

	if err := applyProjectConfigOverride(cwd, settings, item, "load"); err != nil {
		t.Fatal(err)
	}
	settings.Reload()
	assertEffective(true)
	if err := applyProjectConfigOverride(cwd, settings, item, "inherit"); err != nil {
		t.Fatal(err)
	}
	settings.Reload()
	if got := settings.GetProjectSettings().Packages; len(got) != 0 {
		t.Fatalf("inherit left a project Package delta: %#v", got)
	}
	assertEffective(true)
}

func TestProjectDeltaBindsNormalizedRelativeGlobalPackage(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "work")
	agentDir := filepath.Join(root, "agent")
	packageRoot := filepath.Join(root, "packages", "shared")
	for _, dir := range []string{cwd, agentDir, packageRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"shared","pi":{"prompts":["prompts/review.md"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(packageRoot, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "prompts", "review.md"), []byte("review"), 0o644); err != nil {
		t.Fatal(err)
	}
	globalSource, err := filepath.Rel(agentDir, packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: filepath.ToSlash(globalSource)}}); err != nil {
		t.Fatal(err)
	}
	item := &tui.ResourceItem{
		Path: filepath.Join(packageRoot, "prompts", "review.md"), ResourceType: tui.ResourcePrompts,
		Scope: "user", Origin: "package", Source: filepath.ToSlash(globalSource), BaseDir: packageRoot,
		Inherited: true, InheritedEnabled: true,
	}
	if err := applyProjectConfigOverride(cwd, settings, item, "unload"); err != nil {
		t.Fatal(err)
	}
	settings.Reload()
	projectPackages := settings.GetProjectSettings().Packages
	if len(projectPackages) != 1 {
		t.Fatalf("project packages = %#v", projectPackages)
	}
	projectSource, err := filepath.Rel(filepath.Join(cwd, ".pig"), packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	// Pi persists path.relative(projectBase, sourcePath) with the platform
	// separator (config-selector.ts createPackageOverrideSource).
	if projectPackages[0].Source != projectSource {
		t.Fatalf("project delta source = %q, want %q", projectPackages[0].Source, projectSource)
	}
	packages := packagemanager.ConfiguredPackagesForResolution(cwd, settings.AgentDir(), settings)
	if len(packages) != 1 || packages[0].Scope != "user" || packages[0].InstalledPath != packageRoot {
		t.Fatalf("normalized source produced duplicate installations: %#v", packages)
	}
	resources := packagemanager.ResolvedConfiguredPackageSources(cwd, settings.AgentDir(), settings, true)
	if len(resources) != 2 || resources[0].Scope != "project" || resources[1].Scope != "user" || resources[0].InstalledPath != packageRoot || resources[1].InstalledPath != packageRoot {
		t.Fatalf("resource delta did not retain the inherited install path: %#v", resources)
	}
}

func TestProjectConfigTopLevelOverridesPersistExactScopeShape(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	projectPath := filepath.Join(cwd, ".pig", "prompts", "local.md")
	projectItem := &tui.ResourceItem{
		Path: projectPath, ResourceType: tui.ResourcePrompts, Scope: "project",
		Origin: "top-level", Source: "local", BaseDir: filepath.Join(cwd, ".pig"),
	}
	for _, state := range []string{"unload", "unload", "load"} {
		if err := applyProjectConfigOverride(cwd, settings, projectItem, state); err != nil {
			t.Fatal(err)
		}
	}
	settings.Reload()
	// Pi persists path.relative(baseDir, item.path) for a project resource and
	// item.path for an inherited global one, both with the platform separator
	// (config-selector.ts setProjectTopLevelOverride).
	if got, want := settings.GetProjectSettings().Prompts, []string{"+" + filepath.Join("prompts", "local.md")}; !slices.Equal(got, want) {
		t.Fatalf("project-local paths = %v, want %v", got, want)
	}
	if err := applyProjectConfigOverride(cwd, settings, projectItem, "inherit"); err != nil {
		t.Fatal(err)
	}
	settings.Reload()
	if got := settings.GetProjectSettings().Prompts; len(got) != 0 {
		t.Fatalf("project-local inherit left entries: %v", got)
	}

	globalPath := filepath.Join(agentDir, "prompts", "global.md")
	globalItem := &tui.ResourceItem{
		Path: globalPath, ResourceType: tui.ResourcePrompts, Scope: "user",
		Origin: "top-level", Source: "local", BaseDir: agentDir, Inherited: true, InheritedEnabled: true,
	}
	if err := applyProjectConfigOverride(cwd, settings, globalItem, "unload"); err != nil {
		t.Fatal(err)
	}
	settings.Reload()
	if got, want := settings.GetProjectSettings().Prompts, []string{globalPath, "-" + globalPath}; !slices.Equal(got, want) {
		t.Fatalf("inherited-global paths = %v, want %v", got, want)
	}
	if err := applyProjectConfigOverride(cwd, settings, globalItem, "inherit"); err != nil {
		t.Fatal(err)
	}
	settings.Reload()
	if got := settings.GetProjectSettings().Prompts; len(got) != 0 {
		t.Fatalf("inherited-global inherit left entries: %v", got)
	}
}

func TestProjectConfigCyclesInheritedMissingMemberToUnload(t *testing.T) {
	cwd, agentDir, packageRoot := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"pkg","pi":{"extensions":["extensions/missing"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
		t.Fatal(err)
	}
	global := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	selector, err := newScopedConfigSelector(cwd, agentDir, global, settings, true, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	selector.HandleInput(" ")
	settings.Reload()
	if got := settings.GetGlobalSettings().Packages[0].Extensions; got != nil {
		t.Fatalf("project override changed global Package filters: %v", got)
	}
	projectPackages := settings.GetProjectSettings().Packages
	if len(projectPackages) != 1 || len(projectPackages[0].Extensions) != 1 || projectPackages[0].Extensions[0] != "-extensions/missing" {
		t.Fatalf("project Package overrides = %#v", projectPackages)
	}
	if err := startupPackageValidationError(cwd, settings); err != nil {
		t.Fatalf("project unload did not recover startup: %v", err)
	}

	selector.HandleInput(" ") // unload -> load
	selector.HandleInput(" ") // load -> inherit
	settings.Reload()
	if got := settings.GetProjectSettings().Packages; len(got) != 0 {
		t.Fatalf("inherit left a project Package override: %#v", got)
	}
}

// Pi 1be312b4 (#11588) config-selector.ts getResourceItemKey: a global and a project resource whose paths differ in casing only are one item on Windows, and
// two on a case-sensitive platform.
func TestConfigItemKeyFoldsPathCasingOnWindows(t *testing.T) {
	t.Cleanup(resolvepath.OverridePlatform("linux")) // the first half pins POSIX rules, whatever the host
	global := tui.ResourceItem{ResourceType: tui.ResourceSkills, Path: `C:\Users\Me\skills\demo\SKILL.md`}
	project := tui.ResourceItem{ResourceType: tui.ResourceSkills, Path: `c:\users\me\skills\demo\skill.md`}
	restore := resolvepath.OverridePlatform("linux")
	if configItemKey(global) == configItemKey(project) {
		restore()
		t.Fatal("case-sensitive platform: two casings of one path share a key")
	}
	restore()
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	if configItemKey(global) != configItemKey(project) {
		t.Fatalf("Windows: keys %q and %q differ", configItemKey(global), configItemKey(project))
	}
	if configItemKey(global) == configItemKey(tui.ResourceItem{ResourceType: tui.ResourcePrompts, Path: global.Path}) {
		t.Fatal("two resource types share a key")
	}
}
