package mcpext

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Requests Pi sends through the global fetch (the OAuth flow's discovery and token calls included) use Node's fetch semantics when no client is given.
func TestNodeFetchIsNodeFetch(t *testing.T) {
	if _, ok := nodeFetch().(*ai.NodeFetch); !ok {
		t.Fatalf("nodeFetch() is %T, want *ai.NodeFetch", nodeFetch())
	}
}
