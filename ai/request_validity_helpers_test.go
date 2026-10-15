package ai

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// invalidRequest is the context of a test that sends a request which breaks the provider's message contract on purpose: a lone tool result or assistant message that checks the conversion of one message, or an end-to-end case that Pi's own test suite sends. Every other request a test sends is checked (internal/requestvalidity).
func invalidRequest(t testing.TB) context.Context {
	t.Helper()
	return requestvalidity.Allow(t.Context())
}
