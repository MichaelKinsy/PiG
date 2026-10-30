package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/test/project-trust-saved-decision.test.ts (Pi 31ade0e0, #10947). AGENTS.md alone is not a trust-requiring project resource.
func agentsOnlyProject(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("# untrusted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return project
}

// "resolveProjectTrusted honors the saved decision before auto-trust".
func TestResolveProjectTrustedHonorsASavedDecisionBeforeAutoTrust(t *testing.T) {
	project := agentsOnlyProject(t)
	store := codingagent.NewProjectTrustStore(t.TempDir())
	resolve := func(override *bool) bool {
		t.Helper()
		trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
			CWD: project, Store: store, Override: override, Default: "ask", UI: extension.NoopUIContext,
		})
		if err != nil {
			t.Fatal(err)
		}
		return trusted
	}
	if !resolve(nil) {
		t.Fatal("a project without trust-requiring resources and no saved decision was not auto-trusted")
	}
	if err := store.Set(project, new(false)); err != nil {
		t.Fatal(err)
	}
	if resolve(nil) {
		t.Fatal("a saved Do not trust decision was ignored for a project without trust-requiring resources")
	}
	if !resolve(new(true)) {
		t.Fatal("--approve did not beat the saved decision")
	}
	if err := store.Set(project, new(true)); err != nil {
		t.Fatal(err)
	}
	if !resolve(nil) {
		t.Fatal("a saved Trust decision did not trust the project")
	}
}

// pig additive (D211) makes a project with a context file need a decision when the Piglet withholds context files of an untrusted project; the saved decision must still win there.
func TestResolveProjectTrustedHonorsASavedDecisionWhenContextFilesNeedTrust(t *testing.T) {
	project := agentsOnlyProject(t)
	store := codingagent.NewProjectTrustStore(t.TempDir())
	resolve := func(defaultTrust string) bool {
		t.Helper()
		trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
			CWD: project, Store: store, Default: defaultTrust, UI: extension.NoopUIContext, ContextFilesTrustedOnly: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return trusted
	}
	if resolve("ask") {
		t.Fatal("a context-file-only project was trusted without a decision")
	}
	if err := store.Set(project, new(false)); err != nil {
		t.Fatal(err)
	}
	if resolve("always") {
		t.Fatal("defaultProjectTrust always beat a saved Do not trust decision")
	}
	if err := store.Set(project, new(true)); err != nil {
		t.Fatal(err)
	}
	if !resolve("never") {
		t.Fatal("defaultProjectTrust never beat a saved Trust decision")
	}
}

// "reports a saved Do not trust decision to extensions" and "auto-trusts when no decision is saved": the startup build of a project that holds only an AGENTS.md reports the trust that ctx.isProjectTrusted() returns.
// The Piglet case is the D211 path, where a context file raises the need for a decision.
func TestStartupBuildHonorsASavedDecisionForAProjectWithoutTrustRequiringResources(t *testing.T) {
	for _, tc := range []struct {
		name         string
		contextFiles bool
		saved        *bool
		want         bool
	}{
		{"saved Do not trust", false, new(false), false},
		{"no decision auto-trusts", false, nil, true},
		{"saved Trust", false, new(true), true},
		{"D211 saved Do not trust", true, new(false), false},
		{"D211 saved Trust", true, new(true), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			agentDir := filepath.Join(home, "agent")
			cwd := agentsOnlyProject(t)
			t.Setenv("PIG_HOME", home)
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			t.Setenv("PIG_TEST_FAUX", "1")
			store := codingagent.NewProjectTrustStore(agentDir)
			if tc.saved != nil {
				if err := store.Set(cwd, tc.saved); err != nil {
					t.Fatal(err)
				}
			}
			flags := parseArgs([]string{"--model", "test-faux/faux-1", "--print"})
			flags.ContextFilesTrustedOnly = tc.contextFiles
			settings := codingagent.NewSettingsManager(cwd, agentDir)
			builder := &cliRuntimeBuilder{
				mode: processAppMode(flags), flags: flags, agentDir: agentDir, launchCWD: cwd,
				settingsManager: settings, settingsDiagnostics: codingagent.CollectSettingsDiagnostics(settings),
				startupUIOptions: startupUIOptions(flags, false, cwd, agentDir, settings),
				trustStore:       store, trustByCWD: map[string]bool{},
				stageExtensionSDKs: func() error { return nil },
			}
			build, err := builder.buildResources(t.Context(), cliBuildInput{CWD: cwd, Startup: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if build.Host != nil {
					build.Host.Shutdown("test")
				}
				build.Services.Close()
			})
			if build.ProjectTrusted != tc.want {
				t.Fatalf("ProjectTrusted = %v, want %v", build.ProjectTrusted, tc.want)
			}
			if got := build.Services.SettingsManager().IsProjectTrusted(); got != tc.want {
				t.Fatalf("settings IsProjectTrusted = %v, want %v", got, tc.want)
			}
		})
	}
}
