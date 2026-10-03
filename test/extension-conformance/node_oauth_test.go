package extensionconformance

import (
	"context"
	"fmt"
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

	modRoot := findModuleRoot(t)
	fixture := filepath.Join(modRoot, "test", "extension-conformance", "testdata", "node-oauth-fixture", "main.mjs")

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
	if creds.Access != "access-typed-CONF" || creds.Expires != 4242 || creds.AccountID != "account-login" || creds.Scope != "scope-login" {
		t.Fatalf("login creds = %+v, want access-typed-CONF / 4242", creds)
	}

	if key := provider.GetAPIKey(ai.OAuthCredentials{Access: "abc"}); key != "key:abc" {
		t.Fatalf("getApiKey = %q, want key:abc", key)
	}
	if err := oauthAPIKeyError(t); err == nil || !strings.Contains(err.Error(), "getApiKey exploded") {
		t.Fatalf("failing getApiKey error = %v, want the extension's error", err)
	}

	refreshed, err := provider.RefreshToken(ai.OAuthCredentials{Refresh: "seed-refresh", AccountID: "account-refresh", Scope: "scope-refresh"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.Access != "refreshed-seed-refresh" || refreshed.Expires != 9999 || refreshed.AccountID != "account-refresh" || refreshed.Scope != "scope-refresh" {
		t.Fatalf("refresh creds = %+v, want refreshed-seed-refresh / 9999", refreshed)
	}

	// config.oauth defines no credential store, so the proxy must not advertise one.
	if _, isStore := provider.(ai.OAuthCredentialStore); isStore {
		t.Fatal("node config.oauth provider unexpectedly exposes a credential store")
	}
	captureOAuthCredentialObject(t)
	captureOAuthLargeExpiry(t)
}

// TestNodeOAuthSignals checks the AbortSignals Pi hands OAuth callbacks: login receives the interaction signal, refreshToken receives a live signal that a host cancellation aborts, and a signal an extension retains after the callback returns stays unaborted (auth/resolve.ts:149-153, provider-composer.ts:281-292).
func TestNodeOAuthSignals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node OAuth signal test in short mode (spawns node runtime)")
	}
	modRoot := findModuleRoot(t)
	fixture := filepath.Join(modRoot, "test", "extension-conformance", "testdata", "node-oauth-fixture", "main.mjs")
	h := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	built, err := subprocess.NewBuilder(t.TempDir()).Build("node-oauth-signal", fixture)
	if err != nil {
		t.Fatalf("build node launcher: %v", err)
	}
	if _, err := h.Load(context.Background(), subprocess.ExtConfig{Name: "node-oauth-signal", Path: built.BinaryPath, Enabled: true}); err != nil {
		t.Fatalf("load node fixture: %v", err)
	}
	provider, ok := ai.GetOAuthProvider("conformance-oauth-signal")
	if !ok {
		t.Fatal("conformance-oauth-signal provider not registered")
	}
	type contextual interface {
		LoginContext(context.Context, ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error)
		RefreshTokenContext(context.Context, ai.OAuthCredentials) (ai.OAuthCredentials, error)
		GetAPIKeyContext(context.Context, ai.OAuthCredentials) (string, error)
	}
	bridged := provider.(contextual)
	login, err := bridged.LoginContext(context.Background(), ai.OAuthLoginCallbacks{})
	if err != nil || login.Access != "login-signal:true:false" {
		t.Fatalf("login = %+v, %v", login, err)
	}
	refreshed, err := bridged.RefreshTokenContext(context.Background(), ai.OAuthCredentials{Refresh: "r"})
	if err != nil || refreshed.Access != "refresh-signal:true:false" {
		t.Fatalf("refresh = %+v, %v", refreshed, err)
	}
	if key, err := bridged.GetAPIKeyContext(context.Background(), refreshed); err != nil || key != "retained:true:false" {
		t.Fatalf("retained signal after the callback returned = %q, %v", key, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if _, err := bridged.RefreshTokenContext(ctx, ai.OAuthCredentials{Refresh: "hang"}); err == nil {
		t.Fatal("a cancelled host request did not fail")
	}
	// The Host returns on cancellation whether or not the extension observed it; the fixture records the signal only when its abort event fires. Distinct credentials bypass the Host's getApiKey cache.
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; ; attempt++ {
		key, err := bridged.GetAPIKeyContext(context.Background(), ai.OAuthCredentials{Access: fmt.Sprint("probe-", attempt)})
		if err == nil && key == "retained:true:true" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the host cancellation did not abort the refresh signal: getApiKey = %q, %v", key, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestNodeOAuthRefreshSignalFollowsItsCaller checks the refresh signal's sources and lifetime. resolveStoredOAuth passes AbortSignal.any([caller, AbortSignal.timeout(15_000)]) (auth/resolve.ts:149-153); Models.refresh passes the caller's signal alone (models.ts:474). The signal an extension retains follows the caller after the callback returned, and it carries the timeout only in the first case.
func TestNodeOAuthRefreshSignalFollowsItsCaller(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node OAuth signal test in short mode (spawns node runtime)")
	}
	t.Parallel()
	modRoot := findModuleRoot(t)
	fixture := filepath.Join(modRoot, "test", "extension-conformance", "testdata", "node-oauth-fixture", "main.mjs")
	h := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	built, err := subprocess.NewBuilder(t.TempDir()).Build("node-oauth-signal-caller", fixture)
	if err != nil {
		t.Fatalf("build node launcher: %v", err)
	}
	if _, err := h.Load(context.Background(), subprocess.ExtConfig{Name: "node-oauth-signal-caller", Path: built.BinaryPath, Enabled: true}); err != nil {
		t.Fatalf("load node fixture: %v", err)
	}
	provider, ok := ai.GetOAuthProvider("conformance-oauth-signal")
	if !ok {
		t.Fatal("conformance-oauth-signal provider not registered")
	}
	bridged := provider.(interface {
		RefreshTokenContext(context.Context, ai.OAuthCredentials) (ai.OAuthCredentials, error)
		GetAPIKeyContext(context.Context, ai.OAuthCredentials) (string, error)
	})
	probes := 0
	retained := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			// Distinct credentials bypass the Host's getApiKey cache.
			probes++
			key, err := bridged.GetAPIKeyContext(context.Background(), ai.OAuthCredentials{Access: fmt.Sprint("reason:", probes)})
			if err == nil && key == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("retained signal = %q, %v; want %q", key, err, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	steady := func(want string, duration time.Duration) {
		t.Helper()
		for end := time.Now().Add(duration); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			probes++
			if key, err := bridged.GetAPIKeyContext(context.Background(), ai.OAuthCredentials{Access: fmt.Sprint("reason:", probes)}); err != nil || key != want {
				t.Fatalf("retained signal = %q, %v; want it to stay %q", key, err, want)
			}
		}
	}
	const timeout = 300 * time.Millisecond

	// Models.refresh: the caller's signal alone. It outlives the time a composed timeout would have fired, then follows the caller's abort after the callback returned.
	caller, abort := context.WithCancel(context.Background())
	defer abort()
	if _, err := bridged.RefreshTokenContext(caller, ai.OAuthCredentials{Refresh: "r"}); err != nil {
		t.Fatal(err)
	}
	steady("reason:false:undefined", 3*timeout)
	abort()
	retained("reason:true:context canceled")

	// resolveStoredOAuth: the timeout is composed into the signal and fires after the callback returned.
	composed := t.Context()
	refresh, stop := context.WithTimeout(composed, time.Hour)
	defer stop()
	if _, err := bridged.RefreshTokenContext(ai.WithOAuthRefreshTimeout(refresh, composed, timeout), ai.OAuthCredentials{Refresh: "r"}); err != nil {
		t.Fatal(err)
	}
	retained("reason:true:TimeoutError")

	// The composed signal also follows the caller when that abort comes first.
	early, abortEarly := context.WithCancel(context.Background())
	defer abortEarly()
	if _, err := bridged.RefreshTokenContext(ai.WithOAuthRefreshTimeout(early, early, time.Hour), ai.OAuthCredentials{Refresh: "r"}); err != nil {
		t.Fatal(err)
	}
	steady("reason:false:undefined", 2*timeout)
	abortEarly()
	retained("reason:true:context canceled")
}
