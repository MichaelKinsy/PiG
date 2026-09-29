package experimental

import "testing"

// An empty Promise.all input performs no connect or attach. This boundary guard does not replace the original two-client concurrency case.
func TestAttachExperimentalClientsEmpty(t *testing.T) {
	t.Parallel()
	peers := attachExperimentalClients(t, nil, []string{})
	if peers == nil || len(peers) != 0 {
		t.Fatalf("empty attachment batch = %#v, want non-nil empty list", peers)
	}
}
