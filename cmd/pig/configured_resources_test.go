package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestCollectStartupThemePathsExcludesProjectThemes(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	globalTheme := filepath.Join(agentDir, "themes", "global.json")
	projectTheme := filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "themes", "project.json")
	for _, path := range []string{globalTheme, projectTheme} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"name":"fixture","colors":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := collectStartupThemePaths(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir))
	if !slices.Contains(got, globalTheme) {
		t.Fatalf("startup themes missing global theme: %v", got)
	}
	if slices.Contains(got, projectTheme) {
		t.Fatalf("startup themes contain project theme before trust: %v", got)
	}
}

func TestCollectStartupThemePathsUsesOnlyGlobalPackageFiltersBeforeTrust(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir, packageRoot := filepath.Join(root, "work"), filepath.Join(root, "agent"), filepath.Join(root, "pkg")
	for _, dir := range []string{cwd, agentDir, filepath.Join(packageRoot, "themes")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"pkg","pi":{"themes":["themes/*.json"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	alpha := filepath.Join(packageRoot, "themes", "alpha.json")
	beta := filepath.Join(packageRoot, "themes", "beta.json")
	for _, path := range []string{alpha, beta} {
		if err := os.WriteFile(path, []byte(`{"name":"fixture","colors":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	trusted := codingagent.NewSettingsManager(cwd, agentDir)
	globalSource, err := filepath.Rel(agentDir, packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := trusted.SetPackages([]codingagent.PackageSource{{Source: filepath.ToSlash(globalSource), Themes: []string{"-themes/alpha.json"}}}); err != nil {
		t.Fatal(err)
	}
	projectSource, err := filepath.Rel(filepath.Join(cwd, ".pig"), packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := trusted.SetProjectPackages([]codingagent.PackageSource{{Source: filepath.ToSlash(projectSource), Themes: []string{"+themes/alpha.json"}}}); err != nil {
		t.Fatal(err)
	}

	untrusted := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	got := collectStartupThemePaths(cwd, agentDir, untrusted)
	if slices.Contains(got, alpha) {
		t.Fatalf("pre-trust startup applied project delta or ignored global filter: %v", got)
	}
	if count := slices.Index(got, beta); count < 0 {
		t.Fatalf("pre-trust startup omitted enabled global Package theme: %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("pre-trust Package themes = %v, want beta exactly once", got)
	}
}

func TestValidateConfiguredPackageContentsReportsInvalidPackage(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	packageRoot := filepath.Join(t.TempDir(), "pkg")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"pkg","pig":{"hooks":["hooks/missing.json"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
		t.Fatal(err)
	}
	if err := startupPackageValidationError(cwd, sm); err == nil || !strings.Contains(err.Error(), "hooks/missing.json") || !strings.Contains(err.Error(), "user Package") {
		t.Fatalf("error = %v", err)
	}
}

func TestMergeExtConfigsPreservesDistinctDuplicateIdentity(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	configs := mergeExtConfigs([]subprocess.ExtConfig{
		{Name: "duplicate", Path: first, Enabled: true},
		{Name: "duplicate", Path: second, Enabled: true},
		{Name: "duplicate", Path: first, Enabled: true},
	})
	if len(configs) != 2 || configs[0].Path != first || configs[1].Path != second {
		t.Fatalf("normalized configs = %#v", configs)
	}
	// Both copies are attempted, as upstream loads each extension path.
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	_, errs := host.LoadAll(t.Context(), configs)
	if joined := fmt.Sprint(errs); len(errs) != 2 || !strings.Contains(joined, "spawn "+first) || !strings.Contains(joined, "spawn "+second) {
		t.Fatalf("duplicate copies = %v", errs)
	}
}

func TestMergeExtConfigsPreservesFirstSeenOrder(t *testing.T) {
	configs := mergeExtConfigs([]subprocess.ExtConfig{
		{Name: "c", Path: "/c", Enabled: true},
		{Name: "b", Path: "/b", Enabled: true},
		{Name: "a", Path: "/a", Enabled: true},
		{Name: "c", Path: "/c", Enabled: false},
	})
	got := make([]string, len(configs))
	for i := range configs {
		got[i] = configs[i].Name
	}
	if !slices.Equal(got, []string{"c", "b", "a"}) {
		t.Fatalf("merged order = %v, want first-seen [c b a]", got)
	}
	if configs[0].Enabled {
		t.Fatal("duplicate value did not retain the later configuration")
	}
}

func TestCollectExtensionConfigs_UsesSettingsAndCLI(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	cwd := t.TempDir()
	agentDir := t.TempDir()
	globalExt := filepath.Join(agentDir, "ext-global")
	projectExt := filepath.Join(cwd, ".pig", "ext-project")
	cliExt := filepath.Join(cwd, "ext-cli")
	for _, dir := range []string{globalExt, projectExt, cliExt} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\ngo 1.26\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.UpdateGlobal(func(s *codingagent.Settings) { s.Extensions = []string{"ext-global"} }); err != nil {
		t.Fatal(err)
	}
	if err := sm.UpdateProject(func(s *codingagent.Settings) { s.Extensions = []string{"ext-project"} }); err != nil {
		t.Fatal(err)
	}
	cfgs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{Extensions: []string{"./ext-cli"}}, nil)
	if len(cfgs) != 3 {
		t.Fatalf("len = %d, want 3", len(cfgs))
	}
	for _, cfg := range cfgs {
		if cfg.Source == "" {
			t.Fatalf("config %#v missing Source", cfg)
		}
	}
}

func TestCollectExtensionConfigsCLIExactStandaloneWithNoExtensions(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	cwd := t.TempDir()
	agentDir := t.TempDir()
	binary := filepath.Join(cwd, "cmd-ext")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{NoExtensions: true, Extensions: []string{"./cmd-ext"}}, nil)
	if len(configs) != 1 || configs[0].Name != "cmd-ext" || configs[0].Path != binary || configs[0].Source != "" || configs[0].EntrypointKind != "standalone" {
		t.Fatalf("standalone configs = %#v", configs)
	}
}

func TestCollectExtensionConfigsAutoDiscoveryUsesSelectedDirectoryIdentity(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	cwd := t.TempDir()
	agentDir := t.TempDir()
	extDir := filepath.Join(agentDir, "extensions", "auto-ext")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "go.mod"), []byte("module example.com/auto-ext\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "package autoext\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return sdk.New(\"auto-ext\") }\n"
	if err := os.WriteFile(filepath.Join(extDir, "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), CLIFlags{}, nil)
	if len(configs) != 1 || configs[0].Name != "auto-ext" || configs[0].Source != extDir {
		t.Fatalf("auto configs = %#v", configs)
	}
}

func TestCollectPackageBackedResources_NoSymlinkMaterializationNeeded(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	cwd := t.TempDir()
	agentDir := t.TempDir()
	pkgRoot := filepath.Join(cwd, "pkg")
	mustMkdirAll := func(path string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdirAll(filepath.Join(pkgRoot, "prompts"))
	mustMkdirAll(filepath.Join(pkgRoot, "themes"))
	mustMkdirAll(filepath.Join(pkgRoot, "skills", "skill-a"))
	mustMkdirAll(filepath.Join(pkgRoot, "extensions", "ext-a"))
	mustWrite(filepath.Join(pkgRoot, "prompts", "pkg.md"), "# prompt")
	mustWrite(filepath.Join(pkgRoot, "themes", "pkg.json"), `{}`)
	mustWrite(filepath.Join(pkgRoot, "skills", "skill-a", "SKILL.md"), `# skill`)
	mustWrite(filepath.Join(pkgRoot, "extensions", "ext-a", "go.mod"), "module example.com/ext\ngo 1.26\n")
	mustWrite(filepath.Join(pkgRoot, "extensions", "ext-a", "extension.go"), "package exta\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return sdk.New(\"ext-a\") }\n")

	sm := codingagent.NewSettingsManager(cwd, agentDir)
	storedSource, _ := filepath.Rel(filepath.Join(cwd, ".pig"), pkgRoot)
	if !strings.HasPrefix(storedSource, ".") {
		storedSource = "." + string(filepath.Separator) + storedSource
	}
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: storedSource}}); err != nil {
		t.Fatal(err)
	}

	prompts := collectPromptPaths(cwd, agentDir, sm, CLIFlags{}, true)
	if !slices.Contains(prompts, filepath.Join(pkgRoot, "prompts", "pkg.md")) {
		t.Fatalf("package prompt missing from collectPromptPaths: %v", prompts)
	}
	themes := collectThemePaths(cwd, agentDir, sm, CLIFlags{}, true)
	if !slices.Contains(themes, filepath.Join(pkgRoot, "themes", "pkg.json")) {
		t.Fatalf("package theme missing from collectThemePaths: %v", themes)
	}
	skills := collectSkillInputs(cwd, agentDir, sm, CLIFlags{}, nil)
	if !slices.Contains(skills, filepath.Join(pkgRoot, "skills", "skill-a")) {
		t.Fatalf("package skill missing from collectSkillInputs: %v", skills)
	}
	exts := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, nil)
	found := false
	for _, cfg := range exts {
		if cfg.Source == filepath.Join(pkgRoot, "extensions", "ext-a") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("package extension missing from collectExtensionConfigs: %#v", exts)
	}

	// No symlink materialization into PIG_HOME config tree should be required.
	if _, err := os.Stat(filepath.Join(codingagent.ConfigRoot(), "prompts", "pkg.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no symlink/materialized prompt under config root, stat err=%v", err)
	}
}

func TestCollectPackageBackedResources_FilterChangesApplyOnNextRead(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	pkgRoot := filepath.Join(cwd, "pkg")
	for _, dir := range []string{
		filepath.Join(pkgRoot, "prompts"),
		filepath.Join(pkgRoot, "skills", "skill-a"),
		filepath.Join(pkgRoot, "skills", "skill-b"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "prompts", "allowed.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "prompts", "blocked.md"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "skills", "skill-a", "SKILL.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "skills", "skill-b", "SKILL.md"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	storedSource, _ := filepath.Rel(filepath.Join(cwd, ".pig"), pkgRoot)
	if !strings.HasPrefix(storedSource, ".") {
		storedSource = "." + string(filepath.Separator) + storedSource
	}
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{
		Source:  storedSource,
		Prompts: []string{"prompts/allowed.md"},
		Skills:  []string{"skills/skill-a/SKILL.md"},
	}}); err != nil {
		t.Fatal(err)
	}
	prompts := collectPromptPaths(cwd, agentDir, sm, CLIFlags{}, true)
	if !slices.Contains(prompts, filepath.Join(pkgRoot, "prompts", "allowed.md")) || slices.Contains(prompts, filepath.Join(pkgRoot, "prompts", "blocked.md")) {
		t.Fatalf("prompt filtering failed: %v", prompts)
	}
	skills := collectSkillInputs(cwd, agentDir, sm, CLIFlags{}, nil)
	if !slices.Contains(skills, filepath.Join(pkgRoot, "skills", "skill-a")) || slices.Contains(skills, filepath.Join(pkgRoot, "skills", "skill-b")) {
		t.Fatalf("skill filtering failed: %v", skills)
	}

	// Change filters without reinstall; next collection should reflect the new settings.
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{
		Source:  storedSource,
		Prompts: []string{"prompts/blocked.md"},
		Skills:  []string{"skills/skill-b/SKILL.md"},
	}}); err != nil {
		t.Fatal(err)
	}
	prompts = collectPromptPaths(cwd, agentDir, sm, CLIFlags{}, true)
	if !slices.Contains(prompts, filepath.Join(pkgRoot, "prompts", "blocked.md")) || slices.Contains(prompts, filepath.Join(pkgRoot, "prompts", "allowed.md")) {
		t.Fatalf("prompt filter change not reflected without reinstall: %v", prompts)
	}
	skills = collectSkillInputs(cwd, agentDir, sm, CLIFlags{}, nil)
	if !slices.Contains(skills, filepath.Join(pkgRoot, "skills", "skill-b")) || slices.Contains(skills, filepath.Join(pkgRoot, "skills", "skill-a")) {
		t.Fatalf("skill filter change not reflected without reinstall: %v", skills)
	}
}

func TestCollectPromptPathsPreservesUpstreamPrecedence(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	writePrompt := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePrompt(filepath.Join(agentDir, "prompts", "same.md"), "USER")
	writePrompt(filepath.Join(cwd, ".pig", "prompts", "same.md"), "PROJECT")
	writePrompt(filepath.Join(agentDir, "prompts", "cli.md"), "USER")
	cliPath := filepath.Join(t.TempDir(), "cli.md")
	writePrompt(cliPath, "CLI")

	paths := collectPromptPaths(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), CLIFlags{PromptTemplates: []string{cliPath}}, true)
	result := codingagent.LoadPromptTemplates("", "", paths...)
	byName := make(map[string]string, len(result.Templates))
	for _, template := range result.Templates {
		byName[template.Name] = template.Content
	}
	if byName["same"] != "PROJECT" || byName["cli"] != "CLI" {
		t.Fatalf("prompt winners = %#v; paths = %v", byName, paths)
	}
}

func TestCollectPromptPaths_AutoDiscoveryRespectsOverridePatterns(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	promptsDir := filepath.Join(agentDir, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(promptsDir, "keep.md")
	drop := filepath.Join(promptsDir, "drop.md")
	for _, path := range []string{keep, drop} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.UpdateGlobal(func(s *codingagent.Settings) { s.Prompts = []string{"!drop.md", "+keep.md"} }); err != nil {
		t.Fatal(err)
	}
	got := collectPromptPaths(cwd, agentDir, sm, CLIFlags{}, true)
	if !slices.Contains(got, keep) {
		t.Fatalf("keep prompt missing: %v", got)
	}
	if slices.Contains(got, drop) {
		t.Fatalf("drop prompt should be excluded: %v", got)
	}
}

func TestCollectPromptPaths_ExplicitDirectoryRecurses(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	extra := filepath.Join(cwd, "extra-prompts")
	nested := filepath.Join(extra, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(nested, "deep.md")
	if err := os.WriteFile(want, []byte("# deep"), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.UpdateGlobal(func(s *codingagent.Settings) { s.Prompts = []string{extra} }); err != nil {
		t.Fatal(err)
	}
	got := collectPromptPaths(cwd, agentDir, sm, CLIFlags{}, true)
	if !slices.Contains(got, want) {
		t.Fatalf("recursive prompt file missing: %v", got)
	}
}

func TestCollectExtensionConfigs_AutoDiscoveryAndOverrides(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	extDir := filepath.Join(agentDir, "extensions")
	keepDir := filepath.Join(extDir, "keep")
	dropDir := filepath.Join(extDir, "drop")
	for _, dir := range []string{keepDir, dropDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte("export default {}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.UpdateGlobal(func(s *codingagent.Settings) { s.Extensions = []string{"!drop/index.ts", "+keep/index.ts"} }); err != nil {
		t.Fatal(err)
	}
	got := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, nil)
	var sources []string
	for _, cfg := range got {
		sources = append(sources, cfg.Source)
	}
	if !slices.Contains(sources, keepDir) {
		t.Fatalf("auto-discovered keep extension missing: %v", sources)
	}
	if slices.Contains(sources, dropDir) {
		t.Fatalf("drop extension should be excluded: %v", sources)
	}
}

func TestCollectSkillInputs_AgentsOverridesUseProjectBaseDir(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	agentsDir := filepath.Join(cwd, ".agents", "skills", "shadow")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "SKILL.md"), []byte("# shadow"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: ./.git/worktree"), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.UpdateProject(func(s *codingagent.Settings) { s.Skills = []string{"!skills/shadow"} }); err != nil {
		t.Fatal(err)
	}
	got := collectSkillInputs(cwd, agentDir, sm, CLIFlags{}, nil)
	if slices.Contains(got, filepath.Join(cwd, ".agents", "skills", "shadow")) {
		t.Fatalf("project .agents skill should be excluded: %v", got)
	}
}

func TestCollectPackageSkillPaths_ReadsCrossToolPluginManifest(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	pkgRoot := filepath.Join(cwd, "plugin-pkg")
	skillDir := filepath.Join(pkgRoot, "skills", "debugger")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Debugger"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgRoot, "plugin.json"), []byte(`{"name":"plugin-pkg","skills":"skills/"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: pkgRoot}}); err != nil {
		t.Fatal(err)
	}
	got := collectPackageSkillPaths(cwd, sm, nil)
	if !slices.Equal(got, []string{skillDir}) {
		t.Fatalf("collectPackageSkillPaths() = %v, want [%s]", got, skillDir)
	}
}

func TestCollectPackagePromptPaths_DedupesSharedPackageWithProjectPrecedence(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	pkgRoot := filepath.Join(cwd, "shared-pkg")
	if err := os.MkdirAll(filepath.Join(pkgRoot, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(pkgRoot, "prompts", "keep.md")
	drop := filepath.Join(pkgRoot, "prompts", "drop.md")
	for _, path := range []string{keep, drop} {
		if err := os.WriteFile(path, []byte("# prompt"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: pkgRoot, Prompts: []string{"prompts/drop.md"}}}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: pkgRoot, Prompts: []string{"prompts/keep.md"}}}); err != nil {
		t.Fatal(err)
	}
	got := collectPackagePromptPaths(cwd, sm)
	if !slices.Equal(got, []string{keep}) {
		t.Fatalf("collectPackagePromptPaths() = %v, want [%s]", got, keep)
	}
}

func TestPigletAmbientScopesFilterExtensionsAndSkills(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	writeExtension := func(path, module string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "go.mod"), []byte("module "+module+"\ngo 1.26\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill := func(path, name string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userExt := filepath.Join(agentDir, "extensions", "user-ext")
	workspaceExt := filepath.Join(cwd, ".pig", "extensions", "workspace-ext")
	cliExt := filepath.Join(cwd, "cli-ext")
	writeExtension(userExt, "example.test/user")
	writeExtension(workspaceExt, "example.test/workspace")
	writeExtension(cliExt, "example.test/cli")
	userSkill := filepath.Join(agentDir, "skills", "user-skill")
	workspaceSkill := filepath.Join(cwd, ".pig", "skills", "workspace-skill")
	cliSkill := filepath.Join(cwd, "cli-skill")
	writeSkill(userSkill, "user-skill")
	writeSkill(workspaceSkill, "workspace-skill")
	writeSkill(cliSkill, "cli-skill")

	sm := codingagent.NewSettingsManager(cwd, agentDir)
	flags := CLIFlags{Extensions: []string{cliExt}, Skills: []string{cliSkill}}
	cases := map[string]struct {
		scopes         []string
		wantExtensions []string
		wantSkills     []string
	}{
		"none":      {scopes: []string{}, wantExtensions: []string{cliExt}, wantSkills: []string{cliSkill}},
		"workspace": {scopes: []string{"workspace"}, wantExtensions: []string{workspaceExt, cliExt}, wantSkills: []string{workspaceSkill, cliSkill}},
		"user":      {scopes: []string{"user"}, wantExtensions: []string{userExt, cliExt}, wantSkills: []string{userSkill, cliSkill}},
		"both":      {scopes: []string{"workspace", "user"}, wantExtensions: []string{workspaceExt, userExt, cliExt}, wantSkills: []string{workspaceSkill, userSkill, cliSkill}},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			configs := collectExtensionConfigs(cwd, agentDir, sm, flags, &test.scopes)
			gotExtensions := make([]string, 0, len(configs))
			for _, config := range configs {
				gotExtensions = append(gotExtensions, config.Source)
			}
			for _, want := range test.wantExtensions {
				if !slices.Contains(gotExtensions, want) {
					t.Fatalf("extensions = %v, missing %s", gotExtensions, want)
				}
			}
			if len(gotExtensions) != len(test.wantExtensions) {
				t.Fatalf("extensions = %v, want %v", gotExtensions, test.wantExtensions)
			}
			skills := collectSkillInputs(cwd, agentDir, sm, flags, &test.scopes)
			for _, want := range test.wantSkills {
				if !slices.Contains(skills, want) {
					t.Fatalf("skills = %v, missing %s", skills, want)
				}
			}
			if len(skills) != len(test.wantSkills) {
				t.Fatalf("skills = %v, want %v", skills, test.wantSkills)
			}
		})
	}
}

func TestPigletAmbientScopesFilterPackageScope(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	makePackage := func(root, name string) string {
		t.Helper()
		skill := filepath.Join(root, "skills", name)
		if err := os.MkdirAll(skill, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"`+name+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return skill
	}
	userRoot := filepath.Join(t.TempDir(), "user-package")
	projectRoot := filepath.Join(t.TempDir(), "project-package")
	userSkill := makePackage(userRoot, "user-package")
	projectSkill := makePackage(projectRoot, "project-package")
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: userRoot}}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: projectRoot}}); err != nil {
		t.Fatal(err)
	}
	workspace := []string{"workspace"}
	if got := collectPackageSkillPaths(cwd, sm, &workspace); !slices.Equal(got, []string{projectSkill}) {
		t.Fatalf("workspace packages = %v", got)
	}
	user := []string{"user"}
	if got := collectPackageSkillPaths(cwd, sm, &user); !slices.Equal(got, []string{userSkill}) {
		t.Fatalf("user packages = %v", got)
	}
	none := []string{}
	if got := collectPackageSkillPaths(cwd, sm, &none); len(got) != 0 {
		t.Fatalf("disabled packages = %v", got)
	}
}

func TestHomeDirectoryDoesNotRediscoverGlobalPigRootAsProjectResources(t *testing.T) {
	home := t.TempDir()
	configRoot := filepath.Join(home, ".pig")
	agentDir := filepath.Join(configRoot, "agent")
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", configRoot)
	if err := os.MkdirAll(filepath.Join(configRoot, "skills", "duplicate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "skills", "duplicate", "SKILL.md"), []byte("---\nname: duplicate\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(home, agentDir)
	if got := collectSkillInputs(home, agentDir, sm, CLIFlags{}, nil); len(got) != 0 {
		t.Fatalf("global config root rediscovered as project skills: %v", got)
	}
	items, err := collectConfigResourceItems(home, agentDir, sm)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Scope == "project" && samePath(item.Path, filepath.Join(configRoot, "skills", "duplicate")) {
			t.Fatalf("global skill shown as project resource: %+v", item)
		}
	}
}

func TestDiscoverSkillDirUsesAgentsConvention(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agents", "skills")
	rootMarkdown := filepath.Join(root, "README.md")
	nestedMarkdown := filepath.Join(root, "group", "nested.md")
	for path, body := range map[string]string{
		rootMarkdown:   "---\ndescription: documentation\n---\n",
		nestedMarkdown: "---\ndescription: nested skill\n---\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := discoverSkillDir(root); !slices.Equal(got, []string{nestedMarkdown}) {
		t.Fatalf(".agents skill discovery = %v", got)
	}
}

func TestSkillInputOrderPreservesPiCollisionPrecedence(t *testing.T) {
	home, cwd, agentDir := t.TempDir(), t.TempDir(), t.TempDir()
	// The home directory is HOME on Unix and USERPROFILE on Windows, as for
	// upstream's os.homedir().
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))

	makeNamedSkill := func(root, relative, description string) string {
		t.Helper()
		dir := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: review\ndescription: " + description + "\n---\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	userPackage := t.TempDir()
	projectPackage := t.TempDir()
	userPackageSkill := makeNamedSkill(userPackage, "skills/review", "user package")
	projectPackageSkill := makeNamedSkill(projectPackage, "skills/review", "project package")
	userAgentsSkill := makeNamedSkill(home, ".agents/skills/review", "user agents")
	userPigSkill := makeNamedSkill(agentDir, "skills/review", "user pig")
	projectAgentsSkill := makeNamedSkill(cwd, ".agents/skills/review", "project agents")
	projectPigSkill := makeNamedSkill(cwd, ".pig/skills/review", "project pig")
	cliSkill := makeNamedSkill(t.TempDir(), "review", "cli")

	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: userPackage}}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: projectPackage}}); err != nil {
		t.Fatal(err)
	}
	inputs := collectSkillInputs(cwd, agentDir, sm, CLIFlags{Skills: []string{cliSkill}}, nil)
	want := []string{cliSkill, projectPigSkill, projectAgentsSkill, userPigSkill, userAgentsSkill, projectPackageSkill, userPackageSkill}
	if !slices.Equal(inputs, want) {
		t.Fatalf("skill precedence order =\n%v\nwant high-to-low =\n%v", inputs, want)
	}
	loaded, err := loadSkills(inputs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Description != "cli" {
		t.Fatalf("same-name winner = %+v, want CLI skill", loaded)
	}
}

func TestCollectExtensionConfigsIgnoresRetiredTOMLPlanes(t *testing.T) {
	pigHome := t.TempDir()
	cwd := t.TempDir()
	agentDir := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	retiredFile := "extensions." + "toml"
	for _, path := range []string{
		filepath.Join(pigHome, retiredFile),
		filepath.Join(cwd, ".pig", retiredFile),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("[[extension]]\nname=\"retired\"\npath=\"/bin/retired\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), CLIFlags{}, nil)
	if len(configs) != 0 {
		t.Fatalf("retired TOML planes loaded extensions: %#v", configs)
	}
}

func TestCollectExtensionConfigsDoesNotRediscoverLegacyGlobalDirectory(t *testing.T) {
	pigHome := t.TempDir()
	cwd := t.TempDir()
	agentDir := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	legacy := filepath.Join(pigHome, "extensions", "legacy")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "go.mod"), []byte("module example.com/legacy\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), CLIFlags{}, nil)
	if len(configs) != 0 {
		t.Fatalf("legacy global extension directory was rediscovered: %#v", configs)
	}
}
