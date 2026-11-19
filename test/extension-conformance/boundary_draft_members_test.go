package extensionconformance

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// wantBoundaryDrafts is what every SDK's fixture leaves behind for the drafts its agent_before_settle handler returns: the line the next handler writes about the
// drafts it was handed, then each draft as the host decoded it. The fixture returns a custom draft without data, a context_edit with a null replacement, a
// context_edit without the member, a compaction with a null firstKeptEntryId, details and a null usage, a custom draft with a null data and a custom_message with
// null details. An SDK that drops a null member, writes null for an omitted one, or does not hand the result to the next handler leaves another line.
const wantBoundaryDrafts = `agent_before_settle_drafts:6:null:absent:null:conformance-checkpoint:absent:null:null:null:info` +
	`|{"type":"context_edit","targetId":"conformance-failed-attempt","replacement":null}` +
	`|{"type":"context_edit","targetId":"conformance-omitted-member"}` +
	`|{"type":"compaction","summary":"conformance-summary","firstKeptEntryId":null,"details":{"format":"conformance-checkpoint"},"usage":null}` +
	`|{"type":"custom","customType":"conformance-null-data","data":null}` +
	`|{"type":"custom_message","customType":"conformance-null-details","content":"x","display":false,"details":null}`

// boundaryDraftsLine joins the next handler's report with the drafts after the first as the host decoded them.
func boundaryDraftsLine(t *testing.T, report string, drafts []extension.SessionBoundaryDraft) string {
	t.Helper()
	line := report
	for _, draft := range drafts[1:] {
		raw, err := json.Marshal(draft)
		if err != nil {
			t.Fatalf("encode boundary draft: %v", err)
		}
		line += "|" + string(raw)
	}
	return line
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1007-1021 (ContextEditEntryDraft.replacement, CompactionEntryDraft.firstKeptEntryId);
// packages/coding-agent/src/core/session-manager.ts:1371-1379 (appendContextEdit: null omits the target, undefined is rejected), :1289 (appendCompaction: null and
// undefined keep nothing). An extension that owns compaction returns these drafts to continue a turn after a provider overflow, so the members reach the host as
// the handler wrote them from every SDK, isolated, packed and fused, and the next handler is handed the same.
func TestBoundaryDraftNullAndAbsentMembersAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, test := range append(allHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(test.name, func(t *testing.T) {
			h := test.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("boundary drafts complete")
				}
			})
			h.ui.ClearRecorded()
			boundary, err := h.runner.EmitBoundary(t.Context(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", Outcome: extension.AgentActivityCompleted},
				func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
					return extension.BoundaryContextPreview{ContextEntries: make([]extension.ProjectedSessionEntry, len(entries)), CanContinue: len(entries) > 0}, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if !boundary.Valid || !boundary.Continue || len(boundary.Entries) != 6 {
				t.Fatalf("boundary result: %+v", boundary)
			}
			waitFor(t, func() bool { return len(h.ui.Recorded()) > 1 })
			if got := boundaryDraftsLine(t, h.ui.Recorded()[1], boundary.Entries); got != wantBoundaryDrafts {
				t.Errorf("boundary drafts = %q, want %q", got, wantBoundaryDrafts)
			}
		})
	}
}
