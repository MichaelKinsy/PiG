package sessionentry

import (
	"encoding/json"
	"strings"
	"testing"
)

// session-manager.ts appendCompaction/branchWithSummary write `details` as the object the hook or summarizer returned, with its member order;
// an extension event carries that entry, so the decoded members (formerly AsCompaction and AsBranchSummary) must hand it on with the order intact.
func TestAsCompactionAndAsBranchSummaryKeepDetailsOrder(t *testing.T) {
	compactionRaw := json.RawMessage(`{"type":"compaction","id":"c1","parentId":null,"timestamp":"t","summary":"s","firstKeptEntryId":"k","tokensBefore":7,"details":{"zeta":1,"alpha":2},"fromHook":true}`)
	compaction, ok := DecodeSessionEntry(compactionRaw).(CompactionEntry)
	if !ok || compaction.Summary != "s" || compaction.FirstKeptEntryID != "k" || compaction.TokensBefore != 7 || !compaction.FromHook {
		t.Fatalf("DecodeSessionEntry = %+v, %v", compaction, ok)
	}
	if out, err := json.Marshal(compaction.Details); err != nil || string(out) != `{"zeta":1,"alpha":2}` {
		t.Fatalf("compaction details marshal to %s, %v, want the written member order", out, err)
	}
	summaryRaw := json.RawMessage(`{"type":"branch_summary","id":"b1","parentId":"p","timestamp":"t","fromId":"f","summary":"bs","details":{"b":1,"a":2}}`)
	summary, ok := DecodeSessionEntry(summaryRaw).(BranchSummaryEntry)
	if !ok || summary.FromID != "f" || summary.Summary != "bs" {
		t.Fatalf("DecodeSessionEntry = %+v, %v", summary, ok)
	}
	if out, err := json.Marshal(summary.Details); err != nil || string(out) != `{"b":1,"a":2}` {
		t.Fatalf("branch summary details marshal to %s, %v, want the written member order", out, err)
	}
}

// A message entry is no compaction or branch summary entry, and a compaction entry whose record is not JSON describes no member at all.
func TestAsCompactionAndAsBranchSummaryRejectOtherEntries(t *testing.T) {
	message := DecodeSessionEntry(json.RawMessage(`{"type":"message","id":"m","parentId":null,"timestamp":"t","message":{"role":"user","content":"x"}}`))
	if _, ok := message.(CompactionEntry); ok {
		t.Error("a message entry decoded as a compaction entry")
	}
	if _, ok := message.(BranchSummaryEntry); ok {
		t.Error("a message entry decoded as a branch summary entry")
	}
	corrupt := DecodeSessionEntry(json.RawMessage(`{"type":`))
	if _, ok := corrupt.(CompactionEntry); ok {
		t.Error("a corrupt compaction entry decoded")
	}
	if _, ok := corrupt.(RawEntry); !ok {
		t.Errorf("a corrupt record is %T, want the RawEntry that holds it", corrupt)
	}
}

// types.ts:786 SessionCompactEvent.compactionEntry is decoded from the persisted entry's JSON (the extension package aliases this type), so
// decoding a CompactionEntry directly keeps the member order of `details`, as the entry's author wrote it (session-manager.ts appendCompaction).
func TestCompactionEntryDecodesWithDetailsOrder(t *testing.T) {
	var entry CompactionEntry
	raw := `{"type":"compaction","id":"c1","parentId":null,"timestamp":"t","summary":"s","firstKeptEntryId":"k","tokensBefore":7,"details":{"zeta":1,"alpha":2},"fromHook":true}`
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.ID != "c1" || entry.Summary != "s" || !entry.FromHook {
		t.Fatalf("entry = %+v", entry)
	}
	if out, err := json.Marshal(entry.Details); err != nil || string(out) != `{"zeta":1,"alpha":2}` {
		t.Fatalf("details marshal to %s, %v, want the written member order", out, err)
	}
}

// A branch summary's details come back in the order they were written: the session_tree event hands the stored entry to extensions (Pi passes
// the parsed entry object, whose members keep insertion order). A record's fromHook member is written back exactly as it was read: false stays
// false, and a record without the member (Pi's optional fromHook left undefined) stays without it.
func TestBranchSummaryEntryKeepsTheMemberOrderOfDetails(t *testing.T) {
	for _, record := range []string{
		`{"type":"branch_summary","id":"s","parentId":"p","timestamp":"t","fromId":"f","summary":"sum","details":{"z":1,"a":{"y":2,"b":3}},"fromHook":false}`,
		`{"type":"branch_summary","id":"s","parentId":"p","timestamp":"t","fromId":"f","summary":"sum","details":{"z":1,"a":{"y":2,"b":3}}}`,
	} {
		var entry BranchSummaryEntry
		if err := json.Unmarshal([]byte(record), &entry); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != record {
			t.Fatalf("entry = %s, want %s", raw, record)
		}
	}
}

// session-manager.ts:1604-1621 branchWithSummary builds the entry literal {type, id, parentId, timestamp, fromId, summary, details, usage, fromHook}; navigateTree passes fromExtension=false (agent-session.ts:4110-4114), so a summary PiG writes carries "fromHook":false after the usage member, exactly as Pi's file does. Found by the tree-navigate-branch-summary journey, whose session comparison showed the member missing.
func TestBranchSummaryEntryWritesFromHookFalseInPiMemberOrder(t *testing.T) {
	entry := BranchSummaryEntry{
		SessionEntryBase: SessionEntryBase{Type: "branch_summary", ID: "b1", Timestamp: "t"},
		FromID:           "f", Summary: "s", Details: map[string]any{"a": 1},
	}
	out, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"branch_summary","id":"b1","parentId":null,"timestamp":"t","fromId":"f","summary":"s","details":{"a":1},"fromHook":false}`
	if string(out) != want {
		t.Fatalf("branch summary entry =\n%s\nwant\n%s", out, want)
	}
}

// messages.ts:39 declares excludeFromContext optional, and agent-session.ts:3889 assigns options?.excludeFromContext, so the recorded message has the
// member exactly when the caller passed a boolean: interactive ! commands write false (interactive-mode.ts:7029,7058), an RPC bash command without the
// member writes none (rpc-mode.ts:571,577). PiG omitted a false value. Found by the bash journey (session file diff against Pi).
func TestBashExecutionEntryKeepsExcludeFromContextFalseAndOmitsUnset(t *testing.T) {
	for _, tc := range []struct {
		name    string
		exclude *bool
		want    string
	}{
		{"false", new(false), `,"excludeFromContext":false}`},
		{"true", new(true), `,"excludeFromContext":true}`},
		{"unset", nil, `"timestamp":5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := BashExecutionEntry{
				SessionEntryBase: SessionEntryBase{Type: "message", ID: "e1"},
				Role:             "bashExecution", Command: "ls", ExcludeFromContext: tc.exclude, MessageTimestamp: 5,
			}
			encoded, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), tc.want) {
				t.Fatalf("encoded %s lacks %s", encoded, tc.want)
			}
			var decoded BashExecutionEntry
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if (decoded.ExcludeFromContext == nil) != (tc.exclude == nil) || (tc.exclude != nil && *decoded.ExcludeFromContext != *tc.exclude) {
				t.Fatalf("round trip changed excludeFromContext: got %v want %v", decoded.ExcludeFromContext, tc.exclude)
			}
		})
	}
}
