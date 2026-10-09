//go:build pig_strip_google_vertex

package ai

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// This build has no google-vertex provider: golang.org/x/oauth2 (Application Default Credentials) is not linked.
func init() { pigstrip.Strip(pigstrip.ListAPIs, string(APIGoogleVertex)) }

// NewGoogleVertexAPIProvider reports that this Piglet strips google-vertex.
func NewGoogleVertexAPIProvider(cfg GoogleVertexConfig) (Provider, error) {
	return nil, strippedGoogleVertexError(&cfg)
}
