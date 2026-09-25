package coding

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// AgentSession.compact and _runAutoCompaction synchronously notify compaction_end
// listeners before awaiting _emitSessionCompactFailed, including early aborts.
func TestCompactionFailureWaitsForEndListeners(t *testing.T) {
	for _, mode := range []string{"manual", "manual-early-abort", "overflow", "early-abort", "exhausted-overflow"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
				defer func() { _ = sess.Close() }()
				sess.completer = &fakeCompleter{err: errors.New("summary failed")}
				endEntered := make(chan struct{})
				releaseEnd := make(chan struct{})
				failed := make(chan extension.SessionCompactFailedEvent, 1)
				releaseFailed := make(chan struct{})
				var end agent.CompactionEndEvent
				sess.Subscribe(func(event agent.AgentEvent) {
					switch event := event.(type) {
					case agent.CompactionStartEvent:
						if strings.HasSuffix(mode, "early-abort") {
							sess.AbortCompaction()
						}
					case agent.CompactionEndEvent:
						end = event
						close(endEntered)
						<-releaseEnd
					}
				})
				sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Path: "failure-order", Handlers: map[string][]extension.HandlerFn{
					"session_compact_failed": {func(args ...any) (any, error) {
						failed <- args[0].(extension.SessionCompactFailedEvent)
						<-releaseFailed
						return nil, nil
					}},
				}}}, t.TempDir()))
				done := make(chan struct{})
				go func() {
					defer close(done)
					switch {
					case strings.HasPrefix(mode, "manual"):
						_, _ = sess.CompactResult(context.Background(), "")
					case mode == "exhausted-overflow":
						sess.overflowRecoveryAttempted.Store(true)
						_, _ = sess.checkCompaction(context.Background(), &agent.AssistantMessage{
							Role: "assistant", StopReason: "error", ErrorMessage: "context window exceeded",
							Provider: "fake", ModelID: "fake-1",
						}, true, nil)
					default:
						sess.runAutoCompaction(context.Background(), "overflow", true)
					}
				}()
				<-endEntered
				manual := strings.HasPrefix(mode, "manual")
				if manual && sess.IsCompacting() {
					t.Error("manual compaction still owns its state during compaction_end")
				}
				synctest.Wait()
				if len(failed) != 0 {
					t.Error("session_compact_failed overtook a blocked compaction_end listener")
				}
				close(releaseEnd)
				event := <-failed
				if event.Reason != end.Reason || event.Aborted != end.Aborted || event.ErrorMessage != end.ErrorMessage || event.WillRetry || event.FromExtension {
					t.Errorf("session_compact_failed = %+v; compaction_end = %+v", event, end)
				}
				synctest.Wait()
				select {
				case <-done:
					t.Error("compaction returned before the failure handler completed")
				default:
				}
				// A listener may start another operation after manual compaction releases ownership.
				replacement := manual && sess.beginCompaction()
				close(releaseFailed)
				<-done
				if replacement {
					if !sess.IsCompacting() {
						t.Error("completed manual compaction cleared its replacement's state")
					}
					sess.finishCompaction()
				}
			})
		})
	}
}
