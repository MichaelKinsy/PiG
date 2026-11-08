package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// pi: packages/coding-agent/test/suite/regressions/9221-reload-during-tool.test.ts "rejects reload during tree navigation" (Pi c3b938e3e;
// the branch summary controller counts as compacting, agent-session.ts isCompacting): a reload while session_before_tree is pending is
// refused before session_shutdown, keeps the runner, and leaves the navigation to finish.
func TestSessionReloadRejectedDuringTreeNavigation(t *testing.T) {
	sess := newTreeTestSession(t)
	started, released := make(chan struct{}), make(chan struct{})
	shutdowns := 0
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{
		"session_shutdown": {func(...any) (any, error) { shutdowns++; return nil, nil }},
		"session_before_tree": {func(...any) (any, error) {
			close(started)
			<-released
			return nil, nil
		}},
	}}}, t.TempDir()))
	runner := sess.ExtensionRunner()
	target := seedTreeHistory(t, sess)
	navigation := make(chan error, 1)
	go func() {
		_, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{})
		navigation <- err
	}()
	waitFor(t, started, "session_before_tree")
	if sess.IsStreaming() {
		t.Fatal("tree navigation must not stream")
	}
	err := sess.Reload(t.Context())
	if err == nil || err.Error() != "Wait for compaction to finish before reloading." {
		t.Fatalf("Reload error = %v, want the compaction refusal", err)
	}
	if shutdowns != 0 || runner.IsStale() || sess.ExtensionRunner() != runner {
		t.Fatalf("a rejected reload touched the runner: shutdowns=%d stale=%v same=%v", shutdowns, runner.IsStale(), sess.ExtensionRunner() == runner)
	}
	close(released)
	if navErr := <-navigation; navErr != nil {
		t.Fatal(navErr)
	}
	if !sess.IsIdle() {
		t.Fatal("the session is not idle after the navigation finished")
	}
}
