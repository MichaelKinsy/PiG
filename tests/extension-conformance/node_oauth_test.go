package extensionconformance

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestNodeOAuthBridge proves pig's node runtime bridges an upstream
// config.oauth provider. The .mjs fixture registers login/refreshToken/getApiKey
// through the upstream callback surface; the node runtime strips the closures,
// advertises capability flags, and services the oauth_* requests plus the
// oauth.cb.* login callbacks over the wire. Values mirror the Go/Rust/Python
// conformance fixtures, so a regression here is a node-SDK drift.
//
// This is a separate test rather than a fourth column of
// TestConformance_OAuthTransportsMatch because upstream config.oauth defines no
// credential store, so the node provider legitimately lacks the store the other
// fixtures assert.
func TestNodeOAuthBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node OAuth bridge test in short mode (spawns node runtime)")
	}
	shortSockDir(t)

	modRoot := findModuleRoot(t)
	fixture := filepath.Join(modRoot, "tests", "extension-conformance", "testdata", "node-oauth-fixture", "main.mjs")

	h := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })

	// The node runtime ships as an embedded launcher; build it into a runnable
	// script the way pig does before loading a .mjs source extension.
	built, err := subprocess.NewBuilder(t.TempDir()).Build("node-oauth-fixture", fixture)
	if err != nil {
		t.Fatalf("build node launcher: %v", err)
	}

	if _, err := h.Load(context.Background(), subprocess.ExtConfig{
		Name:    "node-oauth-fixture",
		Path:    built.BinaryPath,
		Enabled: true,
	}); err != nil {
		t.Fatalf("load node fixture: %v", err)
	}

	provider, ok := ai.GetOAuthProvider("conformance-oauth")
	if !ok {
		t.Fatal("conformance-oauth provider not registered by node fixture")
	}
	if !ai.IsOAuthSubscriptionProvider(provider.ID()) {
		t.Fatal("Node config.oauth.isSubscription=true was lost during registration")
	}
	if provider.Name() != "Conformance OAuth" {
		t.Fatalf("provider name = %q, want %q", provider.Name(), "Conformance OAuth")
	}

	var deviceCode string
	var deviceCodeMu sync.Mutex
	deviceCodeStarted := make(chan struct{})
	releaseDeviceCode := make(chan struct{})
	cb := ai.OAuthLoginCallbacks{
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			deviceCodeMu.Lock()
			deviceCode = info.UserCode + "|" + info.VerificationURI
			deviceCodeMu.Unlock()
			close(deviceCodeStarted)
			<-releaseDeviceCode
		},
		OnPrompt: func(prompt ai.OAuthPrompt) (string, error) {
			return "typed-CONF", nil
		},
	}
	type loginResult struct {
		credentials ai.OAuthCredentials
		err         error
	}
	loginDone := make(chan loginResult, 1)
	go func() {
		credentials, loginErr := provider.Login(cb)
		loginDone <- loginResult{credentials: credentials, err: loginErr}
	}()
	select {
	case <-deviceCodeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("device-code callback did not start")
	}
	select {
	case result := <-loginDone:
		t.Fatalf("login completed before no-result callback settled: %#v", result)
	default:
	}
	close(releaseDeviceCode)
	var login loginResult
	select {
	case login = <-loginDone:
	case <-time.After(5 * time.Second):
		t.Fatal("login did not complete after callback settled")
	}
	if login.err != nil {
		t.Fatalf("login: %v", login.err)
	}
	creds := login.credentials
	deviceCodeMu.Lock()
	observedDeviceCode := deviceCode
	deviceCodeMu.Unlock()
	if observedDeviceCode != "CONF-USER-CODE|https://conf.example/verify" {
		t.Fatalf("device code not relayed to host callback: %q", observedDeviceCode)
	}
	if creds.Access != "access-typed-CONF" || creds.Expires != 4242 {
		t.Fatalf("login creds = %+v, want access-typed-CONF / 4242", creds)
	}

	if key := provider.GetAPIKey(ai.OAuthCredentials{Access: "abc"}); key != "key:abc" {
		t.Fatalf("getApiKey = %q, want key:abc", key)
	}
	if err := oauthAPIKeyError(t); err == nil || !strings.Contains(err.Error(), "getApiKey exploded") {
		t.Fatalf("failing getApiKey error = %v, want the extension's error", err)
	}

	refreshed, err := provider.RefreshToken(ai.OAuthCredentials{Refresh: "seed-refresh"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.Access != "refreshed-seed-refresh" || refreshed.Expires != 9999 {
		t.Fatalf("refresh creds = %+v, want refreshed-seed-refresh / 9999", refreshed)
	}

	// config.oauth defines no credential store, so the proxy must not advertise one.
	if _, isStore := provider.(ai.OAuthCredentialStore); isStore {
		t.Fatal("node config.oauth provider unexpectedly exposes a credential store")
	}
}
