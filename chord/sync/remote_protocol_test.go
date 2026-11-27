package sync

import "testing"

func TestSyncProtocolConstant(t *testing.T) {
	if SyncProtocol != 1 {
		t.Fatalf("SyncProtocol = %d, want 1", SyncProtocol)
	}
}
