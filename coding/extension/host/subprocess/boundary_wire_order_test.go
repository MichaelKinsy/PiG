package subprocess

import (
	stdjson "encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/boundarywire"
)

// A TS extension reads the event object runner.ts emitBoundary builds as {...baseEvent, entries, continue, context}: turn_end's base is
// {type, turnIndex, message, toolResults, messageEntryId, toolResultEntryIds, outcome} (agent-session.ts:867-875) and agent_before_settle's is
// {type, outcome} (agent-session.ts:1989). Object.keys of both under real Pi (Node 24, 2026-10-15) is the list below, and it is what the
// encoding makeEventHandler sends to an extension process must carry, with or without the dispatch's cache.
func TestMarshalEventWireKeepsPiBoundaryMemberOrder(t *testing.T) {
	want := []string{
		"type turnIndex message toolResults messageEntryId toolResultEntryIds outcome entries continue context",
		"type outcome entries continue context",
	}
	_, cache := boundarywire.WithCache(t.Context())
	for _, c := range []*boundarywire.Cache{nil, cache} {
		for i, event := range boundaryEvents(boundaryPreview("order", false)) {
			encoded, err := marshalEventWire(boundaryEventWire(event, c))
			if err != nil {
				t.Fatal(err)
			}
			if direct := marshalEvent(t, event); string(encoded) != direct {
				t.Errorf("event %d (cache %v) bytes differ from the event's own encoding:\n got %s\nwant %s", i, c != nil, encoded, direct)
			}
			decoder := stdjson.NewDecoder(strings.NewReader(string(encoded)))
			if _, err := decoder.Token(); err != nil {
				t.Fatal(err)
			}
			var keys []string
			for decoder.More() {
				key, _ := decoder.Token()
				keys = append(keys, key.(string))
				var skip stdjson.RawMessage
				if err := decoder.Decode(&skip); err != nil {
					t.Fatal(err)
				}
			}
			if got := strings.Join(keys, " "); got != want[i] {
				t.Errorf("event %d (cache %v) members %q, want %q", i, c != nil, got, want[i])
			}
		}
	}
}
