package subagents

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Ports the `failureSource` cases of apps/p2/test/p2/subagent-reads.test.ts (Pi main 335814952): where a report takes a
// subagent's run failure from.
func entry(id int) *durable.EntryId { v := durable.EntryId(id); return &v }

func TestFailureSourceKeepsTheCachedFailureOfAnIdleSubagentSameNewestEntryNoEnd(t *testing.T) {
	known := &seenChild{lastEntryID: entry(7), failure: &childFailure{reason: "aborted"}}
	if got := failureSourceOf(known, entry(7), false); got.Read || got.Failure == nil || got.Failure.reason != "aborted" {
		t.Fatalf("got %+v", got)
	}
	known.failure = nil
	if got := failureSourceOf(known, entry(7), false); got.Read || got.Failure != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestFailureSourceReadsAfterANewEntryAfterItsWorkEndedAndForASubagentNotKnownYet(t *testing.T) {
	known := &seenChild{lastEntryID: entry(7), failure: &childFailure{reason: "aborted"}}
	for name, got := range map[string]FailureSource{
		"new entry":    failureSourceOf(known, entry(8), false),
		"work ended":   failureSourceOf(known, entry(7), true),
		"first report": failureSourceOf(nil, entry(7), false),
		"no entries":   failureSourceOf(nil, nil, false),
	} {
		if !got.Read {
			t.Errorf("%s: got %+v, want a read", name, got)
		}
	}
}

func TestFailureSourceKeepsTheCachedFailureOfASubagentWithoutEntries(t *testing.T) {
	known := &seenChild{failure: &childFailure{reason: "aborted"}}
	if got := failureSourceOf(known, nil, false); got.Read || got.Failure == nil || got.Failure.reason != "aborted" {
		t.Fatalf("got %+v", got)
	}
}
