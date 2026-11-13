//go:build !pig_strip_google_vertex

package ai

import (
	"context"
	"testing"
)

// providerStreamEventRejectionGoogleVertexCases is the Google Vertex case of TestProviderStreamEventObserverRejectionEndsTheStreamOnEveryAPI.
func providerStreamEventRejectionGoogleVertexCases(googleFirst, googleSecond string) []providerStreamEventRejectionCase {
	return []providerStreamEventRejectionCase{{"google-vertex", "OAuth (ADC token), custom base URL", func(t *testing.T, options StreamOptions) *AssistantMessage {
		server := serveSSE(t, sseFrames(googleFirst, googleSecond))
		want := mustGeneratedModel(t, "google-vertex", "gemini-3-flash-preview").ToModel()
		vertex := NewGoogleVertexProvider(GoogleVertexConfig{ModelMetadata: want, Model: want.ID, ProviderID: want.ProviderMeta.ProviderID, Project: "test-project", Location: "us-central1", BaseURL: server.URL})
		vertex.(*googleVertexProvider).accessToken = func(context.Context, ProviderEnv) (string, error) { return "test-adc", nil }
		stream, err := vertex.Stream(t.Context(), userTranscript("hello"), options)
		if err != nil {
			t.Fatal(err)
		}
		return stream.Result()
	}}}
}
