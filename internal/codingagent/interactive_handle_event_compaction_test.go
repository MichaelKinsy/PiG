package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts handleEvent (3467-3865): the successful branch of compaction_end.

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// A completed compaction redraws the transcript from the session's context entries and puts the new summary last, below the kept entries, with
// the cost notice of its usage when cache notices are on.
func TestHandleEventCompactionEndRedrawsTheTranscriptWithTheSummaryLast(t *testing.T) {
	p, ids := boundaryCompactionMode(t, func(sess *Session, ids map[string]string) {
		appendMessage(t, sess, ids, "old", userMsg("OLD QUESTION"))
		appendMessage(t, sess, ids, "kept", userMsg("KEPT QUESTION"))
		id, err := sess.AppendCompaction("STORED SUMMARY", ids["kept"], 100, nil, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids["compaction"] = id
	})
	_ = ids
	p.opts.Settings = Settings{ShowCacheMissNotices: true}
	p.toolsExpanded = true
	p.chatContainer.Add(tui.NewTruncatedText("STALE TRANSCRIPT", 0, 0))
	p.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual", HasResult: true, Summary: "NEW SUMMARY", TokensBefore: 4321, Usage: &ai.Usage{Input: 1000, Output: 200, Cost: ai.UsageCost{Total: 0.5}}})
	got := p.chat()
	kept, summary, notice := strings.Index(got, "KEPT QUESTION"), strings.Index(got, "NEW SUMMARY"), strings.Index(got, "Compaction: 1.2k tokens billed (~$0.50)")
	if strings.Contains(got, "STALE TRANSCRIPT") || strings.Contains(got, "OLD QUESTION") || strings.Contains(got, "STORED SUMMARY") || kept < 0 || summary < kept || notice < summary {
		t.Fatalf("chat = %q, want the kept entry, the new summary and its cost notice, in that order, and nothing else", got)
	}
}
