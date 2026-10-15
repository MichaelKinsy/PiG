package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
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

// versionFormatCase is one PIG_VERSION_FORMAT state: unset, set to a value, or set to the empty string.
type versionFormatCase struct {
	name, format, want string
	set                bool
}

// versionFormatCases lists the states; the pi case wants Pi's own `--version` output, the package version of the pinned build.
func versionFormatCases() []versionFormatCase {
	return []versionFormatCase{
		{name: "default", want: Version},
		{name: "empty", format: "", want: Version, set: true},
		{name: "unrecognized", format: "upstream", want: Version, set: true},
		{name: "uppercase", format: "PI", want: Version, set: true},
		{name: "padded", format: " pi", want: Version, set: true},
		{name: "pi", format: "pi", want: UpstreamPackageVersion, set: true},
	}
}

// setVersionFormat applies one case to this process and restores the caller's value when the test ends.
func (c versionFormatCase) setVersionFormat(t *testing.T) {
	t.Helper()
	t.Setenv("PIG_VERSION_FORMAT", c.format)
	if !c.set {
		if err := os.Unsetenv("PIG_VERSION_FORMAT"); err != nil {
			t.Fatal(err)
		}
	}
}

// childEnv is the environment for a pig subprocess: the caller's environment without PIG_VERSION_FORMAT, the case applied,
// and every variable that selects the agent directory pointed at a fresh temporary directory.
func (c versionFormatCase) childEnv(t *testing.T) []string {
	t.Helper()
	managed := []string{"PIG_VERSION_FORMAT", "HOME", "PIG_HOME", "PI_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"}
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.Contains(managed, name)
	})
	if c.set {
		env = append(env, "PIG_VERSION_FORMAT="+c.format)
	}
	for _, name := range managed[1:] {
		env = append(env, name+"="+t.TempDir())
	}
	return env
}

// D63: --version prints the composite PigVersion+UpstreamVersion unless the caller explicitly requests Pi's version format.
func TestCLIVersionStringFormat(t *testing.T) {
	for _, test := range versionFormatCases() {
		t.Run(test.name, func(t *testing.T) {
			test.setVersionFormat(t)
			if got := cliVersionString(); got != test.want {
				t.Fatalf("cliVersionString() = %q, want %q", got, test.want)
			}
		})
	}
	if want := PigVersion + "+" + strings.ReplaceAll(UpstreamVersion, "+", "."); Version != want {
		t.Fatalf("Version = %q, want %q", Version, want)
	}
}

func TestVersionFlagsHonorPiFormatWithoutChangingDetailedVersion(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, test := range versionFormatCases() {
		for _, flag := range []string{"--version", "-v"} {
			t.Run(test.name+"/"+flag, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), binary, flag)
				cmd.Env = test.childEnv(t)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				output, err := cmd.Output()
				if err != nil || stderr.Len() != 0 || string(output) != test.want+"\n" {
					t.Fatalf("exit error=%v stdout=%q stderr=%q, want %q", err, output, stderr.String(), test.want+"\n")
				}
			})
		}
	}

	cmd := exec.CommandContext(t.Context(), binary, "version")
	cmd.Env = versionFormatCase{format: "pi", set: true}.childEnv(t)
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pig: " + PigVersion + "\n", "upstream pi: " + UpstreamVersion + "\n", "go: ", "platform: "} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("pig version = %q, missing %q", output, want)
		}
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
	want := `<file name="` + filePath + `">` + "\nhello from file\n\n</file>\n prompt text "
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

// Ports packages/coding-agent/test/initial-message.test.ts:15,26,37 with the exact stdin, file text, CLI messages, and remaining-message expectations.
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
	if !strings.HasPrefix(got, "pig "+PigVersion+"+"+strings.ReplaceAll(UpstreamVersion, "+", ".")+" [") {
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
	// test/evals/harnesses.toml reads PiG's own version from the last word of
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
	flags := Args{}
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

	p, baked := resolvePiglet(map[string]any{}, nil)
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

	p, baked := resolvePiglet(map[string]any{}, nil)
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

	p, baked := resolvePiglet(map[string]any{"piglet": flagPath}, nil)
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
	p, baked := resolvePiglet(map[string]any{"piglet": child}, nil)
	if p == nil || baked || p.Name != "child" || p.Description != "inherited" || p.Extends != nil {
		t.Fatalf("runtime Piglet = %#v baked=%t", p, baked)
	}
}

// --piglet <name> resolves the name through the Piglet search paths with the run's trust override: a bare name is never read as a file in the
// working directory, an untrusted project's .pig/piglets Piglet does not shadow the user's, and --approve (override true) selects it.
func TestResolvePigletAppliesTheTrustOverrideAndNeverReadsABareNameFromTheWorkingDirectory(t *testing.T) {
	t.Setenv("PIG_PIGLET_PATH", "")
	t.Setenv("PIG_PIGLET_NAME", "")
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv(codingagent.ENV_AGENT_DIR, t.TempDir())
	project := t.TempDir()
	t.Chdir(project)
	write := func(path, name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("name: "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(project, "planted"), "from-working-directory")
	if p, _ := resolvePiglet(map[string]any{"piglet": "planted"}, nil); p != nil {
		t.Fatalf("--piglet planted read the working directory's file %s", p.Name)
	}

	write(filepath.Join(codingagent.PigletsDir(), "demo.yaml"), "from-user")
	write(filepath.Join(codingagent.ProjectConfigDir(project), "piglets", "demo.yaml"), "from-workspace")
	for _, tc := range []struct {
		name     string
		override *bool
		want     string
	}{
		{name: "undecided", override: nil, want: "from-user"},
		{name: "--approve", override: new(true), want: "from-workspace"},
		{name: "--no-approve", override: new(false), want: "from-user"},
	} {
		got := "<nil>"
		if p, _ := resolvePiglet(map[string]any{"piglet": "demo"}, tc.override); p != nil {
			got = p.Name
		}
		if got != tc.want {
			t.Fatalf("%s: --piglet demo = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Pi's main.ts configures the dispatcher with settingsManager.getHttpIdleTimeoutMs() only; retry.provider.timeoutMs becomes the request's
// timeoutMs (sdk.ts buildRequestOptions), not the dispatcher's idle timeout. Probed: with retry.provider.timeoutMs 1500 and a google
// endpoint that never answers (google passes no request timeout), Pi 1.1.0 waits on the 5-minute dispatcher; PiG failed after 1.5 s.
func TestConfigureHTTPDispatcherFromSettings_ProviderTimeoutIsNotTheIdleTimeout(t *testing.T) {
	before := ai.ConfiguredHTTPIdleTimeoutMs()
	t.Cleanup(func() {
		if err := ai.ConfigureHTTPDispatcher(before); err != nil {
			t.Fatalf("restore ConfigureHTTPDispatcher: %v", err)
		}
	})
	idleTimeout, providerTimeout := 23_456, 1_500
	sm := &codingagent.SettingsManager{}
	sm.ApplyOverrides(codingagent.Settings{
		HTTPIdleTimeoutMs: &idleTimeout,
		Retry:             &codingagent.RetrySettingsJSON{Provider: &codingagent.ProviderRetrySettings{TimeoutMs: &providerTimeout}},
	})
	if err := configureHTTPDispatcherFromSettings(sm); err != nil {
		t.Fatalf("configureHTTPDispatcherFromSettings: %v", err)
	}
	if got := ai.ConfiguredHTTPIdleTimeoutMs(); got != idleTimeout {
		t.Fatalf("ConfiguredHTTPIdleTimeoutMs() = %d, want httpIdleTimeoutMs %d", got, idleTimeout)
	}
}
