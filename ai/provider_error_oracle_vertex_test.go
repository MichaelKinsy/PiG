//go:build !pig_strip_mistral_conversations && !pig_strip_google_vertex

package ai

// The Vertex row of TestProviderErrorResponsesMatchPi; a build that strips the Vertex provider leaves it out.
func init() {
	googleVertexErrorCases = append(googleVertexErrorCases, providerErrorCase{"google-vertex", [4]string{"google-vertex", "googleVertexApi", "google-vertex", "google-vertex"}, func(baseURL string) Provider {
		return NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	}})
}
