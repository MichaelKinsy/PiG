package ai

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testAuthContext(env map[string]string, files ...string) AuthContext {
	return AuthContext{
		Env: func(name string) (string, bool) {
			value := env[name]
			return value, strings.TrimSpace(value) != ""
		},
		FileExists: func(path string) bool {
			return slices.Contains(files, path)
		},
	}
}

func fakeOAuth(refreshes *atomic.Int32, refreshed Credential, err error) *OAuthAuth {
	return &OAuthAuth{
		Name: "fake",
		Refresh: func(_ context.Context, current Credential) (Credential, error) {
			refreshes.Add(1)
			if err != nil {
				return Credential{}, err
			}
			return refreshed, nil
		},
		ToAuth: func(credential Credential) (ModelAuth, error) {
			return ModelAuth{APIKey: credential.Access}, nil
		},
	}
}

func TestResolveProviderAuthStoredCredentialOwnsProvider(t *testing.T) {
	ctx := context.Background()
	env := testAuthContext(map[string]string{"OPENAI_API_KEY": "env-key"})
	auth := ProviderAuth{APIKey: EnvAPIKeyAuth("OpenAI API key", "OPENAI_API_KEY")}

	// A stored OAuth credential with no OAuth handler resolves nothing; the
	// environment key is not a silent fallback.
	stored := NewInMemoryAuthStorage(map[string]Credential{"openai": {Type: CredentialOAuth, Access: "a", Refresh: "r", Expires: 1}})
	result, err := ResolveProviderAuth(ctx, "openai", auth, stored, env, AuthResolutionOverrides{})
	if err != nil || result != nil {
		t.Fatalf("stored oauth without handler = %+v, %v; want nil", result, err)
	}

	empty := NewInMemoryAuthStorage(nil)
	result, err = ResolveProviderAuth(ctx, "openai", auth, empty, env, AuthResolutionOverrides{})
	if err != nil || result.Auth.APIKey != "env-key" || result.Source != "OPENAI_API_KEY" {
		t.Fatalf("ambient = %+v, %v", result, err)
	}

	override := "override-key"
	result, err = ResolveProviderAuth(ctx, "openai", auth, stored, env, AuthResolutionOverrides{APIKey: &override})
	if err != nil || result.Auth.APIKey != "override-key" || result.Source != "stored credential" {
		t.Fatalf("override = %+v, %v", result, err)
	}

	withKey := NewInMemoryAuthStorage(map[string]Credential{"openai": {Type: CredentialAPIKey, Key: "stored-key"}})
	result, err = ResolveProviderAuth(ctx, "openai", auth, withKey, env, AuthResolutionOverrides{})
	if err != nil || result.Auth.APIKey != "stored-key" || result.Source != "stored credential" {
		t.Fatalf("stored = %+v, %v", result, err)
	}
}

func TestResolveProviderAuthRefreshesExpiringOAuthOnceAndPersists(t *testing.T) {
	ctx := context.Background()
	var refreshes atomic.Int32
	future := time.Now().Add(time.Hour).UnixMilli()
	oauth := fakeOAuth(&refreshes, Credential{Type: CredentialOAuth, Access: "fresh", Refresh: "r2", Expires: future}, nil)
	store := NewInMemoryAuthStorage(map[string]Credential{"codex": {Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: 0}})

	result, err := ResolveProviderAuth(ctx, "codex", ProviderAuth{OAuth: oauth}, store, testAuthContext(nil), AuthResolutionOverrides{})
	if err != nil || result.Auth.APIKey != "fresh" || result.Source != "OAuth" {
		t.Fatalf("resolve = %+v, %v", result, err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes.Load())
	}
	persisted, _ := store.Read(ctx, "codex")
	if persisted.Access != "fresh" || persisted.Refresh != "r2" {
		t.Fatalf("persisted = %+v", persisted)
	}

	// A token valid for more than five minutes is used as stored.
	if _, err := ResolveProviderAuth(ctx, "codex", ProviderAuth{OAuth: oauth}, store, testAuthContext(nil), AuthResolutionOverrides{}); err != nil {
		t.Fatal(err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d after a valid token, want 1", refreshes.Load())
	}
}

// racingStore simulates another process refreshing between the optimistic
// read and the locked re-check.
type racingStore struct {
	*InMemoryAuthStorage
	rotated Credential
}

func (s *racingStore) Modify(ctx context.Context, providerID string, fn func(*Credential) (*Credential, error)) (*Credential, error) {
	rotated := s.rotated
	next, err := fn(&rotated)
	if err != nil {
		return nil, err
	}
	if next == nil {
		return &rotated, nil
	}
	return next, nil
}

func TestResolveProviderAuthRechecksExpiryUnderTheLock(t *testing.T) {
	var refreshes atomic.Int32
	store := &racingStore{
		InMemoryAuthStorage: NewInMemoryAuthStorage(map[string]Credential{"codex": {Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: 0}}),
		rotated:             Credential{Type: CredentialOAuth, Access: "rotated", Refresh: "r", Expires: time.Now().Add(time.Hour).UnixMilli()},
	}
	oauth := fakeOAuth(&refreshes, Credential{}, nil)
	result, err := ResolveProviderAuth(context.Background(), "codex", ProviderAuth{OAuth: oauth}, store, testAuthContext(nil), AuthResolutionOverrides{})
	if err != nil || result.Auth.APIKey != "rotated" {
		t.Fatalf("resolve = %+v, %v; want the credential another process rotated", result, err)
	}
	if refreshes.Load() != 0 {
		t.Fatalf("refreshes = %d, want 0", refreshes.Load())
	}
}

func TestResolveProviderAuthEnforcesRequestedMinimumValidity(t *testing.T) {
	ctx := context.Background()
	var refreshes atomic.Int32
	tenMinutes := time.Now().Add(10 * time.Minute).UnixMilli()
	oauth := fakeOAuth(&refreshes, Credential{Type: CredentialOAuth, Access: "short", Refresh: "r", Expires: tenMinutes}, nil)
	store := NewInMemoryAuthStorage(map[string]Credential{"codex": {Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: tenMinutes}})

	// Ten minutes left satisfies the default window without a refresh.
	result, err := ResolveProviderAuth(ctx, "codex", ProviderAuth{OAuth: oauth}, store, testAuthContext(nil), AuthResolutionOverrides{})
	if err != nil || result.Auth.APIKey != "old" || refreshes.Load() != 0 {
		t.Fatalf("default window = %+v, %v, refreshes %d", result, err, refreshes.Load())
	}
	// Thirty minutes required: refresh, and reject a token still too short.
	thirty := float64(30 * 60_000)
	_, err = ResolveProviderAuth(ctx, "codex", ProviderAuth{OAuth: oauth}, store, testAuthContext(nil), AuthResolutionOverrides{MinOAuthValidityMs: &thirty})
	var modelsErr *ModelsError
	if !errors.As(err, &modelsErr) || modelsErr.Code != ModelsErrorOAuth || err.Error() != "OAuth refresh returned a token that expires too soon for codex" {
		t.Fatalf("min validity error = %v", err)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d, want 1", refreshes.Load())
	}
}

func TestResolveProviderAuthReportsRefreshFailures(t *testing.T) {
	var refreshes atomic.Int32
	oauth := fakeOAuth(&refreshes, Credential{}, errors.New("invalid_grant"))
	store := NewInMemoryAuthStorage(map[string]Credential{"codex": {Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: 0}})
	_, err := ResolveProviderAuth(context.Background(), "codex", ProviderAuth{OAuth: oauth, APIKey: EnvAPIKeyAuth("k", "CODEX_KEY")}, store,
		testAuthContext(map[string]string{"CODEX_KEY": "env"}), AuthResolutionOverrides{})
	if err == nil || err.Error() != "OAuth refresh failed for codex: invalid_grant" {
		t.Fatalf("refresh failure = %v; want no environment fallback", err)
	}
	persisted, _ := store.Read(context.Background(), "codex")
	if persisted.Access != "old" {
		t.Fatalf("failed refresh changed the stored credential: %+v", persisted)
	}
}

func TestOAuthRefreshAdapterStopsWaitingOnCancellation(t *testing.T) {
	release := make(chan struct{})
	provider := blockingOAuthProvider{release: release}
	refresh := oauthRefresh(provider)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := refresh(ctx, Credential{Type: CredentialOAuth, Refresh: "r"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("refresh = %v; want deadline exceeded", err)
	}
	close(release)
}

type blockingOAuthProvider struct{ release chan struct{} }

func (blockingOAuthProvider) ID() string               { return "blocking" }
func (blockingOAuthProvider) Name() string             { return "Blocking" }
func (blockingOAuthProvider) UsesCallbackServer() bool { return false }
func (blockingOAuthProvider) Login(OAuthLoginCallbacks) (OAuthCredentials, error) {
	return OAuthCredentials{}, errors.New("unused")
}
func (p blockingOAuthProvider) RefreshToken(creds OAuthCredentials) (OAuthCredentials, error) {
	<-p.release
	return creds, nil
}
func (blockingOAuthProvider) GetAPIKey(creds OAuthCredentials) string { return creds.Access }

func TestCheckProviderAuthDoesNotRefreshOAuth(t *testing.T) {
	var refreshes atomic.Int32
	oauth := fakeOAuth(&refreshes, Credential{}, nil)
	store := NewInMemoryAuthStorage(map[string]Credential{"codex": {Type: CredentialOAuth, Access: "old", Refresh: "r", Expires: 0}})
	check, err := CheckProviderAuth(context.Background(), "codex", ProviderAuth{OAuth: oauth}, store, testAuthContext(nil))
	if err != nil || check == nil || check.Type != CredentialOAuth || check.Source != "OAuth" || refreshes.Load() != 0 {
		t.Fatalf("check = %+v, %v, refreshes %d", check, err, refreshes.Load())
	}
	check, err = CheckProviderAuth(context.Background(), "codex", ProviderAuth{}, store, testAuthContext(nil))
	if err != nil || check != nil {
		t.Fatalf("check without handler = %+v, %v", check, err)
	}
}

func TestBuiltinProviderAuthCoversEveryCatalogProvider(t *testing.T) {
	for _, providerID := range ListProviders() {
		auth, err := BuiltinProviderAuth(providerID)
		if err != nil {
			t.Errorf("%s: %v", providerID, err)
			continue
		}
		if auth.APIKey == nil && auth.OAuth == nil {
			t.Errorf("%s has no auth method", providerID)
		}
	}
	codex, _ := BuiltinProviderAuth("openai-codex")
	if codex.APIKey != nil || codex.OAuth == nil {
		t.Fatalf("openai-codex auth = %+v; want OAuth only", codex)
	}
}

func TestBuiltinAPIKeyAuthResolution(t *testing.T) {
	ctx := context.Background()
	resolve := func(providerID string, credential *Credential, env map[string]string, files ...string) *AuthResult {
		t.Helper()
		auth, err := BuiltinProviderAuth(providerID)
		if err != nil {
			t.Fatal(err)
		}
		result, err := auth.APIKey.Resolve(ctx, APIKeyAuthInput{Ctx: testAuthContext(env, files...), Credential: credential})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	anthropic := resolve("anthropic", nil, map[string]string{"ANTHROPIC_AUTH_TOKEN": "tok", "ANTHROPIC_API_KEY": "key"})
	if anthropic.Auth.APIKey != "" || *anthropic.Auth.Headers["Authorization"] != "Bearer tok" || anthropic.Source != "ANTHROPIC_AUTH_TOKEN" {
		t.Fatalf("anthropic auth token = %+v", anthropic)
	}
	anthropic = resolve("anthropic", nil, map[string]string{"ANTHROPIC_OAUTH_TOKEN": "oauth", "ANTHROPIC_API_KEY": "key"})
	if anthropic.Auth.APIKey != "oauth" || anthropic.Source != "ANTHROPIC_OAUTH_TOKEN" {
		t.Fatalf("anthropic oauth token = %+v", anthropic)
	}
	if blank := resolve("openai", nil, map[string]string{"OPENAI_API_KEY": "   "}); blank != nil {
		t.Fatalf("blank env = %+v; want unconfigured", blank)
	}

	bedrock := resolve("amazon-bedrock", &Credential{Type: CredentialAPIKey, Env: map[string]string{"AWS_PROFILE": "work"}}, map[string]string{"AWS_PROFILE": "other"})
	if bedrock.Source != "stored credential" || bedrock.Env["AWS_PROFILE"] != "work" {
		t.Fatalf("bedrock profile = %+v", bedrock)
	}
	if bedrock := resolve("amazon-bedrock", nil, map[string]string{"AWS_ACCESS_KEY_ID": "id"}); bedrock != nil {
		t.Fatalf("bedrock with half a key pair = %+v", bedrock)
	}
	if bedrock := resolve("amazon-bedrock", nil, map[string]string{"AWS_WEB_IDENTITY_TOKEN_FILE": "/t"}); bedrock.Source != "web identity token" {
		t.Fatalf("bedrock web identity = %+v", bedrock)
	}

	adc := filepath.Join(os.TempDir(), "adc.json")
	vertex := resolve("google-vertex", nil, map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": adc, "GCLOUD_PROJECT": "p", "GOOGLE_CLOUD_LOCATION": "l"}, adc)
	if vertex.Source != "gcloud application default credentials" || vertex.Auth.APIKey != "" {
		t.Fatalf("vertex adc = %+v", vertex)
	}
	if vertex := resolve("google-vertex", nil, map[string]string{"GCLOUD_PROJECT": "p", "GOOGLE_CLOUD_LOCATION": "l"}); vertex != nil {
		t.Fatalf("vertex without credentials file = %+v", vertex)
	}
	if vertex := resolve("google-vertex", nil, map[string]string{"GOOGLE_CLOUD_PROJECT": "p", "GOOGLE_CLOUD_LOCATION": "l"}, vertexADCPath); vertex == nil {
		t.Fatal("vertex default ADC path not consulted")
	}

	gateway := resolve("cloudflare-ai-gateway", &Credential{Type: CredentialAPIKey, Key: "cf"}, map[string]string{"CLOUDFLARE_ACCOUNT_ID": "acct", "CLOUDFLARE_GATEWAY_ID": "gw"})
	if gateway.Auth.APIKey != "" || *gateway.Auth.Headers["cf-aig-authorization"] != "Bearer cf" || gateway.Auth.Headers["Authorization"] != nil ||
		gateway.Env["CLOUDFLARE_GATEWAY_ID"] != "gw" || gateway.Source != "stored credential" {
		t.Fatalf("cloudflare gateway = %+v", gateway)
	}
	if workers := resolve("cloudflare-workers-ai", nil, map[string]string{"CLOUDFLARE_API_KEY": "cf"}); workers != nil {
		t.Fatalf("workers ai without account = %+v", workers)
	}
}

func TestOAuthToAuthMirrorsBuiltinProviders(t *testing.T) {
	kimi, ok := OAuthProviderAuth("kimi-coding")
	if !ok {
		t.Fatal("kimi-coding OAuth not registered")
	}
	auth, err := kimi.ToAuth(Credential{Type: CredentialOAuth, Access: "tok"})
	if err != nil || auth.APIKey != "" || *auth.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("kimi toAuth = %+v, %v", auth, err)
	}
	codex, _ := OAuthProviderAuth("openai-codex")
	auth, err = codex.ToAuth(Credential{Type: CredentialOAuth, Access: "tok"})
	if err != nil || auth.APIKey != "tok" || auth.Headers != nil {
		t.Fatalf("codex toAuth = %+v, %v", auth, err)
	}
	if codex.Name != "OpenAI (ChatGPT Plus/Pro)" || !codex.IsSubscription {
		t.Fatalf("codex OAuth metadata = %q %v", codex.Name, codex.IsSubscription)
	}
}

func TestMergeProviderHeadersReplacesCaseInsensitively(t *testing.T) {
	merged := MergeProviderHeaders(ProviderHeaders{"authorization": new("a"), "X-Keep": new("k")}, ProviderHeaders{"Authorization": new("b")})
	if len(merged) != 2 || *merged["Authorization"] != "b" || *merged["X-Keep"] != "k" {
		t.Fatalf("merged = %v", merged)
	}
	if MergeProviderHeaders(nil, nil) != nil {
		t.Fatal("merging nothing should stay nil")
	}
}

func TestResolveProviderAuthHugeMinimumValidityCannotWrap(t *testing.T) {
	var refreshes atomic.Int32
	token := Credential{Type: CredentialOAuth, Access: "token", Refresh: "r", Expires: time.Now().Add(time.Hour).UnixMilli()}
	oauth := fakeOAuth(&refreshes, token, nil)
	store := NewInMemoryAuthStorage(map[string]Credential{"codex": token})
	minimum := 9e24
	_, err := ResolveProviderAuth(t.Context(), "codex", ProviderAuth{OAuth: oauth}, store, testAuthContext(nil), AuthResolutionOverrides{MinOAuthValidityMs: &minimum})
	if err == nil || err.Error() != "OAuth refresh returned a token that expires too soon for codex" || refreshes.Load() != 1 {
		t.Fatalf("huge validity: err=%v refreshes=%d", err, refreshes.Load())
	}
}
