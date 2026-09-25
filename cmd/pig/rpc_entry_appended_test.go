package main

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// JSON and RPC modes print a cache-warming usage entry as upstream's
// {"type":"entry_appended","entry":…} session event, with the entry exactly
// as it was persisted.
func TestRPCEntryAppendedEventCarriesThePersistedEntry(t *testing.T) {
	entry := `{"type":"usage","id":"u1","parentId":"m1","timestamp":"2026-01-01T00:00:00Z","kind":"cache_warm","provider":"anthropic","model":"claude-opus-4-6","usage":{"input":0,"output":1,"cacheRead":100,"cacheWrite":0,"totalTokens":101,"cost":{"input":0,"output":0,"cacheRead":0.01,"cacheWrite":0,"total":0.01}}}`
	converted := drive(t, agent.EntryAppendedEvent{Entry: json.RawMessage(entry)})
	if len(converted) != 1 {
		t.Fatalf("converted %d events, want 1", len(converted))
	}
	encoded, err := json.Marshal(converted[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"entry_appended","entry":` + entry + `}`; string(encoded) != want {
		t.Fatalf("event = %s\nwant    %s", encoded, want)
	}
}
