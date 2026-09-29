package subprocess

import (
	"encoding/json"
	"net"
	"testing"
)

// A subscription cursor belongs to the current Session, not to the extension.
// A longer replacement used to receive only the tail after the old cursor.
func TestStatePushRestartsCursorOnSessionReplacement(t *testing.T) {
	hostEnd, peer := net.Pipe()
	conn := NewConn("mirror", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test complete"); _ = peer.Close() })
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("getSessionID", func() string { return "replacement" })
	entries := entriesOfSize(3)
	bridge.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
		return entries[cursor:], len(entries), false, "e2"
	})
	host := NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	host.sessionLogSubs = map[string]struct{}{"mirror": {}}
	managed := withConn(&managedExt{config: ExtConfig{Name: "mirror"}, entryCursor: 2, entrySessionID: "outgoing"}, conn)
	done := make(chan error, 1)
	go func() { done <- host.pushStateTo(t.Context(), managed, managed.connection()) }()
	envelope := readLivenessEnvelope(t, peer)
	var payload struct {
		State StatePayload `json:"state"`
	}
	if err := json.Unmarshal(envelope.Notify.Args, &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload.State.Session.EntriesAppended; len(got) != len(entries) {
		t.Fatalf("replacement received %d entries, want the complete %d-entry log", len(got), len(entries))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if managed.entryCursor != len(entries) {
		t.Fatalf("cursor = %d", managed.entryCursor)
	}
}
