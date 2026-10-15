package coding

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports session-manager.ts sessionEntryToContextMessages (:439-466) for the entry types the lax-content test does not reach: a branch summary becomes a branchSummary message (an empty
// summary projects to nothing), a compaction becomes a compactionSummary message preceded by its systemMessage when it has one, a custom message keeps its display and details, and
// every state-only entry projects to no message.
func TestSessionEntryToContextMessagesProjectsEachEntryKindLikePi(t *testing.T) {
	const stamp = "2026-01-02T03:04:05.678Z"
	wantMs := float64(time.Date(2026, 1, 2, 3, 4, 5, 678_000_000, time.UTC).UnixMilli())
	entry := func(fields map[string]any) icodingagent.SessionEntry {
		fields["id"], fields["parentId"], fields["timestamp"] = "e1", nil, stamp
		return laxMessageEntry(t, fields)
	}
	project := func(fields map[string]any) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, message := range icodingagent.SessionEntryToContextMessages(entry(fields)) {
			raw, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			out = append(out, decoded)
		}
		return out
	}

	t.Run("branch summary", func(t *testing.T) {
		got := project(map[string]any{"type": "branch_summary", "summary": "went left", "fromId": "abc"})
		want := []map[string]any{{"role": "branchSummary", "summary": "went left", "fromId": "abc", "timestamp": wantMs}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("projected %v, want %v", got, want)
		}
		if got := project(map[string]any{"type": "branch_summary", "summary": "", "fromId": "abc"}); len(got) != 0 {
			t.Fatalf("an empty branch summary projected %v, want nothing", got)
		}
	})

	t.Run("compaction", func(t *testing.T) {
		got := project(map[string]any{"type": "compaction", "summary": "earlier work", "firstKeptEntryId": "k", "tokensBefore": 1234})
		want := []map[string]any{{"role": "compactionSummary", "summary": "earlier work", "tokensBefore": float64(1234), "timestamp": wantMs}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("projected %v, want %v", got, want)
		}
		system := map[string]any{"role": "system", "content": "be brief"}
		got = project(map[string]any{"type": "compaction", "summary": "earlier work", "firstKeptEntryId": "k", "tokensBefore": 1234, "systemMessage": system})
		if len(got) != 2 || got[0]["role"] != "system" || got[0]["content"] != "be brief" || got[1]["role"] != "compactionSummary" {
			t.Fatalf("a compaction with a systemMessage projected %v, want [system message, compactionSummary]", got)
		}
	})

	t.Run("custom message keeps display and details", func(t *testing.T) {
		got := project(map[string]any{"type": "custom_message", "customType": "note", "content": "hi", "display": true, "details": map[string]any{"k": "v"}})
		want := []map[string]any{{"role": "custom", "customType": "note", "content": "hi", "display": true, "details": map[string]any{"k": "v"}, "timestamp": wantMs}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("projected %v, want %v", got, want)
		}
	})

	t.Run("state-only entries project to nothing", func(t *testing.T) {
		for _, fields := range []map[string]any{
			{"type": "model_change", "provider": "p", "modelId": "m"},
			{"type": "thinking_level_change", "thinkingLevel": "high"},
			{"type": "label", "targetId": "x", "label": "l"},
			{"type": "session_info", "name": "n"},
			{"type": "custom", "customType": "state", "data": map[string]any{"a": 1}},
		} {
			if got := project(fields); len(got) != 0 {
				t.Errorf("%v entry projected %v, want nothing", fields["type"], got)
			}
		}
	})
}
