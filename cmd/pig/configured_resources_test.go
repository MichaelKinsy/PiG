package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func BenchmarkResourceLoaderExtensionDiscovery(b *testing.B) {
	for _, count := range []int{0, 32, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			b.Setenv("PIG_HOME", filepath.Join(root, "home"))
			cwd, agentDir, shared := filepath.Join(root, "project"), filepath.Join(root, "agent"), filepath.Join(root, "shared")
			for _, dir := range []string{filepath.Join(cwd, ".pig"), agentDir, shared} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					b.Fatal(err)
				}
			}
			for _, dir := range []string{filepath.Join(cwd, ".pig"), agentDir} {
				testenv.RequireDirectoryLink(b, shared, filepath.Join(dir, "extensions"))
			}
			for i := range count {
				if err := os.WriteFile(filepath.Join(shared, fmt.Sprintf("extension-%03d.ts", i)), []byte("export default function() {}"), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			b.ReportAllocs()
			for b.Loop() {
				configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, nil)
				if len(configs) != count {
					b.Fatalf("loaded %d configs for %d shared extension files", len(configs), count)
				}
			}
		})
	}
}

func writeResourceLoaderFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:183-211. Discovery must retain the first (project) alias and run the shared factory only once.
func TestResourceLoaderUpstreamSymlinkedExtensions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	shared := filepath.Join(root, "shared-extensions")
	writeResourceLoaderFixture(t, filepath.Join(shared, "shared.ts"), `export default function(pi) {
 pi.registerCommand("shared", {description: "shared command", handler: async () => {}});
}`)
	for _, dir := range []string{agentDir, filepath.Join(cwd, ".pig")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		testenv.RequireDirectoryLink(t, shared, filepath.Join(dir, "extensions"))
	}
	configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), CLIFlags{}, nil)
	wantPath := filepath.Join(cwd, ".pig", "extensions", "shared.ts")
	if len(configs) != 1 {
		t.Fatalf("extension configs = %#v, want only project alias %s", configs, wantPath)
	}
	exts, host, _, errs := loadSubprocessExtensions(t.Context(), cwd, extension.ModePrint, nil, configs, nil, nil)
	if host != nil {
		t.Cleanup(func() { host.Shutdown("test complete") })
	}
	if len(errs) != 0 || len(exts) != 1 || exts[0].Path != wantPath {
		t.Fatalf("loaded extensions = %#v, errors = %v", exts, errs)
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:260-324 and :868-978 through the actual Node loader and command/tool runner, in both subprocess realizations.
func TestResourceLoaderUpstreamExtensionConflicts(t *testing.T) {
	for _, isolation := range []string{"isolated", "shared-ok"} {
		for _, kind := range []string{"commands", "tools", "explicit CLI"} {
			t.Run(isolation+"/"+kind, func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("PIG_HOME", filepath.Join(root, "home"))
				cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
				if err := os.MkdirAll(cwd, 0o755); err != nil {
					t.Fatal(err)
				}
				firstPath := filepath.Join(cwd, ".pig", "extensions", "project.ts")
				secondPath := filepath.Join(agentDir, "extensions", "user.ts")
				first, second := "project", "user"
				flags := CLIFlags{}
				switch kind {
				case "tools":
					firstPath = filepath.Join(agentDir, "extensions", "ext1", "index.ts")
					secondPath = filepath.Join(agentDir, "extensions", "ext2", "index.ts")
					first, second = "First", "Second"
				case "explicit CLI":
					firstPath = filepath.Join(root, "explicit-extension.ts")
					secondPath = filepath.Join(agentDir, "extensions", "global.ts")
					first, second = "explicit", "global"
					flags.Extensions = []string{firstPath}
				}
				for i, path := range []string{firstPath, secondPath} {
					label := []string{first, second}[i]
					body := ""
					if kind != "tools" {
						description := label + " deploy"
						if kind == "explicit CLI" {
							description = label + " command"
						}
						body += fmt.Sprintf(`pi.registerCommand("deploy", {description: %q, handler: async () => {}});`, description)
						if kind == "commands" {
							body += fmt.Sprintf(`pi.registerCommand(%q, {description: %q, handler: async () => {}});`, label+"-only", label+" only")
						}
					}
					if kind != "commands" {
						description := label
						if kind == "explicit CLI" {
							description += " tool"
						}
						body += fmt.Sprintf(`pi.registerTool({name: "duplicate-tool", description: %q, parameters: Type.Object({}), execute: async () => ({result: %q})});`, description, label)
					}
					writeResourceLoaderFixture(t, path, `import { Type } from "typebox"; export default function(pi) {`+body+`}`)
				}
				configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), flags, nil)
				for i := range configs {
					configs[i].Isolation = isolation
				}
				exts, host, _, errs := loadSubprocessExtensions(t.Context(), cwd, extension.ModePrint, nil, configs, nil, nil)
				if host != nil {
					t.Cleanup(func() { host.Shutdown("test complete") })
				}
				if len(errs) != 0 || len(exts) != 2 {
					t.Fatalf("extensions=%#v errors=%v", exts, errs)
				}
				if exts[0].Path != firstPath || exts[1].Path != secondPath {
					t.Fatalf("load order = %q, %q; want %q, %q", exts[0].Path, exts[1].Path, firstPath, secondPath)
				}
				conflicts := codingagent.DetectExtensionConflicts(exts)
				runner := inproc.NewRunner(exts, cwd)
				if kind == "commands" {
					if len(conflicts) != 0 {
						t.Fatalf("command collisions are not load errors: %v", conflicts)
					}
					commands := runner.Commands()
					var names []string
					for _, command := range commands {
						names = append(names, command.InvocationName)
					}
					if !slices.Equal(names, []string{"deploy:1", "project-only", "deploy:2", "user-only"}) {
						t.Fatalf("commands = %v", names)
					}
					for name, description := range map[string]string{"deploy:1": "project deploy", "deploy:2": "user deploy", "project-only": "project only", "user-only": "user only"} {
						if command, ok := runner.Command(name); !ok || command.Description != description {
							t.Fatalf("command %s = %#v, found=%v", name, command, ok)
						}
					}
				} else {
					if len(conflicts) != 1 || !strings.Contains(conflicts[0].Message, `Tool "duplicate-tool" conflicts`) {
						t.Fatalf("tool conflicts = %v", conflicts)
					}
					if kind == "explicit CLI" {
						tool, ok := runner.GetToolDefinition("duplicate-tool")
						if !ok || tool.Description != "explicit tool" {
							t.Fatalf("winning tool = %#v, found=%v", tool, ok)
						}
						for name, description := range map[string]string{"deploy:1": "explicit command", "deploy:2": "global command"} {
							if command, ok := runner.Command(name); !ok || command.Description != description {
								t.Fatalf("command %s = %#v, found=%v", name, command, ok)
							}
						}
					}
				}
			})
		}
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:43-121, :326-368 and :786-828 through the same collectors and loaders used at startup and by /reload.
func TestResourceLoaderUpstreamDiscovery(t *testing.T) {
	for _, kind := range []string{"skill", "skill siblings", "prompt", "invalid prompt", "disabled", "no skills", "additional skill"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PIG_HOME", filepath.Join(root, "home"))
			t.Setenv("HOME", filepath.Join(root, "home"))
			cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
			for _, dir := range []string{cwd, agentDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			flags := CLIFlags{}
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			skillName, promptName := "", ""
			switch kind {
			case "skill", "no skills":
				body := "Skill content here."
				if kind == "no skills" {
					body = "Content"
				}
				writeResourceLoaderFixture(t, filepath.Join(agentDir, "skills", "test-skill.md"), "---\nname: test-skill\ndescription: A test skill\n---\n"+body)
				skillName = "test-skill"
				if kind == "no skills" {
					flags.NoSkills, skillName = true, ""
				}
			case "additional skill":
				dir := filepath.Join(root, "custom-skills")
				writeResourceLoaderFixture(t, filepath.Join(dir, "custom.md"), "---\nname: custom\ndescription: Custom skill\n---\nContent")
				flags.NoSkills, flags.Skills, skillName = true, []string{dir}, "custom"
			case "skill siblings":
				dir := filepath.Join(agentDir, "skills", "pi-skills", "browser-tools")
				writeResourceLoaderFixture(t, filepath.Join(dir, "SKILL.md"), "---\nname: browser-tools\ndescription: Browser tools\n---\nSkill content here.")
				writeResourceLoaderFixture(t, filepath.Join(dir, "EFFICIENCY.md"), "No frontmatter here")
				skillName = "browser-tools"
			case "prompt":
				writeResourceLoaderFixture(t, filepath.Join(agentDir, "prompts", "test-prompt.md"), "---\ndescription: A test prompt\n---\nPrompt content.")
				promptName = "test-prompt"
			case "invalid prompt":
				writeResourceLoaderFixture(t, filepath.Join(agentDir, "prompts", "invalid.md"), "---\ndescription: Broken: unquoted colon\n---\nDo something.\n")
				writeResourceLoaderFixture(t, filepath.Join(agentDir, "prompts", "valid.md"), "Valid prompt content.")
				promptName = "valid"
			case "disabled":
				writeResourceLoaderFixture(t, filepath.Join(agentDir, "extensions", "disabled.ts"), "export default function() {}")
				writeResourceLoaderFixture(t, filepath.Join(agentDir, "skills", "skip-skill", "SKILL.md"), "---\nname: skip-skill\ndescription: Skip me\n---\nContent")
				writeResourceLoaderFixture(t, filepath.Join(agentDir, "prompts", "skip.md"), "Skip prompt")
				writeResourceLoaderFixture(t, filepath.Join(agentDir, "themes", "skip.json"), "{}")
				if err := sm.UpdateGlobal(func(settings *codingagent.Settings) {
					settings.Extensions = []string{"-extensions/disabled.ts"}
					settings.Skills = []string{"-skills/skip-skill"}
					settings.Prompts = []string{"-prompts/skip.md"}
					settings.Themes = []string{"-themes/skip.json"}
				}); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := reloadResourceSnapshotProvider(cwd, agentDir, sm, flags, nil)()
			skills, err := resolveAndLoadSkills(nil, snapshot.SkillPaths)
			if err != nil {
				t.Fatal(err)
			}
			if skillName == "" {
				if len(skills.Defs) != 0 {
					t.Fatalf("skills = %#v", skills.Defs)
				}
			} else if len(skills.Defs) != 1 || skills.Defs[0].Name != skillName || len(skills.Diagnostics) != 0 {
				t.Fatalf("skills = %#v, want %s", skills, skillName)
			}
			loaded := codingagent.LoadPromptTemplates("", "", snapshot.PromptPaths...)
			if promptName == "" {
				if len(loaded.Templates) != 0 {
					t.Fatalf("prompts = %#v", loaded)
				}
			} else if len(loaded.Templates) != 1 || loaded.Templates[0].Name != promptName {
				t.Fatalf("prompts = %#v, want %s", loaded, promptName)
			}
			if kind == "invalid prompt" {
				path := filepath.Join(agentDir, "prompts", "invalid.md")
				if len(loaded.Diagnostics) != 1 || loaded.Diagnostics[0].Type != "warning" || loaded.Diagnostics[0].Path != path || !strings.Contains(loaded.Diagnostics[0].Message, "line 1, column 14") {
					t.Fatalf("prompt diagnostics = %#v", loaded.Diagnostics)
				}
			} else if len(loaded.Diagnostics) != 0 {
				t.Fatalf("unexpected prompt diagnostics = %#v", loaded.Diagnostics)
			}
			if kind == "disabled" {
				configs := collectExtensionConfigs(cwd, agentDir, sm, flags, nil)
				if len(configs) != 0 || len(snapshot.ThemePaths) != 0 {
					t.Fatalf("disabled extensions=%#v themes=%v", configs, snapshot.ThemePaths)
				}
			}
		})
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:213-258. A file-side evaluation count replaces process-global state across subprocesses; the same loaded instance must survive the trust transition.
func TestResourceLoaderUpstreamPreTrustExtensions(t *testing.T) {
	for _, isolation := range []string{"isolated", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PIG_HOME", filepath.Join(root, "home"))
			cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
			userPath := filepath.Join(agentDir, "extensions", "user.ts")
			projectPath := filepath.Join(cwd, ".pig", "extensions", "project.ts")
			countPath := filepath.Join(root, "load-count")
			writeResourceLoaderFixture(t, userPath, fmt.Sprintf(`import { appendFileSync } from "node:fs";
appendFileSync(%q, "loaded\n");
export default function(pi) {
 pi.on("project_trust", () => ({trusted: "yes"}));
 pi.registerCommand("user-trust", {description: "user trust", handler: async () => {}});
}`, countPath))
			writeResourceLoaderFixture(t, projectPath, `export default function(pi) { pi.registerCommand("project-trusted", {description: "project trusted", handler: async () => {}}); }`)
			sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
			userScopes := []string{"user"}
			configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, &userScopes)
			for i := range configs {
				configs[i].Isolation = isolation
			}
			preloaded := &startupExtensionSet{}
			t.Cleanup(preloaded.close)
			exts, _, _, errs := loadFinalSubprocessExtensions(t.Context(), cwd, extension.ModePrint, nil, configs, nil, nil, preloaded)
			if len(errs) != 0 || len(exts) != 1 || exts[0].Path != userPath {
				t.Fatalf("pre-trust extensions=%#v errors=%v", exts, errs)
			}
			runner := inproc.NewRunner(exts, cwd)
			trusted, err := resolveProjectTrusted(t.Context(), projectTrustResolutionOptions{CWD: cwd, Runner: runner, Store: codingagent.NewProjectTrustStore(filepath.Join(root, "trust")), Default: "ask"})
			if err != nil || !trusted {
				t.Fatalf("trust = %v, error = %v", trusted, err)
			}
			sm.SetProjectTrusted(trusted)
			configs = collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, nil)
			for i := range configs {
				configs[i].Isolation = isolation
			}
			exts, _, _, errs = loadFinalSubprocessExtensions(t.Context(), cwd, extension.ModePrint, nil, configs, nil, nil, preloaded)
			if len(errs) != 0 || len(exts) != 2 || exts[0].Path != projectPath || exts[1].Path != userPath {
				t.Fatalf("final extensions=%#v errors=%v", exts, errs)
			}
			count, err := os.ReadFile(countPath)
			if err != nil || string(count) != "loaded\n" {
				t.Fatalf("module evaluations = %q, error = %v", count, err)
			}
		})
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:440-484. Context text remains visible when executable/configured project resources are not trusted.
func TestResourceLoaderUpstreamUntrustedProject(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for path, content := range map[string]string{
		filepath.Join(cwd, ".pig", "SYSTEM.md"):                           "Project system prompt.",
		filepath.Join(agentDir, "SYSTEM.md"):                              "Global system prompt.",
		filepath.Join(agentDir, "AGENTS.md"):                              "Global instructions",
		filepath.Join(cwd, "AGENTS.md"):                                   "Project instructions",
		filepath.Join(cwd, ".pig", "extensions", "project.ts"):            `throw new Error("should not load");`,
		filepath.Join(cwd, ".pig", "skills", "project-skill", "SKILL.md"): "---\nname: project-skill\ndescription: Project skill\n---\nProject skill content",
		filepath.Join(cwd, ".pig", "prompts", "project.md"):               "Project prompt",
		filepath.Join(cwd, ".pig", "themes", "project.json"):              resourceLoaderTheme(t, "project-theme"),
	} {
		writeResourceLoaderFixture(t, path, content)
	}
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	userScopes := []string{"user"}
	snapshot := reloadResourceSnapshotProvider(cwd, agentDir, sm, CLIFlags{}, &userScopes)()
	configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, &userScopes)
	if len(configs) != 0 || len(snapshot.SkillPaths) != 0 || len(snapshot.PromptPaths) != 0 || len(snapshot.ThemePaths) != 0 {
		t.Fatalf("untrusted resources: extensions=%#v snapshot=%#v", configs, snapshot)
	}
	if got := resolvePromptInputs(cwd, agentDir, CLIFlags{}, false).custom; got != "Global system prompt." {
		t.Fatalf("system prompt = %q", got)
	}
	if len(snapshot.ContextFiles) != 2 || snapshot.ContextFiles[0].Path != filepath.Join(agentDir, "AGENTS.md") || snapshot.ContextFiles[1].Path != filepath.Join(cwd, "AGENTS.md") {
		t.Fatalf("context files = %#v", snapshot.ContextFiles)
	}
}

func resourceLoaderTheme(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../tui/theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(string(data), `"name": "dark"`, `"name": "`+name+`"`, 1)
}

// Ports packages/coding-agent/test/resource-loader.test.ts:123-181. Project resources win collisions independently for prompts, skills, and the ordered theme inputs consumed by loadThemePaths.
func TestResourceLoaderUpstreamProjectPrecedence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	projectRoot := filepath.Join(cwd, ".pig")
	for _, dir := range []string{agentDir, projectRoot} {
		label := "User"
		if dir == projectRoot {
			label = "Project"
		}
		writeResourceLoaderFixture(t, filepath.Join(dir, "prompts", "commit.md"), label+" prompt")
		writeResourceLoaderFixture(t, filepath.Join(dir, "skills", "collision-skill", "SKILL.md"), "---\nname: collision-skill\ndescription: "+strings.ToLower(label)+"\n---\n"+label+" skill")
		theme := resourceLoaderTheme(t, "collision-theme")
		if dir == projectRoot {
			theme = strings.Replace(theme, `"accent": "#8abeb7"`, `"accent": "#ff00ff"`, 1)
		}
		writeResourceLoaderFixture(t, filepath.Join(dir, "themes", "collision.json"), theme)
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	snapshot := reloadResourceSnapshotProvider(cwd, agentDir, sm, CLIFlags{}, nil)()
	prompts := codingagent.LoadPromptTemplates("", "", snapshot.PromptPaths...)
	if len(prompts.Templates) != 1 || prompts.Templates[0].FilePath != filepath.Join(projectRoot, "prompts", "commit.md") {
		t.Fatalf("winning prompt = %#v", prompts)
	}
	skills, err := resolveAndLoadSkills(nil, snapshot.SkillPaths)
	if err != nil || len(skills.Defs) != 1 || skills.Defs[0].Path != filepath.Join(projectRoot, "skills", "collision-skill", "SKILL.md") {
		t.Fatalf("winning skill = %#v, error = %v", skills, err)
	}
	wantThemePaths := []string{filepath.Join(projectRoot, "themes", "collision.json"), filepath.Join(agentDir, "themes", "collision.json")}
	if !slices.Equal(snapshot.ThemePaths, wantThemePaths) {
		t.Fatalf("theme precedence = %v, want %v", snapshot.ThemePaths, wantThemePaths)
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:34-41 and :831-865 through the production Node SDK adapter. DefaultResourceLoader's constructor/override callbacks are Node SDK contracts; the CLI's shared Go collectors are exercised separately above.
func TestResourceLoaderUpstreamSDKOptions(t *testing.T) {
	for _, isolation := range []string{"isolated", "shared-ok"} {
		for _, tc := range []struct{ name, body, want string }{
			{"initial results", `const loader = new DefaultResourceLoader({cwd, agentDir});
result = {extensions: loader.getExtensions().extensions, skills: loader.getSkills().skills, prompts: loader.getPrompts().prompts, themes: loader.getThemes().themes};`, `{"extensions":[],"skills":[],"prompts":[],"themes":[]}`},
			{"skillsOverride", `const injectedSkill = {name: "injected", description: "Injected skill", filePath: "/fake/path", baseDir: "/fake", sourceInfo: {path: "/fake/path", source: "custom", scope: "temporary", origin: "top-level"}, disableModelInvocation: false};
const loader = new DefaultResourceLoader({cwd, agentDir, skillsOverride: () => ({skills: [injectedSkill], diagnostics: []})});
await loader.reload(); result = loader.getSkills().skills.map(skill => skill.name);`, `["injected"]`},
			{"systemPromptOverride", `const loader = new DefaultResourceLoader({cwd, agentDir, systemPromptOverride: () => "Custom system prompt"});
await loader.reload(); result = loader.getSystemPrompt();`, `"Custom system prompt"`},
		} {
			t.Run(isolation+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("PIG_HOME", filepath.Join(root, "home"))
				cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
				for _, dir := range []string{cwd, agentDir} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				path := filepath.Join(root, "sdk-options.ts")
				writeResourceLoaderFixture(t, path, fmt.Sprintf(`import { DefaultResourceLoader } from "@earendil-works/pi-coding-agent";
export default async function(pi) {
 const cwd = %q, agentDir = %q;
 let result;
 %s
 pi.registerCommand("inspect", {description: JSON.stringify(result), handler: async () => {}});
}`, cwd, agentDir, tc.body))
				configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), CLIFlags{Extensions: []string{path}}, nil)
				for i := range configs {
					configs[i].Isolation = isolation
				}
				exts, host, _, errs := loadSubprocessExtensions(t.Context(), cwd, extension.ModePrint, nil, configs, nil, nil)
				if host != nil {
					t.Cleanup(func() { host.Shutdown("test complete") })
				}
				if len(errs) != 0 || len(exts) != 1 {
					t.Fatalf("SDK extension load: extensions=%#v errors=%v", exts, errs)
				}
				command, ok := inproc.NewRunner(exts, cwd).Command("inspect")
				if !ok || command.Description != tc.want {
					t.Fatalf("SDK result = %q (found=%v), want %q", command.Description, ok, tc.want)
				}
			})
		}
	}
}

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
	// Pi package-manager.ts:collectAutoResources applies extension overrides relative to the config root, not its extensions directory.
	for _, tc := range []struct {
		name     string
		patterns []string
		drop     bool
	}{
		{"config-root paths", []string{"!extensions/drop/index.ts", "+extensions/keep/index.ts"}, false},
		{"extension-relative paths do not match", []string{"!drop/index.ts", "+keep/index.ts"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			if err := sm.UpdateGlobal(func(s *codingagent.Settings) { s.Extensions = tc.patterns }); err != nil {
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
			if slices.Contains(sources, dropDir) != tc.drop {
				t.Fatalf("drop selected = %t, want %t; sources: %v", slices.Contains(sources, dropDir), tc.drop, sources)
			}
		})
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

// Project trust alone gates <cwd>/.pig resources, so a cwd whose config directory is the user config root loads its skills as Project skills as Pi does for <home>/.pi (package-manager.ts:2398-2401, 2417, 2453).
func TestHomeDirectoryProjectConfigDirIsNotSkippedAsUserConfigRoot(t *testing.T) {
	home := t.TempDir()
	configRoot := filepath.Join(home, ".pig")
	agentDir := filepath.Join(configRoot, "agent")
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", configRoot)
	skill := filepath.Join(configRoot, "skills", "duplicate")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: duplicate\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trusted := codingagent.NewSettingsManager(home, agentDir)
	got := collectSkillInputs(home, agentDir, trusted, CLIFlags{}, nil)
	if len(got) != 1 || !samePath(got[0], skill) {
		t.Fatalf("trusted skills = %v, want only %s", got, skill)
	}
	items, err := collectConfigResourceItems(home, agentDir, trusted)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		found = found || item.Scope == "project" && samePath(item.Path, skill)
	}
	if !found {
		t.Fatalf("trusted config items = %+v, want the project skill %s", items, skill)
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

// resource-loader.ts:468-470,850-861 appends --skill paths after resolved
// project/user/Package resources. skills.ts:425-454 keeps the first name.
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
	want := []string{projectPigSkill, projectAgentsSkill, userPigSkill, userAgentsSkill, projectPackageSkill, userPackageSkill, cliSkill}
	if !slices.Equal(inputs, want) {
		t.Fatalf("skill precedence order =\n%v\nwant high-to-low =\n%v", inputs, want)
	}
	loaded, _, err := loadSkills(inputs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Description != "project pig" {
		t.Fatalf("same-name winner = %+v, want project skill", loaded)
	}
	if got := collectSkillInputs(cwd, agentDir, sm, CLIFlags{Skills: []string{cliSkill}, NoSkills: true}, nil); !slices.Equal(got, []string{cliSkill}) {
		t.Fatalf("--no-skills suppressed the explicit skill: %v", got)
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

// Upstream orders theme paths by resourcePrecedenceRank after the --theme
// paths: project settings entries, project auto-discovery, user settings
// entries, then user auto-discovery.
func TestCollectThemePathsFollowsUpstreamPrecedence(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	projectDir := filepath.Join(cwd, codingagent.CONFIG_DIR_NAME)
	cli := filepath.Join(cwd, "cli.json")
	projectEntry := filepath.Join(projectDir, "extra", "project-entry.json")
	projectAuto := filepath.Join(projectDir, "themes", "project-auto.json")
	userEntry := filepath.Join(agentDir, "extra", "user-entry.json")
	userAuto := filepath.Join(agentDir, "themes", "user-auto.json")
	for _, path := range []string{cli, projectEntry, projectAuto, userEntry, userAuto} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"name":"fixture","colors":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	if err := sm.SetThemePaths([]string{"extra/user-entry.json"}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetProjectThemePaths([]string{"extra/project-entry.json"}); err != nil {
		t.Fatal(err)
	}
	got := collectThemePaths(cwd, agentDir, sm, CLIFlags{Themes: []string{cli}}, true)
	want := []string{cli, projectEntry, projectAuto, userEntry, userAuto}
	if !slices.Equal(got, want) {
		t.Fatalf("collectThemePaths = %v\nwant %v", got, want)
	}
}

// A user settings entry that names a project auto-discovered skill and excludes it wins, because the entries fill Pi's first-wins accumulator before auto-discovery and key every skill by its SKILL.md (package-manager.ts:365-400, 927-961, 2555-2565). The CLI skill list follows the same result as the default loader.
func TestCollectSkillInputsExplicitExclusionDisablesAutoDiscoveredSkill(t *testing.T) {
	for name, entry := range map[string]func(dir string) string{
		"directory": func(dir string) string { return dir },
		"file":      func(dir string) string { return filepath.Join(dir, "SKILL.md") },
	} {
		t.Run(name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), filepath.Join(t.TempDir(), "agent")
			for _, skill := range []string{"shared-skill", "other-skill"} {
				dir := filepath.Join(cwd, ".pig", "skills", skill)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+skill+"\ndescription: d\n---\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			shared := entry(filepath.Join(cwd, ".pig", "skills", "shared-skill"))
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			settings, err := json.Marshal(map[string]any{"skills": []string{shared, "-" + shared}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settings, 0o644); err != nil {
				t.Fatal(err)
			}
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			for _, path := range collectSkillInputs(cwd, agentDir, sm, CLIFlags{}, nil) {
				if strings.Contains(path, "shared-skill") {
					t.Fatalf("excluded skill listed: %s", path)
				}
			}
		})
	}
}
