//go:build !pig_strip_node_extensions

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestSharedSubprocessExecAcrossModes proves an extension's pi.exec call
// succeeds in every mode that binds coding.BindSessionExtensionActions (print, JSON,
// RPC), matching upstream, where core/extensions/loader.ts binds
// ExtensionContext.exec identically regardless of mode (createExtensionContext
// is not mode-conditional). Before this fix, only interactive mode registered
// the "exec" host action (internal/codingagent/interactive_extensions.go);
// print, JSON, and RPC mode left HostCallbacks.Exec nil, so ui_bridge.go's
// handleExec always returned code 127 ("exec not available") for a subprocess
// extension running under those modes.
//
// Both fixtures ask pi.exec to run PIG_TEST_EXEC_HELPER_PATH (this test
// binary itself, in "echo" mode via execEchoHelperEnv) instead of an external
// echo binary, so the exec call spawns a real, natively executable command on
// every OS the test binary runs on, including native Windows.
func TestSharedSubprocessExecAcrossModes(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	goSDKBin := buildExecSDKFixture(t)

	type fixtureCase struct {
		name        string
		requireTool string // exec.LookPath name required before this case can run, or "" for none.
		buildCfg    func(t *testing.T) subprocess.ExtConfig
	}
	cases := []fixtureCase{
		{name: "node", requireTool: "node", buildCfg: func(t *testing.T) subprocess.ExtConfig {
			fixture, err := filepath.Abs(filepath.Join("testdata", "exec-command.mjs"))
			if err != nil {
				t.Fatal(err)
			}
			return subprocess.ExtConfig{Name: "exec-command", Source: fixture, Enabled: true}
		}},
		{name: "go-sdk", buildCfg: func(t *testing.T) subprocess.ExtConfig {
			return subprocess.ExtConfig{Name: "exec-sdk-fixture", Path: goSDKBin, Enabled: true}
		}},
	}

	for _, mode := range []extension.ExtensionMode{extension.ModePrint, extension.ModeJSON, extension.ModeRPC} {
		for _, fc := range cases {
			t.Run(string(mode)+"/"+fc.name, func(t *testing.T) {
				if fc.requireTool != "" {
					requireExtensionTool(t, fc.requireTool)
				}
				t.Setenv(execEchoHelperEnv, "1")
				t.Setenv("PIG_TEST_EXEC_HELPER_PATH", self)
				t.Setenv("PIG_TEST_FAUX", "1")
				t.Setenv("PIG_TEST_FAUX_SCENARIO", "parity-basic")
				agentDir := t.TempDir()
				config := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
				if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
					t.Fatal(err)
				}
				services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
				if err != nil {
					t.Fatal(err)
				}
				model, err := coding.BuildModel("test-faux/faux-1", services)
				if err != nil {
					t.Fatal(err)
				}
				runtime, err := coding.NewRuntime(coding.RuntimeOptions{Services: services})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = runtime.Close() }()
				session, err := runtime.New(coding.SessionStartOptions{Model: model, NoSession: true})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = session.Close() }()

				bridge := subprocess.NewUIBridge(func() {})
				host := subprocess.NewHost(t.TempDir())
				host.SetMode(string(mode))
				host.SetUIBridge(bridge)
				defer host.Shutdown("test done")

				// This is the shared, mode-independent wiring under test: print,
				// JSON, and RPC mode all call coding.BindSessionExtensionActions to bind
				// the session-backed subprocess host actions, including exec.
				coding.BindSessionExtensionActions(nil, bridge, func() *coding.Session { return session }, extension.ContextActions{
					ModelRegistry:    services.Registry(),
					IsProjectTrusted: services.SettingsManager().IsProjectTrusted,
				}, nil)

				ctx := testbudget.Context(t)
				ext, err := host.Load(ctx, fc.buildCfg(t))
				if err != nil {
					t.Fatal(err)
				}
				command, ok := ext.Commands["exec_probe"]
				if !ok {
					t.Fatal("exec_probe command not registered")
				}
				if err := command.Handler(ctx, ""); err != nil {
					t.Fatalf("pi.exec in %s mode: %v", mode, err)
				}
				// The action the session binds runs under the call's context, so a signal the extension aborts ends a running command here as in
				// interactive mode (core/exec.ts:46-52).
				abortCommand, ok := ext.Commands["exec_abort_probe"]
				if !ok {
					t.Fatal("exec_abort_probe command not registered")
				}
				t.Setenv(execEchoHelperEnv, "hang")
				if err := abortCommand.Handler(ctx, ""); err != nil {
					t.Fatalf("pi.exec with an abort signal in %s mode: %v", mode, err)
				}
			})
		}
	}
}
