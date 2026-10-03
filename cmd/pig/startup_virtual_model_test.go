package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func registerRouterAuto(t *testing.T, services *coding.Services) {
	t.Helper()
	route := func(context.Context, extension.ModelRouteRequest) (extension.ModelRoute, error) {
		return extension.ModelRoute{}, nil
	}
	if err := services.Registry().RegisterVirtualModel(extension.VirtualModelDefinition{Provider: "router", ID: "auto", Name: "Auto", Route: route}); err != nil {
		t.Fatal(err)
	}
}

// Pi resolves --model, the saved default and the --models scope against ModelRuntime.getModels() and getAvailableSnapshot(), which list the virtual models an extension registered (model-runtime.ts:285-293, virtual-models.ts:190-227) and mark a provider of only virtual models configured (model-runtime.ts:953-958). createAgentSessionServices registers them before model resolution (agent-session-services.ts:182-193), so `--model router/auto` selects the virtual model. Pig read the registry's models, which omit them, and fuzzy-matched openrouter/openrouter/auto-beta.
func TestSelectStartupModelSelectsRegisteredVirtualModel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		options  startupModelOptions
		settings codingagent.Settings
	}{
		{name: "--model provider/id", options: startupModelOptions{CLIModel: "router/auto"}},
		{name: "--provider and --model", options: startupModelOptions{CLIProvider: "router", CLIModel: "auto"}},
		{name: "--model with a thinking suffix", options: startupModelOptions{CLIModel: "router/auto:high"}},
		{name: "--models scope", options: startupModelOptions{ScopePatterns: []string{"router/auto"}}},
		{name: "saved default", settings: codingagent.Settings{DefaultProvider: "router", DefaultModel: "auto"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := testServices(t, t.TempDir())
			t.Cleanup(services.Close)
			registerRouterAuto(t, services)
			selected, err := selectStartupModel(t.Context(), tc.options, tc.settings, services)
			if err != nil {
				t.Fatalf("selectStartupModel: %v", err)
			}
			model := selected.Model
			if model == nil || model.ProviderMeta.ProviderID != "router" || model.ID != "auto" || !coding.IsVirtualModel(model) {
				t.Fatalf("selected %+v, want the virtual model router/auto", model)
			}
			if len(selected.Warnings) != 0 || len(selected.ScopeWarnings) != 0 || len(selected.Errors) != 0 {
				t.Fatalf("diagnostics: warnings %v, scope warnings %v, errors %v", selected.Warnings, selected.ScopeWarnings, selected.Errors)
			}
		})
	}
}

// probeRun is one binary run: its exit status, output and the records the extensions wrote.
type probeRun struct {
	exit           int
	stdout, stderr string
	records        []string
}

// runStartupProbe runs the real binary in mode with a fresh HOME and agent directory, which prepare may fill, and no prompt.
func runStartupProbe(t *testing.T, mode string, prepare func(agentDir string), args []string, extensions ...string) probeRun {
	t.Helper()
	binary := buildPigBinaryForSignalTest(t)
	home, cwd := t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if prepare != nil {
		prepare(agentDir)
	}
	report := filepath.Join(t.TempDir(), "report.jsonl")
	full := []string{"--no-skills", "--no-prompt-templates", "--no-approve", "--session-dir", t.TempDir()}
	for _, name := range extensions {
		fixture, err := filepath.Abs(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		full = append(full, "-e", fixture)
	}
	full = append(full, args...)
	if mode == "print" {
		full = append(full, "--print")
	} else {
		full = append(full, "--mode", mode)
	}
	cmd := exec.CommandContext(t.Context(), binary, full...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "WIRING_REPORT="+report)
	cmd.Stdin = strings.NewReader("")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	run := probeRun{}
	if err := cmd.Run(); err != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatalf("%s mode: %v", mode, err)
		}
		run.exit = exitErr.ExitCode()
	}
	run.stdout, run.stderr = stdout.String(), stderr.String()
	if data, err := os.ReadFile(report); err == nil {
		run.records = strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	return run
}

// With `--model router/auto` and an SDK extension that registers router/auto, Pi 0.99.1 starts the Session on the virtual model in -p, --mode json and --mode rpc: its session_start reads ctx.model as router/auto.
func TestStartupModelFlagSelectsExtensionVirtualModel(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			run := runStartupProbe(t, mode, nil, []string{"--no-extensions", "--model", "router/auto"}, "startup-model-probe.mjs", "host-wiring-virtual-model.py")
			if run.exit != 0 || run.stderr != "" {
				t.Fatalf("%s mode exited %d with stderr %q", mode, run.exit, run.stderr)
			}
			if !slices.Contains(run.records, `{"event":"start_model","value":"router/auto"}`) {
				t.Fatalf("records %v, want a session_start on router/auto", run.records)
			}
		})
	}
}

// Pi reports warning-only startup diagnostics and goes on; only an error diagnostic stops startup (main.ts:908-916: hasRuntimeErrors is any runtime diagnostic of type "error"). A package that lists a host-provided package under dependencies is a warning (resource-loader.ts:66-93, main.ts:797-800). Probed with Pi 0.99.1 in -p and --mode rpc: one `Warning: Extension package "<dir>/package.json": ...` line on stderr, the session starts and the exit status is 0. Pig exited 1 for any extension diagnostic.
func TestWarningOnlyExtensionDiagnosticsDoNotStopStartup(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			var manifest string
			prepare := func(agentDir string) {
				pkg := filepath.Join(agentDir, "warnpkg")
				resolved, err := filepath.EvalSymlinks(agentDir)
				if err != nil {
					t.Fatal(err)
				}
				manifest = filepath.Join(resolved, "warnpkg", "package.json")
				if runtime.GOOS == "windows" {
					// The settings entry is the temp path as given (8.3 short form on CI), and the warning names it as listed.
					manifest = filepath.Join(pkg, "package.json")
				}
				writeStartupFixtureFile(t, filepath.Join(pkg, "package.json"), `{"name":"warnpkg","version":"1.0.0","dependencies":{"@earendil-works/pi-coding-agent":"*"},"pi":{"extensions":["./ext.mjs"]}}`)
				writeStartupFixtureFile(t, filepath.Join(pkg, "ext.mjs"), "export default function () {}\n")
				writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), `{"packages":[`+strconv.Quote(pkg)+`]}`)
			}
			run := runStartupProbe(t, mode, prepare, nil, "startup-model-probe.mjs")
			want := `Warning: Extension package "` + manifest + `": Host-provided extension packages must be declared in peerDependencies with a "*" range, not dependencies: @earendil-works/pi-coding-agent. Installed copies can bypass the extension loader and create duplicate runtime modules.` + "\n"
			if run.exit != 0 || run.stderr != want {
				t.Fatalf("%s mode exited %d with stderr %q, want exit 0 and stderr %q", mode, run.exit, run.stderr, want)
			}
			if !slices.Contains(run.records, `{"event":"start_model","value":"probe/probe-model"}`) {
				t.Fatalf("records %v, want the session to start", run.records)
			}
		})
	}
}

// Pi hands extensions the scope it resolved against modelRuntime (main.ts:803-807, agent-session.ts:3353 getScopedModels), so `--models router/auto` puts the registered virtual model in ctx.scopedModels.
func TestExtensionScopedModelsIncludeRegisteredVirtualModel(t *testing.T) {
	services := testServices(t, t.TempDir())
	t.Cleanup(services.Close)
	registerRouterAuto(t, services)
	scoped := extensionScopedModels(services, []string{"router/auto:off"})
	if len(scoped) != 1 || scoped[0].Model == nil || scoped[0].Model.ProviderMeta.ProviderID != "router" || scoped[0].Model.ID != "auto" || !coding.IsVirtualModel(scoped[0].Model) || scoped[0].ThinkingLevel != "off" {
		t.Fatalf("scope = %+v, want the virtual model router/auto at thinking off", scoped)
	}
}

// Startup selects the runtime's own catalog entry for a virtual model, the object every later lookup returns. Pi never rewrites it (virtual-models.ts:159-176 createVirtualModel builds it once at registration), so selecting a reasoning virtual model at startup must leave the entry as registered. Pig set Capabilities.MaxThinking on the shared entry, so a startup selection changed what /model, ctx.model and the RPC model list later saw.
func TestSelectStartupModelLeavesTheVirtualCatalogEntryUnchanged(t *testing.T) {
	services := testServices(t, t.TempDir())
	t.Cleanup(services.Close)
	route := func(context.Context, extension.ModelRouteRequest) (extension.ModelRoute, error) {
		return extension.ModelRoute{}, nil
	}
	if err := services.Registry().RegisterVirtualModel(extension.VirtualModelDefinition{Provider: "router", ID: "auto", Name: "Auto", ThinkingLevels: []ai.ModelThinkingLevel{ai.ThinkingOff, ai.ThinkingLow}, Route: route}); err != nil {
		t.Fatal(err)
	}
	entry := services.ModelRuntime().GetModel("router", "auto")
	before := *entry
	selected, err := selectStartupModel(t.Context(), startupModelOptions{CLIModel: "router/auto"}, codingagent.Settings{}, services)
	if err != nil {
		t.Fatalf("selectStartupModel: %v", err)
	}
	if selected.Model != entry || !selected.Model.ProviderMeta.Reasoning {
		t.Fatalf("selected %+v, want the registered reasoning virtual model", selected.Model)
	}
	if !reflect.DeepEqual(entry.Capabilities, before.Capabilities) || entry.ProviderMeta.Reasoning != before.ProviderMeta.Reasoning {
		t.Fatalf("startup rewrote the registered virtual model: capabilities %+v, reasoning %t; registered %+v, %t", entry.Capabilities, entry.ProviderMeta.Reasoning, before.Capabilities, before.ProviderMeta.Reasoning)
	}
}
