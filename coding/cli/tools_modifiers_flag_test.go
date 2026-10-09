package cli

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A --tools list of only +name/-name entries changes the default selection instead of replacing it (ddaa0a03, sdk.ts:280-294, cli.md "Tools").
func TestToolsFlagModifiersChangeTheDefaultSelection(t *testing.T) {
	for _, tc := range []struct {
		name         string
		args         []string
		settings     string
		wantPrompt   []string
		absentPrompt []string
		wantAllowed  []string // nil: no allowlist
		wantModifier []string
		wantNoTools  string
	}{
		{
			name: "adds to and removes from the built-in defaults", args: []string{"--tools", "+grep,-write"},
			wantPrompt: []string{"- read", "- bash", "- edit", "- grep"}, absentPrompt: []string{"- write"}, wantModifier: []string{"+grep", "-write"},
		},
		{
			name: "applies on top of the defaultTools setting", args: []string{"-t", "+find,-bash"}, settings: `{"defaultTools":["read","ls"]}`,
			wantPrompt: []string{"- read", "- ls", "- find"}, absentPrompt: []string{"- bash", "- write"}, wantModifier: []string{"+find", "-bash"},
		},
		{
			name: "--no-tools starts from no tools and becomes an allowlist", args: []string{"--no-tools", "--tools", "+grep"},
			wantPrompt: []string{"- grep"}, absentPrompt: []string{"- read", "- bash"}, wantAllowed: []string{"grep"}, wantModifier: []string{"+grep"}, // sdk.ts:473 passes defaultToolModifiers whatever noTools is
		},
		{
			name: "--no-builtin-tools starts from no tools without an allowlist", args: []string{"--no-builtin-tools", "--tools", "+grep,+read"},
			wantPrompt: []string{"- grep", "- read"}, absentPrompt: []string{"- bash", "- edit"}, wantModifier: []string{"+grep", "+read"}, wantNoTools: "builtin",
		},
		{
			name: "--exclude-tools applies after the modifiers", args: []string{"--tools", "+grep", "--exclude-tools", "grep,edit"},
			wantPrompt: []string{"- read", "- bash", "- write"}, absentPrompt: []string{"- grep", "- edit"}, wantModifier: []string{"+grep"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			agentDir := filepath.Join(home, "agent")
			cwd := t.TempDir()
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.settings != "" {
				if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(tc.settings), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PIG_HOME", home)
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			t.Setenv("PI_CODING_AGENT_DIR", agentDir)
			t.Setenv("PIG_TEST_FAUX", "1")
			flags := parseArgs(append([]string{"--model", "test-faux/faux-1", "--print"}, tc.args...))
			if len(flags.Diagnostics) != 0 {
				t.Fatalf("diagnostics = %+v", flags.Diagnostics)
			}
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
			for _, want := range tc.wantPrompt {
				if !strings.Contains(build.SystemPrompt, want) {
					t.Errorf("system prompt lacks %q:\n%s", want, build.SystemPrompt)
				}
			}
			for _, absent := range tc.absentPrompt {
				if strings.Contains(build.SystemPrompt, absent) {
					t.Errorf("system prompt keeps %q:\n%s", absent, build.SystemPrompt)
				}
			}
			var allowed []string
			if build.Allowed != nil {
				allowed = slices.Sorted(maps.Keys(build.Allowed))
				if allowed == nil {
					allowed = []string{}
				}
			}
			if !slices.Equal(allowed, tc.wantAllowed) || (build.Allowed == nil) != (tc.wantAllowed == nil) {
				t.Errorf("Allowed = %v, want %v", allowed, tc.wantAllowed)
			}
			if !slices.Equal(build.DefaultToolModifiers, tc.wantModifier) {
				t.Errorf("DefaultToolModifiers = %v, want %v", build.DefaultToolModifiers, tc.wantModifier)
			}
			if build.NoTools != tc.wantNoTools {
				t.Errorf("NoTools = %q, want %q", build.NoTools, tc.wantNoTools)
			}
			if build.SkipBuiltinTools {
				t.Error("SkipBuiltinTools is set: a +name entry must keep the built-in registry")
			}
		})
	}
}

// Only --no-tools turns a modifier list into an allowlist of the registry (sdk.ts allowedToolNames).
func TestToolRegistryFiltersTreatModifiersAsNoAllowlist(t *testing.T) {
	allowed, _ := toolRegistryFilters(parseArgs([]string{"--tools", "+grep"}))
	if allowed != nil {
		t.Fatalf("allowed = %v, want none", allowed)
	}
	allowed, _ = toolRegistryFilters(parseArgs([]string{"--no-builtin-tools", "--tools", "+grep"}))
	if allowed != nil {
		t.Fatalf("with --no-builtin-tools allowed = %v, want none", allowed)
	}
	allowed, _ = toolRegistryFilters(parseArgs([]string{"--no-tools", "--tools", "+grep,-read,+read"}))
	if got := slices.Sorted(maps.Keys(allowed)); !slices.Equal(got, []string{"grep", "read"}) {
		t.Fatalf("with --no-tools allowed = %v", got)
	}
}
