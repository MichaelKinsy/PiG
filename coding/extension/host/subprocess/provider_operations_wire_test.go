package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// providerOperationRig runs one in-process Go extension that registers the provider "dyn" with a refreshModels and an oauth.modifyModels, and hands back the callbacks the host wired for it.
type providerOperationRig struct {
	refresh func(extension.RefreshModelsContext) ([]extension.ProviderModelConfig, error)
	modify  func([]extension.Model, extension.OAuthCredentials) ([]extension.Model, error)
	conn    chan net.Conn

	mu     sync.Mutex
	frames map[string][]json.RawMessage // request method -> Args of each such request the host wrote
}

func newProviderOperationRig(t *testing.T, refreshModels sdk.ProviderRefreshModelsFunc, modify func([]map[string]any, sdk.OAuthCredentials) ([]map[string]any, error)) *providerOperationRig {
	t.Helper()
	rig := &providerOperationRig{conn: make(chan net.Conn, 1), frames: map[string][]json.RawMessage{}}
	if refreshModels == nil {
		refreshModels = func(sdk.RefreshModelsContext) ([]map[string]any, error) { return nil, nil }
	}
	if modify == nil {
		modify = func(models []map[string]any, _ sdk.OAuthCredentials) ([]map[string]any, error) { return models, nil }
	}
	ext := sdk.New("probe")
	ext.RegisterProvider("dyn", sdk.ProviderConfig{"api": "dyn-api", "baseUrl": "https://dyn.test/v1", "apiKey": "k",
		"models": []any{map[string]any{"id": "a", "name": "A", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100}},
		"oauth": &sdk.OAuthProvider{
			Name: "Dynamic OAuth",
			Login: func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				return sdk.OAuthCredentials{Access: "login"}, nil
			},
			RefreshToken: func(creds sdk.OAuthCredentials) (sdk.OAuthCredentials, error) { return creds, nil },
			GetAPIKey:    func(creds sdk.OAuthCredentials) string { return creds.Access },
			ModifyModels: modify,
		},
		"refreshModels": refreshModels})
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetUIBridge(NewUIBridge(func() {}))
	host.SetOutboundObserver(func(_ string, frame []byte) {
		var envelope struct {
			Request *struct {
				Method string          `json:"method"`
				Args   json.RawMessage `json:"args"`
			} `json:"request"`
		}
		if json.Unmarshal(frame, &envelope) != nil || envelope.Request == nil {
			return
		}
		rig.mu.Lock()
		rig.frames[envelope.Request.Method] = append(rig.frames[envelope.Request.Method], append(json.RawMessage(nil), envelope.Request.Args...))
		rig.mu.Unlock()
	})
	host.SetProviderCallbacks(func(_ string, cfg extension.ProviderConfig) error {
		rig.mu.Lock()
		defer rig.mu.Unlock()
		if cfg.RefreshModels != nil {
			rig.refresh = cfg.RefreshModels
		}
		if cfg.OAuth != nil && cfg.OAuth.ModifyModels != nil {
			rig.modify = cfg.OAuth.ModifyModels
		}
		return nil
	}, func(string) {})
	if _, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "probe", Path: "/ext/probe.go", Enabled: true}, func(conn net.Conn) error { rig.conn <- conn; return ext.RunWithConn(conn) }); err != nil {
		t.Fatal(err)
	}
	applyQueuedRegistrations(host)
	rig.mu.Lock()
	defer rig.mu.Unlock()
	if rig.refresh == nil || rig.modify == nil {
		t.Fatalf("the host wired refreshModels = %v and oauth.modifyModels = %v", rig.refresh != nil, rig.modify != nil)
	}
	return rig
}

func (r *providerOperationRig) args(method string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, args := range r.frames[method] {
		out = append(out, string(args))
	}
	return out
}

// Pi's runProviderRefreshPhase hands refreshModels {credential, stored, publish, allowNetwork, force, signal, baseUrl} (packages/ai/src/models.ts:554-562) and JSON.stringify keeps that order,
// so the context the host sends is {credential, stored, allowNetwork, force, baseUrl}, not Go's sorted map keys; the request around it is {kind, context} and the publication's update request is
// {method, params: {token}}.
func TestRefreshModelsRequestKeepsPisMemberOrder(t *testing.T) {
	rig := newProviderOperationRig(t, sdk.ProviderRefreshModelsFunc(func(r sdk.RefreshModelsContext) ([]map[string]any, error) {
		if _, err := r.Publish(sdk.ModelsPublication{Update: func() error { return nil }}); err != nil {
			return nil, err
		}
		return nil, nil
	}), nil)
	force := true
	checkedAt := 77.0
	_, err := rig.refresh(ai.RefreshModelsContext{
		Credential: &ai.Credential{Type: ai.CredentialOAuth, Access: "tok"}, Stored: &ai.ModelsStoreEntry{CheckedAt: &checkedAt}, AllowNetwork: true, Force: &force, BaseURL: "https://dyn.test/v1",
		Signal: t.Context(), Publish: func(publication ai.ModelsPublication) (bool, error) {
			if publication.Update != nil {
				publication.Update()
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"kind":"refreshModels","context":{"credential":{"type":"oauth","refresh":"","access":"tok","expires":0},"stored":{"models":[],"checkedAt":77},"allowNetwork":true,"force":true,"baseUrl":"https://dyn.test/v1"}}`
	operations := rig.args(RequestProviderOperation)
	if len(operations) != 1 || operations[0] != want {
		t.Fatalf("refreshModels request args = %q, want [%s]", operations, want)
	}
	updates := rig.args(MethodProviderSync)
	if len(updates) != 1 || !regexp.MustCompile(`^\{"method":"update","params":\{"token":"[^"]+"\}\}$`).MatchString(updates[0]) {
		t.Fatalf("publication update request args = %q, want {method, params: {token}}", updates)
	}
}

// A context with no credential, stored catalog, force or base URL leaves those members out, as JSON.stringify drops what Pi has undefined (the Node runtime sets them undefined); a force of false is a value and stays.
func TestRefreshModelsRequestWithoutOptionalMembers(t *testing.T) {
	rig := newProviderOperationRig(t, sdk.ProviderRefreshModelsFunc(func(sdk.RefreshModelsContext) ([]map[string]any, error) { return nil, nil }), nil)
	notForced := false
	for _, refreshContext := range []extension.RefreshModelsContext{{}, {Force: &notForced}} {
		refreshContext.Signal = t.Context()
		refreshContext.Publish = func(ai.ModelsPublication) (bool, error) { return true, nil }
		if _, err := rig.refresh(refreshContext); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		`{"kind":"refreshModels","context":{"allowNetwork":false}}`,
		`{"kind":"refreshModels","context":{"allowNetwork":false,"force":false}}`,
	}
	if operations := rig.args(RequestProviderOperation); !slices.Equal(operations, want) {
		t.Fatalf("refreshModels request args = %q, want %q", operations, want)
	}
}

// providerOperationConn is a started connection whose peer reads every frame and never answers, owned by a managedExt as a host holds it.
func providerOperationConn(t *testing.T) (*Conn, *managedExt) {
	t.Helper()
	client, server := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, server) }()
	conn := NewConn("dyn", client)
	ctx, cancel := context.WithCancel(t.Context())
	conn.Start(ctx)
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		<-conn.Done()
	})
	return conn, withConn(&managedExt{config: ExtConfig{Name: "dyn"}}, conn)
}

func providerOperationModels() []extension.Model {
	return []extension.Model{&ai.Model{ID: "a", DisplayName: "A", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "dyn", API: "dyn-api", BaseURL: "https://dyn.test/v1"}}}
}

// adoptConn publishes a connection to its extension before it starts it, and a crash restart's model listing can call oauth.modifyModels in between: the call has a context then, waits for the connection, and ends with an error when the connection ends. It never reads a lifetime that does not exist yet.
func TestProviderRequestsOnAConnectionNotYetStarted(t *testing.T) {
	client, server := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, server) }()
	conn := NewConn("dyn", client)
	me := withConn(&managedExt{config: ExtConfig{Name: "dyn"}}, conn)
	done := make(chan error, 2)
	go func() {
		_, err := providerModifyModels(me, "dyn")(providerOperationModels(), extension.OAuthCredentials{Access: "tok"})
		done <- err
	}()
	go func() {
		_, err := (&Host{}).providerRefreshModels(me, "dyn")(extension.RefreshModelsContext{Publish: func(ai.ModelsPublication) (bool, error) { return true, nil }})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("a request on a connection that has not started returned: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	ctx, cancel := context.WithCancel(t.Context())
	conn.Start(ctx)
	cancel()
	_ = server.Close()
	for range 2 {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("a request whose connection ended returned no error")
			}
		case <-time.After(15 * time.Second):
			t.Fatal("a request did not return after its connection ended")
		}
	}
	<-conn.Done()
}

// When the host closes the connection of an extension (a reload, a disable, shutdown) while oauth.modifyModels runs, the model listing's error is the connection's, "connection closed", as it was when the request ran under no context, not the lifetime's cancellation.
func TestModifyModelsHostCloseIsTheConnectionsError(t *testing.T) {
	conn, me := providerOperationConn(t)
	done := make(chan error, 1)
	go func() {
		_, err := providerModifyModels(me, "dyn")(providerOperationModels(), extension.OAuthCredentials{Access: "tok"})
		done <- err
	}()
	awaitPendingRequest(t, conn)
	_ = conn.Close("test")
	requireConnectionClosed(t, "modifyModels", done)
}

// awaitPendingRequest returns once a request is in flight on the connection.
func awaitPendingRequest(t *testing.T, conn *Conn) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn.pendingMu.Lock()
		requests := len(conn.pending)
		conn.pendingMu.Unlock()
		if requests > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the request never reached the connection")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// requireConnectionClosed waits for the operation's error and requires the connection's own, "connection closed", and not the lifetime's context.Canceled. Which error value carries the text depends on how far the request had got when Close ran (the remote error Close answers a pending request with, or the plain error of a request whose frame the writer abandoned), as it does for a request under no lifetime; TestConnectionEndedIsTheConnectionsRemoteError pins the type connectionEnded returns.
func requireConnectionClosed(t *testing.T, operation string, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err == nil || err.Error() != "connection closed" || errors.Is(err, context.Canceled) {
			t.Fatalf("%s after the host closed the connection = %#v, want \"connection closed\"", operation, err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("%s did not return after the host closed the connection", operation)
	}
}

// connectionEnded turns the cancellation of an ended lifetime into what Close answers a request waiting on the connection, the remote error "connection closed" (errors.As matches it as before), or the failure that stopped the connection, and leaves every other error, and the cancellation of a lifetime still running, alone.
func TestConnectionEndedIsTheConnectionsRemoteError(t *testing.T) {
	conn := NewConn("dyn", nil)
	other := errors.New("other")
	if got := connectionEnded(conn, context.Canceled); got != context.Canceled {
		t.Fatalf("a running lifetime's cancellation = %v, want it unchanged", got)
	}
	conn.endLifetime()
	var remote *remoteError
	if got := connectionEnded(conn, context.Canceled); !errors.As(got, &remote) || got.Error() != "connection closed" || errors.Is(got, context.Canceled) {
		t.Fatalf("an ended lifetime's cancellation = %#v, want the remote error \"connection closed\"", got)
	}
	if got := connectionEnded(conn, other); got != other {
		t.Fatalf("another error = %v, want it unchanged", got)
	}
	if got := connectionEnded(conn, nil); got != nil {
		t.Fatalf("no error = %v, want nil", got)
	}
	failure := errors.New("transport failed")
	conn.failureMu.Lock()
	conn.failure = failure
	conn.failureMu.Unlock()
	if got := connectionEnded(conn, context.Canceled); got != failure {
		t.Fatalf("an ended lifetime with a failure = %v, want the failure", got)
	}
}

// Cancelling the Signal of a refresh (Pi's RefreshModelsContext.signal, models.ts:97-98) ends the request promptly with the cancellation, the extension's own signal aborts, and no
// goroutine of the request is left (TestMain runs the package under the leak check).
func TestRefreshModelsSignalCancelsTheRequest(t *testing.T) {
	started := make(chan struct{})
	aborted := make(chan struct{})
	rig := newProviderOperationRig(t, sdk.ProviderRefreshModelsFunc(func(r sdk.RefreshModelsContext) ([]map[string]any, error) {
		close(started)
		<-r.Signal.Done()
		close(aborted)
		return nil, r.Signal.Err()
	}), nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := rig.refresh(extension.RefreshModelsContext{Signal: ctx, Publish: func(ai.ModelsPublication) (bool, error) { return true, nil }})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled refresh = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the refresh did not return after its Signal was cancelled")
	}
	select {
	case <-aborted:
	case <-time.After(10 * time.Second):
		t.Fatal("the extension's refreshModels never saw its signal abort")
	}
}

// A refresh context built without a Signal has no caller to cancel it: it runs under the connection's lifetime, and when the host closes the connection the refresh's error is the connection's own, "connection closed", the same remote error a request waiting on the closed connection gets, not the lifetime's cancellation.
func TestRefreshModelsWithoutSignalEndsWithTheConnection(t *testing.T) {
	conn, me := providerOperationConn(t)
	done := make(chan error, 1)
	go func() {
		_, err := (&Host{}).providerRefreshModels(me, "dyn")(extension.RefreshModelsContext{Publish: func(ai.ModelsPublication) (bool, error) { return true, nil }})
		done <- err
	}()
	awaitPendingRequest(t, conn)
	_ = conn.Close("test")
	requireConnectionClosed(t, "a refresh without a Signal", done)
}

// Pi's oauth.modifyModels takes (models, credentials) and no AbortSignal (types.ts:2034) and runs inside the synchronous getAllModels (provider-composer.ts:566-575), so nothing cancels it but
// the end of the extension: the request ends, with an error, when the extension's connection ends, and leaves no goroutine behind.
func TestModifyModelsEndsWithTheConnection(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	rig := newProviderOperationRig(t, nil, func(models []map[string]any, _ sdk.OAuthCredentials) ([]map[string]any, error) {
		close(started)
		<-release
		return models, nil
	})
	t.Cleanup(func() { close(release) })
	done := make(chan error, 1)
	go func() {
		_, err := rig.modify([]extension.Model{&ai.Model{ID: "a", DisplayName: "A", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: "dyn", API: "dyn-api", BaseURL: "https://dyn.test/v1"}}}, extension.OAuthCredentials{Access: "tok"})
		done <- err
	}()
	<-started
	_ = (<-rig.conn).Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("modifyModels whose extension connection closed returned no error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("modifyModels did not return after the extension's connection closed")
	}
}
