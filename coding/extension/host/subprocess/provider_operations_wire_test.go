package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"regexp"
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
			Name:         "Dynamic OAuth",
			Login:        func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) { return sdk.OAuthCredentials{Access: "login"}, nil },
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

// A context with no credential, stored catalog or force sends them as null, baseUrl is absent when the provider has none, and the order of the rest is unchanged.
func TestRefreshModelsRequestWithoutOptionalMembers(t *testing.T) {
	rig := newProviderOperationRig(t, sdk.ProviderRefreshModelsFunc(func(sdk.RefreshModelsContext) ([]map[string]any, error) { return nil, nil }), nil)
	if _, err := rig.refresh(extension.RefreshModelsContext{Signal: t.Context(), Publish: func(ai.ModelsPublication) (bool, error) { return true, nil }}); err != nil {
		t.Fatal(err)
	}
	const want = `{"kind":"refreshModels","context":{"credential":null,"stored":null,"allowNetwork":false,"force":null}}`
	if operations := rig.args(RequestProviderOperation); len(operations) != 1 || operations[0] != want {
		t.Fatalf("refreshModels request args = %q, want [%s]", operations, want)
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

// A refresh context built without a Signal has no caller to cancel it: it runs under the connection's lifetime and ends, with an error, when the extension's connection ends.
func TestRefreshModelsWithoutSignalEndsWithTheConnection(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	rig := newProviderOperationRig(t, sdk.ProviderRefreshModelsFunc(func(sdk.RefreshModelsContext) ([]map[string]any, error) {
		close(started)
		<-release
		return nil, nil
	}), nil)
	t.Cleanup(func() { close(release) })
	done := make(chan error, 1)
	go func() {
		_, err := rig.refresh(extension.RefreshModelsContext{Publish: func(ai.ModelsPublication) (bool, error) { return true, nil }})
		done <- err
	}()
	<-started
	_ = (<-rig.conn).Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a refresh whose extension connection closed returned no error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the refresh did not return after the extension's connection closed")
	}
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
