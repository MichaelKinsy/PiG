// SPDX-License-Identifier: MIT

package coding

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// pig divergence (D239): an abort ends the turn only; a running compaction finishes, and a second request is refused.
func startBlockedCompaction(t *testing.T) (*Session, func(), chan error) {
	t.Helper()
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	_ = sess.Events()
	t.Cleanup(func() { _ = sess.Close() })
	completer := newBlockingCompactionCompleter()
	sess.completer = completer
	release := sync.OnceFunc(func() { close(completer.release) })
	t.Cleanup(release) // a failing row must not leave the summary blocked
	done := make(chan error, 1)
	go func() {
		_, err := sess.Compact(t.Context(), "")
		done <- err
	}()
	select {
	case <-completer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction did not start")
	}
	return sess, release, done
}

func waitCompactDone(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("compaction did not finish")
		return nil
	}
}

func compactionEntries(sess *Session) int {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	n := 0
	for _, entry := range sess.currentBranch() {
		if entry.Base().Type == "compaction" {
			n++
		}
	}
	return n
}

// R1: RequestAbort during a running compaction leaves it running; it completes and appends its entry.
func TestRequestAbortLeavesARunningCompactionRunning(t *testing.T) {
	sess, release, done := startBlockedCompaction(t)
	sess.RequestAbort()
	select {
	case err := <-done:
		t.Fatalf("compaction ended on abort: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if !sess.IsCompacting() {
		t.Fatal("abort stopped the compaction")
	}
	release()
	if err := waitCompactDone(t, done); err != nil {
		t.Fatalf("compaction: %v", err)
	}
	if compactionEntries(sess) != 1 {
		t.Fatalf("compaction entries = %d, want 1", compactionEntries(sess))
	}
	for _, event := range drainEvents(t, sess) {
		if end, ok := event.(agent.CompactionEndEvent); ok && end.Aborted {
			t.Fatalf("compaction_end aborted: %+v", end)
		}
	}
}

// R3: a second compaction request is refused before any side effect; the first completes.
func TestSecondCompactionRequestIsRefusedAndTheFirstCompletes(t *testing.T) {
	sess, release, done := startBlockedCompaction(t)
	second := make(chan error, 1)
	go func() {
		_, err := sess.Compact(t.Context(), "")
		second <- err
	}()
	select {
	case err := <-second:
		if err == nil || err.Error() != "Already compacting" {
			t.Fatalf("second Compact error = %v, want Already compacting", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Compact did not return Already compacting")
	}
	if sess.AgentRunAbortRequested() {
		t.Fatal("the refused request aborted the run")
	}
	select {
	case err := <-done:
		t.Fatalf("first compaction ended: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	if err := waitCompactDone(t, done); err != nil {
		t.Fatalf("first compaction: %v", err)
	}
	if compactionEntries(sess) != 1 {
		t.Fatalf("compaction entries = %d, want 1", compactionEntries(sess))
	}
}

// R4 (session half): AbortCompaction, the /compact-cancel path, still cancels.
func TestAbortCompactionStillCancelsARunningCompaction(t *testing.T) {
	sess, _, done := startBlockedCompaction(t)
	sess.AbortCompaction()
	err := waitCompactDone(t, done)
	if err == nil || !strings.Contains(err.Error(), "cancel") && !strings.Contains(err.Error(), "abort") {
		t.Fatalf("error = %v, want a cancellation", err)
	}
}

// R5: an awaited abort during a compaction returns once the compaction completes, without deadlock.
func TestAwaitedAbortWaitsForARunningCompaction(t *testing.T) {
	sess, release, done := startBlockedCompaction(t)
	aborted := make(chan error, 1)
	go func() { aborted <- sess.Abort(t.Context()) }()
	select {
	case err := <-aborted:
		t.Fatalf("Abort returned during the compaction: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case err := <-aborted:
		if err != nil {
			t.Fatalf("Abort: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Abort did not return after the compaction completed")
	}
	if err := waitCompactDone(t, done); err != nil {
		t.Fatalf("compaction: %v", err)
	}
}
