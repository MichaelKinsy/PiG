package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// upstream (agent-session.ts 1.5.0+c23b9ec1): lines 3016-3024 (manual) and 3342-3350 (auto) await runner.emit({ type: "session_compact" }) after the entry is saved, and runner.ts:1103-1133 emit() takes no signal: an abort raised while the handlers run neither skips an extension nor fails the compaction.
// The second extension's dispatch context must stay live, or a subprocess extension's request fails with the compaction's cancellation and is never delivered.
func TestSessionCompactReachesEveryExtensionWhenAbortedDuringDispatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		run    func(t *testing.T, sess *Session) (summary string)
	}{
		{"manual", "manual", func(t *testing.T, sess *Session) string {
			result, err := sess.Compact(context.Background(), "")
			if err != nil {
				t.Fatalf("Compact: %v", err)
			}
			return result.Summary
		}},
		{"auto", "threshold", func(t *testing.T, sess *Session) string {
			if compacted, err := sess.runAutoCompaction(context.Background(), "threshold", false); !compacted || err != nil {
				t.Fatalf("runAutoCompaction = %v, %v; want a completed compaction", compacted, err)
			}
			return "extension summary"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
			_ = sess.Events()
			defer func() { _ = sess.Close() }()
			var delivered, failed []string
			var liveAtSecond []bool
			first := extension.Extension{Path: "first", Handlers: map[string][]extension.HandlerFn{
				"session_before_compact": {func(args ...any) (any, error) {
					prep := args[0].(extension.SessionBeforeCompactEvent).Preparation
					return extension.SessionBeforeCompactResult{Compaction: &extension.CompactionResult{
						Summary: "extension summary", FirstKeptEntryID: prep.FirstKeptEntryID, TokensBefore: prep.TokensBefore,
					}}, nil
				}},
				"session_compact": {func(args ...any) (any, error) {
					delivered = append(delivered, "first")
					sess.AbortCompaction()
					return nil, nil
				}},
			}}
			second := extension.Extension{Path: "second", Handlers: map[string][]extension.HandlerFn{
				"session_compact": {func(args ...any) (any, error) {
					delivered = append(delivered, "second:"+args[0].(extension.SessionCompactEvent).Reason)
					liveAtSecond = append(liveAtSecond, args[1].(context.Context).Err() == nil)
					return nil, nil
				}},
				"session_compact_failed": {func(args ...any) (any, error) {
					failed = append(failed, args[0].(extension.SessionCompactFailedEvent).Reason)
					return nil, nil
				}},
			}}
			sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{first, second}, t.TempDir()))

			summary := tc.run(t, sess)

			if want := []string{"first", "second:" + tc.reason}; len(delivered) != 2 || delivered[0] != want[0] || delivered[1] != want[1] {
				t.Fatalf("session_compact delivered to %v, want %v", delivered, want)
			}
			if len(liveAtSecond) != 1 || !liveAtSecond[0] {
				t.Fatalf("the second extension's dispatch context was cancelled by the abort: live = %v", liveAtSecond)
			}
			if len(failed) != 0 {
				t.Fatalf("session_compact_failed after a saved compaction: %v", failed)
			}
			saved := false
			for _, entry := range sess.Entries() {
				if entry.Base().Type == "compaction" {
					saved = true
				}
			}
			if !saved {
				t.Fatal("the compaction entry was not saved")
			}
			var end *agent.CompactionEndEvent
			for _, event := range drainEvents(t, sess) {
				if value, ok := event.(agent.CompactionEndEvent); ok {
					end = &value
				}
			}
			if end == nil || end.Aborted || end.ErrorMessage != "" || end.Summary != summary {
				t.Fatalf("compaction_end = %#v, want a completed compaction with summary %q", end, summary)
			}
		})
	}
}
