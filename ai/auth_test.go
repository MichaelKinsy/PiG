package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestAuthDiscriminatorParity verifies pig writes the same auth.json shape
// upstream pi reads (audit, ).
func TestAuthDiscriminatorParity(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if err := store.Set("openai", Credential{Type: CredentialAPIKey, Key: "sk-test"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"type": "api_key"`) {
		t.Errorf("expected discriminator \"api_key\" in %s", s)
	}
	if !strings.Contains(s, `"key": "sk-test"`) {
		t.Errorf("expected JSON field \"key\" in %s", s)
	}
	if strings.Contains(s, `"apiKey"`) {
		t.Errorf("legacy field \"apiKey\" must not be written, got %s", s)
	}
	if strings.Contains(s, `"type": "api"`) && !strings.Contains(s, `"type": "api_key"`) {
		t.Errorf("legacy discriminator \"api\" must not be written, got %s", s)
	}
}

// TestAuthLegacyMigration verifies an auth.json written by an older pig
// build (or hand-edited) with "type":"api"/"apiKey" reads in the current
// shape without being rewritten on read (upstream never writes on read), and
// that the next write stores the current shape.
func TestAuthLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	legacy := []byte(`{"openai":{"type":"api","apiKey":"sk-old"}}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	cred, ok, err := store.Get("openai")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatalf("missing openai entry")
	}
	if cred.Type != CredentialAPIKey {
		t.Errorf("type: want %q got %q", CredentialAPIKey, cred.Type)
	}
	if cred.Key != "sk-old" {
		t.Errorf("key: want sk-old got %q", cred.Key)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != string(legacy) {
		t.Fatalf("read rewrote auth.json: %s, %v", raw, err)
	}

	// The next write stores every entry in the current format.
	if err := store.Set("anthropic", Credential{Type: CredentialAPIKey, Key: "sk-new"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	var disk map[string]map[string]any
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := disk["openai"]
	if got["type"] != "api_key" {
		t.Errorf("post-write type: want api_key got %v", got["type"])
	}
	if got["key"] != "sk-old" {
		t.Errorf("post-write key: want sk-old got %v", got["key"])
	}
	if _, hasOld := got["apiKey"]; hasOld {
		t.Errorf("post-write must drop legacy apiKey field, got %v", got)
	}
}

// TestAuthOAuthRoundTrip ensures the oauth variant is unchanged by the
// 1.7 fix.
func TestAuthOAuthRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	in := Credential{
		Type:             CredentialOAuth,
		Refresh:          "rt",
		Access:           "at",
		Expires:          1234567890,
		EnterpriseDomain: "github.example.com",
	}
	if err := store.Set("github-copilot", in); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := store.Get("github-copilot")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, in)
	}
}

// TestAuthGetResolvesAPIKeyFromEnv proves the wiring: an auth.json where
// the api_key value is an explicit "$ENV_VAR" reference resolves to the env
// value when read via Get. Mirrors upstream auth-storage.ts (v0.78.1), where
// bare names are literals and env references require the "$" sigil (the
// startup migration rewrites legacy bare names to "$NAME").
func TestAuthGetResolvesAPIKeyFromEnv(t *testing.T) {
	t.Setenv("PIG_TEST_OPENAI_KEY_VIA_ENV", "sk-resolved-from-env")

	dir := t.TempDir()
	store, err := NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// Store an explicit env reference: {"openai":{"type":"api_key","key":"$OPENAI_API_KEY"}}.
	if err := store.Set("openai", Credential{Type: CredentialAPIKey, Key: "$PIG_TEST_OPENAI_KEY_VIA_ENV"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	cred, ok, err := store.Get("openai")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if cred.Key != "sk-resolved-from-env" {
		t.Errorf("Get should resolve env-var-name; got Key=%q want sk-resolved-from-env", cred.Key)
	}

	// On-disk value is preserved verbatim (Set wrote the literal name).
	raw, _, err := store.GetRaw("openai")
	if err != nil {
		t.Fatalf("getraw: %v", err)
	}
	if raw.Key != "$PIG_TEST_OPENAI_KEY_VIA_ENV" {
		t.Errorf("GetRaw should return on-disk value; got Key=%q", raw.Key)
	}
}

// TestAuthGetLiteralKeyPassesThrough proves that a literal sk-... key
// (env-var name unset) flows through Resolve untouched (the literal
// IS the resolution per upstream contract).
func TestAuthGetLiteralKeyPassesThrough(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := store.Set("openai", Credential{Type: CredentialAPIKey, Key: "sk-literal-abc123"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	cred, ok, err := store.Get("openai")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if cred.Key != "sk-literal-abc123" {
		t.Errorf("literal key must pass through; got %q", cred.Key)
	}
}

// TestAuthGetDoesNotResolveOAuthFields proves Refresh/Access tokens are
// returned verbatim even if they happen to look like env-var names.
// Regression guard: we must NOT call configvalue.Resolve on OAuth
// secrets: they're opaque values, not config strings.
func TestAuthGetDoesNotResolveOAuthFields(t *testing.T) {
	t.Setenv("PIG_TEST_OAUTH_LOOKALIKE", "this-must-not-leak")

	dir := t.TempDir()
	store, err := NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	in := Credential{
		Type:    CredentialOAuth,
		Refresh: "PIG_TEST_OAUTH_LOOKALIKE",
		Access:  "access-token-verbatim",
	}
	if err := store.Set("github-copilot", in); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, _, err := store.Get("github-copilot")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Refresh != "PIG_TEST_OAUTH_LOOKALIKE" {
		t.Errorf("OAuth refresh must NOT be resolved; got %q", got.Refresh)
	}
}

func TestGetAuthGetAuthStatus(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if got := store.GetAuthStatus("openai"); got.Configured || got.Source != "" || got.Label != "" {
		t.Fatalf("empty status = %+v", got)
	}
	if err := store.Set("openai", Credential{Type: CredentialAPIKey, Key: "sk-stored"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := store.GetAuthStatus("openai"); !got.Configured || got.Source != AuthSourceStored {
		t.Fatalf("stored status = %+v", got)
	}

	t.Setenv("OPENROUTER_API_KEY", "sk-env")
	if got := store.GetAuthStatus("openrouter"); got.Configured || got.Source != AuthSourceEnvironment || got.Label != "OPENROUTER_API_KEY" {
		t.Fatalf("env status = %+v", got)
	}

	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek")
	if got := store.GetAuthStatus("deepseek"); got.Configured || got.Source != AuthSourceEnvironment || got.Label != "DEEPSEEK_API_KEY" {
		t.Fatalf("deepseek env status = %+v", got)
	}

	t.Setenv("MOONSHOT_API_KEY", "sk-moonshot")
	if got := store.GetAuthStatus("moonshotai"); got.Configured || got.Source != AuthSourceEnvironment || got.Label != "MOONSHOT_API_KEY" {
		t.Fatalf("moonshot env status = %+v", got)
	}

	t.Setenv("CLOUDFLARE_API_KEY", "sk-cloudflare")
	if got := store.GetAuthStatus("cloudflare-ai-gateway"); got.Configured || got.Source != AuthSourceEnvironment || got.Label != "CLOUDFLARE_API_KEY" {
		t.Fatalf("cloudflare env status = %+v", got)
	}

	t.Setenv("COPILOT_GITHUB_TOKEN", "copilot-token")
	if got := store.GetAuthStatus("github-copilot"); got.Configured || got.Source != AuthSourceEnvironment || got.Label != "COPILOT_GITHUB_TOKEN" {
		t.Fatalf("copilot env status = %+v", got)
	}

	t.Setenv("TOGETHER_API_KEY", "sk-together")
	if got := store.GetAuthStatus("together"); got.Configured || got.Source != AuthSourceEnvironment || got.Label != "TOGETHER_API_KEY" {
		t.Fatalf("together env status = %+v", got)
	}

	t.Setenv("XIAOMI_API_KEY", "sk-xiaomi")
	if got := store.GetAuthStatus("xiaomi"); got.Configured || got.Source != AuthSourceEnvironment || got.Label != "XIAOMI_API_KEY" {
		t.Fatalf("xiaomi env status = %+v", got)
	}
}

// TestAuthGetProviderEnv verifies per-provider env overrides round-trip through
// auth.json and that GetProviderEnv returns a copy only for api_key creds with
// a non-empty env. Mirrors upstream auth-storage.ts getProviderEnv.
func TestAuthGetProviderEnv(t *testing.T) {
	dir := t.TempDir()
	store, err := NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("NewAuthStorage: %v", err)
	}
	if err := store.Set("openai", Credential{
		Type: CredentialAPIKey,
		Key:  "sk-x",
		Env:  map[string]string{"OPENAI_BASE_REGION": "eu"},
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set("anthropic", Credential{Type: CredentialAPIKey, Key: "sk-y"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	env, err := store.GetProviderEnv("openai")
	if err != nil {
		t.Fatalf("GetProviderEnv: %v", err)
	}
	if env["OPENAI_BASE_REGION"] != "eu" {
		t.Errorf("provider env not returned: got %v", env)
	}
	// returned map is a copy: mutating it must not affect storage
	env["OPENAI_BASE_REGION"] = "mutated"
	again, _ := store.GetProviderEnv("openai")
	if again["OPENAI_BASE_REGION"] != "eu" {
		t.Error("GetProviderEnv must return a copy, not the stored map")
	}
	// cred without env → nil
	if got, _ := store.GetProviderEnv("anthropic"); got != nil {
		t.Errorf("provider without env should return nil, got %v", got)
	}
	// missing provider → nil
	if got, _ := store.GetProviderEnv("does-not-exist"); got != nil {
		t.Errorf("missing provider should return nil, got %v", got)
	}
}

// TestAuthGetResolvesKeyAgainstProviderEnv verifies a credential's own `$VAR`
// key reference resolves against the credential's scoped env, taking
// precedence over the process environment. Mirrors upstream auth-storage.ts
// resolveConfigValue(cred.key, cred.env).
func TestAuthGetResolvesKeyAgainstProviderEnv(t *testing.T) {
	t.Setenv("PIG_TEST_SCOPED_KEY", "from-process")
	dir := t.TempDir()
	store, err := NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("NewAuthStorage: %v", err)
	}
	if err := store.Set("openai", Credential{
		Type: CredentialAPIKey,
		Key:  "$PIG_TEST_SCOPED_KEY",
		Env:  map[string]string{"PIG_TEST_SCOPED_KEY": "from-scope"},
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	c, ok, err := store.Get("openai")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if c.Key != "from-scope" {
		t.Errorf("credential key should resolve against scoped env: got %q want %q", c.Key, "from-scope")
	}
}

// TestAuthFileWithBOMLoads mirrors upstream auth-storage.ts, which parses
// JSON.parse(stripBom(content)) so a hand-edited auth.json saved with a byte
// order mark still loads.
func TestAuthFileWithBOMLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbf"+`{"openai":{"type":"api_key","key":"sk-bom"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	cred, ok, err := store.Get("openai")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if cred.Key != "sk-bom" {
		t.Fatalf("key = %q, want sk-bom", cred.Key)
	}
}
