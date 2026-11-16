//go:build !pig_strip_node_extensions

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

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
	configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), Args{}, nil)
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
				flags := Args{}
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
			configs := collectExtensionConfigs(cwd, agentDir, sm, Args{}, &userScopes)
			for i := range configs {
				configs[i].Isolation = isolation
			}
			preloaded := &startupExtensionSet{}
			t.Cleanup(preloaded.close)
			exts, _, _, errs := loadFinalSubprocessExtensions(t.Context(), cwd, extension.ModePrint, nil, configs, nil, nil, preloaded, nil)
			if len(errs) != 0 || len(exts) != 1 || exts[0].Path != userPath {
				t.Fatalf("pre-trust extensions=%#v errors=%v", exts, errs)
			}
			runner := inproc.NewRunner(exts, cwd)
			trusted, err := resolveProjectTrusted(t.Context(), projectTrustResolutionOptions{CWD: cwd, Runner: runner, Store: codingagent.NewProjectTrustStore(filepath.Join(root, "trust")), Default: "ask"})
			if err != nil || !trusted {
				t.Fatalf("trust = %v, error = %v", trusted, err)
			}
			sm.SetProjectTrusted(trusted)
			configs = collectExtensionConfigs(cwd, agentDir, sm, Args{}, nil)
			for i := range configs {
				configs[i].Isolation = isolation
			}
			exts, _, _, errs = loadFinalSubprocessExtensions(t.Context(), cwd, extension.ModePrint, nil, configs, nil, nil, preloaded, nil)
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
				configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), Args{Extensions: []string{path}}, nil)
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
