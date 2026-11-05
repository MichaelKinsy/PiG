package coding

// Ports packages/coding-agent/test/suite/regressions/11692-dispose-waits-for-oauth-refresh.test.ts (Pi main b2cb62923, #11692).

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// rotatingOAuthRuntime builds the process's Runtime over services holding an expired OAuth credential of the extension provider "rotating-oauth", whose token endpoint (RefreshToken) has already spent "old-refresh" when its request is in flight. refreshStarted closes when that request is in flight; closing respond lets it answer with the new token.
func rotatingOAuthRuntime(t *testing.T) (runtime *Runtime, services *AgentSessionServices, refreshStarted <-chan struct{}, respond func()) {
	t.Helper()
	services = newTestServices(t)
	if err := services.Auth().Set("rotating-oauth", ai.Credential{Type: ai.CredentialOAuth, Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Second).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	registerRefreshSignalProvider(t, services, "rotating-oauth", ProviderConfigInput{Name: "Rotating OAuth", BaseURL: "https://example.test/v1", API: ai.APIOpenAICompletions, Models: []ai.AnyModel{refreshSignalModel("model")}, OAuth: &ExtensionOAuthConfig{Name: "Rotating subscription", Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
		return ai.Credential{}, errors.New("unused")
	}, RefreshToken: func(_ context.Context, credential ai.Credential) (ai.Credential, error) {
		close(started)
		<-release
		return ai.Credential{Type: ai.CredentialOAuth, Access: "new-access", Refresh: "new-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
	}, GetAPIKey: func(value ai.Credential) string { return value.Access }}, RefreshModels: func(ai.RefreshModelsContext) ([]ai.AnyModel, error) {
		// Like the built-in catalogs, the model list does not need the credential, but the catalog refresh still resolves (and so refreshes) an expired one.
		return nil, nil
	}})
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		session, err := NewSession(services, SessionOptions{SessionManager: options.SessionManager, Model: fakeModel(), Runner: inproc.NewRunner(nil, services.CWD())})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		t.Cleanup(func() { _ = session.Close() })
		return CreateAgentSessionRuntimeResult{Session: session, Services: services}, nil
	}
	runtime, err = CreateAgentSessionRuntime(t.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	return runtime, services, started, func() { once.Do(func() { close(release) }) }
}

func storedOAuth(t *testing.T, services *AgentSessionServices) ai.Credential {
	t.Helper()
	credential, err := services.Registry().ReadCredential(t.Context(), "rotating-oauth")
	if err != nil || credential == nil {
		t.Fatalf("stored credential = %v, %v", credential, err)
	}
	return *credential
}

// Pi: "dispose waits until the rotated token from a background catalog refresh is saved". The refresh starts before the exit and is cancelled by nobody; Close, the process's dispose, returns only once the new token is in auth.json. Red on 989927eff4: Close returns at once and auth.json keeps the spent refresh token.
func TestRuntimeCloseWaitsUntilTheRotatedTokenOfAStartedRefreshIsSaved(t *testing.T) {
	runtime, services, refreshStarted, respond := rotatingOAuthRuntime(t)
	defer respond()
	refreshed := make(chan error, 1)
	go func() {
		_, err := services.Registry().NativeModels().GetAuth(t.Context(), "rotating-oauth", ai.AuthResolutionOverrides{})
		refreshed <- err
	}()
	<-refreshStarted

	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v while the token request was in flight", err)
	case <-time.After(50 * time.Millisecond):
	}

	respond()
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	if got := storedOAuth(t, services); got.Access != "new-access" || got.Refresh != "new-refresh" {
		t.Fatalf("auth.json after Close = access %q refresh %q, want the rotated token", got.Access, got.Refresh)
	}
	if err := <-refreshed; err != nil {
		t.Fatalf("GetAuth = %v", err)
	}
}

// The RPC startup scenario of #11692: the background catalog refresh refreshes the expired credential, the client then closes stdin (the mode's context ends), and the process disposes. The cancelled catalog refresh neither cuts the token request nor discards its result, and Close returns only after the new token is saved.
func TestRuntimeCloseWaitsForTheCatalogRefreshOfAnExpiredCredentialAfterItsContextEnded(t *testing.T) {
	runtime, services, refreshStarted, respond := rotatingOAuthRuntime(t)
	defer respond()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	catalogRefresh := make(chan struct{})
	// As runRPCMode starts it: a model task of the mode's context, with the 15 s refresh timeout.
	if !services.Registry().StartModelTask(ctx, func(taskContext context.Context) {
		defer close(catalogRefresh)
		refreshCtx, cancelRefresh := context.WithTimeout(taskContext, 15*time.Second)
		defer cancelRefresh()
		services.Registry().RefreshCatalogs(refreshCtx, icodingagent.CatalogRefreshOptions{AllowNetwork: true})
	}) {
		t.Fatal("model task not admitted")
	}
	<-refreshStarted
	cancel(errors.New("stdin closed"))

	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v while the token request was in flight", err)
	case <-time.After(50 * time.Millisecond):
	}
	respond()
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	if got := storedOAuth(t, services); got.Access != "new-access" || got.Refresh != "new-refresh" {
		t.Fatalf("auth.json after Close = access %q refresh %q, want the rotated token", got.Access, got.Refresh)
	}
	<-catalogRefresh
}

// A caller that gave up on the refresh (the request's context ended, as at a signal) does not end the refresh; Close still waits for the write, and the refresh's start is not delayed by the exit.
func TestRuntimeCloseWaitsForARefreshWhoseCallerWasCancelled(t *testing.T) {
	runtime, services, refreshStarted, respond := rotatingOAuthRuntime(t)
	defer respond()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	refreshed := make(chan error, 1)
	go func() {
		_, err := services.Registry().NativeModels().GetAuth(ctx, "rotating-oauth", ai.AuthResolutionOverrides{})
		refreshed <- err
	}()
	<-refreshStarted
	cancel(errors.New("exit"))
	<-refreshed

	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v before the refresh was saved", err)
	case <-time.After(50 * time.Millisecond):
	}
	respond()
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	if got := storedOAuth(t, services); got.Refresh != "new-refresh" {
		t.Fatalf("auth.json refresh token after Close = %q, want the rotated one", got.Refresh)
	}
}

// SettleCredentialWrites ends with the caller's cancellation (the second signal's exit) and leaves the refresh running.
func TestRuntimeSettleCredentialWritesReturnsTheCancellationCause(t *testing.T) {
	runtime, services, refreshStarted, respond := rotatingOAuthRuntime(t)
	defer respond()
	go func() {
		_, _ = services.Registry().NativeModels().GetAuth(t.Context(), "rotating-oauth", ai.AuthResolutionOverrides{})
	}()
	<-refreshStarted
	cause := errors.New("second signal")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	if err := runtime.SettleCredentialWrites(ctx); !errors.Is(err, cause) {
		t.Fatalf("SettleCredentialWrites = %v, want the cancellation cause", err)
	}
	respond()
	if err := runtime.SettleCredentialWrites(t.Context()); err != nil {
		t.Fatalf("SettleCredentialWrites after the refresh = %v", err)
	}
	if got := storedOAuth(t, services); got.Refresh != "new-refresh" {
		t.Fatalf("auth.json refresh token = %q, want the rotated one", got.Refresh)
	}
}

// A subprocess extension's provider refreshes the credential in the catalog refresh through the registry's own credential transaction. Once its token request has started, the cancellation of the catalog refresh (stdin closed, a signal) ends only the wait for the auth.json lock: the rotated token is still written, and Close waits for it. Red without ai.ModifyCredentialDetached: the transaction's lock context is the cancelled one and the new token is discarded after the request.
func TestNativeProviderCatalogRefreshSavesTheRotatedTokenAfterItsContextEnded(t *testing.T) {
	runtime, services, _, respond := rotatingOAuthRuntime(t)
	defer respond()
	if err := services.Auth().Set("native-oauth", ai.Credential{Type: ai.CredentialOAuth, Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Second).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	provider := &extension.NativeProvider{
		ID: "native-oauth", Name: "Native OAuth", BaseURL: "https://example.test/v1",
		Models:    []extension.ProviderModelConfig{{ID: "model", Name: "model"}},
		Auth:      ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: "Native subscription"}},
		CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) { return nil, nil },
		ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
			return nil, nil, errors.New("unused")
		},
		// The proxy of a subprocess extension: the token request ignores the catalog refresh's cancellation.
		ResolveRefreshCredential: func(_ context.Context, credential *ai.Credential) (*ai.Credential, *ai.Credential, error) {
			close(started)
			<-release
			next := ai.Credential{Type: ai.CredentialOAuth, Access: "new-access", Refresh: "new-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}
			return &next, &next, nil
		},
		RefreshModels: func(ai.RefreshModelsContext) error { return nil },
		Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return nil, errors.New("unused")
		},
	}
	if err := services.Registry().RegisterNativeProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	catalogRefresh := make(chan struct{})
	go func() {
		defer close(catalogRefresh)
		services.Registry().RefreshCatalogs(ctx, icodingagent.CatalogRefreshOptions{AllowNetwork: true, Providers: []string{"native-oauth"}})
	}()
	<-started
	cancel(errors.New("stdin closed"))

	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v while the token request was in flight", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	<-catalogRefresh
	got, err := services.Registry().ReadCredential(t.Context(), "native-oauth")
	if err != nil || got == nil || got.Refresh != "new-refresh" {
		t.Fatalf("auth.json after Close = %+v, %v; want the rotated token", got, err)
	}
}

// The request-time refresh of a subprocess extension's provider (NativeProviderAuth): the caller that stopped waiting (its request was cancelled, as at an exit) returns at once with the abort, the refresh that has started goes on and is saved, and Close waits for it. Red without the detached transaction: the cancelled caller's lock context discards the rotated token after the request and Close returns at once.
func TestNativeProviderAuthSavesTheRotatedTokenAfterTheCallerWasCancelled(t *testing.T) {
	runtime, services, _, respond := rotatingOAuthRuntime(t)
	defer respond()
	if err := services.Auth().Set("native-oauth", ai.Credential{Type: ai.CredentialOAuth, Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Second).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	provider := &extension.NativeProvider{
		ID: "native-oauth", Name: "Native OAuth", BaseURL: "https://example.test/v1",
		Models:    []extension.ProviderModelConfig{{ID: "model", Name: "model"}},
		Auth:      ai.ProviderAuth{OAuth: &ai.OAuthAuth{Name: "Native subscription"}},
		CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) { return nil, nil },
		ResolveAuth: func(_ context.Context, _ *ai.Credential, _ ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
			close(started)
			<-release
			next := ai.Credential{Type: ai.CredentialOAuth, Access: "new-access", Refresh: "new-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}
			return &ai.AuthResult{}, &next, nil
		},
		ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
		Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return nil, errors.New("unused")
		},
	}
	if err := services.Registry().RegisterNativeProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	returned := make(chan error, 1)
	go func() {
		_, err := services.Registry().NativeProviderAuth(ctx, "native-oauth", ai.AuthResolutionOverrides{})
		returned <- err
	}()
	<-started
	cancel(errors.New("exit"))
	if err := <-returned; err == nil {
		t.Fatal("NativeProviderAuth returned no error to a caller whose request was cancelled")
	}

	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v while the refresh was in flight", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	got, err := services.Registry().ReadCredential(t.Context(), "native-oauth")
	if err != nil || got == nil || got.Refresh != "new-refresh" {
		t.Fatalf("auth.json after Close = %+v, %v; want the rotated token", got, err)
	}
}

// Pi's dispose awaits settleCredentialWrites right after session.dispose(), with the extensions still running. PiG's own teardown of a Close (the factory's Dispose: Host.Shutdown kills the extension processes, Services.Close cancels the model tasks) comes after the wait, or it would cut the in-flight refresh request that the wait is for. Red when Close retires first: the retirement runs while the token request is in flight.
func TestRuntimeCloseSettlesCredentialWritesBeforeItRetiresTheFactoryResources(t *testing.T) {
	runtime, services, refreshStarted, respond := rotatingOAuthRuntime(t)
	defer respond()
	var mu sync.Mutex
	var order []string
	runtime.replacement.current.Load().Dispose = func(reason string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "retire:"+reason)
	}
	go func() {
		_, _ = services.Registry().NativeModels().GetAuth(t.Context(), "rotating-oauth", ai.AuthResolutionOverrides{})
	}()
	<-refreshStarted
	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	early := append([]string(nil), order...)
	mu.Unlock()
	if len(early) != 0 {
		t.Fatalf("Close retired %v while the token request was in flight", early)
	}
	respond()
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 || order[0] != "retire:quit" {
		t.Fatalf("retirements after Close = %v, want [retire:quit]", order)
	}
}
