package codingagent

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// provider-composer.ts:382 throws "authHeader requires a resolved API key", and model-registry.ts:113 turns exactly that failure into
// `No API key found for "<provider>"`. ResolveCompatibilityModelAuth recognises it with errors.Is, so the composed API-key and OAuth
// auth must return the sentinel itself, not an error that only has the same text.
func TestWithConfiguredAuthReturnsTheAuthHeaderSentinel(t *testing.T) {
	if _, err := withConfiguredAuth(ai.ModelAuth{}, nil, true); !errors.Is(err, errAuthHeaderNeedsAPIKey) {
		t.Fatalf("withConfiguredAuth without an API key = %v, want errAuthHeaderNeedsAPIKey", err)
	}
	auth, err := withConfiguredAuth(ai.ModelAuth{APIKey: "k"}, nil, true)
	if err != nil || auth.Headers["Authorization"] == nil || *auth.Headers["Authorization"] != "Bearer k" {
		t.Fatalf("withConfiguredAuth with an API key = %+v, %v", auth, err)
	}
}
