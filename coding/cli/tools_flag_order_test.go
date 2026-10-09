package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// --tools selects the tools in its own order, as Pi does: main.ts:545-546 copies the --tools list into options.tools,
// sdk.ts:271-276 takes initialActiveToolNames from it in the given order, agent-session.ts:3552-3562 activates them in
// that order (a Set keeps the first occurrence) and system-prompt.ts lists them in that order. A repeated name stays once,
// at its first position, and an excluded one is dropped.
func TestToolsFlagSelectsToolsInItsOrder(t *testing.T) {
	build := buildToolOrderRuntime(t, "--tools", "grep,read,grep,bash,find", "--exclude-tools", "find")
	if want := []string{"grep", "read", "bash"}; !slices.Equal(build.AgentToolNames, want) {
		t.Fatalf("--tools grep,read,grep,bash,find --exclude-tools find selected %v, want %v", build.AgentToolNames, want)
	}
	grep, read, bash := strings.Index(build.SystemPrompt, "- grep:"), strings.Index(build.SystemPrompt, "- read:"), strings.Index(build.SystemPrompt, "- bash:")
	if grep < 0 || read < 0 || bash < 0 || grep > read || read > bash {
		t.Fatalf("system prompt lists the tools out of the --tools order (grep %d, read %d, bash %d):\n%s", grep, read, bash, build.SystemPrompt)
	}
}

// buildToolOrderRuntime runs the print-mode startup build with the given extra arguments and returns the finished build.
func buildToolOrderRuntime(t *testing.T, args ...string) *cliBuild {
	t.Helper()
	home := t.TempDir()
	agentDir := home + "/agent"
	cwd := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_TEST_FAUX", "1")
	flags := parseArgs(append([]string{"--model", "test-faux/faux-1", "--print"}, args...))
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	builder := &cliRuntimeBuilder{
		mode: processAppMode(flags), flags: flags, agentDir: agentDir, launchCWD: cwd,
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

// toolOrderAPIs are the provider shapes whose requests the tool order tests read.
var toolOrderAPIs = []struct {
	name     string
	provider func(*testing.T) *scopeProvider
}{
	{"openai-completions", newScopeProvider},
	{"anthropic-messages", newAnthropicScopeProvider},
}

// toolOrderFixture writes a Stock PiG home whose models.json points the fake model at provider with api, and an
// extension, scoped.mjs, that registers scoped_tool. It returns the home and the extension's directory.
func toolOrderFixture(t *testing.T, provider *scopeProvider, api string) (home, dir string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, dir = filepath.Join(root, "home"), filepath.Join(root, "work")
	writeStartupFixtureFile(t, filepath.Join(home, "agent", "models.json"), fmt.Sprintf(`{"providers":{"fake":{"baseUrl":%q,"api":%q,"apiKey":"synthetic-test-key","models":[{"id":"fake","name":"Fake","reasoning":false,"input":["text"],"contextWindow":128000,"maxTokens":1024,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`, provider.url, api))
	writeStartupFixtureFile(t, filepath.Join(dir, "scoped.mjs"), `export default function (pi) {
  pi.registerTool({ name: "scoped_tool", label: "Scoped", description: "scoped", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });
}
`)
	return home, dir
}

// Stock PiG sends the --tools list to the provider in its order in every mode, extension tools included: Pi's
// initialActiveToolNames is options.tools as given (sdk.ts:271-276), and the request declares agent.state.tools in
// that order (agent-session.ts:1561-1565, _applyToolLoadout).
func TestToolsFlagOrderReachesTheProviderInEveryMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts a Node extension")
	}
	t.Parallel()
	want := []string{"grep", "scoped_tool", "read"}
	for _, api := range toolOrderAPIs {
		t.Run(api.name, func(t *testing.T) {
			for _, mode := range []string{"print", "json", "rpc", "interactive"} {
				t.Run(mode, func(t *testing.T) {
					provider := api.provider(t)
					home, dir := toolOrderFixture(t, provider, api.name)
					if names := firstRequestToolsIn(t, provider, home, dir, mode, "-e", filepath.Join(dir, "scoped.mjs"), "--tools", "grep,scoped_tool,read"); !slices.Equal(names, want) {
						t.Errorf("--tools grep,scoped_tool,read offered %v, want %v", names, want)
					}
				})
			}
		})
	}
}

// Interactive /reload keeps the --tools order: Pi's reload rebuilds the tool registry and keeps the active tools in
// their order (agent-session.ts _refreshToolRegistry: previousActiveToolNames, then a Set).
func TestReloadKeepsTheToolsFlagOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts a Node extension")
	}
	t.Parallel()
	want := []string{"grep", "scoped_tool", "read"}
	for _, api := range toolOrderAPIs {
		t.Run(api.name, func(t *testing.T) {
			provider := api.provider(t)
			home, dir := toolOrderFixture(t, provider, api.name)
			requests := interactiveRequestsAcrossReload(t, provider, home, dir, "-e", filepath.Join(dir, "scoped.mjs"), "--tools", "grep,scoped_tool,read")
			for i, names := range requests {
				if !slices.Equal(names, want) {
					t.Errorf("request %d (%s) offered %v, want %v", i+1, []string{"before /reload", "after /reload"}[i], names, want)
				}
			}
		})
	}
}

// interactiveRequestsAcrossReload starts interactive pig in dir, prompts once, runs /reload once the first run settled,
// prompts again, and returns the tool names of both model requests.
func interactiveRequestsAcrossReload(t *testing.T, provider *scopeProvider, home, dir string, launch ...string) [][]string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	if resolved, err := exec.Command(node, "-p", "process.execPath").Output(); err == nil {
		node = strings.TrimSpace(string(resolved))
	}
	writeStartupFixtureFile(t, filepath.Join(home, "agent", "settings.json"), "{}")
	// Pi refuses /reload while a run streams (interactive-mode.ts:6431-6433 handleReloadCommand), and no screen text
	// marks the end of a run: the reply "ok" also occurs in the startup help ("look up"). This command waits for the run
	// to settle (ExtensionCommandContext.waitForIdle) and then shows a marker.
	idle := filepath.Join(t.TempDir(), "idle.mjs")
	writeStartupFixtureFile(t, idle, `export default function (pi) {
  pi.registerCommand("settled", { description: "settled", handler: async (_args, ctx) => { await ctx.waitForIdle(); ctx.ui.notify("first run settled", "info"); } });
}
`)
	cmd := exec.Command(buildPigBinaryForSignalTest(t), append(slices.Clone(launch), "-e", idle, "--no-session", "--offline", "--no-context-files", "--model", "fake/fake")...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PATH="+filepath.Dir(node)+string(os.PathListSeparator)+os.Getenv("PATH"), "PI_OFFLINE=1", "PI_TELEMETRY=0", "PI_SKIP_VERSION_CHECK=1", "TERM=xterm-256color")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	terminal := startTerminalDriver(t, cmd)
	defer terminal.close()
	ctx := testbudget.Context(t)
	waitFor := func(what string, done func() bool) {
		t.Helper()
		switch err := waitUntil(ctx, provider, terminal, done); {
		case errors.Is(err, errTerminalClosed):
			t.Fatalf("pig exited while waiting for %s\nstderr:\n%s", what, stderr.String())
		case err != nil:
			t.Fatalf("timed out waiting for %s\nscreen:\n%s\nstderr:\n%s", what, terminal, stderr.String())
		}
	}
	// The startup listing names every loaded extension; the prompt follows it.
	waitFor("the startup listing", func() bool { return terminal.shows("scoped.mjs") })
	terminal.send("hi\r")
	waitFor("the first model request", func() bool { return provider.requests() >= 1 })
	terminal.send("/settled\r")
	waitFor("the first run to settle", func() bool { return terminal.shows("first run settled") })
	terminal.clear()
	terminal.send("/reload\r")
	waitFor("the /reload summary", func() bool { return terminal.shows("Reloaded") })
	terminal.send("again\r")
	waitFor("the second model request", func() bool { return provider.requests() >= 2 })
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return [][]string{provider.offered[0], provider.offered[1]}
}
