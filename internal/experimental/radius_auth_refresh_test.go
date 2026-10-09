package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestRadiusAuthRefreshesFiveMinuteCredentialAtLocalGateway(t *testing.T) {
	var requests atomic.Int32
	var expiresIn atomic.Int64
	expiresIn.Store(3600)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/oauth/token" || r.Method != http.MethodPost {
			t.Errorf("unexpected network request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 400)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-old" {
			t.Error(r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh-token", "refresh_token": "refresh-new", "expires_in": expiresIn.Load()}); err != nil {
			t.Error(err)
		}
	}))
	defer gateway.Close()
	dir := t.TempDir()
	config := map[string]any{"providers": map[string]any{"radius": map[string]any{"baseUrl": gateway.URL + "/v1", "oauth": "radius"}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	store := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"radius": {Type: ai.CredentialOAuth, Access: "old-token", Refresh: "refresh-old", Expires: time.Now().Add(4 * time.Minute).UnixMilli()}})
	resolver, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Gateway: gateway.URL, CreateRuntime: func() (*codingagent.RequestAuthRuntime, error) {
		return codingagent.NewRequestAuthRuntime(t.Context(), codingagent.RequestAuthRuntimeOptions{Credentials: store, AgentDir: dir})
	}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		auth, err := resolver.Resolve(t.Context(), true)
		if err != nil || auth == nil || auth.Token != "fresh-token" {
			t.Fatalf("resolve = %+v %v", auth, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("expected exactly the expired credential exchange, got %d requests", requests.Load())
	}
	credential, err := store.Read(t.Context(), "radius")
	if err != nil || credential == nil || credential.Refresh != "refresh-new" {
		t.Fatalf("persisted refresh = %+v %v", credential, err)
	}
	expiresIn.Store(120)
	_, err = store.Modify(t.Context(), "radius", func(*ai.Credential) (*ai.Credential, error) {
		return &ai.Credential{Type: ai.CredentialOAuth, Access: "old-token", Refresh: "refresh-old", Expires: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth, err := resolver.Resolve(t.Context(), true); auth != nil || err == nil || !strings.Contains(err.Error(), "expires too soon") {
		t.Fatalf("short refreshed credential accepted: %+v %v", auth, err)
	}
}

func TestRadiusAuthCachesRuntimeFailureAndDoesNotSelectDefaults(t *testing.T) {
	if _, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Input: &AuthInput{Type: "token", Token: "secret"}}); err == nil {
		t.Fatal("selected implicit gateway")
	}
	if _, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Gateway: "http://localhost"}); err == nil {
		t.Fatal("selected implicit stored-auth runtime")
	}
	calls := 0
	failure := errors.New("runtime creation failed")
	resolver, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Gateway: "http://localhost", CreateRuntime: func() (*codingagent.RequestAuthRuntime, error) { calls++; return nil, failure }})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := resolver.Resolve(t.Context(), true); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolver.Resolve(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// upstream: packages/coding-agent/src/experimental/radius-auth.ts:47-49 asks the model runtime for an OAuth credential valid for at least 5 * 60_000 ms. A credential 4m30s from expiry is refreshed; one 5m30s from expiry is used as stored, so a shorter or longer threshold changes the gateway traffic.
func TestRadiusAuthRefreshThresholdIsFiveMinutes(t *testing.T) {
	for _, row := range []struct {
		name      string
		remaining time.Duration
		refreshes int32
		token     string
	}{
		{"4m30s remaining refreshes", 4*time.Minute + 30*time.Second, 1, "fresh-token"},
		{"5m30s remaining is used as stored", 5*time.Minute + 30*time.Second, 0, "old-token"},
	} {
		t.Run(row.name, func(t *testing.T) {
			var requests atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh-token", "refresh_token": "refresh-new", "expires_in": 3600})
			}))
			defer gateway.Close()
			dir := t.TempDir()
			config, err := json.Marshal(map[string]any{"providers": map[string]any{"radius": map[string]any{"baseUrl": gateway.URL + "/v1", "oauth": "radius"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "models.json"), config, 0o600); err != nil {
				t.Fatal(err)
			}
			store := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"radius": {Type: ai.CredentialOAuth, Access: "old-token", Refresh: "refresh-old", Expires: time.Now().Add(row.remaining).UnixMilli()}})
			resolver, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Gateway: gateway.URL, CreateRuntime: func() (*codingagent.RequestAuthRuntime, error) {
				return codingagent.NewRequestAuthRuntime(t.Context(), codingagent.RequestAuthRuntimeOptions{Credentials: store, AgentDir: dir})
			}})
			if err != nil {
				t.Fatal(err)
			}
			auth, err := resolver.Resolve(t.Context(), true)
			if err != nil || auth == nil || auth.Token != row.token || requests.Load() != row.refreshes {
				t.Fatalf("resolve = %+v %v after %d gateway requests", auth, err, requests.Load())
			}
		})
	}
}
