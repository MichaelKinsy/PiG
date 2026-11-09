package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// upstream: agent-session.ts:2878-2890 (manual) and 3206-3218 (auto) await runner.emit({ type: "session_before_compact", signal }), and runner.ts emit() awaits every handler in order with event.signal aborted. An abort raised while the handlers run neither skips an extension nor ends the dispatch: the abort reaches a handler as event.signal.
// The second extension's dispatch context must stay live, or a subprocess extension's request fails with the compaction's cancellation and is never delivered.
func TestSessionBeforeCompactReachesEveryExtensionWhenAbortedDuringDispatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		run    func(t *testing.T, sess *Session)
	}{
		{"manual", "manual", func(t *testing.T, sess *Session) {
			if _, err := sess.Compact(context.Background(), ""); !isCompactionCancelled(err) && !errors.Is(err, errOperationAborted) {
				t.Fatalf("Compact error = %v, want the aborted compaction", err)
			}
		}},
		{"auto", "threshold", func(t *testing.T, sess *Session) {
			if compacted, err := sess.runAutoCompaction(context.Background(), "threshold", false); compacted || err != nil {
				t.Fatalf("runAutoCompaction = %v, %v; want an aborted compaction", compacted, err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
			_ = sess.Events()
			defer func() { _ = sess.Close() }()
			var delivered []string
			var liveAtSecond, signalEnded []bool
			first := extension.Extension{Path: "first", Handlers: map[string][]extension.HandlerFn{
				"session_before_compact": {func(args ...any) (any, error) {
					delivered = append(delivered, "first")
					sess.AbortCompaction()
					return nil, nil
				}},
			}}
			second := extension.Extension{Path: "second", Handlers: map[string][]extension.HandlerFn{
				"session_before_compact": {func(args ...any) (any, error) {
					event := args[0].(extension.SessionBeforeCompactEvent)
					delivered = append(delivered, "second:"+event.Reason)
					liveAtSecond = append(liveAtSecond, args[1].(context.Context).Err() == nil)
					signalEnded = append(signalEnded, event.Signal.Err() != nil)
					return nil, nil
				}},
			}}
			sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{first, second}, t.TempDir()))

			tc.run(t, sess)

			if want := []string{"first", "second:" + tc.reason}; len(delivered) != 2 || delivered[0] != want[0] || delivered[1] != want[1] {
				t.Fatalf("session_before_compact delivered to %v, want %v", delivered, want)
			}
			if len(liveAtSecond) != 1 || !liveAtSecond[0] {
				t.Fatalf("the second extension's dispatch context was cancelled by the abort: live = %v", liveAtSecond)
			}
			if len(signalEnded) != 1 || !signalEnded[0] {
				t.Fatalf("event.signal of the second extension's event had not aborted: %v", signalEnded)
			}
			var end *agent.CompactionEndEvent
			for _, event := range drainEvents(t, sess) {
				if value, ok := event.(agent.CompactionEndEvent); ok {
					end = &value
				}
			}
			if end == nil || !end.Aborted {
				t.Fatalf("compaction_end = %#v, want an aborted compaction", end)
			}
		})
	}
}

// upstream: agent-session.ts:4107-4135 awaits runner.emit({ type: "session_before_tree", signal }) and checks no abort after it: a cancel, or a summary, that a handler returns after the navigation was aborted is honoured. The abort reaches the handlers as event.signal; it does not end their dispatch.
func TestNavigateTreeHonoursAHandlerResultReturnedAfterTheAbort(t *testing.T) {
	var delivered []string
	var liveAtSecond, signalEnded []bool
	setup := func(t *testing.T, second extension.HandlerFn) (*Session, string, string) {
		sess := newTreeTestSession(t)
		first := extension.Extension{Path: "first", Handlers: map[string][]extension.HandlerFn{
			"session_before_tree": {func(...any) (any, error) {
				delivered = append(delivered, "first")
				sess.AbortBranchSummary()
				return nil, nil
			}},
		}}
		observer := extension.Extension{Path: "second", Handlers: map[string][]extension.HandlerFn{
			"session_before_tree": {func(args ...any) (any, error) {
				delivered = append(delivered, "second")
				liveAtSecond = append(liveAtSecond, args[1].(context.Context).Err() == nil)
				signalEnded = append(signalEnded, args[0].(extension.SessionBeforeTreeEvent).Signal.Err() != nil)
				return second(args...)
			}},
		}}
		sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{first, observer}, t.TempDir()))
		target := appendTreeUser(t, sess, "first")
		appendUpstreamTreeAssistant(t, sess, "reply")
		current := appendTreeUser(t, sess, "second")
		return sess, target, current
	}
	check := func(t *testing.T) {
		t.Helper()
		if len(delivered) != 2 || delivered[0] != "first" || delivered[1] != "second" {
			t.Fatalf("session_before_tree delivered to %v, want first then second", delivered)
		}
		if len(liveAtSecond) != 1 || !liveAtSecond[0] {
			t.Fatalf("the second extension's dispatch context was cancelled by the abort: live = %v", liveAtSecond)
		}
		if len(signalEnded) != 1 || !signalEnded[0] {
			t.Fatalf("event.signal of the second extension's event had not aborted: %v", signalEnded)
		}
	}

	t.Run("cancel", func(t *testing.T) {
		delivered, liveAtSecond, signalEnded = nil, nil, nil
		sess, target, current := setup(t, func(...any) (any, error) {
			return extension.SessionBeforeTreeResult{Cancel: true}, nil
		})
		res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		check(t)
		if res != (NavigateTreeResult{Cancelled: true}) {
			t.Fatalf("result = %+v, want the late cancel honoured", res)
		}
		if got := treeLeaf(sess); got != current {
			t.Fatalf("leaf = %s, want %s: the navigation ran despite the cancel", got, current)
		}
	})

	t.Run("summary", func(t *testing.T) {
		delivered, liveAtSecond, signalEnded = nil, nil, nil
		sess, target, _ := setup(t, func(...any) (any, error) {
			return extension.SessionBeforeTreeResult{Summary: &extension.SessionBeforeTreeResultSummary{Summary: "late extension summary"}}, nil
		})
		res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		check(t)
		if res.Cancelled || res.SummaryEntry == nil || res.SummaryEntry.Summary != "late extension summary" {
			t.Fatalf("result = %+v, want the late extension summary saved", res)
		}
	})
}
