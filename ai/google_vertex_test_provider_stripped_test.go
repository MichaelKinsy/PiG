//go:build pig_strip_google_vertex

package ai

import (
	"context"
	"net/http"
	"testing"
)

// newGoogleVertexTestProvider panics: this build strips google-vertex.
func newGoogleVertexTestProvider(cfg GoogleVertexConfig, _ *http.Client) Provider {
	panic(strippedGoogleVertexError(&cfg))
}

// newGoogleVertexADCTestProvider panics: this build strips google-vertex.
func newGoogleVertexADCTestProvider(cfg GoogleVertexConfig, _ string) Provider {
	panic(strippedGoogleVertexError(&cfg))
}

// vertexADCTestContext panics: this build strips google-vertex.
func vertexADCTestContext(testing.TB, *http.Client) (context.Context, StreamOptions) {
	panic(strippedGoogleVertexError(&GoogleVertexConfig{}))
}
