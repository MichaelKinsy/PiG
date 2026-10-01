package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// settings.defaultTools with only +name/-name entries changes the built-in default selection: upstream getDefaultTools
// resolves them and sdk.ts:264-269 uses the result as the initial tools (.upstream/v0.99.1/packages/coding-agent/src/core/settings-manager.ts:1430-1434).
func TestRPCDefaultToolsModifiersSelectInitialTools(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultTools":["+grep","-edit"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1"}, "--no-session", "--provider", "test-faux", "--model", "faux-1")
	p.send(`{"id":"prompt","type":"prompt","message":"hello"}`)
	p.await("settled prompt", func(record rpcRecord) bool { return record["type"] == "agent_settled" })
	p.send(`{"id":"sent","type":"get_messages"}`)
	p.await("tools section", func(record rpcRecord) bool {
		if record["id"] != "sent" {
			return false
		}
		system := record["data"].(map[string]any)["messages"].([]any)[0].(map[string]any)
		tools, _ := system["sections"].(map[string]any)["tools"].(string)
		for _, want := range []string{"- read", "- bash", "- write", "- grep"} {
			if !strings.Contains(tools, want) {
				t.Fatalf("tools section lacks %q:\n%s", want, tools)
			}
		}
		if strings.Contains(tools, "- edit") {
			t.Fatalf("tools section keeps the removed tool:\n%s", tools)
		}
		return true
	})
}

// The print and interactive startup path resolves the same setting (sdk.ts:264-269): its system prompt lists the resolved tools.
func TestCLIDefaultToolsModifiersSelectInitialTools(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	cwd := t.TempDir()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultTools":["+grep","-edit"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_TEST_FAUX", "1")
	flags := parseFlags([]string{"--model", "test-faux/faux-1", "--print"})
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
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.buildSession(t.Context(), build, cliBuildInput{CWD: cwd, Manager: manager}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if build.Host != nil {
			build.Host.Shutdown("test")
		}
		build.Services.Close()
	})
	for _, want := range []string{"- read", "- bash", "- write", "- grep"} {
		if !strings.Contains(build.SystemPrompt, want) {
			t.Fatalf("system prompt lacks %q:\n%s", want, build.SystemPrompt)
		}
	}
	if strings.Contains(build.SystemPrompt, "- edit") {
		t.Fatalf("system prompt keeps the removed tool:\n%s", build.SystemPrompt)
	}
}
