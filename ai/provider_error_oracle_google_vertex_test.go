//go:build !pig_strip_google_vertex

package ai

// providerErrorGoogleVertexCases is the Google Vertex case of TestProviderErrorResponsesMatchPi.
func providerErrorGoogleVertexCases() []providerErrorCase {
	return []providerErrorCase{{"google-vertex", [4]string{"google-vertex", "googleVertexApi", "google-vertex", "google-vertex"}, func(baseURL string) Provider {
		return NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "k", Model: "m", BaseURL: baseURL})
	}}}
}
