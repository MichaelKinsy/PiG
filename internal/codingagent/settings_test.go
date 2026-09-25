package codingagent

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPackageSource_JSONRoundTrip_StringShape(t *testing.T) {
	data, err := json.Marshal(PackageSource{Source: "npm:@foo/bar"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `"npm:@foo/bar"` {
		t.Fatalf("Marshal = %s", got)
	}
	var src PackageSource
	if err := json.Unmarshal(data, &src); err != nil {
		t.Fatal(err)
	}
	if src.Source != "npm:@foo/bar" || src.Filtered() {
		t.Fatalf("src = %+v", src)
	}
}

func TestSettingsManagerUntrustedProjectIgnoresAndCannotWriteProjectSettings(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultModel":"global"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, CONFIG_DIR_NAME, "settings.json"), []byte(`{"defaultModel":"project","defaultProjectTrust":"always"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	if sm.IsProjectTrusted() {
		t.Fatal("untrusted settings manager reports trusted")
	}
	if got := sm.Get().DefaultModel; got != "global" {
		t.Fatalf("merged default model = %q, want global", got)
	}
	if got := sm.GetProjectSettings(); got.DefaultModel != "" || got.DefaultProjectTrust != "" {
		t.Fatalf("project settings leaked while untrusted: %+v", got)
	}
	if err := sm.UpdateProject(func(s *Settings) { s.DefaultModel = "written" }); err == nil {
		t.Fatal("UpdateProject succeeded while untrusted")
	}

	sm.SetProjectTrusted(true)
	if !sm.IsProjectTrusted() || sm.Get().DefaultModel != "project" {
		t.Fatalf("trusted reload did not expose project settings: %+v", sm.Get())
	}
}

func TestPackageSource_JSONRoundTrip_ObjectShape(t *testing.T) {
	data, err := json.Marshal(PackageSource{Source: "npm:@foo/bar", Skills: []string{"alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, `"source":"npm:@foo/bar"`) || !strings.Contains(got, `"skills":["alpha"]`) {
		t.Fatalf("Marshal = %s", got)
	}
	var src PackageSource
	if err := json.Unmarshal(data, &src); err != nil {
		t.Fatal(err)
	}
	if src.Source != "npm:@foo/bar" || !slices.Equal(src.Skills, []string{"alpha"}) {
		t.Fatalf("src = %+v", src)
	}
}

func TestPackageSource_Filtered_ExplicitEmptyArraysStayObjectShape(t *testing.T) {
	src := PackageSource{Source: "npm:@foo/bar", Prompts: []string{}}
	if !src.Filtered() {
		t.Fatal("explicit empty filter arrays should count as filtered")
	}
	data, err := json.Marshal(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, `"source":"npm:@foo/bar"`) || !strings.Contains(got, `"prompts":[]`) {
		t.Fatalf("Marshal = %s", got)
	}
}

func writeModelsJSON(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestModelRegistryResolvesAPIKeyFromEnv proves wiring at the
// ModelRegistry call site: a models.json entry whose APIKey is an explicit
// "$ENV_VAR" reference resolves to the env value when read via Resolve.
// Mirrors upstream model-registry.ts (v0.78.1), where bare names are
// literals and env references require the "$" sigil.
func TestModelRegistryResolvesAPIKeyFromEnv(t *testing.T) {
	t.Setenv("PIG_TEST_REG_KEY", "sk-from-env-via-registry")

	dir := t.TempDir()
	writeModelsJSON(t, dir, `{
		"providers": {
			"openai": {
				"apiKey": "$PIG_TEST_REG_KEY",
				"models": [{"id": "gpt-4o"}]
			}
		}
	}`)

	r := NewModelRegistry(dir)
	got, ok := r.Resolve("openai", "gpt-4o")
	if !ok {
		t.Fatalf("Resolve missed openai/gpt-4o")
	}
	if got.APIKey != "sk-from-env-via-registry" {
		t.Errorf("APIKey not resolved; got %q want sk-from-env-via-registry", got.APIKey)
	}
}

// TestModelRegistryResolvesBangCmd proves the !cmd path is honored
// at the registry call site.
func TestModelRegistryResolvesBangCmd(t *testing.T) {
	dir := t.TempDir()
	writeModelsJSON(t, dir, `{
		"providers": {
			"anthropic": {
				"apiKey": "!echo sk-from-shell",
				"models": [{"id": "claude-x"}]
			}
		}
	}`)

	r := NewModelRegistry(dir)
	got, ok := r.Resolve("anthropic", "claude-x")
	if !ok {
		t.Fatalf("Resolve missed anthropic/claude-x")
	}
	if got.APIKey != "sk-from-shell" {
		t.Errorf("!cmd not executed; got %q", got.APIKey)
	}
}

// TestModelRegistryLiteralAPIKeyPreserved proves a literal key (env-var
// unset, no !cmd prefix) flows through verbatim.
func TestModelRegistryLiteralAPIKeyPreserved(t *testing.T) {
	dir := t.TempDir()
	writeModelsJSON(t, dir, `{
		"providers": {
			"openai": {
				"apiKey": "sk-literal-xyz",
				"models": [{"id": "gpt-4o"}]
			}
		}
	}`)

	r := NewModelRegistry(dir)
	got, _ := r.Resolve("openai", "gpt-4o")
	if got.APIKey != "sk-literal-xyz" {
		t.Errorf("literal must pass through; got %q", got.APIKey)
	}
}

// ─── Compaction settings ──────────────────────────────────────────────────────

// TestGetCompactionSettings_Defaults proves zero-value Settings returns the
// canonical defaults (Enabled=true, ReserveTokens=16384, KeepRecentTokens=20000).
func TestGetCompactionSettings_Defaults(t *testing.T) {
	sm := &SettingsManager{}
	got := sm.GetCompactionSettings()
	if !got.Enabled {
		t.Errorf("Enabled: want true, got false")
	}
	if got.ReserveTokens != 16384 {
		t.Errorf("ReserveTokens: want 16384, got %d", got.ReserveTokens)
	}
	if got.KeepRecentTokens != 20000 {
		t.Errorf("KeepRecentTokens: want 20000, got %d", got.KeepRecentTokens)
	}
}

func TestSettingsManager_SetProjectPackages(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sm := NewSettingsManager(cwd, agentDir)
	pkgs := []PackageSource{{Source: "npm:@foo/bar"}, {Source: "git:https://example.com/repo", Themes: []string{"dark"}}}
	if err := sm.SetProjectPackages(pkgs); err != nil {
		t.Fatal(err)
	}
	got := sm.GetProjectSettings().Packages
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Source != "npm:@foo/bar" || got[1].Source != "git:https://example.com/repo" {
		t.Fatalf("got = %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(cwd, ".pig", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"packages"`) {
		t.Fatalf("settings missing packages: %s", data)
	}
}

func TestSettingsUnmarshalJSON_MigratesQueueModeAndWebsockets(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"queueMode":"all","websockets":true}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.SteeringMode != "all" {
		t.Fatalf("SteeringMode = %q, want all", s.SteeringMode)
	}
	if s.Transport != "websocket" {
		t.Fatalf("Transport = %q, want websocket", s.Transport)
	}
}

func TestSettingsUnmarshalJSON_MigratesLegacySkillsObject(t *testing.T) {
	var s Settings
	if err := json.Unmarshal([]byte(`{"skills":{"enableSkillCommands":false,"customDirectories":["/tmp/skills-a","/tmp/skills-b"]}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.EnableSkillCommands == nil || *s.EnableSkillCommands != false {
		t.Fatalf("EnableSkillCommands = %#v, want false", s.EnableSkillCommands)
	}
	if !slices.Equal(s.Skills, []string{"/tmp/skills-a", "/tmp/skills-b"}) {
		t.Fatalf("Skills = %v", s.Skills)
	}
}

func TestSettingsManager_GetSessionDir_ExpandsHomeForms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	sm := &SettingsManager{merged: Settings{SessionDir: "~/sessions"}}
	if got, _ := sm.GetSessionDir(); got != filepath.Join(home, "sessions") {
		t.Fatalf("GetSessionDir() = %q, want %q", got, filepath.Join(home, "sessions"))
	}

	sm = &SettingsManager{merged: Settings{SessionDir: "~"}}
	if got, _ := sm.GetSessionDir(); got != home {
		t.Fatalf("GetSessionDir() = %q, want %q", got, home)
	}
}

func TestSettingsManager_GettersExposeUpstreamHelperSurface(t *testing.T) {
	falseVal := false
	trueVal := true
	idleTimeout := 12_345
	sm := &SettingsManager{merged: Settings{
		Compaction:           &CompactionSettingsJSON{Enabled: &falseVal, ReserveTokens: new(8192), KeepRecentTokens: new(4096)},
		BranchSummary:        &BranchSummaryConfig{SkipPrompt: true},
		Retry:                &RetrySettingsJSON{Enabled: &falseVal},
		ShowTerminalProgress: &trueVal,
		HTTPIdleTimeoutMs:    &idleTimeout,
	}}
	if sm.GetCompactionEnabled() {
		t.Fatal("GetCompactionEnabled() = true, want false")
	}
	if got := sm.GetCompactionReserveTokens(); got != 8192 {
		t.Fatalf("GetCompactionReserveTokens() = %d, want 8192", got)
	}
	if got := sm.GetCompactionKeepRecentTokens(); got != 4096 {
		t.Fatalf("GetCompactionKeepRecentTokens() = %d, want 4096", got)
	}
	if !sm.GetBranchSummarySkipPrompt() {
		t.Fatal("GetBranchSummarySkipPrompt() = false, want true")
	}
	if sm.GetRetryEnabled() {
		t.Fatal("GetRetryEnabled() = true, want false")
	}
	if !sm.GetShowTerminalProgress() {
		t.Fatal("GetShowTerminalProgress() = false, want true")
	}
	if got := mustTimeout(t)(sm.GetHttpIdleTimeoutMs()); got != 12_345 {
		t.Fatalf("GetHttpIdleTimeoutMs() = %d, want 12345", got)
	}
}

func TestSettingsManager_GetHttpIdleTimeoutMs_DefaultAndMergedOverride(t *testing.T) {
	sm := &SettingsManager{}
	if got := mustTimeout(t)(sm.GetHttpIdleTimeoutMs()); got != defaultHTTPIdleTimeoutMs {
		t.Fatalf("GetHttpIdleTimeoutMs() = %d, want %d", got, defaultHTTPIdleTimeoutMs)
	}

	global := 300_000
	project := 0
	sm = &SettingsManager{merged: Settings{HTTPIdleTimeoutMs: &project}, global: Settings{HTTPIdleTimeoutMs: &global}}
	if got := mustTimeout(t)(sm.GetHttpIdleTimeoutMs()); got != 0 {
		t.Fatalf("GetHttpIdleTimeoutMs() = %d, want 0", got)
	}
}

// retry.provider.timeoutMs overrides httpIdleTimeoutMs for the effective
// per-request stream timeout (upstream sdk.ts:311). When unset, falls back to
// httpIdleTimeoutMs (or its default).
func TestSettingsManager_GetProviderRequestTimeoutMs(t *testing.T) {
	// No retry.provider: falls back to httpIdleTimeoutMs default.
	sm := &SettingsManager{}
	if got := mustTimeout(t)(sm.GetProviderRequestTimeoutMs()); got != defaultHTTPIdleTimeoutMs {
		t.Fatalf("fallback default = %d, want %d", got, defaultHTTPIdleTimeoutMs)
	}

	// httpIdleTimeoutMs set, no provider override: uses httpIdleTimeoutMs.
	idle := 12_345
	sm = &SettingsManager{merged: Settings{HTTPIdleTimeoutMs: &idle}}
	if got := mustTimeout(t)(sm.GetProviderRequestTimeoutMs()); got != 12_345 {
		t.Fatalf("fallback to idle = %d, want 12345", got)
	}

	// retry.provider.timeoutMs set: overrides httpIdleTimeoutMs.
	sm = &SettingsManager{merged: Settings{
		HTTPIdleTimeoutMs: &idle,
		Retry:             &RetrySettingsJSON{Provider: &ProviderRetrySettings{TimeoutMs: new(90_000)}},
	}}
	if got := mustTimeout(t)(sm.GetProviderRequestTimeoutMs()); got != 90_000 {
		t.Fatalf("provider override = %d, want 90000", got)
	}
}

// retry.provider.maxRetryDelayMs distinguishes unset (nil -> 60s default cap)
// from an explicit 0, which disables the cap, mirroring upstream
// provider-retry.ts (maxRetryDelayMs ?? 60000; "set it to zero to disable the
// limit"). A plain int collapsed explicit 0 into the default, hiding the
// disable-cap intent from the provider retry transport.
func TestSettingsManager_GetProviderRetrySettings_MaxRetryDelayMs(t *testing.T) {
	zero := 0
	five := 5000
	cases := []struct {
		name  string
		field *int
		want  int
	}{
		{"unset defaults to 60s cap", nil, 60000},
		{"explicit zero disables cap", &zero, 0},
		{"explicit value passes through", &five, 5000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := &SettingsManager{merged: Settings{
				Retry: &RetrySettingsJSON{Provider: &ProviderRetrySettings{MaxRetryDelayMs: tc.field}},
			}}
			if got := sm.GetProviderRetrySettings().MaxRetryDelayMs; got != tc.want {
				t.Fatalf("MaxRetryDelayMs = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSettingsManager_IsInstallTelemetryEnabled(t *testing.T) {
	enabled := true
	disabled := false

	// Env unset: the setting decides (default true when absent).
	if orig, ok := os.LookupEnv("PI_TELEMETRY"); ok {
		_ = os.Unsetenv("PI_TELEMETRY")
		t.Cleanup(func() { _ = os.Setenv("PI_TELEMETRY", orig) })
	}
	if !(&SettingsManager{}).IsInstallTelemetryEnabled() {
		t.Error("default (no setting, env unset) should be enabled")
	}
	if (&SettingsManager{merged: Settings{EnableInstallTelemetry: &disabled}}).IsInstallTelemetryEnabled() {
		t.Error("setting=false, env unset should be disabled")
	}

	// Env override wins over the setting in both directions.
	off := &SettingsManager{merged: Settings{EnableInstallTelemetry: &disabled}}
	on := &SettingsManager{merged: Settings{EnableInstallTelemetry: &enabled}}
	t.Setenv("PI_TELEMETRY", "1")
	if !off.IsInstallTelemetryEnabled() {
		t.Error("env=1 should override setting=false")
	}
	t.Setenv("PI_TELEMETRY", "false")
	if on.IsInstallTelemetryEnabled() {
		t.Error("env=false should override setting=true")
	}
	t.Setenv("PI_TELEMETRY", "maybe")
	if on.IsInstallTelemetryEnabled() {
		t.Error("non-truthy env value should be falsy")
	}
}

// TestSettingsManager_GetHttpIdleTimeoutMs_InvalidIsAnError mirrors upstream
// parseTimeoutSetting, which throws an Error (not a crash) for an invalid
// value (CFG-10).
func TestSettingsManager_GetHttpIdleTimeoutMs_InvalidIsAnError(t *testing.T) {
	invalid := -1
	sm := &SettingsManager{merged: Settings{HTTPIdleTimeoutMs: &invalid}}
	if _, err := sm.GetHttpIdleTimeoutMs(); err == nil || err.Error() != "Invalid httpIdleTimeoutMs setting: -1" {
		t.Fatalf("GetHttpIdleTimeoutMs() error = %v", err)
	}
}

// mustTimeout unwraps a timeout getter's result, failing the test on error.
func mustTimeout(t *testing.T) func(int, error) int {
	return func(timeoutMs int, err error) int {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return timeoutMs
	}
}

func TestSettingsManager_SetHttpIdleTimeoutMs_FloorsAndPersists(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sm := NewSettingsManager(cwd, agentDir)
	if err := sm.SetHttpIdleTimeoutMs(12_345.9); err != nil {
		t.Fatalf("SetHttpIdleTimeoutMs() error = %v", err)
	}
	if got := mustTimeout(t)(sm.GetHttpIdleTimeoutMs()); got != 12_345 {
		t.Fatalf("GetHttpIdleTimeoutMs() = %d, want 12345", got)
	}

	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"httpIdleTimeoutMs": 12345`) {
		t.Fatalf("settings missing floored httpIdleTimeoutMs: %s", data)
	}
}

func TestSettingsManager_SetHttpIdleTimeoutMs_RejectsInvalid(t *testing.T) {
	sm := &SettingsManager{}
	for _, tc := range []float64{-1, math.NaN(), math.Inf(1)} {
		if err := sm.SetHttpIdleTimeoutMs(tc); err == nil {
			t.Fatalf("SetHttpIdleTimeoutMs(%v) error = nil, want non-nil", tc)
		}
	}
}

func TestSettingsManager_ApplyOverridesIsNonPersistent(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sm := NewSettingsManager(cwd, agentDir)
	sm.ApplyOverrides(Settings{Theme: "light", Transport: "websocket"})
	if got := sm.GetTheme(); got != "light" {
		t.Fatalf("GetTheme() = %q, want light", got)
	}
	if got := sm.GetTransport(); got != "websocket" {
		t.Fatalf("GetTransport() = %q, want websocket", got)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("ApplyOverrides should not persist settings, stat err = %v", err)
	}
	if err := sm.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
}

func TestSettingsManager_InMemorySeedsInitialSettings(t *testing.T) {
	showProgress := true
	s := Settings{Theme: "dark", ShowTerminalProgress: &showProgress}
	mgr := &SettingsManager{global: s, merged: s}
	got := mgr.Get()
	if got.Theme != "dark" {
		t.Fatalf("Theme = %q, want dark", got.Theme)
	}
	if !got.GetShowTerminalProgress() {
		t.Fatal("GetShowTerminalProgress() = false, want true")
	}
}

func TestSettingsManager_GetReturnsClonedSettings(t *testing.T) {
	showImages := true
	sm := &SettingsManager{merged: Settings{
		Extensions:    []string{"/a"},
		ShowImages:    &showImages,
		Compaction:    &CompactionSettingsJSON{ReserveTokens: new(10)},
		NpmCommand:    []string{"npm"},
		Packages:      []PackageSource{{Source: "npm:foo", Prompts: []string{}}},
		EnabledModels: []string{"a"},
	}}
	got := sm.Get()
	got.Extensions[0] = "/mutated"
	*got.ShowImages = false
	*got.Compaction.ReserveTokens = 99
	got.NpmCommand[0] = "pnpm"
	got.Packages[0].Source = "npm:bar"
	got.EnabledModels[0] = "b"

	again := sm.Get()
	if again.Extensions[0] != "/a" {
		t.Fatalf("Extensions leaked mutation: %v", again.Extensions)
	}
	if !*again.ShowImages {
		t.Fatal("ShowImages leaked mutation")
	}
	if *again.Compaction.ReserveTokens != 10 {
		t.Fatalf("Compaction leaked mutation: %+v", again.Compaction)
	}
	if again.NpmCommand[0] != "npm" {
		t.Fatalf("NpmCommand leaked mutation: %v", again.NpmCommand)
	}
	if again.Packages[0].Source != "npm:foo" {
		t.Fatalf("Packages leaked mutation: %+v", again.Packages)
	}
	if again.EnabledModels[0] != "a" {
		t.Fatalf("EnabledModels leaked mutation: %v", again.EnabledModels)
	}
}

func TestMergeSettings_ProjectCanOverrideWithEmptySlices(t *testing.T) {
	global := Settings{
		Extensions:    []string{"/a"},
		Packages:      []PackageSource{{Source: "npm:foo"}},
		EnabledModels: []string{"model-a"},
		NpmCommand:    []string{"npm"},
	}
	project := Settings{
		Extensions:    []string{},
		Packages:      []PackageSource{},
		EnabledModels: []string{},
		NpmCommand:    []string{},
	}
	merged := mergeSettings(global, project)
	if merged.Extensions == nil || len(merged.Extensions) != 0 {
		t.Fatalf("Extensions = %#v, want explicit empty override", merged.Extensions)
	}
	if merged.Packages == nil || len(merged.Packages) != 0 {
		t.Fatalf("Packages = %#v, want explicit empty override", merged.Packages)
	}
	if merged.EnabledModels == nil || len(merged.EnabledModels) != 0 {
		t.Fatalf("EnabledModels = %#v, want explicit empty override", merged.EnabledModels)
	}
	if merged.NpmCommand == nil || len(merged.NpmCommand) != 0 {
		t.Fatalf("NpmCommand = %#v, want explicit empty override", merged.NpmCommand)
	}
}

func TestMergeSettings_MergesMissingScalarAndPointerOverrides(t *testing.T) {
	telemetry := false
	hardwareCursor := true
	padding := 2
	maxVisible := 9
	merged := mergeSettings(Settings{}, Settings{
		Transport:              "websocket",
		EnableInstallTelemetry: &telemetry,
		ShowHardwareCursor:     &hardwareCursor,
		EditorPaddingX:         &padding,
		AutocompleteMaxVisible: &maxVisible,
	})
	if merged.Transport != "websocket" {
		t.Fatalf("Transport = %q, want websocket", merged.Transport)
	}
	if merged.EnableInstallTelemetry == nil || *merged.EnableInstallTelemetry != false {
		t.Fatalf("EnableInstallTelemetry = %#v, want false", merged.EnableInstallTelemetry)
	}
	if merged.ShowHardwareCursor == nil || *merged.ShowHardwareCursor != true {
		t.Fatalf("ShowHardwareCursor = %#v, want true", merged.ShowHardwareCursor)
	}
	if merged.EditorPaddingX == nil || *merged.EditorPaddingX != 2 {
		t.Fatalf("EditorPaddingX = %#v, want 2", merged.EditorPaddingX)
	}
	if merged.AutocompleteMaxVisible == nil || *merged.AutocompleteMaxVisible != 9 {
		t.Fatalf("AutocompleteMaxVisible = %#v, want 9", merged.AutocompleteMaxVisible)
	}
}

func TestSettingsManager_DrainErrorsOnInvalidSettingsFiles(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultModel":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pig", "settings.json"), []byte(`{"theme":`), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := NewSettingsManager(cwd, agentDir)
	errs := sm.DrainErrors()
	if len(errs) != 2 {
		t.Fatalf("DrainErrors len = %d, want 2", len(errs))
	}
	if got := sm.DrainErrors(); len(got) != 0 {
		t.Fatalf("second DrainErrors = %v, want empty", got)
	}
}

func TestSettingsManager_UpdateGlobalRefusesToClobberInvalidFile(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	path := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"defaultModel":`), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := NewSettingsManager(cwd, agentDir)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sm.SetTheme("dark"); err == nil {
		t.Fatal("SetTheme unexpectedly succeeded with invalid global settings file")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid settings file was clobbered")
	}
}

func TestSettingsManager_UpdateGlobalPreservesExternalUnrelatedChanges(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	path := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","packages":["npm:old"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(cwd, agentDir)
	if err := os.WriteFile(path, []byte(`{"theme":"dark","packages":[],"externalField":"preserve"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetTheme("light"); err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if packages, ok := persisted["packages"].([]any); !ok || len(packages) != 0 {
		t.Fatalf("external packages change was clobbered: %#v", persisted)
	}
	if persisted["externalField"] != "preserve" || persisted["theme"] != "light" {
		t.Fatalf("persisted settings = %#v", persisted)
	}
}

func TestSettingsManager_UpdateProjectPreservesExternalUnrelatedChanges(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	projectDir := filepath.Join(cwd, CONFIG_DIR_NAME)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"extensions":["old"],"prompts":["old"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(cwd, agentDir)
	if err := os.WriteFile(path, []byte(`{"extensions":["external"],"prompts":["new"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetProjectExtensionPaths([]string{"managed"}); err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(anyStrings(persisted["extensions"]), []string{"managed"}) || !slices.Equal(anyStrings(persisted["prompts"]), []string{"new"}) {
		t.Fatalf("persisted project settings = %#v", persisted)
	}
}

func TestSettingsManager_NestedSetterPreservesExternalSiblingChange(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	path := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"terminal":{"showImages":true,"imageWidthCells":60}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(cwd, agentDir)
	if err := os.WriteFile(path, []byte(`{"terminal":{"showImages":true,"imageWidthCells":90}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetShowImages(false); err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Terminal struct {
			ShowImages      bool `json:"showImages"`
			ImageWidthCells int  `json:"imageWidthCells"`
		} `json:"terminal"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Terminal.ShowImages || persisted.Terminal.ImageWidthCells != 90 {
		t.Fatalf("persisted terminal settings = %#v", persisted.Terminal)
	}
}

func TestSettingsManager_ConcurrentManagersPreserveDistinctWrites(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	first := NewSettingsManager(cwd, agentDir)
	second := NewSettingsManager(cwd, agentDir)
	start := make(chan struct{})
	errors := make(chan error, 2)
	go func() {
		<-start
		errors <- first.SetTheme("light")
	}()
	go func() {
		<-start
		errors <- second.SetDefaultModel("model")
	}()
	close(start)
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	reloaded := NewSettingsManager(cwd, agentDir)
	if reloaded.GetTheme() != "light" || reloaded.GetDefaultModel() != "model" {
		t.Fatalf("concurrent writes lost: %+v", reloaded.GetGlobalSettings())
	}
}

func anyStrings(value any) []string {
	values, _ := value.([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		result = append(result, text)
	}
	return result
}

// TestGetCompactionSettings_DisabledOverride proves CompactionEnabled=false
// overrides the default Enabled=true.
func TestGetCompactionSettings_DisabledOverride(t *testing.T) {
	sm := &SettingsManager{merged: Settings{Compaction: &CompactionSettingsJSON{Enabled: new(false)}}}
	got := sm.GetCompactionSettings()
	if got.Enabled {
		t.Errorf("Enabled: want false, got true")
	}
}

// TestGetCompactionSettings_ReserveTokensOverride proves a non-zero
// ReserveTokens overrides the default 16384.
func TestGetCompactionSettings_ReserveTokensOverride(t *testing.T) {
	sm := &SettingsManager{merged: Settings{Compaction: &CompactionSettingsJSON{ReserveTokens: new(8192)}}}
	got := sm.GetCompactionSettings()
	if got.ReserveTokens != 8192 {
		t.Errorf("ReserveTokens: want 8192, got %d", got.ReserveTokens)
	}
}

// TestGetBranchSummarySettings_Default proves zero-value Settings returns
// BranchSummaryConfig{ReserveTokens:16384}.
func TestGetBranchSummarySettings_Default(t *testing.T) {
	sm := &SettingsManager{}
	got := sm.GetBranchSummarySettings()
	if got.ReserveTokens != 16384 {
		t.Errorf("ReserveTokens: want 16384, got %d", got.ReserveTokens)
	}
}

// ─── models.json upstream schema tests ────────────────────────────────────────

func TestModelRegistryUpstreamSchema_OllamaCompat(t *testing.T) {
	dir := t.TempDir()
	writeModelsJSON(t, dir, `{
		"providers": {
			"ollama": {
				"baseUrl": "http://localhost:11434/v1",
				"api": "openai-completions",
				"apiKey": "ollama",
				"compat": {
					"supportsDeveloperRole": false,
					"supportsReasoningEffort": false
				},
				"models": [
					{ "id": "llama3.1:8b" },
					{ "id": "qwen2.5-coder:7b" }
				]
			}
		}
	}`)

	r := NewModelRegistry(dir)

	// Resolve first model.
	got, ok := r.Resolve("ollama", "llama3.1:8b")
	if !ok {
		t.Fatal("Resolve missed ollama/llama3.1:8b")
	}
	if got.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("BaseURL = %q, want http://localhost:11434/v1", got.BaseURL)
	}
	if got.APIKey != "ollama" {
		t.Errorf("APIKey = %q, want ollama", got.APIKey)
	}
	if got.Compat == nil {
		t.Fatal("Compat is nil, want non-nil")
	}
	if got.Compat.SupportsDeveloperRole == nil || *got.Compat.SupportsDeveloperRole != false {
		t.Error("SupportsDeveloperRole should be false")
	}
	if got.Compat.SupportsReasoningEffort == nil || *got.Compat.SupportsReasoningEffort != false {
		t.Error("SupportsReasoningEffort should be false")
	}

	// Resolve second model: same provider config.
	got2, ok := r.Resolve("ollama", "qwen2.5-coder:7b")
	if !ok {
		t.Fatal("Resolve missed ollama/qwen2.5-coder:7b")
	}
	if got2.BaseURL != got.BaseURL {
		t.Errorf("second model BaseURL mismatch")
	}
}

func TestModelRegistryUpstreamSchema_LiteLLMProxy(t *testing.T) {
	dir := t.TempDir()
	writeModelsJSON(t, dir, `{
		"providers": {
			"litellm": {
				"baseUrl": "http://localhost:4000/v1",
				"api": "openai-completions",
				"apiKey": "sk-test-key",
				"compat": {
					"supportsDeveloperRole": false,
					"supportsReasoningEffort": false,
					"supportsUsageInStreaming": false
				},
				"models": [
					{ "id": "gpt-4o" },
					{ "id": "claude-3-5-sonnet", "reasoning": true }
				]
			}
		}
	}`)

	r := NewModelRegistry(dir)

	got, ok := r.Resolve("litellm", "claude-3-5-sonnet")
	if !ok {
		t.Fatal("Resolve missed litellm/claude-3-5-sonnet")
	}
	if !got.Reasoning {
		t.Error("Reasoning should be true")
	}
	if got.Compat == nil || got.Compat.SupportsUsageInStreaming == nil || *got.Compat.SupportsUsageInStreaming != false {
		t.Error("SupportsUsageInStreaming should be false")
	}
}

func TestModelRegistryUpstreamSchema_ModelOverride(t *testing.T) {
	dir := t.TempDir()
	writeModelsJSON(t, dir, `{
		"providers": {
			"openai": {
				"modelOverrides": {
					"gpt-4o": {
						"name": "GPT-4o Custom",
						"contextWindow": 200000
					}
				}
			}
		}
	}`)

	r := NewModelRegistry(dir)

	// Provider-level config exists (even without models), so Resolve finds the override.
	got, ok := r.Resolve("openai", "gpt-4o")
	if !ok {
		t.Fatal("Resolve missed openai/gpt-4o override")
	}
	if got.DisplayName != "GPT-4o Custom" {
		t.Errorf("DisplayName = %q, want GPT-4o Custom", got.DisplayName)
	}
	if got.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", got.ContextWindow)
	}
}

func TestModelRegistryUpstreamSchema_PerModelCompat(t *testing.T) {
	dir := t.TempDir()
	writeModelsJSON(t, dir, `{
		"providers": {
			"custom": {
				"baseUrl": "http://localhost:8080/v1",
				"apiKey": "test",
				"compat": {
					"supportsDeveloperRole": false
				},
				"models": [
					{
						"id": "base-model"
					},
					{
						"id": "reasoning-model",
						"reasoning": true,
						"compat": {
							"supportsReasoningEffort": true
						}
					}
				]
			}
		}
	}`)

	r := NewModelRegistry(dir)

	// base-model inherits provider compat only.
	base, _ := r.Resolve("custom", "base-model")
	if base.Compat == nil || base.Compat.SupportsDeveloperRole == nil || *base.Compat.SupportsDeveloperRole != false {
		t.Error("base-model should inherit provider supportsDeveloperRole=false")
	}
	if base.Compat.SupportsReasoningEffort != nil {
		t.Error("base-model should not have supportsReasoningEffort set")
	}

	// reasoning-model merges provider + model compat.
	reasoning, _ := r.Resolve("custom", "reasoning-model")
	if reasoning.Compat == nil {
		t.Fatal("reasoning-model compat is nil")
	}
	if reasoning.Compat.SupportsDeveloperRole == nil || *reasoning.Compat.SupportsDeveloperRole != false {
		t.Error("reasoning-model should inherit provider supportsDeveloperRole=false")
	}
	if reasoning.Compat.SupportsReasoningEffort == nil || *reasoning.Compat.SupportsReasoningEffort != true {
		t.Error("reasoning-model should have supportsReasoningEffort=true from model-level override")
	}
}

func TestModelRegistryLoadError(t *testing.T) {
	dir := t.TempDir()
	writeModelsJSON(t, dir, `{ invalid json }`)
	r := NewModelRegistry(dir)
	if r.LoadError() == "" {
		t.Error("expected load error for invalid JSON")
	}
}

func TestModelRegistryValidation_MissingID(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{
		"providers": {
			"my-proxy": {
				"baseUrl": "http://localhost:8080/v1",
				"apiKey": "key",
				"models": [{"id": ""}]
			}
		}
	}`), 0o644)
	r := NewModelRegistry(dir)
	if r.LoadError() == "" {
		t.Error("expected validation error for empty model id")
	}
	if !strings.Contains(r.LoadError(), "\"id\" is required") {
		t.Errorf("unexpected error: %s", r.LoadError())
	}
}

func TestModelRegistryValidation_MissingBaseUrl(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{
		"providers": {
			"custom-provider": {
				"apiKey": "key",
				"models": [{"id": "model-1"}]
			}
		}
	}`), 0o644)
	r := NewModelRegistry(dir)
	if r.LoadError() == "" {
		t.Error("expected validation error for missing baseUrl")
	}
	if !strings.Contains(r.LoadError(), "\"baseUrl\" is required") {
		t.Errorf("unexpected error: %s", r.LoadError())
	}
}

func TestModelRegistryValidation_BuiltInNoBaseUrl(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{
		"providers": {
			"openai": {
				"models": [{"id": "custom-model"}]
			}
		}
	}`), 0o644)
	r := NewModelRegistry(dir)
	// Built-in providers don't require baseUrl.
	if r.LoadError() != "" {
		t.Errorf("built-in provider should not require baseUrl, got: %s", r.LoadError())
	}
}

func TestSettings_NewFieldDefaults(t *testing.T) {
	var s Settings
	// Test default values match upstream.
	if got := s.GetShowHardwareCursor(); got != false {
		t.Errorf("ShowHardwareCursor default = %v, want false", got)
	}
	if got := s.GetEditorPaddingX(); got != 0 {
		t.Errorf("EditorPaddingX default = %d, want 0", got)
	}
	if got := s.GetAutocompleteMaxVisible(); got != 5 {
		t.Errorf("AutocompleteMaxVisible default = %d, want 5", got)
	}
	if got := s.GetClearOnShrink(); got != false {
		t.Errorf("ClearOnShrink default = %v, want false", got)
	}
	if got := s.GetShowTerminalProgress(); got != false {
		t.Errorf("ShowTerminalProgress default = %v, want false", got)
	}
	if got := s.GetCodeBlockIndent(); got != "  " {
		t.Errorf("CodeBlockIndent default = %q, want %q", got, "  ")
	}
}

func TestSettings_NewFieldClamp(t *testing.T) {
	// Pi preserves direct file values; only setters clamp these fields.
	v5 := 5
	s := Settings{EditorPaddingX: &v5}
	if got := s.GetEditorPaddingX(); got != 5 {
		t.Errorf("EditorPaddingX(5) = %d, want 5", got)
	}
	vNeg := -1
	s = Settings{EditorPaddingX: &vNeg}
	if got := s.GetEditorPaddingX(); got != -1 {
		t.Errorf("EditorPaddingX(-1) = %d, want -1", got)
	}
	v1 := 1
	s = Settings{AutocompleteMaxVisible: &v1}
	if got := s.GetAutocompleteMaxVisible(); got != 1 {
		t.Errorf("AutocompleteMaxVisible(1) = %d, want 1", got)
	}
	v25 := 25
	s = Settings{AutocompleteMaxVisible: &v25}
	if got := s.GetAutocompleteMaxVisible(); got != 25 {
		t.Errorf("AutocompleteMaxVisible(25) = %d, want 25", got)
	}
}

func TestSettingsManager_DefaultAndValidationParity(t *testing.T) {
	t.Setenv("PI_CLEAR_ON_SHRINK", "1")
	sm := &SettingsManager{global: Settings{DefaultProjectTrust: "sometimes"}, merged: Settings{TreeFilterMode: "sometimes"}}
	if got := sm.GetDefaultProjectTrust(); got != "ask" {
		t.Fatalf("GetDefaultProjectTrust() = %q, want ask", got)
	}
	sm.global.DefaultProjectTrust = "always"
	sm.merged.DefaultProjectTrust = "never"
	if got := sm.GetDefaultProjectTrust(); got != "always" {
		t.Fatalf("project trust override leaked: got %q, want global always", got)
	}
	if got := sm.GetTreeFilterMode(); got != "default" {
		t.Fatalf("GetTreeFilterMode() = %q, want default", got)
	}
	if !sm.GetClearOnShrink() {
		t.Fatal("GetClearOnShrink() = false with PI_CLEAR_ON_SHRINK=1")
	}
	disabled := false
	sm.merged.ClearOnShrink = &disabled
	if sm.GetClearOnShrink() {
		t.Fatal("explicit clearOnShrink=false did not override environment")
	}
	if warnings := (&SettingsManager{}).GetWarnings(); !warnings.AnthropicExtraUsage {
		t.Fatalf("default warnings = %#v, want anthropic extra usage enabled", warnings)
	}
}

func TestSettings_ThinkingBudgetsRoundTrip(t *testing.T) {
	low := 1024
	high := 8192
	s := Settings{ThinkingBudgets: &ThinkingBudgetsSettings{Low: &low, High: &high}}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var s2 Settings
	if err := json.Unmarshal(data, &s2); err != nil {
		t.Fatal(err)
	}
	if s2.ThinkingBudgets == nil {
		t.Fatal("ThinkingBudgets is nil after round-trip")
	}
	if s2.ThinkingBudgets.Low == nil || *s2.ThinkingBudgets.Low != 1024 {
		t.Errorf("Low = %v, want 1024", s2.ThinkingBudgets.Low)
	}
	if s2.ThinkingBudgets.High == nil || *s2.ThinkingBudgets.High != 8192 {
		t.Errorf("High = %v, want 8192", s2.ThinkingBudgets.High)
	}
}

func TestSettings_084DisplaySettingsRoundTripAndDefaults(t *testing.T) {
	var defaults Settings
	defaultManager := &SettingsManager{merged: defaults}
	if got := defaultManager.GetMermaidRenderingMode(); got != "streaming" {
		t.Fatalf("default Mermaid mode = %q, want streaming", got)
	}
	if got := defaultManager.GetTuiMode(); got != "regular" {
		t.Fatalf("default TUI mode = %q, want regular", got)
	}
	if got := defaultManager.GetFullscreenScrollbar(); got != "auto" {
		t.Fatalf("default fullscreen scrollbar = %q, want auto", got)
	}

	input := []byte(`{"markdown":{"mermaid":"final"},"tuiMode":"fullscreen","fullscreenScrollbar":"always"}`)
	var settings Settings
	if err := json.Unmarshal(input, &settings); err != nil {
		t.Fatal(err)
	}
	manager := &SettingsManager{merged: settings}
	if got := manager.GetMermaidRenderingMode(); got != "final" {
		t.Fatalf("Mermaid mode = %q, want final", got)
	}
	if got := manager.GetTuiMode(); got != "fullscreen" {
		t.Fatalf("TUI mode = %q, want fullscreen", got)
	}
	if got := manager.GetFullscreenScrollbar(); got != "always" {
		t.Fatalf("fullscreen scrollbar = %q, want always", got)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"mermaid":"final"`, `"tuiMode":"fullscreen"`, `"fullscreenScrollbar":"always"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("encoded settings missing %s: %s", want, encoded)
		}
	}

	merged := mergeSettings(settings, Settings{
		Markdown:            &MarkdownSettings{Mermaid: "off"},
		TuiMode:             "regular",
		FullscreenScrollbar: "hidden",
	})
	if merged.Markdown == nil || merged.Markdown.Mermaid != "off" || merged.TuiMode != "regular" || merged.FullscreenScrollbar != "hidden" {
		t.Fatalf("merged display settings = %+v", merged)
	}
}

func TestSettingsManagerFullscreenExitOutput(t *testing.T) {
	defaults := &SettingsManager{merged: Settings{}}
	if got := defaults.GetFullscreenExitOutput(); got != "transcript" {
		t.Fatalf("default fullscreen exit output = %q, want transcript", got)
	}
	invalid := &SettingsManager{merged: Settings{FullscreenExitOutput: "invalid"}}
	if got := invalid.GetFullscreenExitOutput(); got != "transcript" {
		t.Fatalf("invalid fullscreen exit output = %q, want transcript", got)
	}

	var decoded Settings
	if err := json.Unmarshal([]byte(`{"fullscreenExitOutput":"resume-hint"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if got := (&SettingsManager{merged: decoded}).GetFullscreenExitOutput(); got != "resume-hint" {
		t.Fatalf("decoded fullscreen exit output = %q, want resume-hint", got)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"fullscreenExitOutput":"resume-hint"`) {
		t.Fatalf("encoded settings omit fullscreen exit output: %s", encoded)
	}
	if got := mergeSettings(Settings{}, decoded).FullscreenExitOutput; got != "resume-hint" {
		t.Fatalf("merged fullscreen exit output = %q, want resume-hint", got)
	}

	dir := t.TempDir()
	manager := NewSettingsManager(t.TempDir(), dir)
	if err := manager.SetFullscreenExitOutput("resume-hint"); err != nil {
		t.Fatal(err)
	}
	if got := NewSettingsManager(t.TempDir(), dir).GetFullscreenExitOutput(); got != "resume-hint" {
		t.Fatalf("persisted fullscreen exit output = %q, want resume-hint", got)
	}
}

func TestSettingsManagerFullscreenCopyOnSelect(t *testing.T) {
	defaults := &SettingsManager{merged: Settings{}}
	if !defaults.GetFullscreenCopyOnSelect() {
		t.Fatal("fullscreen copy on select should default true")
	}

	var decoded Settings
	if err := json.Unmarshal([]byte(`{"fullscreenCopyOnSelect":false}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if (&SettingsManager{merged: decoded}).GetFullscreenCopyOnSelect() {
		t.Fatal("decoded fullscreen copy on select = true, want false")
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"fullscreenCopyOnSelect":false`) {
		t.Fatalf("encoded settings omit fullscreen copy on select: %s", encoded)
	}
	if got := mergeSettings(Settings{}, decoded).FullscreenCopyOnSelect; got == nil || *got {
		t.Fatalf("merged fullscreen copy on select = %v, want false", got)
	}
	cloned := cloneSettings(decoded)
	if cloned.FullscreenCopyOnSelect == nil || *cloned.FullscreenCopyOnSelect {
		t.Fatalf("cloned fullscreen copy on select = %v, want false", cloned.FullscreenCopyOnSelect)
	}
	*decoded.FullscreenCopyOnSelect = true
	if *cloned.FullscreenCopyOnSelect {
		t.Fatal("clone aliases fullscreen copy on select")
	}

	dir := t.TempDir()
	manager := NewSettingsManager(t.TempDir(), dir)
	if err := manager.SetFullscreenCopyOnSelect(false); err != nil {
		t.Fatal(err)
	}
	if NewSettingsManager(t.TempDir(), dir).GetFullscreenCopyOnSelect() {
		t.Fatal("persisted fullscreen copy on select = true, want false")
	}
}

func TestSettingsManager_084DisplaySettingsPersist(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(t.TempDir(), dir)
	if err := sm.SetMermaidRenderingMode("final"); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetTuiMode("fullscreen"); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetFullscreenScrollbar("always"); err != nil {
		t.Fatal(err)
	}

	reloaded := NewSettingsManager(t.TempDir(), dir)
	if got := reloaded.GetMermaidRenderingMode(); got != "final" {
		t.Fatalf("reloaded Mermaid mode = %q, want final", got)
	}
	if got := reloaded.GetTuiMode(); got != "fullscreen" {
		t.Fatalf("reloaded TUI mode = %q, want fullscreen", got)
	}
	if got := reloaded.GetFullscreenScrollbar(); got != "always" {
		t.Fatalf("reloaded fullscreen scrollbar = %q, want always", got)
	}
	writeSettingsClosureTrace(t,
		settingsClosureTraceEvent{kind: "persist", subject: "mermaid-rendering", value: "final"},
		settingsClosureTraceEvent{kind: "persist", subject: "tui-mode", value: "fullscreen"},
		settingsClosureTraceEvent{kind: "persist", subject: "fullscreen-scrollbar", value: "always"},
		settingsClosureTraceEvent{kind: "reload", subject: "display-settings", value: "final|fullscreen|always"},
	)
}

func TestSettingsManager_NewGetters(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{
		"defaultProvider": "openai",
		"defaultModel": "gpt-4",
		"theme": "dark",
		"transport": "websocket",
		"sessionDir": "/tmp/sessions",
		"npmCommand": ["pnpm"],
		"showHardwareCursor": true
	}`), 0o644)
	sm := NewSettingsManager(cwd, dir)
	if sm.GetDefaultProvider() != "openai" {
		t.Errorf("DefaultProvider = %q, want 'openai'", sm.GetDefaultProvider())
	}
	if sm.GetDefaultModel() != "gpt-4" {
		t.Errorf("DefaultModel = %q, want 'gpt-4'", sm.GetDefaultModel())
	}
	if sm.GetTheme() != "dark" {
		t.Errorf("Theme = %q, want 'dark'", sm.GetTheme())
	}
	if sm.GetTransport() != "websocket" {
		t.Errorf("Transport = %q, want 'websocket'", sm.GetTransport())
	}
	if got, err := sm.GetSessionDir(); err != nil || got != "/tmp/sessions" {
		t.Errorf("SessionDir = %q, %v, want '/tmp/sessions'", got, err)
	}
	cmd := sm.GetNpmCommand()
	if len(cmd) != 1 || cmd[0] != "pnpm" {
		t.Errorf("NpmCommand = %v, want [pnpm]", cmd)
	}
	if !sm.GetShowHardwareCursor() {
		t.Error("ShowHardwareCursor should be true")
	}
	// Default transport
	sm2 := NewSettingsManager(t.TempDir(), t.TempDir())
	if sm2.GetTransport() != "auto" {
		t.Errorf("default Transport = %q, want 'auto'", sm2.GetTransport())
	}
}

func TestSettingsManager_Setters(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	sm := NewSettingsManager(cwd, dir)

	check := func(name string, setErr error) {
		t.Helper()
		if setErr != nil {
			t.Fatalf("Set%s: %v", name, setErr)
		}
	}

	check("DefaultProvider", sm.SetDefaultProvider("anthropic"))
	check("DefaultModel", sm.SetDefaultModel("claude-4"))
	check("Theme", sm.SetTheme("light"))
	check("Transport", sm.SetTransport("websocket"))
	check("SteeringMode", sm.SetSteeringMode("all"))
	check("FollowUpMode", sm.SetFollowUpMode("all"))
	check("DefaultThinkingLevel", sm.SetDefaultThinkingLevel("high"))
	check("HideThinkingBlock", sm.SetHideThinkingBlock(true))
	check("ShellPath", sm.SetShellPath("/bin/zsh"))
	check("QuietStartup", sm.SetQuietStartup(true))
	check("ShellCommandPrefix", sm.SetShellCommandPrefix("set -e; "))
	check("NpmCommand", sm.SetNpmCommand([]string{"pnpm"}))
	check("CollapseChangelog", sm.SetCollapseChangelog(true))
	check("EnableInstallTelemetry", sm.SetEnableInstallTelemetry(false))
	check("EnableSkillCommands", sm.SetEnableSkillCommands(false))
	check("ShowImages", sm.SetShowImages(false))
	check("ImageWidthCells", sm.SetImageWidthCells(80))
	check("ClearOnShrink", sm.SetClearOnShrink(true))
	check("ShowTerminalProgress", sm.SetShowTerminalProgress(true))
	check("ImageAutoResize", sm.SetImageAutoResize(false))
	check("BlockImages", sm.SetBlockImages(true))
	check("EnabledModels", sm.SetEnabledModels([]string{"gpt-4*"}))
	check("DoubleEscapeAction", sm.SetDoubleEscapeAction("fork"))
	check("TreeFilterMode", sm.SetTreeFilterMode("user-only"))
	check("ShowHardwareCursor", sm.SetShowHardwareCursor(true))
	check("EditorPaddingX", sm.SetEditorPaddingX(2))
	check("AutocompleteMaxVisible", sm.SetAutocompleteMaxVisible(10))
	check("CompactionEnabled", sm.SetCompactionEnabled(false))
	check("RetryEnabled", sm.SetRetryEnabled(false))

	// Reload from disk and verify persistence.
	sm2 := NewSettingsManager(cwd, dir)
	if sm2.GetDefaultProvider() != "anthropic" {
		t.Errorf("DefaultProvider = %q, want 'anthropic'", sm2.GetDefaultProvider())
	}
	if sm2.GetDefaultModel() != "claude-4" {
		t.Errorf("DefaultModel = %q, want 'claude-4'", sm2.GetDefaultModel())
	}
	if sm2.GetTheme() != "light" {
		t.Errorf("Theme = %q, want 'light'", sm2.GetTheme())
	}
	if sm2.GetTransport() != "websocket" {
		t.Errorf("Transport = %q, want 'websocket'", sm2.GetTransport())
	}
	if sm2.GetHideThinkingBlock() != true {
		t.Error("HideThinkingBlock should be true")
	}
	if sm2.GetEnableInstallTelemetry() != false {
		t.Error("EnableInstallTelemetry should be false")
	}
	if sm2.GetQuietStartup() != true {
		t.Error("QuietStartup should be true")
	}
	if !sm2.GetShowHardwareCursor() {
		t.Error("ShowHardwareCursor should be true")
	}
	if !sm2.GetShowTerminalProgress() {
		t.Error("ShowTerminalProgress should be true")
	}
	if sm2.GetEditorPaddingX() != 2 {
		t.Errorf("EditorPaddingX = %d, want 2", sm2.GetEditorPaddingX())
	}
	if sm2.GetAutocompleteMaxVisible() != 10 {
		t.Errorf("AutocompleteMaxVisible = %d, want 10", sm2.GetAutocompleteMaxVisible())
	}
	if sm2.GetDoubleEscapeAction() != "fork" {
		t.Errorf("DoubleEscapeAction = %q, want 'fork'", sm2.GetDoubleEscapeAction())
	}
	if sm2.GetTreeFilterMode() != "user-only" {
		t.Errorf("TreeFilterMode = %q, want 'user-only'", sm2.GetTreeFilterMode())
	}
	cs := sm2.GetCompactionSettings()
	if cs.Enabled {
		t.Error("CompactionEnabled should be false")
	}
	rs := sm2.GetRetrySettings()
	if rs.Enabled {
		t.Error("RetryEnabled should be false")
	}
}

func TestSettings_JSONUsesUpstreamWireShape(t *testing.T) {
	show := false
	clear := true
	autoResize := false
	block := true
	hide := false
	quiet := true
	collapse := false
	s := Settings{
		CommandPrefix:        "set -e; ",
		ShowImages:           &show,
		ImageWidthCells:      80,
		ClearOnShrink:        &clear,
		ImageAutoResize:      &autoResize,
		BlockImages:          block,
		blockImagesSet:       true,
		HideThinkingBlock:    hide,
		hideThinkingBlockSet: true,
		QuietStartup:         quiet,
		quietStartupSet:      true,
		CollapseChangelog:    collapse,
		collapseChangelogSet: true,
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	for _, want := range []string{
		`"shellCommandPrefix":"set -e; "`,
		`"terminal":{"showImages":false,"imageWidthCells":80,"clearOnShrink":true}`,
		`"images":{"autoResize":false,"blockImages":true}`,
		`"hideThinkingBlock":false`,
		`"quietStartup":true`,
		`"collapseChangelog":false`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("marshal missing %s in %s", want, js)
		}
	}
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"commandPrefix", "showImages", "imageWidthCells", "imageAutoResize", "blockImages"} {
		if _, ok := top[forbidden]; ok {
			t.Errorf("marshal should not contain legacy top-level field %s in %s", forbidden, js)
		}
	}
}

func TestSettings_UnmarshalAcceptsUpstreamAndLegacyShapes(t *testing.T) {
	payload := []byte(`{
		"shellCommandPrefix": "set -e; ",
		"terminal": {"showImages": false, "imageWidthCells": 70, "clearOnShrink": true},
		"images": {"autoResize": false, "blockImages": true},
		"hideThinkingBlock": false,
		"quietStartup": true,
		"collapseChangelog": false
	}`)
	var s Settings
	if err := json.Unmarshal(payload, &s); err != nil {
		t.Fatal(err)
	}
	if s.CommandPrefix != "set -e; " {
		t.Fatalf("CommandPrefix = %q", s.CommandPrefix)
	}
	if s.GetShowImages() != false || s.GetImageWidthCells() != 70 || s.GetClearOnShrink() != true {
		t.Fatalf("terminal settings not decoded correctly: %+v", s)
	}
	if s.GetImageAutoResize() != false || !s.GetBlockImages() {
		t.Fatalf("image settings not decoded correctly: %+v", s)
	}
	if s.GetHideThinkingBlock() != false || s.GetQuietStartup() != true || s.GetCollapseChangelog() != false {
		t.Fatalf("bool settings not decoded correctly: %+v", s)
	}

	legacy := []byte(`{
		"commandPrefix": "legacy",
		"showImages": true,
		"imageWidthCells": 55,
		"clearOnShrink": false,
		"imageAutoResize": true,
		"blockImages": false
	}`)
	var s2 Settings
	if err := json.Unmarshal(legacy, &s2); err != nil {
		t.Fatal(err)
	}
	if s2.CommandPrefix != "legacy" || s2.GetShowImages() != true || s2.GetImageWidthCells() != 55 || s2.GetImageAutoResize() != true || s2.GetBlockImages() != false {
		t.Fatalf("legacy settings not decoded correctly: %+v", s2)
	}
}

func TestMergeSettings_DeepMergeAndFalseOverride(t *testing.T) {
	globalShow := true
	projectShow := false
	globalAuto := true
	projectAuto := false
	globalHide := true
	projectHide := false
	globalQuiet := true
	projectQuiet := false
	globalCollapse := true
	projectCollapse := false
	globalBlock := true
	projectBlock := false
	globalSkip := true
	projectSkip := false
	globalLow := 100
	projectHigh := 400
	global := Settings{
		HideThinkingBlock:    globalHide,
		hideThinkingBlockSet: true,
		QuietStartup:         globalQuiet,
		quietStartupSet:      true,
		CollapseChangelog:    globalCollapse,
		collapseChangelogSet: true,
		ShowImages:           &globalShow,
		ImageWidthCells:      60,
		ClearOnShrink:        new(true),
		ImageAutoResize:      &globalAuto,
		BlockImages:          globalBlock,
		blockImagesSet:       true,
		BranchSummary:        &BranchSummaryConfig{ReserveTokens: 1000, SkipPrompt: globalSkip, skipPromptSet: true},
		ThinkingBudgets:      &ThinkingBudgetsSettings{Low: &globalLow},
		Markdown:             &MarkdownSettings{CodeBlockIndent: "  "},
	}
	project := Settings{
		HideThinkingBlock:    projectHide,
		hideThinkingBlockSet: true,
		QuietStartup:         projectQuiet,
		quietStartupSet:      true,
		CollapseChangelog:    projectCollapse,
		collapseChangelogSet: true,
		ShowImages:           &projectShow,
		ImageAutoResize:      &projectAuto,
		BlockImages:          projectBlock,
		blockImagesSet:       true,
		BranchSummary:        &BranchSummaryConfig{SkipPrompt: projectSkip, skipPromptSet: true},
		ThinkingBudgets:      &ThinkingBudgetsSettings{High: &projectHigh},
		Markdown:             &MarkdownSettings{CodeBlockIndent: "\t"},
	}
	merged := mergeSettings(global, project)
	if merged.GetHideThinkingBlock() != false || merged.GetQuietStartup() != false || merged.GetCollapseChangelog() != false {
		t.Fatalf("false overrides failed: %+v", merged)
	}
	if merged.GetShowImages() != false || merged.GetImageAutoResize() != false || merged.GetBlockImages() != false {
		t.Fatalf("terminal/image overrides failed: %+v", merged)
	}
	if merged.BranchSummary == nil || merged.BranchSummary.ReserveTokens != 1000 || merged.BranchSummary.SkipPrompt != false {
		t.Fatalf("branch summary deep merge failed: %+v", merged.BranchSummary)
	}
	if merged.ThinkingBudgets == nil || merged.ThinkingBudgets.Low == nil || *merged.ThinkingBudgets.Low != 100 || merged.ThinkingBudgets.High == nil || *merged.ThinkingBudgets.High != 400 {
		t.Fatalf("thinking budgets deep merge failed: %+v", merged.ThinkingBudgets)
	}
	if merged.Markdown == nil || merged.Markdown.CodeBlockIndent != "\t" {
		t.Fatalf("markdown merge failed: %+v", merged.Markdown)
	}
}

func TestPackageSource_ObjectFormWithoutFilters_IsFiltered(t *testing.T) {
	// Upstream: typeof pkg === "object" → filtered = true, even when
	// no filter fields are set.
	data := []byte(`{"source":"git:github.com/test/example"}`)
	var src PackageSource
	if err := json.Unmarshal(data, &src); err != nil {
		t.Fatal(err)
	}
	if src.Source != "git:github.com/test/example" {
		t.Fatalf("Source = %q", src.Source)
	}
	if !src.WasObject {
		t.Fatal("WasObject should be true for object-form JSON")
	}
	if !src.Filtered() {
		t.Fatal("Filtered() should be true for object-form JSON")
	}
}
