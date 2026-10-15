//go:build !pig_strip_google_vertex

package ai

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
)

// newGoogleVertexTestProvider builds the Vertex provider for multi-provider tests that run in builds with and without Vertex. A non-nil client replaces the provider's HTTP client.
func newGoogleVertexTestProvider(cfg GoogleVertexConfig, client *http.Client) Provider {
	provider := NewGoogleVertexProvider(cfg).(*googleVertexProvider)
	if client != nil {
		provider.client = client
	}
	return provider
}

// newGoogleVertexADCTestProvider builds a Vertex provider whose Application Default Credentials resolve to token.
func newGoogleVertexADCTestProvider(cfg GoogleVertexConfig, token string) Provider {
	provider := NewGoogleVertexProvider(cfg).(*googleVertexProvider)
	provider.accessToken = func(context.Context, ProviderEnv) (string, error) { return token, nil }
	return provider
}

func vertexADCTestContext(t testing.TB, tokenClient *http.Client) (context.Context, StreamOptions) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(path, []byte(`{"type":"authorized_user","client_id":"test-client","client_secret":"test-secret","refresh_token":"test-refresh"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))
	return context.WithValue(t.Context(), oauth2.HTTPClient, tokenClient), StreamOptions{Env: ProviderEnv{"GOOGLE_APPLICATION_CREDENTIALS": path}}
}
