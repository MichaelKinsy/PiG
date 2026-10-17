package mcpext

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Requests Pi sends through the global fetch (the OAuth flow's discovery and token calls included) use Node's fetch semantics when no client is given.
func TestOrDefaultFetchIsNodeFetch(t *testing.T) {
	if _, ok := orDefaultFetch(nil).(*ai.NodeFetch); !ok {
		t.Fatalf("orDefaultFetch(nil) is %T, want *ai.NodeFetch", orDefaultFetch(nil))
	}
}
