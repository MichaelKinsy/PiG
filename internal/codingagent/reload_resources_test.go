package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

type namedTool struct{ name string }

func (t namedTool) Name() string          { return t.name }
func (t namedTool) Label() string         { return t.name }
func (t namedTool) Schema() ai.ToolSchema { return ai.ToolSchema{Name: t.name, Description: t.name} }
func (t namedTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{}, nil
}
func (t namedTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }

func TestWireInprocContextActionsBindsInteractiveUI(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	m := &InteractiveMode{
		newRunner: runner,
		tuiInst:   tui.NewWithOutput(io.Discard, 80, 24),
		layout:    tui.NewContainer(),
	}
	m.wireInprocContextActions()
	if !runner.HasUI() {
		t.Fatal("in-process runner should have interactive UI after wiring")
	}
}

func TestReplaceExtensionRunner_PreservesBuiltinAndUI(t *testing.T) {
	m := &InteractiveMode{
		tuiInst: tui.NewWithOutput(io.Discard, 80, 24),
		layout:  tui.NewContainer(),
		opts: InteractiveOptions{
			CWD: t.TempDir(),
			BuiltinExtensions: []extension.Extension{{
				Name:     "piglet",
				Commands: map[string]extension.RegisteredCommand{"piglets": {Name: "piglets", Description: "Show piglets"}},
			}},
		},
	}
	errs := m.replaceExtensionRunner([]extension.Extension{{
		Name:     "context-info",
		Commands: map[string]extension.RegisteredCommand{"context": {Name: "context", Description: "Show context"}},
	}})
	if len(errs) != 0 {
		t.Fatalf("replaceExtensionRunner errors = %v", errs)
	}
	if !m.newRunner.HasUI() {
		t.Fatal("replaced runner should keep interactive UI binding")
	}
	commands := m.newRunner.Commands()
	seen := map[string]bool{}
	for _, cmd := range commands {
		seen[cmd.InvocationName] = true
	}
	for _, want := range []string{"piglets", "context"} {
		if !seen[want] {
			t.Fatalf("reloaded commands missing %q: %#v", want, commands)
		}
	}
}

func TestInteractiveModeReplaceExtensionRunner_RefreshesAgentTools(t *testing.T) {
	m := &InteractiveMode{
		opts: InteractiveOptions{
			CWD:      t.TempDir(),
			Settings: Settings{},
			BridgeExtensionTools: func(rts []extension.RegisteredTool) ([]agent.AgentTool, []error) {
				if len(rts) != 1 || rts[0].Definition.Name != "extra" {
					t.Fatalf("registered tools = %#v, want [extra]", rts)
				}
				return []agent.AgentTool{namedTool{name: "extra"}}, nil
			},
		},
		agent: agent.NewAgent(agent.AgentOptions{Tools: []agent.AgentTool{namedTool{name: "old"}}}),
	}
	oldRunner := m.newRunner
	exts := []extension.Extension{{
		Tools: map[string]extension.RegisteredTool{
			"extra": {Definition: extension.ToolDefinition{Name: "extra", Description: "extra tool"}},
		},
	}}

	errs := m.replaceExtensionRunner(exts)
	if len(errs) != 0 {
		t.Fatalf("replaceExtensionRunner errors = %v", errs)
	}
	if m.newRunner == nil || m.newRunner.ExtensionCount() != 1 {
		t.Fatalf("newRunner = %#v", m.newRunner)
	}
	if oldRunner != nil && !oldRunner.IsStale() {
		t.Fatal("old runner should be invalidated on replacement")
	}
	got := toolNames(m.agent.Tools())
	if !contains(got, "extra") {
		t.Fatalf("agent tools missing reloaded extension tool: %v", got)
	}
	if contains(got, "old") {
		t.Fatalf("agent tools still contain stale tool: %v", got)
	}
}

func TestReloadRefreshesExtensionShortcuts(t *testing.T) {
	oldCalls := make(chan struct{}, 2)
	newCalls := make(chan struct{}, 2)
	shortcut := func(name, key string, calls chan<- struct{}) extension.Extension {
		return extension.Extension{
			Name: name,
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{
				extension.KeyID(key): {
					Shortcut: extension.KeyID(key),
					Handler: func(context.Context) error {
						calls <- struct{}{}
						return nil
					},
				},
			},
		}
	}

	m := &InteractiveMode{
		runCtx: context.Background(),
		opts:   InteractiveOptions{CWD: t.TempDir()},
	}
	m.newRunner = inproc.NewRunner([]extension.Extension{shortcut("old", "ctrl+shift+left", oldCalls)}, m.opts.CWD)
	m.setupExtensionShortcutListener(m.runCtx)
	if !m.notifyTerminalInput("\x1b[1;6D") {
		t.Fatal("initial shortcut was not consumed")
	}
	waitShortcutCall(t, oldCalls, "initial old shortcut")

	if errs := m.replaceExtensionRunner([]extension.Extension{shortcut("new", "ctrl+shift+right", newCalls)}); len(errs) != 0 {
		t.Fatalf("replaceExtensionRunner errors = %v", errs)
	}
	if m.notifyTerminalInput("\x1b[1;6D") {
		t.Fatal("removed shortcut still consumed input after reload")
	}
	if !m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("replacement shortcut was not consumed")
	}
	waitShortcutCall(t, newCalls, "replacement shortcut")
	select {
	case <-oldCalls:
		t.Fatal("stale shortcut handler ran after reload")
	default:
	}

	if errs := m.replaceExtensionRunner([]extension.Extension{shortcut("newer", "ctrl+shift+right", newCalls)}); len(errs) != 0 {
		t.Fatalf("second replaceExtensionRunner errors = %v", errs)
	}
	if got := len(m.terminalInputListeners); got != 0 {
		t.Fatalf("generic terminal listener count = %d after repeated reload, want 0", got)
	}
	if m.extensionShortcutListener == nil {
		t.Fatal("extension shortcut listener missing after repeated reload")
	}
}

func TestTerminalInputListenerUnsubscribeRemovesHandler(t *testing.T) {
	m := &InteractiveMode{}
	calls := 0
	unsubscribe := m.addTerminalInputListener(func(string) bool {
		calls++
		return true
	})
	unsubscribe()
	if m.notifyTerminalInput("x") {
		t.Fatal("unsubscribed terminal listener consumed input")
	}
	if calls != 0 {
		t.Fatalf("unsubscribed terminal listener ran %d times", calls)
	}
	if len(m.terminalInputListeners) != 0 {
		t.Fatalf("unsubscribed terminal listener remains registered: %d", len(m.terminalInputListeners))
	}
}

func waitShortcutCall(t *testing.T, calls <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func TestReloadRefreshesRealSubprocessShortcuts(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the subprocess shortcut fixture")
	}
	root := t.TempDir()
	output := filepath.Join(root, "calls.txt")
	t.Setenv("PIG_SHORTCUT_TEST_OUTPUT", output)
	t.Setenv("PIG_HOME", filepath.Join(root, "pig-home"))
	source := filepath.Join(root, "extension")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	// A .ts entry, because Pi 0.87.1's /reload re-evaluates only what jiti (moduleCache: false) transpiles: a native ESM .js or .mjs stays in Node's module cache, so an edit to it, even a syntax error, is not seen until the process restarts (loader.ts loadExtensionModule, resource-loader.ts reload).
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"type":"module","main":"main.ts"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeNodeShortcutExtension(t, source, "old")

	host := subprocess.NewHost(root)
	host.SetMode("tui")
	config := subprocess.ExtConfig{Name: "reload-shortcut", Source: source, Enabled: true}
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return []subprocess.ExtConfig{config}, nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ext, err := host.Load(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Shutdown("test complete")

	m := &InteractiveMode{runCtx: ctx, opts: InteractiveOptions{CWD: root, SubprocessHost: host}}
	m.newRunner = inproc.NewRunner([]extension.Extension{*ext}, root)
	m.setupExtensionShortcutListener(ctx)
	if !m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("initial subprocess shortcut was not consumed")
	}
	waitFileContains(t, output, "old")

	writeNodeShortcutExtension(t, source, "new")
	reloaded, err := host.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if errs := m.replaceExtensionRunner(reloaded); len(errs) != 0 {
		t.Fatalf("replace after subprocess reload = %v", errs)
	}
	if !m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("replacement subprocess shortcut was not consumed")
	}
	waitFileContains(t, output, "old\nnew")

	if err := os.WriteFile(filepath.Join(source, "main.ts"), []byte("export default function register(pi) { pi.registerShortcut(\"ctrl+shift+right\", { handler: () => {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Like upstream reload, an extension whose replacement fails to load is
	// not kept: its shortcut goes away and the failure is reported.
	reloaded, err = host.Reload(ctx)
	if err != nil {
		t.Fatalf("reload failed as a whole on one failing extension: %v", err)
	}
	if report := host.LastReloadReport(); len(report.Issues) != 1 || !strings.Contains(report.Issues[0], "Failed to load extension: ") {
		t.Fatalf("reload issues = %q, want the failing extension", report.Issues)
	}
	if errs := m.replaceExtensionRunner(reloaded); len(errs) != 0 {
		t.Fatalf("replace after failed extension = %v", errs)
	}
	if m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("failed extension's previous shortcut is still consumed")
	}
	writeNodeShortcutExtension(t, source, "fixed")
	reloaded, err = host.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if errs := m.replaceExtensionRunner(reloaded); len(errs) != 0 {
		t.Fatalf("replace after repaired extension = %v", errs)
	}
	if !m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("repaired extension shortcut was not consumed")
	}
	waitFileContains(t, output, "old\nnew\nfixed")

	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return nil, nil })
	reloaded, err = host.Reload(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if errs := m.replaceExtensionRunner(reloaded); len(errs) != 0 {
		t.Fatalf("replace removed subprocess set = %v", errs)
	}
	if m.notifyTerminalInput("\x1b[1;6C") {
		t.Fatal("removed subprocess shortcut still consumed input")
	}
}

func writeNodeShortcutExtension(t *testing.T, source, marker string) {
	t.Helper()
	content := `import fs from "node:fs";
export default function register(pi) {
  pi.registerShortcut("ctrl+shift+right", {
    description: "reload probe",
    handler: () => fs.appendFileSync(process.env.PIG_SHORTCUT_TEST_OUTPUT, ` + fmt.Sprintf("%q", marker+"\n") + `),
  });
}
`
	if err := os.WriteFile(filepath.Join(source, "main.ts"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitFileContains(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("%s = %q, want to contain %q", path, data, want)
}

func TestReloadSkillsUsesStartupValidationAndKeepsValidSiblings(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	root := t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(root, "bad", "SKILL.md"):    "---\ndescription: [bad\n---\nbody",
		filepath.Join(root, "nodesc", "SKILL.md"): "---\nname: nodesc\n---\nbody",
		filepath.Join(root, "good", "SKILL.md"):   "---\nname: good\ndescription: good\n---\nbody",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.opts.SkillPaths = []string{root}
	m.reloadSkillsFromPaths()
	if len(m.opts.Skills) != 1 || m.opts.Skills[0].Name != "good" {
		t.Fatalf("reloaded skills = %#v", m.opts.Skills)
	}
}

func TestInteractiveModeExtendResourcesFromExtensions_MergesDiscoveredPaths(t *testing.T) {
	cwd := t.TempDir()
	promptPath := filepath.Join(cwd, "dynamic.md")
	skillDir := filepath.Join(cwd, "dynamic-skill")
	themePath := filepath.Join(cwd, "dynamic.json")
	for path, body := range map[string]string{
		promptPath:                          "---\ndescription: dynamic\n---\nbody",
		filepath.Join(skillDir, "SKILL.md"): "---\nname: dynamic-skill\ndescription: dynamic skill\n---\n# dynamic skill",
		themePath:                           `{}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var rebuilt bool
	m := &InteractiveMode{
		opts: InteractiveOptions{
			CWD:      cwd,
			AgentDir: t.TempDir(),
			RebuildSystemPrompt: func(skills []*SkillDef, contextFiles []ContextFile) (string, extension.BuildSystemPromptOptions) {
				rebuilt = true
				return "rebuilt prompt", extension.BuildSystemPromptOptions{Cwd: cwd}
			},
		},
		agent: agent.NewAgent(agent.AgentOptions{}),
		newRunner: inproc.NewRunner([]extension.Extension{{
			Path: "/tmp/dynamic-ext.ts",
			Handlers: map[string][]extension.HandlerFn{
				EventResourcesDiscover: {
					func(args ...any) (any, error) {
						return &extension.ResourcesDiscoverResult{
							PromptPaths: []string{promptPath},
							SkillPaths:  []string{skillDir},
							ThemePaths:  []string{themePath},
						}, nil
					},
				},
			},
		}}, cwd),
	}

	if err := m.extendResourcesFromExtensions(t.Context(), "startup"); err != nil {
		t.Fatal(err)
	}
	if !contains(m.opts.PromptPaths, promptPath) {
		t.Fatalf("PromptPaths = %v, want %s", m.opts.PromptPaths, promptPath)
	}
	if !contains(m.opts.SkillPaths, skillDir) {
		t.Fatalf("SkillPaths = %v, want %s", m.opts.SkillPaths, skillDir)
	}
	if !contains(m.opts.ThemePaths, themePath) {
		t.Fatalf("ThemePaths = %v, want %s", m.opts.ThemePaths, themePath)
	}
	if len(m.promptTemplates) != 1 || m.promptTemplates[0].Name == "" {
		t.Fatalf("promptTemplates = %#v", m.promptTemplates)
	}
	if len(m.opts.Skills) != 1 {
		t.Fatalf("Skills = %#v", m.opts.Skills)
	}
	info, ok := m.resourceSourceInfo[promptPath]
	if !ok || info.Source != "extension:dynamic-ext" || info.Scope != "temporary" {
		t.Fatalf("resourceSourceInfo[%s] = %#v", promptPath, info)
	}
	if !rebuilt {
		t.Fatal("RebuildSystemPrompt was not called")
	}
	if m.opts.SystemPrompt != "rebuilt prompt" {
		t.Fatalf("SystemPrompt = %q", m.opts.SystemPrompt)
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:580-681 through the interactive extension-resource application path. Both ordinary and file-URL resource paths must keep their extension provenance.
func TestResourceLoaderUpstreamExtensionResources(t *testing.T) {
	for _, fileURL := range []bool{false, true} {
		t.Run(fmt.Sprint(fileURL), func(t *testing.T) {
			root := t.TempDir()
			skillName, source := "extra-skill", "extra"
			skillDir := filepath.Join(root, "extra-skills", skillName)
			if fileURL {
				skillName, source = "file-url-skill", "file-url"
				skillDir = filepath.Join(root, "extra skills", skillName)
			}
			skillPath := filepath.Join(skillDir, "SKILL.md")
			promptPath := filepath.Join(root, "extra-prompts", "extra.md")
			description := "Extra skill"
			if fileURL {
				description = "File URL skill"
			}
			for path, content := range map[string]string{
				skillPath:  "---\nname: " + skillName + "\ndescription: " + description + "\n---\nExtra content",
				promptPath: "---\ndescription: Extra prompt\n---\nExtra prompt content",
			} {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			input := skillDir
			if fileURL {
				input = fileURLForTest(skillDir).String()
			}
			m := &InteractiveMode{opts: InteractiveOptions{CWD: root, AgentDir: t.TempDir()}}
			m.newRunner = inproc.NewRunner([]extension.Extension{{Path: filepath.Join(root, source+".ts"), Handlers: map[string][]extension.HandlerFn{
				EventResourcesDiscover: {func(...any) (any, error) {
					return &extension.ResourcesDiscoverResult{SkillPaths: []string{input}, PromptPaths: []string{promptPath}}, nil
				}},
			}}}, root)
			if err := m.extendResourcesFromExtensions(t.Context(), "startup"); err != nil {
				t.Fatal(err)
			}
			if len(m.opts.Skills) != 1 || m.opts.Skills[0].Name != skillName || m.opts.Skills[0].Path != skillPath || len(m.opts.SkillDiagnostics) != 0 {
				t.Fatalf("skills=%#v diagnostics=%v", m.opts.Skills, m.opts.SkillDiagnostics)
			}
			wantSkillSource := PiSourceInfo{Path: skillPath, Source: "extension:" + source, Scope: "temporary", Origin: "top-level", BaseDir: root}
			if m.opts.Skills[0].SourceInfo != wantSkillSource {
				t.Fatalf("loaded skill source = %#v, want %#v", m.opts.Skills[0].SourceInfo, wantSkillSource)
			}
			if len(m.promptTemplates) != 1 || m.promptTemplates[0].Name != "extra" {
				t.Fatalf("prompts=%#v", m.promptTemplates)
			}
			catalog := SlashCommandCatalog{CWD: root, AgentDir: m.opts.AgentDir, SourceInfo: m.resourceSourceInfo}
			for path, kind := range map[string]string{skillPath: "skills", promptPath: "prompts"} {
				info := catalog.SourceInfoForPath(path, kind)
				if info.Source != "extension:"+source || info.Path != path || info.Scope != "temporary" || info.Origin != "top-level" {
					t.Fatalf("source info for %s = %#v", path, info)
				}
			}
		})
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:786-828 at the reload boundary: noSkills suppresses discovery, not the already selected additional paths.
func TestResourceLoaderUpstreamNoSkillsReload(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "custom.md")
	if err := os.WriteFile(path, []byte("---\nname: custom\ndescription: Custom skill\n---\nContent"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{nil, {root}} {
		m := &InteractiveMode{opts: InteractiveOptions{CWD: root, NoSkills: true, SkillPaths: paths}}
		m.reloadSkillsFromPaths()
		if len(paths) == 0 {
			if len(m.opts.Skills) != 0 {
				t.Fatalf("no selected paths loaded skills: %#v", m.opts.Skills)
			}
		} else if len(m.opts.Skills) != 1 || m.opts.Skills[0].Name != "custom" {
			t.Fatalf("additional paths lost with noSkills: %#v", m.opts.Skills)
		}
	}
}

// All three no-resource flags suppress automatic discovery, not extension-selected paths (resource-loader.ts:extendResources and update*FromPaths).
func TestResourceLoaderUpstreamDisabledDiscoveryKeepsExtensions(t *testing.T) {
	previous := tui.ActiveThemeRegistry()
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	t.Cleanup(func() { tui.SetThemeRegistry(previous) })
	root := t.TempDir()
	skillPath := filepath.Join(root, "custom", "SKILL.md")
	promptPath := filepath.Join(root, "custom.md")
	themePath := filepath.Join(root, "custom.json")
	themeData, err := os.ReadFile("../../tui/theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		skillPath:  "---\nname: custom\ndescription: Custom skill\n---\nContent",
		promptPath: "Custom prompt",
		themePath:  strings.Replace(string(themeData), `"name": "dark"`, `"name": "extension-discovery-test"`, 1),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := &InteractiveMode{opts: InteractiveOptions{CWD: root, NoSkills: true, NoPromptTemplates: true, NoThemes: true}}
	agg := &extension.ResourcesDiscoverAggregateResult{
		SkillPaths:  []extension.AttributedResourcePath{{Path: filepath.Dir(skillPath), ExtensionPath: "/custom.ts"}},
		PromptPaths: []extension.AttributedResourcePath{{Path: promptPath, ExtensionPath: "/custom.ts"}},
		ThemePaths:  []extension.AttributedResourcePath{{Path: themePath, ExtensionPath: "/custom.ts"}},
	}
	if err := m.applyDiscoveredResources(agg); err != nil {
		t.Fatal(err)
	}
	if len(m.opts.Skills) != 1 || m.opts.Skills[0].Name != "custom" {
		t.Fatalf("extension skill = %#v", m.opts.Skills)
	}
	if len(m.promptTemplates) != 1 || m.promptTemplates[0].Name != "custom" {
		t.Errorf("extension prompt = %#v", m.promptTemplates)
	}
	if path := tui.ActiveThemeRegistry().PathOf("extension-discovery-test"); path != themePath {
		t.Errorf("extension theme = %s, want %s", path, themePath)
	}
}

func contains(items []string, want string) bool {
	return slices.Contains(items, want)
}

// Upstream rebuilds the command list from the loaded extensions on reload: a
// command of an extension that is no longer loaded is gone, not left
// registered against a stopped runtime.
func TestReloadDropsCommandsOfRemovedExtensions(t *testing.T) {
	m := &InteractiveMode{runCtx: context.Background(), slashRegistry: NewSlashRegistry(), opts: InteractiveOptions{CWD: t.TempDir()}}
	byeHandler := func(context.Context, string) error { return nil }
	m.newRunner = inproc.NewRunner([]extension.Extension{
		{Name: "bye", Commands: map[string]extension.RegisteredCommand{"bye": {Name: "bye", Description: "Say bye", Handler: byeHandler}}, CommandOrder: []string{"bye"}},
		{Name: "hello", Commands: map[string]extension.RegisteredCommand{"hello": {Name: "hello", Description: "Say hello", Handler: byeHandler}}, CommandOrder: []string{"hello"}},
	}, m.opts.CWD)
	m.syncExtensionSlashCommands()
	for _, name := range []string{"bye", "hello"} {
		if _, ok := m.slashRegistry.Resolve(name); !ok {
			t.Fatalf("/%s not registered at startup", name)
		}
	}

	m.newRunner = inproc.NewRunner([]extension.Extension{
		{Name: "hello", Commands: map[string]extension.RegisteredCommand{"hello": {Name: "hello", Description: "Say hello", Handler: byeHandler}}, CommandOrder: []string{"hello"}},
	}, m.opts.CWD)
	m.syncExtensionSlashCommands()
	if _, ok := m.slashRegistry.Resolve("bye"); ok {
		t.Fatal("/bye is still registered after its extension was dropped")
	}
	if _, ok := m.slashRegistry.Resolve("hello"); !ok {
		t.Fatal("/hello was lost")
	}
}
