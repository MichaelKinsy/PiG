package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func assertNoTopLevelVersionJSON(t *testing.T, output string) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document["version"]; exists {
		t.Fatalf("JSON output exposed a Pig-owned format version: %s", output)
	}
}

// D63: --version prints the composite PigVersion+UpstreamVersion.
func TestCLIVersionStringIsCompositeVersion(t *testing.T) {
	want := PigVersion + "+" + UpstreamVersion
	if got := cliVersionString(); got != want {
		t.Fatalf("cliVersionString() = %q, want %q", got, want)
	}
	if Version != want {
		t.Fatalf("Version = %q, want %q", Version, want)
	}
}

func TestAgentDirForModelUsesEnvWithoutOverride(t *testing.T) {
	orig := agentDirForModelOverride
	t.Cleanup(func() { agentDirForModelOverride = orig })
	agentDirForModelOverride = ""
	t.Setenv("PIG_CODING_AGENT_DIR", "/tmp/pig-agent-under-test")
	if got := agentDirForModel(); got != "/tmp/pig-agent-under-test" {
		t.Fatalf("agentDirForModel() = %q, want PIG_CODING_AGENT_DIR", got)
	}
}

func TestBuildInitialMessage_ConcatenatesFileEnvelopeAndFirstPrompt(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(filePath, []byte("hello from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, images, rest, err := prepareInitialMessage(dir, []string{" prompt text ", "second"}, []string{"note.txt"}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := `<file name="` + filePath + `">` + "\nhello from file\n</file>\n prompt text "
	if got != want {
		t.Fatalf("prepareInitialMessage() = %q, want %q", got, want)
	}
	if len(images) != 0 {
		t.Fatalf("prepareInitialMessage() returned %d images, want 0", len(images))
	}
	if !slices.Equal(rest, []string{"second"}) {
		t.Fatalf("remaining messages = %q, want [second]", rest)
	}
}

func TestBuildInitialMessage_MissingFileReturnsError(t *testing.T) {
	if _, _, _, err := prepareInitialMessage(t.TempDir(), []string{"prompt"}, []string{"missing.txt"}, ""); err == nil {
		t.Fatal("expected missing @file error")
	}
}

// Ports upstream test/initial-message.test.ts. Every CLI message after the
// first is returned for sending on its own; pig dropped them.
func TestBuildInitialMessage_UpstreamCases(t *testing.T) {
	for _, tc := range []struct {
		name         string
		messages     []string
		stdin, file  string
		wantInitial  string
		wantMessages []string
	}{
		{"merges piped stdin with the first CLI message into one prompt", []string{"Summarize the text given"}, "README contents\n", "", "README contents\nSummarize the text given", nil},
		{"uses stdin as the initial prompt when no CLI message is present", nil, "README contents", "", "README contents", nil},
		{"combines stdin, file text, and first CLI message in one prompt", []string{"Explain it", "Second message"}, "stdin\n", "file\n", "stdin\nfile\nExplain it", []string{"Second message"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial, _, rest := buildInitialMessage(tc.messages, tc.file, nil, tc.stdin)
			if initial != tc.wantInitial || !slices.Equal(rest, tc.wantMessages) {
				t.Fatalf("buildInitialMessage() = %q, %q; want %q, %q", initial, rest, tc.wantInitial, tc.wantMessages)
			}
		})
	}
}

// Upstream readPipedStdin resolves `data.trim() || undefined`: piped content
// is trimmed, and blank input is no content at all.
func TestReadPipedStdinTrimsContent(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"  README contents\n\n", "README contents"},
		{" \n\t\n", ""},
		{"", ""},
	} {
		path := filepath.Join(t.TempDir(), "stdin.txt")
		if err := os.WriteFile(path, []byte(tc.input), 0o644); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		oldStdin := os.Stdin
		os.Stdin = file
		got, err := readPipedStdin(context.Background())
		os.Stdin = oldStdin
		if err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		if got != tc.want {
			t.Errorf("readPipedStdin(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestVersionStringIncludesPigAndUpstreamVersions(t *testing.T) {
	got := versionString()
	if !strings.HasPrefix(got, "pig "+PigVersion+"+"+UpstreamVersion+" [") {
		t.Fatalf("versionString() does not lead with the composite version: %q", got)
	}
	if !strings.Contains(got, runtime.Version()) {
		t.Fatalf("versionString() missing Go runtime: %q", got)
	}
}

func TestResolveBuildIdentity(t *testing.T) {
	for _, test := range []struct {
		name          string
		linkerBuild   string
		moduleVersion string
		want          string
	}{
		{name: "release workflow metadata wins", linkerBuild: "abc123", moduleVersion: "v0.2.0", want: "abc123"},
		{name: "Go module release", linkerBuild: "dev", moduleVersion: "v0.2.0", want: "v0.2.0"},
		{name: "source build", linkerBuild: "dev", moduleVersion: "(devel)", want: "dev"},
		{name: "missing build info", linkerBuild: "dev", moduleVersion: "", want: "dev"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveBuildIdentity(test.linkerBuild, test.moduleVersion); got != test.want {
				t.Fatalf("resolveBuildIdentity(%q, %q) = %q, want %q", test.linkerBuild, test.moduleVersion, got, test.want)
			}
		})
	}
}

func TestDetailedVersionStringIncludesPigAndUpstreamVersions(t *testing.T) {
	got := detailedVersionString()
	// evals/harnesses.toml reads PiG's own version from the last word of
	// the first line of `pig version`.
	if first, _, _ := strings.Cut(got, "\n"); first != "pig: "+PigVersion {
		t.Fatalf("detailedVersionString() first line = %q, want %q", first, "pig: "+PigVersion)
	}
	for _, want := range []string{"pig: " + PigVersion, "upstream pi: " + UpstreamVersion, "go: " + runtime.Version()} {
		if !strings.Contains(got, want) {
			t.Fatalf("detailedVersionString() missing %q: %q", want, got)
		}
	}
}

func TestConfigureHTTPDispatcherFromSettings_UsesSettingsIdleTimeout(t *testing.T) {
	before := ai.ConfiguredHTTPIdleTimeoutMs()
	t.Cleanup(func() {
		if err := ai.ConfigureHTTPDispatcher(before); err != nil {
			t.Fatalf("restore ConfigureHTTPDispatcher: %v", err)
		}
	})

	idleTimeout := 12_345
	sm := &codingagent.SettingsManager{}
	sm.ApplyOverrides(codingagent.Settings{HTTPIdleTimeoutMs: &idleTimeout})

	if err := configureHTTPDispatcherFromSettings(sm); err != nil {
		t.Fatalf("configureHTTPDispatcherFromSettings: %v", err)
	}
	if got := ai.ConfiguredHTTPIdleTimeoutMs(); got != idleTimeout {
		t.Fatalf("ConfiguredHTTPIdleTimeoutMs() = %d, want %d", got, idleTimeout)
	}
}

func TestConfigureHTTPDispatcherFromSettings_NilUsesDefault(t *testing.T) {
	before := ai.ConfiguredHTTPIdleTimeoutMs()
	t.Cleanup(func() {
		if err := ai.ConfigureHTTPDispatcher(before); err != nil {
			t.Fatalf("restore ConfigureHTTPDispatcher: %v", err)
		}
	})

	if err := ai.ConfigureHTTPDispatcher(1); err != nil {
		t.Fatalf("prime ConfigureHTTPDispatcher: %v", err)
	}
	if err := configureHTTPDispatcherFromSettings(nil); err != nil {
		t.Fatalf("configureHTTPDispatcherFromSettings(nil): %v", err)
	}
	if got := ai.ConfiguredHTTPIdleTimeoutMs(); got != ai.DefaultHTTPIdleTimeoutMs {
		t.Fatalf("ConfiguredHTTPIdleTimeoutMs() = %d, want %d", got, ai.DefaultHTTPIdleTimeoutMs)
	}
}

func TestConfigureHTTPDispatcherFromSettings_WiresProviderRetry(t *testing.T) {
	beforeR, beforeD := ai.ConfiguredProviderRetry()
	t.Cleanup(func() {
		if err := ai.ConfigureProviderRetry(beforeR, beforeD); err != nil {
			t.Fatalf("restore ConfigureProviderRetry: %v", err)
		}
	})

	delay := 7000
	sm := &codingagent.SettingsManager{}
	sm.ApplyOverrides(codingagent.Settings{
		Retry: &codingagent.RetrySettingsJSON{
			Provider: &codingagent.ProviderRetrySettings{MaxRetries: new(2), MaxRetryDelayMs: &delay},
		},
	})

	if err := configureHTTPDispatcherFromSettings(sm); err != nil {
		t.Fatalf("configureHTTPDispatcherFromSettings: %v", err)
	}
	if gotR, gotD := ai.ConfiguredProviderRetry(); gotR != 2 || gotD != 7000 {
		t.Fatalf("ConfiguredProviderRetry() = (%d, %d), want (2, 7000)", gotR, gotD)
	}
}

// --- Pre-start piglet resolver tests ---

func TestPigletModelSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		p    *piglet.Piglet
		want string
	}{
		{"nil piglet", nil, ""},
		{"no model config", &piglet.Piglet{}, ""},
		{"model name only", &piglet.Piglet{Model: &piglet.ModelConfig{Name: "gpt-4o"}}, "gpt-4o"},
		{"provider and name", &piglet.Piglet{Model: &piglet.ModelConfig{Provider: "openai", Name: "gpt-4o"}}, "openai/gpt-4o"},
		{"name already has provider", &piglet.Piglet{Model: &piglet.ModelConfig{Provider: "openai", Name: "openai/gpt-4o"}}, "openai/gpt-4o"},
		{"provider only (no name)", &piglet.Piglet{Model: &piglet.ModelConfig{Provider: "openai"}}, ""},
		{"empty model config", &piglet.Piglet{Model: &piglet.ModelConfig{}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pigletModelSpec(tc.p); got != tc.want {
				t.Errorf("pigletModelSpec() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyPigletPreStart_NilPiglet(t *testing.T) {
	// Should not panic or modify anything.
	flags := CLIFlags{}
	applyPigletPreStart(nil, &flags)
}

func TestResolvePiglet_NoFlagNoEnv(t *testing.T) {
	// Clean env for this test.
	for _, env := range []string{"PIG_PIGLET_PATH", "PIG_PIGLET_NAME"} {
		original := os.Getenv(env)
		t.Cleanup(func() {
			if original == "" {
				_ = os.Unsetenv(env)
			} else {
				_ = os.Setenv(env, original)
			}
		})
		_ = os.Unsetenv(env)
	}

	p, baked := resolvePiglet(map[string]any{})
	if baked {
		t.Fatal("expected baked=false for stock pig")
	}
	if p != nil {
		t.Errorf("expected nil piglet with no flags/env, got %+v", p)
	}
}

func TestResolvePiglet_FromEnvPath(t *testing.T) {
	// Write a minimal piglet YAML.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	_ = os.WriteFile(path, []byte("name: test-piglet\n"), 0644)

	original := os.Getenv("PIG_PIGLET_PATH")
	t.Cleanup(func() {
		if original == "" {
			_ = os.Unsetenv("PIG_PIGLET_PATH")
		} else {
			_ = os.Setenv("PIG_PIGLET_PATH", original)
		}
	})
	_ = os.Setenv("PIG_PIGLET_PATH", path)

	p, baked := resolvePiglet(map[string]any{})
	if baked {
		t.Fatal("expected baked=false for env piglet")
	}
	if p == nil {
		t.Fatal("expected non-nil piglet from PIG_PIGLET_PATH")
	}
	if p.Name != "test-piglet" {
		t.Errorf("piglet name = %q, want %q", p.Name, "test-piglet")
	}
}

func TestResolvePiglet_FlagOverridesEnv(t *testing.T) {
	// Write two piglets.
	dir := t.TempDir()
	envPath := filepath.Join(dir, "env.yaml")
	_ = os.WriteFile(envPath, []byte("name: from-env\n"), 0644)
	flagPath := filepath.Join(dir, "flag.yaml")
	_ = os.WriteFile(flagPath, []byte("name: from-flag\n"), 0644)

	original := os.Getenv("PIG_PIGLET_PATH")
	t.Cleanup(func() {
		if original == "" {
			_ = os.Unsetenv("PIG_PIGLET_PATH")
		} else {
			_ = os.Setenv("PIG_PIGLET_PATH", original)
		}
	})
	_ = os.Setenv("PIG_PIGLET_PATH", envPath)

	p, baked := resolvePiglet(map[string]any{"piglet": flagPath})
	if baked {
		t.Fatal("expected baked=false for flag piglet")
	}
	if p == nil {
		t.Fatal("expected non-nil piglet from --piglet flag")
	}
	if p.Name != "from-flag" {
		t.Errorf("piglet name = %q, want %q (flag should override env)", p.Name, "from-flag")
	}
}

// TestEnforcePigletBinaryScope_StockAllowsPigletFlag proves the Piglet Binary
// scope guard does not over-trigger: stock pig (no baked closure) accepts
// --piglet normally. Reaching the assertion means the guard did not os.Exit.
func TestEnforcePigletBinaryScope_StockAllowsPigletFlag(t *testing.T) {
	enforcePigletBinaryScope(map[string]any{"piglet": "some-other"})
}

// TestResolvePigletResolvesExtends proves the runtime startup path receives the
// effective composition rather than silently ignoring the child's base.
func TestResolvePigletResolvesExtends(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.yaml")
	child := filepath.Join(dir, "child.yaml")
	if err := os.WriteFile(base, []byte("name: base\ndescription: inherited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("name: child\nextends:\n  source: local:./base.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, baked := resolvePiglet(map[string]any{"piglet": child})
	if p == nil || baked || p.Name != "child" || p.Description != "inherited" || p.Extends != nil {
		t.Fatalf("runtime Piglet = %#v baked=%t", p, baked)
	}
}
