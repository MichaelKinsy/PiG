package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The interactive SessionHandle bridge forwards all four navigateTree options (interactive-mode.ts:2001-2007 passes summarize, customInstructions,
// replaceInstructions and label to session.navigateTree), so session_before_tree sees each of them.
func TestSessionNavigateTreeHandleForwardsEveryOption(t *testing.T) {
	var preparations []extension.TreePreparation
	runner := inproc.NewRunner([]extension.Extension{{Path: "/tree", Handlers: map[string][]extension.HandlerFn{
		"session_before_tree": {func(args ...any) (any, error) {
			preparations = append(preparations, args[0].(extension.SessionBeforeTreeEvent).Preparation)
			return map[string]any{"cancel": true}, nil
		}},
	}}}, t.TempDir())
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), Runner: runner, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	drainSessionEvents(t, sess)
	first := appendUser(t, sess, "first")
	appendAsst(t, sess, "reply")

	result, err := sess.NavigateTreeHandle(context.Background(), first, icodingagent.NavigateTreeOptions{Summarize: true, CustomInstructions: "be brief", ReplaceInstructions: true, Label: "kept"})
	if err != nil || !result.Cancelled {
		t.Fatalf("a handler that cancels the navigation: %+v, %v", result, err)
	}
	if len(preparations) != 1 {
		t.Fatalf("session_before_tree ran %d times, want 1", len(preparations))
	}
	got := preparations[0]
	if got.TargetID != first || !got.UserWantsSummary || got.CustomInstructions != "be brief" || !got.ReplaceInstructions || got.Label != "kept" {
		t.Fatalf("session_before_tree preparation = %+v, want every option forwarded", got)
	}
}
