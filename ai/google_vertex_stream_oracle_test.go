//go:build !pig_strip_google_vertex

package ai

import "testing"

// TestGoogleVertexProviderStreamMatchesPi runs the Gemini streams through Pi's google-vertex provider (API key mode) and Pig's.
func TestGoogleVertexProviderStreamMatchesPi(t *testing.T) {
	providerStreamOracle(t, [4]string{"google-vertex", "googleVertexApi", "google-vertex", "google-vertex"}, false, googleStreamBodies(), func(baseURL string) Provider {
		return NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	})
}
