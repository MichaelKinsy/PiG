package coding

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// upstream print-mode.ts and rpc-mode.ts bind ctx.navigateTree to session.navigateTree(targetId, {summarize, customInstructions, replaceInstructions, label}) and return {cancelled}; newSession, fork
// and switchSession pass their options through. Each Session-backed command action, with and without a context, forwards every option and reports cancellation and failure.
// Pi: packages/coding-agent/src/modes/print-mode.ts:85-93 (navigateTree), packages/coding-agent/src/core/extensions/types.ts:334-356.
func TestSessionCommandActionsForwardTheirOptions(t *testing.T) {
	var preparations []extension.TreePreparation
	var forkPositions []string
	cancelTree := true
	runner := inproc.NewRunner([]extension.Extension{{Path: "/actions", Handlers: map[string][]extension.HandlerFn{
		"session_before_tree": {func(args ...any) (any, error) {
			preparations = append(preparations, args[0].(extension.SessionBeforeTreeEvent).Preparation)
			return map[string]any{"cancel": cancelTree}, nil
		}},
		"session_before_fork": {func(args ...any) (any, error) {
			forkPositions = append(forkPositions, args[0].(extension.SessionBeforeForkEvent).Position)
			return nil, nil
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
	original, originalID := sess.Path(), sess.ID()
	actions := sess.ExtensionCommandActions()

	navigate := actions.NavigateTree
	result, err := navigate(first, &extension.NavigateTreeOptions{Summarize: true, CustomInstructions: "be brief", ReplaceInstructions: true, Label: "kept"})
	if err != nil || !result.Cancelled {
		t.Fatalf("a handler that cancels the navigation: %+v, %v", result, err)
	}
	want := extension.TreePreparation{TargetID: first, UserWantsSummary: true, CustomInstructions: "be brief", ReplaceInstructions: true, Label: "kept"}
	if len(preparations) != 1 || preparations[0].TargetID != want.TargetID || preparations[0].UserWantsSummary != want.UserWantsSummary || preparations[0].CustomInstructions != want.CustomInstructions ||
		preparations[0].ReplaceInstructions != want.ReplaceInstructions || preparations[0].Label != want.Label {
		t.Fatalf("session_before_tree preparation = %+v, want the options %+v", preparations, want)
	}
	if _, err := navigate("missing", nil); err == nil {
		t.Error("navigating to an unknown entry returned no error")
	}
	cancelTree = false
	if result, err := navigate(first, nil); err != nil || result.Cancelled {
		t.Errorf("an uncancelled navigation = %+v, %v", result, err)
	}

	fork := actions.Fork
	if result, err := fork(first, &extension.ForkOptions{Position: "at"}); err != nil || result.Cancelled || !slices.Equal(forkPositions, []string{"at"}) {
		t.Fatalf("fork at = %+v, %v, positions %v", result, err, forkPositions)
	}
	if leaf := sess.LeafID(); leaf == nil || *leaf != first {
		t.Errorf("fork at left the leaf at %v, want %s", leaf, first)
	}

	switchSession := actions.SwitchSession
	if result, err := switchSession(original, nil); err != nil || result.Cancelled || sess.ID() != originalID {
		t.Fatalf("switchSession = %+v, %v, id=%s want %s", result, err, sess.ID(), originalID)
	}
	if result, err := actions.SwitchSessionContext(context.Background(), original, nil); err != nil || result.Cancelled || sess.ID() != originalID {
		t.Fatalf("switchSessionContext = %+v, %v, id=%s want %s", result, err, sess.ID(), originalID)
	}

	newSession := actions.NewSession
	if result, err := newSession(&extension.NewSessionOptions{ParentSession: original}); err != nil || result.Cancelled || sess.ID() == originalID || sess.Inner().ParentSession() != original {
		t.Fatalf("newSession = %+v, %v, id=%s parent=%q", result, err, sess.ID(), sess.Inner().ParentSession())
	}
}

// Pi's print, RPC and interactive modes bind newSession, fork, switchSession and reload to the runtime host (print-mode.ts:80-99, rpc-mode.ts:318-343): the runtime replaces the Session, passes each
// option object on, and reports a cancelled replacement, with and without a context.
// Pi: packages/coding-agent/src/modes/print-mode.ts:70-99.
func TestRuntimeCommandActionsForwardTheirOptionsAndReplaceTheSession(t *testing.T) {
	var forkPositions []string
	cancelFork := false
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_before_fork": {func(args ...any) (any, error) {
				forkPositions = append(forkPositions, args[0].(extension.SessionBeforeForkEvent).Position)
				return map[string]any{"cancel": cancelFork}, nil
			}},
		}}
	}})
	rt := h.runtime
	runtimePrompt(t, rt, "hello")
	original := rt.Session()
	path := original.Path()
	actions := rt.ExtensionCommandActions(original)
	reloads := 0
	rt.SetReload(func(context.Context, *Session) error { reloads++; return nil })

	if err := actions.Reload(); err != nil || reloads != 1 {
		t.Errorf("Reload ran the mode's reload %d times: %v", reloads, err)
	}

	withSession := 0
	record := func(*extension.ReplacedSessionContext) error { withSession++; return nil }
	step := func(name string, call func() (extension.CancelledResult, error), wantWithSession int) {
		t.Helper()
		previous := rt.Session()
		result, err := call()
		if err != nil || result.Cancelled {
			t.Fatalf("%s = %+v, %v", name, result, err)
		}
		if rt.Session() == previous {
			t.Errorf("%s did not replace the runtime's Session", name)
		}
		if withSession != wantWithSession {
			t.Errorf("%s ran withSession %d times in total, want %d", name, withSession, wantWithSession)
		}
	}
	firstEntry := func() string { return rt.Session().SessionManager().GetBranch()[0].Base().ID }
	step("fork", func() (extension.CancelledResult, error) {
		return actions.Fork(firstEntry(), &extension.ForkOptions{Position: "at", WithSession: record})
	}, 1)
	step("switchSession", func() (extension.CancelledResult, error) {
		return actions.SwitchSession(path, &extension.SwitchSessionOptions{WithSession: record})
	}, 2)
	step("forkContext", func() (extension.CancelledResult, error) {
		return actions.ForkContext(t.Context(), firstEntry(), &extension.ForkOptions{Position: "at", WithSession: record})
	}, 3)
	step("switchSessionContext", func() (extension.CancelledResult, error) {
		return actions.SwitchSessionContext(t.Context(), path, &extension.SwitchSessionOptions{WithSession: record})
	}, 4)
	step("newSession", func() (extension.CancelledResult, error) {
		return actions.NewSession(&extension.NewSessionOptions{WithSession: record})
	}, 5)
	step("newSessionContext", func() (extension.CancelledResult, error) {
		return actions.NewSessionContext(t.Context(), &extension.NewSessionOptions{WithSession: record})
	}, 6)
	if !slices.Equal(forkPositions, []string{"at", "at"}) {
		t.Errorf("session_before_fork positions = %v, want the Position option twice", forkPositions)
	}

	cancelFork = true
	current := rt.Session()
	for name, call := range map[string]func() (extension.CancelledResult, error){
		"fork":        func() (extension.CancelledResult, error) { return actions.Fork(firstEntry(), nil) },
		"forkContext": func() (extension.CancelledResult, error) { return actions.ForkContext(t.Context(), firstEntry(), nil) },
	} {
		if result, err := call(); err != nil || !result.Cancelled {
			t.Errorf("a cancelled %s = %+v, %v", name, result, err)
		}
	}
	if rt.Session() != current {
		t.Error("a cancelled fork replaced the Session")
	}
}
