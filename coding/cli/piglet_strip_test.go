package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// buildStripRuntime runs the print-mode startup build with the given active
// Piglet and returns the finished build.
func buildStripRuntime(t *testing.T, p *piglet.Piglet) *cliBuild {
	t.Helper()
	home := t.TempDir()
	agentDir := home + "/agent"
	cwd := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_TEST_FAUX", "1")
	flags := parseArgs([]string{"--model", "test-faux/faux-1", "--print"})
	t.Cleanup(applyPigletStrip(p, &flags))
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	builder := &cliRuntimeBuilder{
		mode: processAppMode(flags), flags: flags, agentDir: agentDir, launchCWD: cwd,
		activePiglet:    p,
		settingsManager: settings, settingsDiagnostics: codingagent.CollectSettingsDiagnostics(settings),
		startupUIOptions: startupUIOptions(flags, false, cwd, agentDir, settings),
		trustStore:       codingagent.NewProjectTrustStore(agentDir), trustByCWD: map[string]bool{},
		stageExtensionSDKs: func() {},
	}
	build, err := builder.buildResources(t.Context(), cliBuildInput{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if build.Host != nil {
			build.Host.Shutdown("test")
		}
		build.Services.Close()
	})
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.buildSession(t.Context(), build, cliBuildInput{CWD: cwd, Manager: manager}); err != nil {
		t.Fatal(err)
	}
	return build
}

func builtinExtensionPaths(build *cliBuild) []string {
	var paths []string
	for _, ext := range build.BuiltinExtensions {
		paths = append(paths, ext.Path)
	}
	return paths
}

// TestPigletStripRemovesToolsAndBuiltinExtensions drives the startup build
// with and without a strip list: a stripped tool leaves the tool registry,
// the selected tools and the system prompt; a stripped built-in extension is
// not loaded; an unstripped sibling of each stays.
func TestPigletStripRemovesToolsAndBuiltinExtensions(t *testing.T) {
	if pigstrip.Has(pigstrip.ListExtensions, "mcp") || pigstrip.Has(pigstrip.ListExtensions, "tool-search") {
		t.Skip("this build compiles mcp or tool-search out")
	}
	stock := buildStripRuntime(t, nil)
	if !strings.Contains(stock.SystemPrompt, "- bash") || !slices.Contains(stock.AgentToolNames, "bash") {
		t.Fatalf("stock build lacks bash: tools=%v", stock.AgentToolNames)
	}
	stockPaths := builtinExtensionPaths(stock)
	if !slices.Contains(stockPaths, "builtin:mcp") || !slices.Contains(stockPaths, "builtin:tool-search") {
		t.Fatalf("stock build lacks the default built-in extensions: %v", stockPaths)
	}

	p, err := piglet.ParseBytes([]byte("name: lean\nstrip:\n  tools: [bash]\n  extensions: [mcp]\n"))
	if err != nil {
		t.Fatal(err)
	}
	build := buildStripRuntime(t, p)
	if strings.Contains(build.SystemPrompt, "- bash") || slices.Contains(build.AgentToolNames, "bash") {
		t.Fatalf("stripped bash remains: tools=%v\n%s", build.AgentToolNames, build.SystemPrompt)
	}
	if _, excluded := build.ExcludedTools["bash"]; !excluded {
		t.Fatalf("stripped bash is not excluded from the tool registry: %v", build.ExcludedTools)
	}
	if _, registryExcluded := toolRegistryFilters(build.Flags); !mapHas(registryExcluded, "bash") {
		t.Fatal("stripped bash stays in the registry pi.getAllTools() reports")
	}
	if !strings.Contains(build.SystemPrompt, "- read") {
		t.Fatalf("unstripped read left the system prompt:\n%s", build.SystemPrompt)
	}
	paths := builtinExtensionPaths(build)
	if slices.Contains(paths, "builtin:mcp") {
		t.Fatalf("stripped mcp was loaded: %v", paths)
	}
	if !slices.Contains(paths, "builtin:tool-search") {
		t.Fatalf("unstripped tool-search was not loaded: %v", paths)
	}
}

func mapHas(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}

// TestApplyPigletStripFeatures pins that each stripped feature sets the Pi
// switch it reuses and drops the explicit paths, and that no strip list
// leaves the flags untouched.
func TestApplyPigletStripFeatures(t *testing.T) {
	flags := parseArgs([]string{"--skill", "a", "--prompt-template", "b", "--theme", "c", "--exclude-tools", "read"})
	before := flags
	applyPigletStrip(nil, &flags)
	applyPigletStrip(&piglet.Piglet{Name: "plain"}, &flags)
	if !slices.Equal(flags.Skills, before.Skills) || flags.NoSkills || flags.NoThemes || flags.NoPromptTemplates || !slices.Equal(flags.ExcludeTools, []string{"read"}) || pigstrip.Active() {
		t.Fatalf("no strip list changed the flags: %#v", flags)
	}

	p, err := piglet.ParseBytes([]byte("name: lean\nstrip:\n  tools: [read, grep]\n  features: [skills, prompt-templates, themes]\n"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(applyPigletStrip(p, &flags))
	if !flags.NoSkills || flags.Skills != nil || !flags.NoPromptTemplates || flags.PromptTemplates != nil || !flags.NoThemes || flags.Themes != nil {
		t.Fatalf("stripped features stay enabled: %#v", flags)
	}
	if !slices.Equal(flags.ExcludeTools, []string{"read", "grep"}) {
		t.Fatalf("ExcludeTools = %v", flags.ExcludeTools)
	}
	if options := startupUIOptions(flags, false, t.TempDir(), t.TempDir(), codingagent.NewSettingsManager(t.TempDir(), t.TempDir())); options.ThemePaths != nil {
		t.Fatalf("stripped themes still load before Session selection: %v", options.ThemePaths)
	}
}

// TestApplyPigletStripRecordsEveryList pins D92's one strip state: the runtime strip records every list in pigstrip, as a
// Binary's OFF shims record what it compiled out, so every site that asks pigstrip (the Radius MCP offer after a Radius
// /login, the llama.cpp entry and host, the built-in extension paths, the slash command registry) drops the entry.
func TestApplyPigletStripRecordsEveryList(t *testing.T) {
	for _, name := range []string{"mcp", llamaBuiltinName} {
		if pigstrip.Has(pigstrip.ListExtensions, name) {
			t.Skipf("this build compiles %s out", name)
		}
	}
	p, err := piglet.ParseBytes([]byte("name: lean\nstrip:\n  tools: [grep]\n  commands: [/share]\n  extensions: [mcp, llama.cpp]\n  apis: [mistral-conversations]\n  features: [experimental-server, themes]\n"))
	if err != nil {
		t.Fatal(err)
	}
	var flags Args
	t.Cleanup(applyPigletStrip(p, &flags))
	for _, want := range []struct{ list, id string }{
		{pigstrip.ListTools, "grep"}, {pigstrip.ListCommands, "/share"}, {pigstrip.ListExtensions, "mcp"},
		{pigstrip.ListExtensions, llamaBuiltinName}, {pigstrip.ListAPIs, "mistral-conversations"},
		{pigstrip.ListFeatures, pigstrip.ExperimentalServer}, {pigstrip.ListFeatures, pigstrip.Themes},
	} {
		if !pigstrip.Has(want.list, want.id) {
			t.Errorf("strip.%s %s is not recorded in pigstrip", want.list, want.id)
		}
	}
	if got := llamaInlineExtensions(); got != nil {
		t.Errorf("llamaInlineExtensions() = %v, want nil under strip.extensions llama.cpp", got)
	}
	if !builtinExtensionStripped("builtin:mcp") {
		t.Error("builtin:mcp stays a built-in extension path under strip.extensions mcp")
	}
}
