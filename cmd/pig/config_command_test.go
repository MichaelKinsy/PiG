package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
)

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

func TestRunConfigCommand_Help(t *testing.T) {
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runConfigCommand([]string{"--help"})
	})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Open the resource configuration TUI.") {
		t.Fatalf("stdout missing help text:\n%s", stdout)
	}
}

func TestRunConfigCommand_UnknownOption(t *testing.T) {
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runConfigCommand([]string{"--nope"})
	})
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "pig config: unknown option --nope") {
		t.Fatalf("stderr missing unknown-option line:\n%s", stderr)
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
		packages := configuredPackagesForResolution(cwd, settings)
		if len(packages) != 1 {
			t.Fatalf("effective packages = %#v, want one inherited Package", packages)
		}
		if packages[0].Scope != "user" {
			t.Fatalf("effective Package scope = %q, want user", packages[0].Scope)
		}
		filters, err := effectiveConfiguredPackageFilters(packages[0])
		if err != nil {
			t.Fatal(err)
		}
		if got := packagecontent.ResourceEnabled("extensions/alpha", filters[packagecontent.Extensions]); got != targetEnabled {
			t.Fatalf("alpha enabled = %t, want %t; filters=%v", got, targetEnabled, filters[packagecontent.Extensions])
		}
		if !packagecontent.ResourceEnabled("extensions/beta", filters[packagecontent.Extensions]) ||
			!packagecontent.ResourceEnabled("prompts/review.md", filters[packagecontent.Prompts]) {
			t.Fatalf("unrelated inherited resources were disabled or promoted: package=%#v filters=%#v", packages[0], filters)
		}
		listed := listConfiguredPackages(cwd, settings)
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
	packages := configuredPackagesForResolution(cwd, settings)
	if len(packages) != 1 || packages[0].ProjectDelta == nil || packages[0].Scope != "user" {
		t.Fatalf("normalized source produced duplicate Packages: %#v", packages)
	}
}

func TestProjectPackageDeltaDrivesStartupStatusAndResourceCollectors(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir, packageRoot := filepath.Join(root, "work"), filepath.Join(root, "agent"), filepath.Join(root, "pkg")
	for _, dir := range []string{cwd, agentDir, packageRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	files := map[string]string{
		"package.json":     `{"name":"pkg","pi":{"extensions":["extensions/*"],"skills":["skills/*"],"prompts":["prompts/*.md"],"themes":["themes/*.json"]}}`,
		"prompts/alpha.md": "alpha", "prompts/beta.md": "beta",
		"themes/alpha.json": `{}`, "themes/beta.json": `{}`,
		"skills/alpha/SKILL.md": "---\nname: alpha\ndescription: alpha\n---\n", "skills/beta/SKILL.md": "---\nname: beta\ndescription: beta\n---\n",
	}
	for _, name := range []string{"alpha", "beta"} {
		files["extensions/"+name+"/go.mod"] = "module example.com/" + name + "\n\ngo 1.26\n"
		files["extensions/"+name+"/extension.go"] = "package " + name + "\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n"
	}
	for name, content := range files {
		path := filepath.Join(packageRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
		t.Fatal(err)
	}
	for kind, target := range map[tui.ResourceType]string{
		tui.ResourceExtensions: filepath.Join(packageRoot, "extensions", "alpha"),
		tui.ResourceSkills:     filepath.Join(packageRoot, "skills", "alpha"),
		tui.ResourcePrompts:    filepath.Join(packageRoot, "prompts", "alpha.md"),
		tui.ResourceThemes:     filepath.Join(packageRoot, "themes", "alpha.json"),
	} {
		item := &tui.ResourceItem{Path: target, ResourceType: kind, Scope: "user", Origin: "package", Source: packageRoot, BaseDir: packageRoot, Inherited: true, InheritedEnabled: true}
		if err := applyProjectConfigOverride(cwd, settings, item, "unload"); err != nil {
			t.Fatal(err)
		}
	}
	settings.Reload()
	if err := startupPackageValidationError(cwd, settings); err != nil {
		t.Fatalf("effective startup validation failed: %v", err)
	}
	assertOneBeta := func(name string, paths []string) {
		t.Helper()
		if len(paths) != 1 || !strings.Contains(filepath.ToSlash(paths[0]), "/beta") {
			t.Fatalf("%s paths = %v, want one beta", name, paths)
		}
	}
	assertOneBeta("prompts", collectPackagePromptPaths(cwd, settings))
	assertOneBeta("themes", collectPackageThemePaths(cwd, settings))
	assertOneBeta("skills", collectPackageSkillPaths(cwd, settings, nil))
	exts := collectPackageExtensionConfigs(cwd, settings, nil)
	if len(exts) != 1 || exts[0].Name != "beta" {
		t.Fatalf("extension configs = %#v, want beta only", exts)
	}
	items, err := collectConfigResourceItems(cwd, agentDir, settings)
	if err != nil {
		t.Fatal(err)
	}
	betaEnabled := make(map[tui.ResourceType]int)
	for _, item := range items {
		if item.Origin != "package" || !item.Enabled {
			continue
		}
		name := filepath.Base(item.Path)
		if strings.HasPrefix(name, "alpha") {
			t.Errorf("%s alpha remained enabled: %#v", item.ResourceType, item)
		}
		if strings.HasPrefix(name, "beta") {
			betaEnabled[item.ResourceType]++
		}
	}
	for _, kind := range []tui.ResourceType{tui.ResourceExtensions, tui.ResourceSkills, tui.ResourcePrompts, tui.ResourceThemes} {
		if betaEnabled[kind] != 1 {
			t.Errorf("%s enabled beta count = %d, want 1", kind, betaEnabled[kind])
		}
	}
	status := collectStatus()
	if status.Packages.Total != 1 || status.Packages.User != 1 || status.Packages.Project != 0 || len(status.Errors) != 0 {
		t.Fatalf("status Package delta projection = %#v", status)
	}
	for _, item := range status.Resources.Items {
		if item.Origin != "package" {
			continue
		}
		if strings.HasPrefix(item.Name, "alpha") && item.Enabled {
			t.Errorf("status reports disabled delta resource enabled: %#v", item)
		}
	}
}

func TestProjectPackageDeltaPreservesStrictManifestValidation(t *testing.T) {
	for name, prepare := range map[string]func(*testing.T, string) string{
		"lexical traversal": func(t *testing.T, root string) string { return `{"name":"pkg","pi":{"prompts":["../outside.md"]}}` },
		"absolute path":     func(t *testing.T, root string) string { return `{"name":"pkg","pi":{"prompts":["/tmp/outside.md"]}}` },
		"symlink escape": func(t *testing.T, root string) string {
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "escape.md"), []byte("escape"), 0o644); err != nil {
				t.Fatal(err)
			}
			testenv.Symlink(t, outside, filepath.Join(root, "outside"))
			return `{"name":"pkg","pi":{"prompts":["outside/escape.md"]}}`
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cwd, agentDir, packageRoot := filepath.Join(root, "work"), filepath.Join(root, "agent"), filepath.Join(root, "pkg")
			for _, dir := range []string{cwd, agentDir, packageRoot} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(prepare(t, packageRoot)), 0o644); err != nil {
				t.Fatal(err)
			}
			settings := codingagent.NewSettingsManager(cwd, agentDir)
			if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
				t.Fatal(err)
			}
			projectSource, err := filepath.Rel(filepath.Join(cwd, ".pig"), packageRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := settings.SetProjectPackages([]codingagent.PackageSource{{Source: filepath.ToSlash(projectSource), Prompts: []string{"-prompts/unused.md"}}}); err != nil {
				t.Fatal(err)
			}
			settings.Reload()
			if err := startupPackageValidationError(cwd, settings); err == nil {
				t.Fatal("project delta weakened strict Package validation")
			}
		})
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
	selector, err := newScopedConfigSelector(cwd, agentDir, global, settings, true, true)
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

func TestRunConfigCommandLocalRequiresTrustedProject(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Chdir(cwd)
	_, stderr, code := captureStdoutStderr(t, func() int {
		return runConfigCommand([]string{"--local", "--no-approve"})
	})
	if code != 1 || !strings.Contains(stderr, "project is not trusted") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}
