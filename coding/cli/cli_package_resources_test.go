package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// cliPackageFixture builds a local -e Package (prompts/, skills/, themes/ by convention) whose
// prompt "same" and skill "same-skill" collide with a project's resources.
type cliPackageFixture struct {
	cwd, agentDir, pkg string
	pkgPrompt          string
	pkgSkill           string
	projectPrompt      string
	projectSkill       string
	userPrompt         string
}

func newCLIPackageFixture(t *testing.T) cliPackageFixture {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
	f := cliPackageFixture{cwd: filepath.Join(root, "project"), agentDir: filepath.Join(root, "agent"), pkg: filepath.Join(root, "cli-pkg")}
	write := func(path, content string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	prompt := func(description string) string { return "---\ndescription: " + description + "\n---\nbody\n" }
	skill := func(description string) string {
		return "---\nname: same-skill\ndescription: " + description + "\n---\nbody\n"
	}
	f.pkgPrompt = write(filepath.Join(f.pkg, "prompts", "same.md"), prompt("cli-package"))
	f.pkgSkill = write(filepath.Join(f.pkg, "skills", "same-skill", "SKILL.md"), skill("cli-package"))
	f.projectPrompt = write(filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "prompts", "same.md"), prompt("project"))
	f.projectSkill = write(filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME, "skills", "same-skill", "SKILL.md"), skill("project"))
	f.userPrompt = write(filepath.Join(f.agentDir, "prompts", "same.md"), prompt("user"))
	return f
}

// Pi resource-loader.ts reload(): resolveExtensionSources(additionalExtensionPaths, {temporary: true})
// yields cliEnabledPrompts, and promptPaths = mergePaths([...cliEnabledPrompts, ...enabledPrompts], ...).
// Prompt dedupe keeps the first of a name, so a -e Package's prompt beats the project's and the user's.
func TestCLIPackagePromptWinsSameNameCollision(t *testing.T) {
	f := newCLIPackageFixture(t)
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	flags := Args{Extensions: []string{f.pkg}}
	paths := collectPromptPaths(f.cwd, f.agentDir, sm, flags, true)
	if len(paths) == 0 || paths[0] != f.pkgPrompt {
		t.Fatalf("prompt paths = %v, want -e package prompt %s first", paths, f.pkgPrompt)
	}
	loaded := codingagent.LoadPromptTemplates("", "", paths...)
	for _, template := range loaded.Templates {
		if template.Name == "same" && template.FilePath != f.pkgPrompt {
			t.Fatalf("same wins from %s, want %s", template.FilePath, f.pkgPrompt)
		}
	}
}

// --no-prompt-templates keeps cliEnabledPrompts (resource-loader.ts: noPromptTemplates ? mergePaths(cliEnabledPrompts, additional)).
func TestCLIPackagePromptSurvivesNoPromptTemplates(t *testing.T) {
	f := newCLIPackageFixture(t)
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	paths := collectPromptPaths(f.cwd, f.agentDir, sm, Args{Extensions: []string{f.pkg}, NoPromptTemplates: true}, true)
	if len(paths) != 1 || paths[0] != f.pkgPrompt {
		t.Fatalf("prompt paths = %v, want only %s", paths, f.pkgPrompt)
	}
}

// Same rule for skills: skillPaths = mergePaths([...cliEnabledSkills, ...enabledSkills], additionalSkillPaths).
func TestCLIPackageSkillWinsSameNameCollision(t *testing.T) {
	f := newCLIPackageFixture(t)
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	inputs := collectSkillInputs(f.cwd, f.agentDir, sm, Args{Extensions: []string{f.pkg}}, nil)
	loaded, err := resolveAndLoadSkills(nil, inputs)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, def := range loaded.Defs {
		if def.Name == "same-skill" {
			found = true
			if def.FilePath != f.pkgSkill || def.Description != "cli-package" {
				t.Fatalf("same-skill wins from %s (%s), want %s", def.FilePath, def.Description, f.pkgSkill)
			}
		}
	}
	if !found {
		t.Fatalf("same-skill not loaded: inputs=%v defs=%+v", inputs, loaded.Defs)
	}
}

func TestCLIPackageSkillSurvivesNoSkills(t *testing.T) {
	f := newCLIPackageFixture(t)
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	inputs := collectSkillInputs(f.cwd, f.agentDir, sm, Args{Extensions: []string{f.pkg}, NoSkills: true}, nil)
	loaded, err := resolveAndLoadSkills(nil, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Defs) != 1 || loaded.Defs[0].FilePath != f.pkgSkill {
		t.Fatalf("skills = %+v (inputs %v), want only %s", loaded.Defs, inputs, f.pkgSkill)
	}
}

// Themes follow the same cliEnabledThemes rule.
func TestCLIPackageThemeListedFirst(t *testing.T) {
	f := newCLIPackageFixture(t)
	theme := filepath.Join(f.pkg, "themes", "cli.json")
	if err := os.MkdirAll(filepath.Dir(theme), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theme, []byte(`{"name":"cli"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	paths := collectThemePaths(f.cwd, f.agentDir, sm, Args{Extensions: []string{f.pkg}, NoThemes: true}, true)
	if len(paths) != 1 || paths[0] != theme {
		t.Fatalf("theme paths = %v, want only %s", paths, theme)
	}
}

// The reload snapshot must carry the same -e resources, or /reload drops them.
func TestCLIPackageResourcesSurviveReloadSnapshot(t *testing.T) {
	f := newCLIPackageFixture(t)
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	snapshot := reloadResourceSnapshotProvider(f.cwd, f.agentDir, sm, Args{Extensions: []string{f.pkg}}, nil)()
	if snapshot.Err != nil {
		t.Fatal(snapshot.Err)
	}
	if len(snapshot.PromptPaths) == 0 || snapshot.PromptPaths[0] != f.pkgPrompt {
		t.Fatalf("snapshot prompts = %v", snapshot.PromptPaths)
	}
	if len(snapshot.SkillPaths) == 0 || snapshot.SkillPaths[0] != filepath.Dir(f.pkgSkill) && snapshot.SkillPaths[0] != f.pkgSkill {
		t.Fatalf("snapshot skills = %v", snapshot.SkillPaths)
	}
}

// Pi collectPackageResources treats a directory with an extensions, skills, prompts or themes directory as a Package, so
// `-e <dir>` with only prompts and skills loads no extension (resolveLocalExtensionSource adds the directory as an
// extension only when collectPackageResources finds nothing).
func TestCLIPackageWithoutExtensionsLoadsNoExtension(t *testing.T) {
	f := newCLIPackageFixture(t)
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	configs := collectExtensionConfigs(f.cwd, f.agentDir, sm, Args{Extensions: []string{f.pkg}}, nil)
	if len(configs) != 0 {
		t.Fatalf("extension configs = %+v, want none", configs)
	}
}

// Pi metadataByPath: a -e prompt keeps its Package's PathMetadata ({source: <-e string>, scope: temporary, origin: package,
// baseDir: <package>}); a -e skill is {source: cli, scope: temporary, origin: top-level} with no baseDir. Probed against Pi 1.1.0
// get_commands.
func TestCLIPackageResourceSourceInfoMatchesPi(t *testing.T) {
	f := newCLIPackageFixture(t)
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	infos := resourceSourceInfoProvider(f.cwd, f.agentDir, sm, Args{Extensions: []string{f.pkg}})()
	prompt := infos[f.pkgPrompt]
	if prompt.Source != f.pkg || prompt.Scope != "temporary" || prompt.Origin != "package" || prompt.BaseDir != f.pkg {
		t.Fatalf("prompt info = %+v", prompt)
	}
	skill := infos[f.pkgSkill]
	if skill.Source != "cli" || skill.Scope != "temporary" || skill.Origin != "top-level" || skill.BaseDir != "" {
		t.Fatalf("skill info = %+v", skill)
	}
}

// --prompt-template and --theme paths are Pi's additionalPromptTemplatePaths and additionalThemePaths, merged after every
// resolved path, so a same-name user prompt wins. Probed against Pi 1.1.0.
func TestCLIPackagePromptBeatsPromptTemplateFlag(t *testing.T) {
	f := newCLIPackageFixture(t)
	flagPrompt := filepath.Join(t.TempDir(), "same.md")
	if err := os.WriteFile(flagPrompt, []byte("---\ndescription: flag\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	paths := collectPromptPaths(f.cwd, f.agentDir, sm, Args{Extensions: []string{f.pkg}, PromptTemplates: []string{flagPrompt}}, true)
	want := []string{f.pkgPrompt, f.projectPrompt, f.userPrompt, flagPrompt}
	if !slices.Equal(paths, want) {
		t.Fatalf("prompt paths = %v\nwant %v", paths, want)
	}
}

// Pi reload() records resolvedPaths metadata before cliExtensionPaths (getEnabledResources keeps the first entry), so a
// Package that settings also configure keeps its configured PathMetadata, scope user, for its prompts and skills.
// Probed against Pi 1.1.0 get_commands with packages: [<pkg>] and -e <pkg>.
func TestCLIPackageConfiguredPackageMetadataWins(t *testing.T) {
	f := newCLIPackageFixture(t)
	if err := os.WriteFile(filepath.Join(f.agentDir, "settings.json"), []byte(`{"packages":["`+filepath.ToSlash(f.pkg)+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(f.cwd, f.agentDir)
	infos := resourceSourceInfoProvider(f.cwd, f.agentDir, sm, Args{Extensions: []string{f.pkg}})()
	for _, path := range []string{f.pkgPrompt, f.pkgSkill} {
		if info := infos[path]; info.Scope != "user" || info.Origin != "package" {
			t.Fatalf("%s info = %+v, want the configured Package's user scope", path, info)
		}
	}
}
