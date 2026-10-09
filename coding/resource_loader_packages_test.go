package coding

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func loaderFor(t *testing.T, f resourceLoaderFixture, settings icodingagent.Settings, trusted bool, mutate func(*DefaultResourceLoaderOptions)) *DefaultResourceLoader {
	t.Helper()
	manager := icodingagent.NewInMemorySettingsManager(settings)
	manager.SetProjectTrusted(trusted)
	opts := DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: manager}
	if mutate != nil {
		mutate(&opts)
	}
	loader := NewDefaultResourceLoader(opts)
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	return loader
}

func skillNamed(t *testing.T, loader ResourceLoader, name string) *Skill {
	t.Helper()
	for _, skill := range loader.GetSkills().Skills {
		if skill.Name == name {
			return skill
		}
	}
	t.Fatalf("no skill %q in %v", name, skillNames(loader))
	return nil
}

func promptNamed(t *testing.T, loader ResourceLoader, name string) PromptTemplate {
	t.Helper()
	for _, prompt := range loader.GetPrompts().Prompts {
		if prompt.Name == name {
			return prompt
		}
	}
	t.Fatalf("no prompt %q", name)
	return PromptTemplate{}
}

func writePrompt(t *testing.T, path, description string) {
	t.Helper()
	writeResource(t, path, "---\ndescription: "+description+"\n---\n"+description+" content\n")
}

// A Session's default loader resolves configured Packages as DefaultPackageManager.resolve does: a local Package's manifest skills and prompts are loaded, with the Package's metadata (resource-loader.ts:398-433, package-manager.ts:1251-1305, 1350-1388).
func TestDefaultSessionLoaderResolvesLocalPackages(t *testing.T) {
	f := newResourceLoaderFixture(t)
	pkg := filepath.Join(t.TempDir(), "local-pkg")
	writeResource(t, filepath.Join(pkg, "package.json"), `{"name":"local-pkg","pi":{"skills":["./skills"],"prompts":["./prompts"]}}`)
	writeSkill(t, filepath.Join(pkg, "skills"), "package-skill")
	writeSkill(t, filepath.Join(pkg, "skills"), "filtered-skill")
	writePrompt(t, filepath.Join(pkg, "prompts", "package-prompt.md"), "Package prompt")
	settings, err := json.Marshal(map[string]any{"packages": []any{map[string]any{"source": pkg, "skills": []string{"-skills/filtered-skill/SKILL.md"}}}})
	if err != nil {
		t.Fatal(err)
	}
	writeResource(t, filepath.Join(f.agentDir, "settings.json"), string(settings))

	loader := f.session(t, true, nil).ResourceLoader()
	if got := skillNames(loader); !slices.Equal(got, []string{"package-skill"}) {
		t.Fatalf("skills = %v, want only the enabled Package skill", got)
	}
	want := icodingagent.PiSourceInfo{Source: pkg, Scope: "user", Origin: "package", BaseDir: pkg}
	got := skillNamed(t, loader, "package-skill").SourceInfo
	want.Path = got.Path
	if got != want || filepath.Base(got.Path) != "SKILL.md" {
		t.Errorf("skill sourceInfo = %+v, want %+v", got, want)
	}
	prompt := promptNamed(t, loader, "package-prompt")
	if want.Path = prompt.FilePath; prompt.SourceInfo != want {
		t.Errorf("prompt sourceInfo = %+v, want %+v", prompt.SourceInfo, want)
	}
	if !strings.Contains(prompt.Content, "Package prompt content") {
		t.Errorf("prompt content = %q", prompt.Content)
	}
}

// resource-loader.test.ts:685: "should keep package metadata for skills, prompts, and themes". The extension-discovery and theme halves belong to the Extension Host and the theme controller, not to this loader.
func TestUpstreamDefaultResourceLoaderKeepsPackageMetadata(t *testing.T) {
	f := newResourceLoaderFixture(t)
	root := filepath.Join(f.agentDir, "npm", "node_modules", "metadata-pkg")
	writeResource(t, filepath.Join(root, "package.json"), `{"name":"metadata-pkg","version":"1.0.0"}`)
	writeResource(t, filepath.Join(root, "skills", "package-skill", "SKILL.md"), "---\nname: package-skill\ndescription: Package skill\n---\nPackage skill content")
	writeResource(t, filepath.Join(root, "prompts", "package-prompt.md"), "---\ndescription: Package prompt\n---\nPackage prompt content")

	loader := loaderFor(t, f, icodingagent.Settings{Packages: []icodingagent.PackageSource{{Source: "npm:metadata-pkg"}}}, true, nil)
	want := icodingagent.PiSourceInfo{Source: "npm:metadata-pkg", Scope: "user", Origin: "package", BaseDir: root}
	skill := skillNamed(t, loader, "package-skill")
	if want.Path = skill.FilePath; skill.SourceInfo != want {
		t.Errorf("skill sourceInfo = %+v, want %+v", skill.SourceInfo, want)
	}
	prompt := promptNamed(t, loader, "package-prompt")
	if want.Path = prompt.FilePath; prompt.SourceInfo != want {
		t.Errorf("prompt sourceInfo = %+v, want %+v", prompt.SourceInfo, want)
	}
}

// Project Packages follow project trust (settings-manager.ts getProjectSettings is empty while untrusted) and rank before user resources.
func TestDefaultLoaderPackagesFollowProjectTrust(t *testing.T) {
	f := newResourceLoaderFixture(t)
	pkg := filepath.Join(t.TempDir(), "project-pkg")
	writeResource(t, filepath.Join(pkg, "package.json"), `{"name":"project-pkg","pi":{"skills":["./skills"]}}`)
	writeSkill(t, filepath.Join(pkg, "skills"), "project-package-skill")
	writeResource(t, filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "settings.json"), `{"packages":[`+quote(pkg)+`]}`)

	if got := skillNames(f.session(t, true, nil).ResourceLoader()); !slices.Equal(got, []string{"project-package-skill"}) {
		t.Errorf("trusted skills = %v", got)
	}
	if got := skillNames(f.session(t, false, nil).ResourceLoader()); len(got) != 0 {
		t.Errorf("untrusted skills = %v, want none", got)
	}
	info := skillNamed(t, f.session(t, true, nil).ResourceLoader(), "project-package-skill").SourceInfo
	if info.Scope != "project" || info.Origin != "package" || info.Source != pkg {
		t.Errorf("sourceInfo = %+v", info)
	}
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

// fakeNPM installs an npm Package with a skill when asked to `install`, or exits with failure, and records each call.
func fakeNPM(t *testing.T, root string, fail bool) (command []string, record string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	record = filepath.Join(root, "calls.jsonl")
	script := filepath.Join(root, "npm.cjs")
	writeResource(t, script, `const fs = require('node:fs');
const path = require('node:path');
const [record, mode, ...args] = process.argv.slice(2);
fs.appendFileSync(record, JSON.stringify(args) + '\n');
if (mode === 'fail') process.exit(7);
const prefix = args.indexOf('--prefix');
if (args.includes('install') && prefix >= 0) {
  const dir = path.join(args[prefix+1], 'node_modules', 'installed-pkg');
  fs.mkdirSync(path.join(dir, 'skills', 'installed-skill'), {recursive: true});
  fs.writeFileSync(path.join(dir, 'package.json'), JSON.stringify({name: 'installed-pkg', version: '1.0.0', pi: {skills: ['./skills']}}));
  fs.writeFileSync(path.join(dir, 'skills', 'installed-skill', 'SKILL.md'), '---\nname: installed-skill\ndescription: Installed skill\n---\nBody\n');
}
if (args.includes('root')) process.stdout.write(path.join(process.argv[2], '..', 'none'));
`)
	mode := "ok"
	if fail {
		mode = "fail"
	}
	return []string{node, script, record, mode, "--", "npm"}, record
}

// An absent npm Package is installed when the default loader reloads, as DefaultPackageManager.resolve installs it without an onMissing callback (package-manager.ts:1268-1281, 1292-1301); a failed installation rejects the reload, and offline mode installs nothing (package-manager.ts:1269).
func TestDefaultLoaderInstallsMissingNPMPackages(t *testing.T) {
	newFixture := func(t *testing.T, fail bool) (resourceLoaderFixture, string) {
		f := newResourceLoaderFixture(t)
		t.Setenv("PI_OFFLINE", "")
		t.Setenv("PIG_OFFLINE", "")
		command, record := fakeNPM(t, t.TempDir(), fail)
		settings, err := json.Marshal(map[string]any{"npmCommand": command, "packages": []string{"npm:installed-pkg"}})
		if err != nil {
			t.Fatal(err)
		}
		writeResource(t, filepath.Join(f.agentDir, "settings.json"), string(settings))
		return f, record
	}
	t.Run("installs", func(t *testing.T) {
		f, record := newFixture(t, false)
		loader := f.session(t, true, nil).ResourceLoader()
		if got := skillNames(loader); !slices.Equal(got, []string{"installed-skill"}) {
			t.Fatalf("skills = %v", got)
		}
		if data, err := os.ReadFile(record); err != nil || !strings.Contains(string(data), "install") {
			t.Fatalf("npm calls = %q, %v", data, err)
		}
	})
	t.Run("failed installation rejects the reload", func(t *testing.T) {
		f, _ := newFixture(t, true)
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: f.cwd, AgentDir: f.agentDir})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)
		manager, err := NewInMemorySessionManager(f.cwd)
		if err != nil {
			t.Fatal(err)
		}
		if session, err := NewSession(services, SessionOptions{SessionManager: manager}); err == nil {
			_ = session.Close()
			t.Fatal("NewSession succeeded although the Package installation failed")
		} else if !strings.Contains(err.Error(), "npm:installed-pkg") || !strings.HasSuffix(err.Error(), " failed with code 7") {
			// The failure keeps Pi's runCommand message (package-manager.ts:2679) as the wrapped cause.
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("offline installs nothing", func(t *testing.T) {
		f, record := newFixture(t, false)
		t.Setenv("PI_OFFLINE", "1")
		if got := skillNames(f.session(t, true, nil).ResourceLoader()); len(got) != 0 {
			t.Fatalf("skills = %v", got)
		}
		if data, err := os.ReadFile(record); err == nil && strings.Contains(string(data), "install") {
			t.Fatalf("offline mode installed: %s", data)
		}
	})
}

// Resolved resources carry the metadata Pi records: settings entries are local without a baseDir, auto-discovered ones name their config directory, and .agents skills name the .agents directory (package-manager.ts:2352-2495, resource-loader.ts:687-690, 715-718).
func TestDefaultLoaderKeepsResourceMetadata(t *testing.T) {
	f := newResourceLoaderFixture(t)
	projectDir := icodingagent.ProjectConfigDir(f.cwd)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
	writeSkill(t, filepath.Join(f.agentDir, "extra"), "configured-skill")
	writeSkill(t, filepath.Join(projectDir, "skills"), "project-skill")
	writeSkill(t, filepath.Join(f.home, ".agents", "skills"), "home-skill")
	writeSkill(t, filepath.Join(f.repo, ".agents", "skills"), "ancestor-skill")
	writePrompt(t, filepath.Join(f.agentDir, "prompts", "user-prompt.md"), "User prompt")
	writePrompt(t, filepath.Join(projectDir, "prompts", "project-prompt.md"), "Project prompt")
	writePrompt(t, filepath.Join(f.agentDir, "more", "configured-prompt.md"), "Configured prompt")

	loader := loaderFor(t, f, icodingagent.Settings{Skills: []string{"extra"}, Prompts: []string{"more"}}, true, nil)
	for name, want := range map[string]icodingagent.PiSourceInfo{
		"user-skill":       {Source: "auto", Scope: "user", Origin: "top-level", BaseDir: f.agentDir},
		"configured-skill": {Source: "local", Scope: "user", Origin: "top-level"},
		"project-skill":    {Source: "auto", Scope: "project", Origin: "top-level", BaseDir: projectDir},
		"home-skill":       {Source: "auto", Scope: "user", Origin: "top-level", BaseDir: filepath.Join(f.home, ".agents")},
		"ancestor-skill":   {Source: "auto", Scope: "project", Origin: "top-level", BaseDir: filepath.Join(f.repo, ".agents")},
	} {
		got := skillNamed(t, loader, name).SourceInfo
		want.Path = got.Path
		if got != want || filepath.Base(got.Path) != "SKILL.md" {
			t.Errorf("%s sourceInfo = %+v, want %+v", name, got, want)
		}
	}
	for name, want := range map[string]icodingagent.PiSourceInfo{
		"user-prompt":       {Source: "auto", Scope: "user", Origin: "top-level", BaseDir: f.agentDir},
		"configured-prompt": {Source: "local", Scope: "user", Origin: "top-level"},
		"project-prompt":    {Source: "auto", Scope: "project", Origin: "top-level", BaseDir: projectDir},
	} {
		prompt := promptNamed(t, loader, name)
		want.Path = prompt.FilePath
		if prompt.SourceInfo != want || prompt.Scope != want.Scope {
			t.Errorf("%s sourceInfo = %+v scope %q, want %+v", name, prompt.SourceInfo, prompt.Scope, want)
		}
	}
}

// resource-loader.test.ts:34, 417, 786, 805, 831, 855 and 523-570 on the native loader: empty before reload, noContextFiles, noSkills with and without additional paths, the override callbacks, and literal or file-backed prompt options. The additional-path diagnostics are resource-loader.ts:475-481 and 498-506.
func TestUpstreamDefaultResourceLoaderOptions(t *testing.T) {
	t.Run("empty before reload", func(t *testing.T) {
		f := newResourceLoaderFixture(t)
		loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir})
		if len(loader.GetSkills().Skills) != 0 || len(loader.GetPrompts().Prompts) != 0 || len(loader.GetAgentsFiles().AgentsFiles) != 0 {
			t.Fatal("a new loader holds resources")
		}
	})
	t.Run("noContextFiles", func(t *testing.T) {
		f := newResourceLoaderFixture(t)
		writeResource(t, filepath.Join(f.cwd, "AGENTS.md"), "# Project Guidelines")
		writeResource(t, filepath.Join(f.cwd, "CLAUDE.md"), "# Claude Guidelines")
		if files := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) { o.NoContextFiles = true }).GetAgentsFiles().AgentsFiles; len(files) != 0 {
			t.Fatalf("context files = %v", files)
		}
		if files := loaderFor(t, f, icodingagent.Settings{}, true, nil).GetAgentsFiles().AgentsFiles; len(files) == 0 {
			t.Fatal("context files were not discovered")
		}
	})
	t.Run("noSkills", func(t *testing.T) {
		// The upstream fixtures are root-level markdown skills: agentDir/skills/test-skill.md and custom-skills/custom.md.
		f := newResourceLoaderFixture(t)
		writeResource(t, filepath.Join(f.agentDir, "skills", "test-skill.md"), "---\nname: test-skill\ndescription: A test skill\n---\nContent")
		custom := filepath.Join(t.TempDir(), "custom-skills")
		writeResource(t, filepath.Join(custom, "custom.md"), "---\nname: custom\ndescription: Custom skill\n---\nContent")
		if got := skillNames(loaderFor(t, f, icodingagent.Settings{}, true, nil)); !slices.Equal(got, []string{"test-skill"}) {
			t.Fatalf("skills without noSkills = %v, want the discovered test-skill", got)
		}
		if got := skillNames(loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) { o.NoSkills = true })); len(got) != 0 {
			t.Fatalf("skills = %v", got)
		}
		loader := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
			o.NoSkills, o.AdditionalSkillPaths = true, []string{custom}
		})
		if got := skillNames(loader); !slices.Equal(got, []string{"custom"}) {
			t.Fatalf("skills = %v, want only the additional path", got)
		}
	})
	t.Run("noPromptTemplates", func(t *testing.T) {
		f := newResourceLoaderFixture(t)
		writePrompt(t, filepath.Join(f.agentDir, "prompts", "auto.md"), "Auto")
		extra := filepath.Join(t.TempDir(), "extra.md")
		writePrompt(t, extra, "Extra")
		loader := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
			o.NoPromptTemplates, o.AdditionalPromptTemplatePaths = true, []string{extra}
		})
		prompts := loader.GetPrompts().Prompts
		if len(prompts) != 1 || prompts[0].Name != "extra" || prompts[0].SourceInfo.Scope != "temporary" {
			t.Fatalf("prompts = %+v", prompts)
		}
	})
	t.Run("missing additional paths are diagnosed", func(t *testing.T) {
		f := newResourceLoaderFixture(t)
		missing := filepath.Join(t.TempDir(), "missing")
		diagnostic := func(d extension.ResourceDiagnostic) [3]string { return [3]string{d.Type, d.Message, d.Path} }
		loader := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
			o.AdditionalSkillPaths, o.AdditionalPromptTemplatePaths = []string{missing}, []string{missing}
		})
		// loadSkills warns about the path itself (skills.ts:480), so the loader adds no second diagnostic.
		var skillDiagnostics [][3]string
		for _, d := range loader.GetSkills().Diagnostics {
			skillDiagnostics = append(skillDiagnostics, diagnostic(d))
		}
		if want := [][3]string{{"warning", "skill path does not exist", missing}}; !slices.Equal(skillDiagnostics, want) {
			t.Errorf("skill diagnostics = %v, want %v", skillDiagnostics, want)
		}
		var promptDiagnostics [][3]string
		for _, d := range loader.GetPrompts().Diagnostics {
			promptDiagnostics = append(promptDiagnostics, diagnostic(d))
		}
		if want := [][3]string{{"error", "Prompt template path does not exist", missing}}; !slices.Equal(promptDiagnostics, want) {
			t.Errorf("prompt diagnostics = %v, want %v", promptDiagnostics, want)
		}
		// resource-loader.ts:475-481: an override that drops the diagnostics leaves the loader to report the missing path, and a non-local source is not a path.
		overridden := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
			o.AdditionalSkillPaths = []string{missing, "npm:not-local"}
			o.SkillsOverride = func(SkillsResult) SkillsResult { return SkillsResult{} }
		})
		if got := overridden.GetSkills().Diagnostics; len(got) != 1 || diagnostic(got[0]) != [3]string{"error", "Skill path does not exist", missing} {
			t.Errorf("overridden skill diagnostics = %+v", got)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		f := newResourceLoaderFixture(t)
		writeSkill(t, filepath.Join(f.agentDir, "skills"), "discovered")
		writeResource(t, filepath.Join(f.agentDir, "SYSTEM.md"), "file system prompt")
		injected := &Skill{Name: "injected", Description: "Injected skill", FilePath: "/fake/path", BaseDir: "/fake", SourceInfo: icodingagent.PiSourceInfo{Path: "/fake/path", Source: "custom", Scope: "temporary", Origin: "top-level"}}
		loader := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
			o.SkillsOverride = func(SkillsResult) SkillsResult { return SkillsResult{Skills: []*Skill{injected}} }
			o.SystemPromptOverride = func(*string) *string { return new("Custom system prompt") }
			o.AppendSystemPromptOverride = func(base []string) []string { return append(base, "appended") }
			o.AgentsFilesOverride = func(AgentsFilesResult) AgentsFilesResult {
				return AgentsFilesResult{AgentsFiles: []ContextFile{{Path: "/fake/AGENTS.md", Content: "fake"}}}
			}
		})
		if skills := loader.GetSkills().Skills; len(skills) != 1 || skills[0].Name != "injected" || skills[0].SourceInfo.Source != "custom" {
			t.Errorf("skills = %+v", skills)
		}
		if got, ok := loader.GetSystemPrompt(); !ok || got != "Custom system prompt" {
			t.Errorf("system prompt = %q, %v", got, ok)
		}
		if got := loader.GetAppendSystemPrompt(); !slices.Equal(got, []string{"appended"}) {
			t.Errorf("append = %v", got)
		}
		if files := loader.GetAgentsFiles().AgentsFiles; len(files) != 1 || files[0].Content != "fake" {
			t.Errorf("agents files = %+v", files)
		}
	})
	t.Run("prompt options", func(t *testing.T) {
		f := newResourceLoaderFixture(t)
		writeResource(t, filepath.Join(f.agentDir, "SYSTEM.md"), "discovered")
		writeResource(t, filepath.Join(f.agentDir, "APPEND_SYSTEM.md"), "discovered append")
		file := filepath.Join(t.TempDir(), "custom-system.md")
		writeResource(t, file, "Custom system prompt.")
		literal := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
			o.SystemPrompt, o.AppendSystemPrompt = new("Literal system prompt."), []string{"literal append", file}
		})
		if got, _ := literal.GetSystemPrompt(); got != "Literal system prompt." {
			t.Errorf("system prompt = %q", got)
		}
		if got := literal.GetAppendSystemPrompt(); !slices.Equal(got, []string{"literal append", "Custom system prompt."}) {
			t.Errorf("append = %v", got)
		}
		fromFile := loaderFor(t, f, icodingagent.Settings{}, true, func(o *DefaultResourceLoaderOptions) {
			o.SystemPrompt, o.AppendSystemPrompt = &file, []string{}
		})
		if got, _ := fromFile.GetSystemPrompt(); got != "Custom system prompt." {
			t.Errorf("system prompt = %q", got)
		}
		if got := fromFile.GetAppendSystemPrompt(); len(got) != 0 {
			t.Errorf("an empty option list appended %v", got)
		}
	})
}

// resource-loader.ts:255-256 resolves the cwd and agentDir options with resolvePath, so a relative cwd and a "~" agent directory yield absolute resource paths and sourceInfo.
func TestDefaultResourceLoaderResolvesItsDirectories(t *testing.T) {
	f := newResourceLoaderFixture(t)
	agentDir := filepath.Join(f.home, "agent")
	writeSkill(t, filepath.Join(agentDir, "skills"), "user-skill")
	writeSkill(t, filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "skills"), "project-skill")
	t.Chdir(f.repo)
	relativeCWD, err := filepath.Rel(f.repo, f.cwd)
	if err != nil {
		t.Fatal(err)
	}

	loader := loaderFor(t, resourceLoaderFixture{repo: f.repo, home: f.home, cwd: relativeCWD, agentDir: "~/agent"}, icodingagent.Settings{}, true, nil)
	for name, want := range map[string]icodingagent.PiSourceInfo{
		"user-skill":    {Source: "auto", Scope: "user", Origin: "top-level", BaseDir: agentDir},
		"project-skill": {Source: "auto", Scope: "project", Origin: "top-level", BaseDir: icodingagent.ProjectConfigDir(f.cwd)},
	} {
		skill := skillNamed(t, loader, name)
		want.Path = skill.FilePath
		if !filepath.IsAbs(skill.FilePath) || skill.SourceInfo != want {
			t.Errorf("%s path %q sourceInfo = %+v, want an absolute path and %+v", name, skill.FilePath, skill.SourceInfo, want)
		}
	}
}

// A DefaultPackageManager takes its cwd and agentDir from the loader that creates it (resource-loader.ts:258-272, package-manager.ts:2094-2112), so a git Package is found under the loader's agent directory, not the process's.
func TestDefaultLoaderResolvesGitPackagesUnderItsAgentDir(t *testing.T) {
	f := newResourceLoaderFixture(t)
	t.Setenv("PI_OFFLINE", "1")
	checkout := filepath.Join(f.agentDir, "git", "github.com", "example", "pig-review")
	writeSkill(t, filepath.Join(checkout, "skills"), "git-skill")
	loader := loaderFor(t, f, icodingagent.Settings{Packages: []icodingagent.PackageSource{{Source: "git:github.com/example/pig-review"}}}, true, nil)
	if got := skillNames(loader); !slices.Equal(got, []string{"git-skill"}) {
		t.Fatalf("skills = %v, want the Package installed under %s", got, f.agentDir)
	}
}

// A missing git Package is cloned below the loader's agent directory (package-manager.ts:2094-2112, 1254-1281).
func TestDefaultLoaderInstallsGitPackagesUnderItsAgentDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the git stub is a shell script")
	}
	f := newResourceLoaderFixture(t)
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_OFFLINE", "")
	bin := t.TempDir()
	writeResource(t, filepath.Join(bin, "git"), "#!/bin/sh\nif [ \"$1\" = clone ]; then mkdir -p \"$3/skills/git-skill\"; printf -- '---\\nname: git-skill\\ndescription: Git skill\\n---\\nBody\\n' > \"$3/skills/git-skill/SKILL.md\"; fi\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	loader := loaderFor(t, f, icodingagent.Settings{Packages: []icodingagent.PackageSource{{Source: "git:github.com/example/pig-review"}}}, true, nil)
	if got := skillNames(loader); !slices.Equal(got, []string{"git-skill"}) {
		t.Fatalf("skills = %v", got)
	}
	if _, err := os.Stat(filepath.Join(f.agentDir, "git", "github.com", "example", "pig-review", "skills")); err != nil {
		t.Errorf("the clone is not below the loader's agent directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.agentDir), "ambient")); err == nil {
		t.Errorf("the install wrote below the process's agent directory")
	}
}

// One SettingsManager reused by loaders for different agent directories resolves Packages for each loader's own directories: DefaultPackageManagerOptions carry cwd and agentDir per loader (resource-loader.ts:258-272).
func TestDefaultLoaderPackagesFollowEachLoadersDirectories(t *testing.T) {
	f := newResourceLoaderFixture(t)
	t.Setenv("PI_OFFLINE", "1")
	otherAgent := filepath.Join(filepath.Dir(f.agentDir), "other-agent")
	for _, agentDir := range []string{f.agentDir, otherAgent} {
		root := filepath.Join(agentDir, "npm", "node_modules", "reused-pkg")
		writeResource(t, filepath.Join(root, "package.json"), `{"name":"reused-pkg","version":"1.0.0"}`)
		writeSkill(t, filepath.Join(root, "skills"), "skill-in-"+filepath.Base(agentDir))
	}
	manager := icodingagent.NewInMemorySettingsManager(icodingagent.Settings{Packages: []icodingagent.PackageSource{{Source: "npm:reused-pkg"}}})
	manager.SetProjectTrusted(true)
	for _, agentDir := range []string{f.agentDir, otherAgent} {
		loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: agentDir, SettingsManager: manager})
		if err := loader.Reload(); err != nil {
			t.Fatal(err)
		}
		if got, want := skillNames(loader), []string{"skill-in-" + filepath.Base(agentDir)}; !slices.Equal(got, want) {
			t.Errorf("loader for %s: skills = %v, want %v", agentDir, got, want)
		}
	}
}

// resource-loader.ts:258-261 resolves both directory options, so a relative agentDir yields absolute sourceInfo.baseDir.
func TestDefaultResourceLoaderResolvesRelativeAgentDirMetadata(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "relative-skill")
	t.Chdir(f.repo)
	relativeCWD, err := filepath.Rel(f.repo, f.cwd)
	if err != nil {
		t.Fatal(err)
	}
	relativeAgent, err := filepath.Rel(f.repo, f.agentDir)
	if err != nil {
		t.Fatal(err)
	}
	loader := loaderFor(t, resourceLoaderFixture{repo: f.repo, home: f.home, cwd: relativeCWD, agentDir: relativeAgent}, icodingagent.Settings{}, true, nil)
	if got := skillNamed(t, loader, "relative-skill").SourceInfo.BaseDir; got != f.agentDir {
		t.Errorf("baseDir = %q, want %q", got, f.agentDir)
	}
}

// A path that a Package and a settings entry both resolve keeps the Package's metadata and enabled state, and ranks with Package resources: DefaultPackageManager.resolve fills its first-wins accumulator with Packages before settings and auto-discovered entries, then sorts by resourcePrecedenceRank (package-manager.ts:927-928, 2555-2565, 2576-2594; resource-loader.ts:417-423).
func TestDefaultLoaderPackageResourceWinsItsPathAgainstSettingsEntries(t *testing.T) {
	newFixture := func(t *testing.T, filter []string) (resourceLoaderFixture, string, icodingagent.Settings) {
		f := newResourceLoaderFixture(t)
		pkg := filepath.Join(t.TempDir(), "shared-pkg")
		writeResource(t, filepath.Join(pkg, "package.json"), `{"name":"shared-pkg","pi":{"skills":["./skills"]}}`)
		writeSkill(t, filepath.Join(pkg, "skills"), "shared-skill")
		writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
		source := icodingagent.PackageSource{Source: pkg}
		if filter != nil {
			source.Skills = filter
		}
		return f, pkg, icodingagent.Settings{
			Packages: []icodingagent.PackageSource{source},
			Skills:   []string{filepath.Join(pkg, "skills", "shared-skill")},
		}
	}
	t.Run("metadata and rank", func(t *testing.T) {
		f, pkg, settings := newFixture(t, nil)
		loader := loaderFor(t, f, settings, true, nil)
		skills := loader.GetSkills().Skills
		if len(skills) != 2 || skills[0].Name != "user-skill" || skills[1].Name != "shared-skill" {
			t.Fatalf("skills = %v, want the user skill then the Package skill", orderedSkillNames(loader))
		}
		if info := skills[1].SourceInfo; info.Origin != "package" || info.Source != pkg {
			t.Errorf("sourceInfo = %+v, want the Package's", info)
		}
	})
	t.Run("a disabled Package resource stays disabled", func(t *testing.T) {
		f, _, settings := newFixture(t, []string{"-skills/shared-skill/SKILL.md"})
		if got := skillNames(loaderFor(t, f, settings, true, nil)); !slices.Equal(got, []string{"user-skill"}) {
			t.Errorf("skills = %v, want the disabled Package resource to stay disabled", got)
		}
	})
}

func orderedSkillNames(loader ResourceLoader) []string {
	var names []string
	for _, skill := range loader.GetSkills().Skills {
		names = append(names, skill.Name)
	}
	return names
}

// A resource that settings disable is still recorded in metadataByPath, so it keeps its metadata when an additional path loads it (resource-loader.ts:417-423, package-manager.ts:2329-2350, 2352-2495).
func TestDefaultLoaderKeepsMetadataOfDisabledResources(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "off-skill")
	skillDir := filepath.Join(f.agentDir, "skills", "off-skill")
	settings := icodingagent.Settings{Skills: []string{"-skills/off-skill/SKILL.md"}}
	if got := skillNames(loaderFor(t, f, settings, true, nil)); len(got) != 0 {
		t.Fatalf("skills = %v, want the disabled skill to stay unloaded", got)
	}
	loader := loaderFor(t, f, settings, true, func(opts *DefaultResourceLoaderOptions) { opts.AdditionalSkillPaths = []string{skillDir} })
	want := icodingagent.PiSourceInfo{Source: "auto", Scope: "user", Origin: "top-level", BaseDir: f.agentDir}
	got := skillNamed(t, loader, "off-skill").SourceInfo
	want.Path = got.Path
	if got != want {
		t.Errorf("sourceInfo = %+v, want %+v", got, want)
	}
}

// Pi's accumulator takes every settings entry (project, then user) before any auto-discovered resource, so a path that a user settings entry and project auto-discovery both reach keeps the settings entry's metadata (package-manager.ts:933-961 resolveLocalEntries before addAutoDiscoveredResources, 2555-2565 addResource first-wins).
func TestDefaultLoaderSettingsEntriesFillTheAccumulatorBeforeAutoDiscovery(t *testing.T) {
	f := newResourceLoaderFixture(t)
	projectDir := icodingagent.ProjectConfigDir(f.cwd)
	writeSkill(t, filepath.Join(projectDir, "skills"), "shared-skill")
	writePrompt(t, filepath.Join(projectDir, "prompts", "shared-prompt.md"), "Shared prompt")
	settings := icodingagent.Settings{
		Skills:  []string{filepath.Join(projectDir, "skills", "shared-skill")},
		Prompts: []string{filepath.Join(projectDir, "prompts", "shared-prompt.md")},
	}
	loader := loaderFor(t, f, settings, true, nil)
	want := icodingagent.PiSourceInfo{Source: "local", Scope: "user", Origin: "top-level"}
	skill := skillNamed(t, loader, "shared-skill").SourceInfo
	want.Path = skill.Path
	if skill != want {
		t.Errorf("skill sourceInfo = %+v, want %+v", skill, want)
	}
	prompt := promptNamed(t, loader, "shared-prompt")
	want.Path = prompt.FilePath
	if prompt.SourceInfo != want || prompt.Scope != "user" {
		t.Errorf("prompt sourceInfo = %+v scope %q, want %+v", prompt.SourceInfo, prompt.Scope, want)
	}
}

// Pi records the resolved resources' metadata before it applies noSkills or noPromptTemplates, so an additional path that the resolution also reached keeps that metadata (resource-loader.ts:417-433 before 468-470 and 483-485).
func TestDefaultLoaderRecordsResolvedMetadataWithoutDiscovery(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeSkill(t, filepath.Join(f.agentDir, "skills"), "user-skill")
	promptPath := filepath.Join(f.agentDir, "prompts", "user-prompt.md")
	writePrompt(t, promptPath, "User prompt")
	loader := loaderFor(t, f, icodingagent.Settings{}, true, func(opts *DefaultResourceLoaderOptions) {
		opts.NoSkills, opts.NoPromptTemplates = true, true
		opts.AdditionalSkillPaths = []string{filepath.Join(f.agentDir, "skills", "user-skill", "SKILL.md")}
		opts.AdditionalPromptTemplatePaths = []string{promptPath}
	})
	want := icodingagent.PiSourceInfo{Source: "auto", Scope: "user", Origin: "top-level", BaseDir: f.agentDir}
	skill := skillNamed(t, loader, "user-skill").SourceInfo
	want.Path = skill.Path
	if skill != want {
		t.Errorf("skill sourceInfo = %+v, want %+v", skill, want)
	}
	prompt := promptNamed(t, loader, "user-prompt")
	want.Path = prompt.FilePath
	if prompt.SourceInfo != want {
		t.Errorf("prompt sourceInfo = %+v, want %+v", prompt.SourceInfo, want)
	}
}

// An explicit settings exclusion of a path that project auto-discovery also reaches disables it: the user's "-<path>" entry is resolved before auto-discovery, and the accumulator keeps that first, disabled entry (package-manager.ts:927-961 resolveLocalEntries before addAutoDiscoveredResources, 2329-2350 disabled entries, 2555-2565 addResource first-wins).
func TestDefaultLoaderExplicitExclusionDisablesAutoDiscoveredResources(t *testing.T) {
	f := newResourceLoaderFixture(t)
	projectDir := icodingagent.ProjectConfigDir(f.cwd)
	writeSkill(t, filepath.Join(projectDir, "skills"), "shared-skill")
	writeSkill(t, filepath.Join(projectDir, "skills"), "other-skill")
	writePrompt(t, filepath.Join(projectDir, "prompts", "shared-prompt.md"), "Shared prompt")
	writePrompt(t, filepath.Join(projectDir, "prompts", "other-prompt.md"), "Other prompt")
	skillPath := filepath.Join(projectDir, "skills", "shared-skill")
	promptPath := filepath.Join(projectDir, "prompts", "shared-prompt.md")
	settings := icodingagent.Settings{
		Skills:  []string{skillPath, "-" + skillPath},
		Prompts: []string{promptPath, "-" + promptPath},
	}
	loader := loaderFor(t, f, settings, true, nil)
	if got := skillNames(loader); !slices.Equal(got, []string{"other-skill"}) {
		t.Errorf("skills = %v, want the excluded skill unloaded", got)
	}
	var prompts []string
	for _, prompt := range loader.GetPrompts().Prompts {
		prompts = append(prompts, prompt.Name)
	}
	if !slices.Equal(prompts, []string{"other-prompt"}) {
		t.Errorf("prompts = %v, want the excluded prompt unloaded", prompts)
	}
}

// Project trust alone gates project resources: Pi has no rule that skips them when the project directory is also the user config root (package-manager.ts:2398-2401, 927-961).
func TestDefaultLoaderLoadsProjectResourcesWhenProjectDirIsTheConfigRoot(t *testing.T) {
	f := newResourceLoaderFixture(t)
	projectDir := icodingagent.ProjectConfigDir(f.cwd)
	t.Setenv("PIG_HOME", projectDir)
	writeSkill(t, filepath.Join(projectDir, "skills"), "project-skill")
	writePrompt(t, filepath.Join(projectDir, "prompts", "project-prompt.md"), "Project prompt")
	loader := loaderFor(t, f, icodingagent.Settings{}, true, nil)
	if got := skillNames(loader); !slices.Equal(got, []string{"project-skill"}) {
		t.Errorf("skills = %v, want the project skill", got)
	}
	if got := promptNamed(t, loader, "project-prompt").SourceInfo; got.Scope != "project" {
		t.Errorf("prompt sourceInfo = %+v, want project scope", got)
	}
}

// mapSkillPath maps only an enabled resource whose source is auto or whose origin is package to its SKILL.md, and records that file's metadata only when it has none (resource-loader.ts:636-656).
func TestMapSkillPathMapsOnlyAutoAndPackageDirectories(t *testing.T) {
	dir := t.TempDir()
	writeResource(t, filepath.Join(dir, "SKILL.md"), "---\nname: s\ndescription: d\n---\n")
	file := filepath.Join(dir, "SKILL.md")
	empty := t.TempDir()
	tests := []struct {
		name     string
		resource icodingagent.ResolvedResource
		want     string
	}{
		{"auto directory", icodingagent.ResolvedResource{Path: dir, Enabled: true, Metadata: icodingagent.PathMetadata{Source: "auto", Scope: "user", Origin: "top-level"}}, file},
		{"package directory", icodingagent.ResolvedResource{Path: dir, Enabled: true, Metadata: icodingagent.PathMetadata{Source: "npm:x", Scope: "user", Origin: "package"}}, file},
		{"local directory", icodingagent.ResolvedResource{Path: dir, Enabled: true, Metadata: icodingagent.PathMetadata{Source: "local", Scope: "user", Origin: "top-level"}}, dir},
		{"auto directory without SKILL.md", icodingagent.ResolvedResource{Path: empty, Enabled: true, Metadata: icodingagent.PathMetadata{Source: "auto"}}, empty},
		{"auto file", icodingagent.ResolvedResource{Path: file, Enabled: true, Metadata: icodingagent.PathMetadata{Source: "auto"}}, file},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			index := &pathMetadataIndex{}
			if got := mapSkillPath(tc.resource, index); got != tc.want {
				t.Errorf("mapSkillPath = %q, want %q", got, tc.want)
			}
			if _, recorded := index.metadata[file]; recorded != (tc.want == file && tc.resource.Path == dir) {
				t.Errorf("SKILL.md metadata recorded = %v for %s", recorded, tc.name)
			}
		})
	}
	t.Run("existing metadata wins", func(t *testing.T) {
		index := &pathMetadataIndex{}
		first := icodingagent.PathMetadata{Source: "local", Scope: "project", Origin: "top-level"}
		index.add(file, first)
		mapSkillPath(icodingagent.ResolvedResource{Path: dir, Enabled: true, Metadata: icodingagent.PathMetadata{Source: "auto"}}, index)
		if index.metadata[file] != first {
			t.Errorf("metadata = %+v, want the first", index.metadata[file])
		}
	})
}

// A malformed file URL is a thrown error in Pi, not an ignored setting. Package and settings-entry resolution call resolvePathFromBase and getPackageIdentity outside any catch (package-manager.ts:1334, 1393, 2340; utils/paths.ts:95-106 fileURLToPath throws URI malformed), and mergePaths calls resolvePath for every constructor path (resource-loader.ts:469-488, 850-861), so Reload rejects.
func TestDefaultLoaderReloadPropagatesMalformedFileURLs(t *testing.T) {
	const bad = "file:///%ZZ"
	for _, setting := range []string{"packages", "skills", "prompts"} {
		for _, scope := range []string{"user", "project"} {
			entry := `"` + bad + `"`
			t.Run(scope+" "+setting, func(t *testing.T) {
				f := newResourceLoaderFixture(t)
				dir := f.agentDir
				if scope == "project" {
					dir = icodingagent.ProjectConfigDir(f.cwd)
				}
				writeResource(t, filepath.Join(dir, "settings.json"), `{"`+setting+`":[`+entry+`]}`)
				settings := icodingagent.NewSettingsManagerWithProjectTrust(f.cwd, f.agentDir, true)
				loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: settings})
				if err := loader.Reload(); err == nil {
					t.Fatalf("Reload accepted %s %s entry %q", scope, setting, bad)
				}
			})
		}
	}
	for name, mutate := range map[string]func(*DefaultResourceLoaderOptions){
		"additional skill path":  func(o *DefaultResourceLoaderOptions) { o.AdditionalSkillPaths = []string{bad} },
		"additional prompt path": func(o *DefaultResourceLoaderOptions) { o.AdditionalPromptTemplatePaths = []string{bad} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newResourceLoaderFixture(t)
			manager := icodingagent.NewInMemorySettingsManager(icodingagent.Settings{})
			manager.SetProjectTrusted(true)
			opts := DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: manager}
			mutate(&opts)
			if err := NewDefaultResourceLoader(opts).Reload(); err == nil {
				t.Fatalf("Reload accepted %s %q", name, bad)
			}
		})
	}
}

// An untrusted Project's settings are not read, so its malformed entries never reach resolution (package-manager.ts settings-manager getProjectSettings under isProjectTrusted).
func TestDefaultLoaderReloadIgnoresUntrustedProjectMalformedFileURLs(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeResource(t, filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "settings.json"), `{"packages":["file:///%ZZ"],"skills":["file:///%ZZ"]}`)
	settings := icodingagent.NewSettingsManagerWithProjectTrust(f.cwd, f.agentDir, false)
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: settings})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
}

// Pi keys every skill resource by its SKILL.md file: a settings entry naming a skill directory and auto-discovery both collect <dir>/SKILL.md (package-manager.ts:365-400 collectSkillEntries, 645-653 collectResourceFiles, 2518-2535 collectFilesFromPaths). A user settings exclusion that names the SKILL.md file therefore reaches the same accumulator entry as the project auto-discovered skill directory and disables it (2555-2565 addResource first-wins).
func TestDefaultLoaderExplicitSkillFileExclusionDisablesAutoDiscoveredSkill(t *testing.T) {
	f := newResourceLoaderFixture(t)
	projectDir := icodingagent.ProjectConfigDir(f.cwd)
	writeSkill(t, filepath.Join(projectDir, "skills"), "shared-skill")
	writeSkill(t, filepath.Join(projectDir, "skills"), "other-skill")
	skillFile := filepath.Join(projectDir, "skills", "shared-skill", "SKILL.md")
	loader := loaderFor(t, f, icodingagent.Settings{Skills: []string{skillFile, "-" + skillFile}}, true, nil)
	if got := skillNames(loader); !slices.Equal(got, []string{"other-skill"}) {
		t.Errorf("skills = %v, want the excluded skill unloaded", got)
	}
}

// A user settings entry naming a project auto-discovered skill's SKILL.md takes that skill's metadata, because Pi's accumulator keys the auto-discovered skill by the same SKILL.md path and settings entries fill it first (package-manager.ts:927-961, 2555-2565; resource-loader.ts:417-433).
func TestDefaultLoaderSkillFileSettingsEntryOwnsAutoDiscoveredSkillMetadata(t *testing.T) {
	f := newResourceLoaderFixture(t)
	projectDir := icodingagent.ProjectConfigDir(f.cwd)
	writeSkill(t, filepath.Join(projectDir, "skills"), "shared-skill")
	skillFile := filepath.Join(projectDir, "skills", "shared-skill", "SKILL.md")
	loader := loaderFor(t, f, icodingagent.Settings{Skills: []string{skillFile}}, true, nil)
	got := skillNamed(t, loader, "shared-skill").SourceInfo
	if got.Source != "local" || got.Scope != "user" {
		t.Errorf("skill sourceInfo = %+v, want the user settings entry's {local,user}", got)
	}
}

// Pi's resolve dedupes the Packages, project first, calling getPackageIdentity on each before it installs any (package-manager.ts:912-928, 1687-1717), then installs and collects them (resolvePackageSources) and only then resolves the plain settings entries (933-959, 2340). A malformed project Package therefore wins over a malformed user one, and a missing npm Package is installed before a malformed settings entry rejects the reload.
func TestDefaultLoaderReloadResolvesSourcesInPiOrder(t *testing.T) {
	t.Run("project Package before user Package", func(t *testing.T) {
		f := newResourceLoaderFixture(t)
		writeResource(t, filepath.Join(f.agentDir, "settings.json"), `{"packages":["file:///%ZZuser"]}`)
		writeResource(t, filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "settings.json"), `{"packages":["file:///%ZZproject"]}`)
		settings := icodingagent.NewSettingsManagerWithProjectTrust(f.cwd, f.agentDir, true)
		err := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: settings}).Reload()
		if err == nil || !strings.Contains(err.Error(), "%ZZproject") {
			t.Fatalf("Reload error = %v, want the project Package's", err)
		}
	})
	t.Run("settings entries by resource type, project first", func(t *testing.T) {
		f := newResourceLoaderFixture(t)
		writeResource(t, filepath.Join(f.agentDir, "settings.json"), `{"skills":["file:///%ZZuserskill"]}`)
		writeResource(t, filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "settings.json"), `{"prompts":["file:///%ZZprojectprompt"],"skills":["file:///%ZZprojectskill"]}`)
		settings := icodingagent.NewSettingsManagerWithProjectTrust(f.cwd, f.agentDir, true)
		err := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: settings}).Reload()
		if err == nil || !strings.Contains(err.Error(), "%ZZprojectskill") {
			t.Fatalf("Reload error = %v, want the project skill entry's", err)
		}
	})
	t.Run("missing npm Package installed before a malformed entry rejects", func(t *testing.T) {
		f := newResourceLoaderFixture(t)
		t.Setenv("PI_OFFLINE", "")
		t.Setenv("PIG_OFFLINE", "")
		command, record := fakeNPM(t, t.TempDir(), false)
		settings, err := json.Marshal(map[string]any{"npmCommand": command, "packages": []string{"npm:installed-pkg"}, "skills": []string{"file:///%ZZ"}})
		if err != nil {
			t.Fatal(err)
		}
		writeResource(t, filepath.Join(f.agentDir, "settings.json"), string(settings))
		loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: icodingagent.NewSettingsManagerWithProjectTrust(f.cwd, f.agentDir, true)})
		if err := loader.Reload(); err == nil || !strings.Contains(err.Error(), "%ZZ") {
			t.Fatalf("Reload error = %v, want the malformed skill entry's", err)
		}
		if data, err := os.ReadFile(record); err != nil || !strings.Contains(string(data), "install") {
			t.Fatalf("npm calls = %q, %v; want the install before the rejection", data, err)
		}
	})
}

// Pi's reload assigns the skills (updateSkillsFromPaths) before mergePaths resolves the prompt template paths, so a malformed additional prompt path rejects the reload after the skills have been replaced; the prompts and context files keep their previous values (resource-loader.ts:468-488).
func TestDefaultLoaderPromptPathRejectionKeepsReloadedSkills(t *testing.T) {
	f := newResourceLoaderFixture(t)
	writeResource(t, filepath.Join(icodingagent.ProjectConfigDir(f.cwd), "skills", "kept", "SKILL.md"), "---\nname: kept\ndescription: d\n---\nBody\n")
	manager := icodingagent.NewInMemorySettingsManager(icodingagent.Settings{})
	manager.SetProjectTrusted(true)
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: f.cwd, AgentDir: f.agentDir, SettingsManager: manager, AdditionalPromptTemplatePaths: []string{"file:///%ZZ"}})
	if err := loader.Reload(); err == nil {
		t.Fatal("Reload accepted the malformed prompt path")
	}
	if got := skillNames(loader); !slices.Equal(got, []string{"kept"}) {
		t.Fatalf("skills after the rejected reload = %v, want [kept]", got)
	}
	if got := loader.GetPrompts().Prompts; len(got) != 0 {
		t.Fatalf("prompts after the rejected reload = %+v, want none", got)
	}
}
