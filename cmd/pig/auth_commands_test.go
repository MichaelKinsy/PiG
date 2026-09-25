package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/cellpack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type testExternalOAuthStore struct {
	stored  ai.OAuthCredentials
	deleted bool
}

type testExternalOAuthProvider struct {
	store *testExternalOAuthStore
}

func (p testExternalOAuthProvider) ID() string               { return "external-store-test" }
func (p testExternalOAuthProvider) Name() string             { return "External Store Test" }
func (p testExternalOAuthProvider) UsesCallbackServer() bool { return false }
func (p testExternalOAuthProvider) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{Access: "external-secret", Expires: 4102444800000}, nil
}
func (p testExternalOAuthProvider) RefreshToken(ai.OAuthCredentials) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{}, errors.New("not implemented")
}
func (p testExternalOAuthProvider) GetAPIKey(creds ai.OAuthCredentials) string { return creds.Access }
func (p testExternalOAuthProvider) OAuthCredentialStatus() (ai.OAuthCredentialStatus, bool) {
	if p.store.stored.Access == "" {
		return ai.OAuthCredentialStatus{}, false
	}
	return ai.OAuthCredentialStatus{AuthType: "oauth", Source: "test-store"}, true
}
func (p testExternalOAuthProvider) StoreOAuthCredentials(creds ai.OAuthCredentials) (string, error) {
	p.store.stored = creds
	return "/external/credentials.json", nil
}
func (p testExternalOAuthProvider) DeleteOAuthCredentials() (bool, error) {
	existed := p.store.stored.Access != ""
	p.store.stored = ai.OAuthCredentials{}
	p.store.deleted = true
	return existed, nil
}

func captureWithStdin(t *testing.T, stdin string, fn func() int) (stdout, stderr string, code int) {
	t.Helper()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	oldStdin := os.Stdin
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wIn.WriteString(stdin); err != nil {
		t.Fatal(err)
	}
	_ = wIn.Close()
	os.Stdout = wOut
	os.Stderr = wErr
	os.Stdin = rIn
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		os.Stdin = oldStdin
	}()

	outDone := drainPipe(rOut)
	errDone := drainPipe(rErr)
	code = fn()
	_ = wOut.Close()
	_ = wErr.Close()
	out := <-outDone
	if out.err != nil {
		t.Fatal(out.err)
	}
	errOutput := <-errDone
	if errOutput.err != nil {
		t.Fatal(errOutput.err)
	}
	_ = rOut.Close()
	_ = rErr.Close()
	_ = rIn.Close()
	return string(out.data), string(errOutput.data), code
}

func TestRunAuthCommandListJSONIsStableAndSecretFree(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	stdout, stderr, code := captureWithStdin(t, "", func() int {
		return runLoginCommand([]string{"login", "--list", "--json", "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var output authTargetListOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Targets) < 3 {
		t.Fatalf("output = %#v", output)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["version"]; exists {
		t.Fatalf("auth inventory exposed a Pig-owned format version: %s", stdout)
	}
	ids := make([]string, len(output.Targets))
	for i, target := range output.Targets {
		ids[i] = target.ID
	}
	for _, want := range []string{"anthropic", "github-copilot", "openai-codex"} {
		if !strings.Contains(strings.Join(ids, ","), want) {
			t.Fatalf("missing %s in %v", want, ids)
		}
	}
	if strings.Contains(strings.ToLower(stdout), "token") || strings.Contains(strings.ToLower(stdout), "secret") {
		t.Fatalf("target inventory contains credential-shaped data: %s", stdout)
	}
}

func TestRunAuthCommandUsesExtensionOwnedCredentialStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	store := &testExternalOAuthStore{}
	provider := testExternalOAuthProvider{store: store}
	ai.RegisterOAuthProvider(provider.ID(), provider)
	t.Cleanup(func() { ai.UnregisterOAuthProvider(provider.ID()) })

	stdout, stderr, code := captureWithStdin(t, "", func() int {
		return runLoginCommand([]string{"login", provider.ID(), "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("login code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if store.stored.Access != "external-secret" {
		t.Fatalf("extension store credential = %#v", store.stored)
	}
	if !strings.Contains(stdout, "/external/credentials.json") {
		t.Fatalf("login stdout missing extension store path: %s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("login wrote core auth.json for extension-owned store: %v", err)
	}

	stdout, stderr, code = captureWithStdin(t, "", func() int {
		return runLoginCommand([]string{"logout", provider.ID(), "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("logout code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !store.deleted || store.stored.Access != "" {
		t.Fatalf("extension store was not deleted: %#v", store)
	}
}

func TestAuthContributionLoadHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	registry := &authContributionRegistry{
		cwd: t.TempDir(), ctx: ctx,
		configs: map[string]subprocess.ExtConfig{
			"cancelled": {Name: "cancelled", Source: t.TempDir(), Enabled: true},
		},
		embedded: map[string]struct{}{}, loaded: map[string]struct{}{},
	}
	t.Setenv("PIG_HOME", t.TempDir())
	err := registry.loadExtension("cancelled")
	registry.close()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loadExtension error = %v, want context.Canceled", err)
	}
}

func TestAuthContributionRegistryRejectsDuplicateTargetsDeterministically(t *testing.T) {
	registry := &authContributionRegistry{targets: map[string]authContribution{}}
	if err := registry.add(authContribution{ID: "shared", Name: "Shared", Extension: "zeta"}); err != nil {
		t.Fatal(err)
	}
	err := registry.add(authContribution{ID: "shared", Name: "Other", Extension: "alpha"})
	if err == nil || err.Error() != `duplicate auth target "shared" from extensions "alpha" and "zeta"` {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestRegisteredAuthTargetItemsKeepsCoreProviderOnConflict(t *testing.T) {
	registry := &authContributionRegistry{targets: map[string]authContribution{
		"anthropic": {ID: "anthropic", Name: "Malicious override", Extension: "override"},
	}}
	items := registeredAuthTargetItems(registry)
	var anthropic []authTargetItem
	for _, item := range items {
		if item.ID == "anthropic" {
			anthropic = append(anthropic, item)
		}
	}
	if len(anthropic) != 1 || anthropic[0].Name != "Anthropic" {
		t.Fatalf("anthropic targets = %#v", anthropic)
	}
}

func TestAuthListKeepsHealthyTargetWhenUnrelatedExtensionIsBroken(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Cleanup(ai.ResetOAuthProviders)
	writeAuthContributionFixture(t, filepath.Join(agentDir, "extensions", "sdk-fixture"))
	broken := filepath.Join(agentDir, "extensions", "broken-unrelated")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "go.mod"), []byte("module example.com/broken\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureWithStdin(t, "", func() int {
		return runLoginCommand([]string{"login", "--list", "--json", "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("list code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"id":"conformance-oauth"`) || !strings.Contains(stdout, `"extension":"broken-unrelated"`) || !strings.Contains(stdout, `"diagnostics"`) {
		t.Fatalf("contained auth inventory = %s", stdout)
	}
}

func TestAuthContributionInspectionAndLoginLoadRuntimeRegistration(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	fixture := filepath.Join(agentDir, "extensions", "sdk-fixture")
	writeAuthContributionFixture(t, fixture)

	stdout, stderr, code := captureWithStdin(t, "", func() int {
		return runLoginCommand([]string{"login", "--list", "--json", "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("list code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"id":"conformance-oauth"`) {
		t.Fatalf("runtime auth target missing: %s", stdout)
	}
	if _, err := os.Stat(filepath.Join(root, "cache")); err != nil {
		t.Fatalf("registration inspection did not build the selected extension: %v", err)
	}
	if _, registered := ai.GetOAuthProvider("conformance-oauth"); registered {
		t.Fatal("list started and registered the contributed provider")
	}

	stdout, stderr, code = captureWithStdin(t, "approved\n", func() int {
		return runLoginCommand([]string{"login", "conformance-oauth"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("login code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "/conf/creds.json") {
		t.Fatalf("login did not use extension-owned credential store: %s", stdout)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("login wrote core auth.json: %v", err)
	}
	if _, registered := ai.GetOAuthProvider("conformance-oauth"); registered {
		t.Fatal("provider remained registered after pre-session host shutdown")
	}
}

func writeAuthContributionFixture(t *testing.T, root string) {
	t.Helper()
	fixtureSource, err := filepath.Abs(filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "sdk-fixture", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	mainSource, err := os.ReadFile(fixtureSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), mainSource, 0o644); err != nil {
		t.Fatal(err)
	}
	goMod := fmt.Sprintf("module example.com/auth-contribution-fixture\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", sdkRoot)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

}

func TestRunAuthCommandNoInputNeverReadsProviderPrompt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	stdout, stderr, code := captureWithStdin(t, "should-not-be-read\n", func() int {
		return runLoginCommand([]string{"login", "github-copilot", "--no-input"})
	})
	if code != 1 || !strings.Contains(stderr, "requires interactive input") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("credentials were written: %v", err)
	}
}

func TestRunAuthCommandNoInputRequiresProvider(t *testing.T) {
	_, stderr, code := captureWithStdin(t, "", func() int {
		return runLoginCommand([]string{"login", "--no-input"})
	})
	if code != 2 || !strings.Contains(stderr, "requires an explicit provider") || !strings.Contains(stderr, "login --list --json") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

func TestParseAuthCommand(t *testing.T) {
	opts, ok := parseLoginCommand([]string{"login", "github-copilot"})
	if !ok {
		t.Fatal("expected auth command")
	}
	if opts.command != authLogin || opts.provider != "github-copilot" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestRunAuthCommand_LoginGitHubCopilotParityHarness(t *testing.T) {
	dir := t.TempDir()
	oldWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	t.Setenv("PIG_PARITY_HARNESS", "1")
	t.Setenv("PIG_CODING_AGENT_DIR", ".")

	stdout, stderr, code := captureWithStdin(t, "\n", func() int {
		return runLoginCommand([]string{"login", "github-copilot"})
	})
	if code != 0 {
		t.Fatalf("code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	for _, needle := range []string{
		"GitHub Enterprise URL/domain (blank for github.com) (company.ghe.com):",
		"Open this URL in your browser:",
		"https://github.com/login/device",
		"Enter code: ABCD-EFGH",
		"Credentials saved to auth.json",
	} {
		if !strings.Contains(stdout, needle) {
			t.Fatalf("stdout missing %q:\n%s", needle, stdout)
		}
	}
	// The harness's fixture model (gpt-4o, policy.state="enabled") already
	// has an enabled policy, so upstream 0.87.1's loginGitHubCopilot never
	// prints "Enabling models...": it only does so when a catalog model's
	// policy is "unconfigured" (github-copilot.ts:471-480). Before pig
	// matched that selection, it posted a policy update to a fixed model
	// list unconditionally and always printed this line.
	if strings.Contains(stdout, "Enabling models...") {
		t.Fatalf("stdout contains \"Enabling models...\" for an already-enabled catalog model:\n%s", stdout)
	}
	if strings.Contains(stdout, "Logging in to github-copilot...") {
		t.Fatalf("stdout contains removed 0.81 preface line:\n%s", stdout)
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"github-copilot"`) || !strings.Contains(text, `"refresh": "ghu_refresh"`) {
		t.Fatalf("auth.json missing stored github-copilot creds:\n%s", text)
	}
}

func TestRunAuthCommand_LogoutDefaultProvider(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"github-copilot":{"type":"oauth","refresh":"r","access":"a","expires":9999999999999}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	t.Setenv("PIG_CODING_AGENT_DIR", ".")

	stdout, stderr, code := captureWithStdin(t, "", func() int {
		return runLoginCommand([]string{"logout"})
	})
	if code != 0 {
		t.Fatalf("code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Logged out from github-copilot. Credentials removed from auth.json") {
		t.Fatalf("stdout = %q", stdout)
	}
	data, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "github-copilot") {
		t.Fatalf("auth.json still contains github-copilot: %s", data)
	}
}

func TestAuthInspectionContainsBrokenPackageExtensions(t *testing.T) {
	cwd, agentDir, packageRoot := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	for path, content := range map[string]string{
		"package.json":                    `{"name":"pkg","pi":{"extensions":["extensions/healthy","extensions/broken","extensions/missing"]}}`,
		"extensions/healthy/go.mod":       "module example.com/healthy\n\ngo 1.26\n",
		"extensions/healthy/extension.go": "package healthy\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n",
		"extensions/broken/go.mod":        "module example.com/broken\n\ngo 1.26\n",
		"extensions/broken/extension.go":  "package broken\n",
	} {
		target := filepath.Join(packageRoot, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
		t.Fatal(err)
	}
	configs, diagnostics, err := authExtensionConfigs(cwd, agentDir, settings)
	if err != nil {
		t.Fatalf("broken Package extension aborted auth inventory: %v", err)
	}
	if len(configs) != 1 || !strings.Contains(filepath.ToSlash(configs[0].Source), "extensions/healthy") {
		t.Fatalf("healthy Package extension configs = %#v", configs)
	}
	if len(diagnostics) != 2 || !slices.ContainsFunc(diagnostics, func(item authInspectionDiagnostic) bool { return strings.Contains(item.Extension, "broken") }) || !slices.ContainsFunc(diagnostics, func(item authInspectionDiagnostic) bool { return strings.Contains(item.Extension, "missing") }) {
		t.Fatalf("Package extension diagnostics = %#v", diagnostics)
	}

	manifest := `{"name":"pkg","pi":{"extensions":["extensions/healthy"],"prompts":["prompts/missing.md"]}}`
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := authExtensionConfigs(cwd, agentDir, settings); err == nil || !strings.Contains(err.Error(), "prompts/missing.md") {
		t.Fatalf("missing non-extension Package member error = %v", err)
	}
}

func TestAuthInspectionContainsBrokenUnrelatedExtension(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Cleanup(ai.ResetOAuthProviders)
	healthyRoot := filepath.Join(t.TempDir(), "sdk-fixture")
	writeAuthContributionFixture(t, healthyRoot)
	healthy, _, err := subprocess.ResolveExtConfig(healthyRoot)
	if err != nil {
		t.Fatal(err)
	}
	broken := subprocess.ExtConfig{Name: "broken-unrelated", Path: filepath.Join(t.TempDir(), "missing"), Enabled: true}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registry := &authContributionRegistry{
		cwd: t.TempDir(), targets: map[string]authContribution{}, owners: map[string]string{},
		configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}, ctx: ctx, cancel: cancel,
	}
	if err := registry.inspectConfigs([]subprocess.ExtConfig{broken, healthy}); err != nil {
		t.Fatalf("broken unrelated extension aborted auth inventory: %v", err)
	}
	if target, ok := registry.target("conformance-oauth"); !ok || target.Extension != "sdk-fixture" {
		t.Fatalf("healthy auth target missing after unrelated failure: %#v, %t", target, ok)
	}
	if len(registry.diagnostics) != 1 || registry.diagnostics[0].Extension != "broken-unrelated" || !strings.Contains(registry.diagnostics[0].Error, "spawn_failed") {
		t.Fatalf("inspection diagnostics = %#v", registry.diagnostics)
	}
}

func TestAuthRegistrationProjectionReusesExactArtifactAndRejectsStaleDigest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Cleanup(ai.ResetOAuthProviders)
	root := filepath.Join(t.TempDir(), "sdk-fixture")
	writeAuthContributionFixture(t, root)
	config, _, err := subprocess.ResolveExtConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	newRegistry := func() *authContributionRegistry {
		return &authContributionRegistry{cwd: t.TempDir(), targets: map[string]authContribution{}, owners: map[string]string{}, configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}, ctx: ctx, cancel: cancel}
	}
	first := newRegistry()
	if err := first.inspectConfigs([]subprocess.ExtConfig{config}); err != nil {
		t.Fatal(err)
	}
	if err := first.storeProjection(config); err != nil {
		t.Fatal(err)
	}
	entry, _, _, ok := first.projectionEntry(config)
	if !ok {
		t.Fatal("built extension has no exact projection entry")
	}

	second := newRegistry()
	projected, err := second.loadProjection(config)
	if err != nil || !projected {
		t.Fatalf("exact projection load = %t, %v", projected, err)
	}
	if target, ok := second.target("conformance-oauth"); !ok || target.Extension != "sdk-fixture" {
		t.Fatalf("projection target = %#v, %t", target, ok)
	}
	if second.host != nil || len(second.loaded) != 0 {
		t.Fatal("projection reuse restarted the extension")
	}

	projectionPath := filepath.Join(entry, authRegistrationProjectionFile)
	data, err := os.ReadFile(projectionPath)
	if err != nil {
		t.Fatal(err)
	}
	var projection authRegistrationProjection
	if err := json.Unmarshal(data, &projection); err != nil {
		t.Fatal(err)
	}
	projection.ArtifactDigest = "stale"
	data, _ = json.Marshal(projection)
	if err := os.WriteFile(projectionPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	third := newRegistry()
	projected, err = third.loadProjection(config)
	if err != nil || projected || len(third.targets) != 0 {
		t.Fatalf("stale projection load = %t, %v, targets=%v", projected, err, third.targets)
	}
}

func TestAuthInspectionRejectsDuplicateRuntimeProviders(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Cleanup(ai.ResetOAuthProviders)
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSource, err := filepath.Abs(filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "sdk-fixture", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	baseSource, err := os.ReadFile(fixtureSource)
	if err != nil {
		t.Fatal(err)
	}
	configs := make([]subprocess.ExtConfig, 0, 2)
	for _, name := range []string{"alpha", "zeta"} {
		root := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		source := strings.Replace(string(baseSource), `sdk.New("sdk-fixture")`, `sdk.New("`+name+`")`, 1)
		if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		goMod := fmt.Sprintf("module example.com/%s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", name, sdkRoot)
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
			t.Fatal(err)
		}
		config, _, err := subprocess.ResolveExtConfig(root)
		if err != nil {
			t.Fatal(err)
		}
		configs = append(configs, config)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registry := &authContributionRegistry{
		cwd: t.TempDir(), targets: map[string]authContribution{}, owners: map[string]string{},
		configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}, ctx: ctx, cancel: cancel,
	}
	if err := registry.inspectConfigs(configs[:1]); err != nil {
		t.Fatal(err)
	}
	err = registry.inspectConfigs(configs[1:])
	if err == nil || !strings.Contains(err.Error(), `duplicate auth target "conformance-oauth" from extensions "alpha" and "zeta"`) {
		t.Fatalf("duplicate runtime provider error = %v", err)
	}
}

func TestAuthInspectionDispatchesNoLifecycleOrCapabilityHandler(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Cleanup(ai.ResetOAuthProviders)
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "inspection-only")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "handler-ran")
	goMod := fmt.Sprintf("module example.com/inspection-only\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", sdkRoot)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`package inspectiononly
import (
  "os"
  sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)
func Extension() *sdk.Extension {
  ext := sdk.New("inspection-only")
  ext.Tool("must_not_run", "probe", sdk.Schema{"type":"object"}, func(sdk.Context, map[string]any) (any, error) { return nil, os.WriteFile(%q, []byte("tool"), 0600) })
  ext.OnSessionStart(func(sdk.Context, map[string]any) (any, error) { return nil, os.WriteFile(%q, []byte("session"), 0600) })
  return ext
}
`, marker, marker)
	if err := os.WriteFile(filepath.Join(root, "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	config, _, err := subprocess.ResolveExtConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registry := &authContributionRegistry{cwd: t.TempDir(), targets: map[string]authContribution{}, owners: map[string]string{}, configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}, ctx: ctx, cancel: cancel}
	if err := registry.inspectConfigs([]subprocess.ExtConfig{config}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("registration inspection executed a handler: %v", err)
	}
}

func TestAuthInspectionReadsEmbeddedRuntimeRegistration(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Cleanup(ai.ResetOAuthProviders)
	source, err := filepath.Abs(filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "testdata", "sdk-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	binary := testExecutable(filepath.Join(t.TempDir(), "sdk-fixture"))
	command := exec.Command("go", "build", "-o", binary, ".")
	command.Dir = source
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build embedded fixture: %v\n%s", err, output)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registry := &authContributionRegistry{cwd: t.TempDir(), targets: map[string]authContribution{}, owners: map[string]string{}, configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}, ctx: ctx, cancel: cancel}
	cells := []cellpack.LoadedCell{{
		Language: "go", Key: "isolated:sdk-fixture", Strategy: string(subprocess.CellStrategyIsolated), BinaryPath: binary,
		Extensions: []cellpack.ExtEntry{{Name: "sdk-fixture"}},
	}}
	if err := registry.inspectEmbedded(cells); err != nil {
		t.Fatal(err)
	}
	target, ok := registry.target("conformance-oauth")
	if !ok || target.Extension != "sdk-fixture" || target.Name != "Conformance OAuth" {
		t.Fatalf("embedded runtime target = %#v, %t", target, ok)
	}
}

func TestAuthLoginStartsOnlyOwningExtension(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Cleanup(ai.ResetOAuthProviders)
	root := filepath.Join(t.TempDir(), "sdk-fixture")
	writeAuthContributionFixture(t, root)
	owner, _, err := subprocess.ResolveExtConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	registry := &authContributionRegistry{
		cwd: t.TempDir(), targets: map[string]authContribution{
			"conformance-oauth": {ID: "conformance-oauth", Name: "Conformance OAuth", Extension: "sdk-fixture"},
		}, owners: map[string]string{"conformance-oauth": "sdk-fixture"},
		configs: map[string]subprocess.ExtConfig{
			"sdk-fixture": owner,
			"unrelated":   {Name: "unrelated", Path: filepath.Join(t.TempDir(), "missing"), Enabled: true},
		}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}, ctx: ctx, cancel: cancel,
	}
	defer registry.close()
	provider, err := registry.load("conformance-oauth")
	if err != nil {
		t.Fatal(err)
	}
	if provider.ID() != "conformance-oauth" {
		t.Fatalf("provider = %q", provider.ID())
	}
	if _, loaded := registry.loaded["unrelated"]; loaded {
		t.Fatal("login started an extension that does not own the provider")
	}
}

func TestAuthInspectionSurfacesRegistrationTransportFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registry := &authContributionRegistry{cwd: t.TempDir(), targets: map[string]authContribution{}, owners: map[string]string{}, configs: map[string]subprocess.ExtConfig{}, embedded: map[string]struct{}{}, loaded: map[string]struct{}{}, ctx: ctx, cancel: cancel}
	err := registry.inspectConfigs([]subprocess.ExtConfig{{Name: "missing", Path: filepath.Join(t.TempDir(), "missing"), Enabled: true}})
	if err != nil {
		t.Fatalf("isolated inspection failure aborted inventory: %v", err)
	}
	if len(registry.diagnostics) != 1 || registry.diagnostics[0].Extension != "missing" ||
		!strings.Contains(registry.diagnostics[0].Error, "spawn_failed") || !strings.Contains(registry.diagnostics[0].Error, "missing") {
		t.Fatalf("inspection diagnostics = %#v, want precise spawn transport failure", registry.diagnostics)
	}
}

func TestSelectEmbeddedOwnerCellsKeepsContainingBinaryAndOnlyOwner(t *testing.T) {
	cells := []cellpack.LoadedCell{{
		Language: "go", Key: "packed", BinaryPath: "/cells/packed",
		Extensions: []cellpack.ExtEntry{{Name: "owner", Hash: "one"}, {Name: "unrelated", Hash: "two"}},
	}}
	selected := selectEmbeddedOwnerCells(cells, "owner")
	if len(selected) != 1 || selected[0].BinaryPath != "/cells/packed" || len(selected[0].Extensions) != 1 || selected[0].Extensions[0].Name != "owner" {
		t.Fatalf("selected owner cells = %#v", selected)
	}
	if len(cells[0].Extensions) != 2 {
		t.Fatal("owner selection mutated embedded cell manifest")
	}
}
