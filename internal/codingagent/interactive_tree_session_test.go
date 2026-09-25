package codingagent_test

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The complete interactive → Session → summarizer → retry → Esc path must
// preserve the abandoned branch and drain all status events before returning.
func TestInteractiveTreeSummaryRetryEscapePreservesLeaf(t *testing.T) {
	pair := newSessionPair(t, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":30000,"maxDelayMs":30000}}`, 100000, nil,
		reply("first answer", ai.Usage{Input: 10, Output: 10}),
		reply("second answer", ai.Usage{Input: 10, Output: 10}),
		replyError("overloaded_error"),
		reply("next answer", ai.Usage{Input: 10, Output: 10}),
	)
	pair.harness.Do(func() { pair.harness.Enter("first question") })
	pair.harness.WaitIdle(t, 10*time.Second)
	target := *pair.session.Inner().LeafID()
	pair.harness.Do(func() { pair.harness.Enter("second question") })
	pair.harness.WaitIdle(t, 10*time.Second)
	oldLeaf := *pair.session.Inner().LeafID()

	type result struct {
		navigation icodingagent.NavigateTreeResult
		err        error
	}
	done := make(chan result, 1)
	go func() {
		navigation, err := pair.harness.NavigateTree(target, true)
		done <- result{navigation, err}
	}()
	// This safety cancellation also bounds the red case where NavigateTree
	// blocks the owner loop and Status cannot be read. It is not a retry.
	watchdog := time.AfterFunc(10*time.Second, pair.session.AbortBranchSummary)
	defer watchdog.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		kind, label := pair.harness.Status()
		if kind == "retry" {
			if label != "Retrying (1/3) in 30s... (escape to cancel)" {
				t.Fatalf("retry label = %q", label)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("summary retry never reached the live editor: kind=%q label=%q", kind, label)
		}
		time.Sleep(time.Millisecond)
	}
	pair.harness.Do(func() { pair.harness.Key("\x1b") })
	select {
	case outcome := <-done:
		if outcome.err != nil || !outcome.navigation.Aborted {
			t.Fatalf("Esc navigation = %+v, %v", outcome.navigation, outcome.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Esc did not join the summary operation")
	}
	if leaf := pair.session.Inner().LeafID(); leaf == nil || *leaf != oldLeaf {
		t.Fatalf("aborted summary changed the leaf: %v, want %s", leaf, oldLeaf)
	}
	if kind, label := pair.harness.Status(); kind != "" || label != "" {
		t.Fatalf("aborted summary retained status: %s %s", kind, label)
	}
	for _, entry := range pair.session.Inner().Entries() {
		if entry.Base.Type == "branch_summary" {
			t.Fatal("aborted summary was persisted")
		}
	}
	pair.harness.Do(func() { pair.harness.Enter("after cancellation") })
	pair.harness.WaitIdle(t, 10*time.Second)
	if last := pair.session.LastAssistantText(); last == nil || *last != "next answer" {
		t.Fatalf("normal turn after cancellation = %v", last)
	}
}
