package coding

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pi: packages/coding-agent/test/suite/regressions/9154-prompt-during-tree-navigation.test.ts (8a3e163b0, #9155)

const navigationDuringPromptError = "Wait for the current response to finish before navigating the session tree."

func seedTreeHistory(t *testing.T, sess *Session) string {
	t.Helper()
	appendTreeUser(t, sess, "first prompt")
	target := appendTreeAssistant(t, sess, "first response")
	appendTreeUser(t, sess, "second prompt")
	appendTreeAssistant(t, sess, "second response")
	sess.refreshContext()
	return target
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestPromptIsRejectedWhileBranchSummarizationIsInProgress(t *testing.T) {
	sess := newTreeTestSession(t)
	started, released := make(chan struct{}), make(chan struct{})
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_before_tree": {func(args ...any) (any, error) {
			close(started)
			<-released
			return nil, nil
		}},
	})
	target := seedTreeHistory(t, sess)
	navigation := make(chan error, 1)
	go func() {
		_, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{})
		navigation <- err
	}()
	waitFor(t, started, "session_before_tree")
	preflight := 0
	err := sess.Prompt(t.Context(), "prompt during summary", &PromptOptions{Source: extension.InputSourceRPC, PreflightResult: func(PromptDisposition) { preflight++ }})
	close(released)
	if navErr := <-navigation; navErr != nil {
		t.Fatal(navErr)
	}
	if err == nil || !strings.Contains(err.Error(), "compaction is in progress") {
		t.Fatalf("prompt error = %v", err)
	}
	if preflight != 0 {
		t.Fatalf("preflightResult called %d times", preflight)
	}
	for _, entry := range sess.inner.Load().GetEntries() {
		if strings.Contains(string(entry.Raw()), "prompt during summary") {
			t.Fatalf("the rejected prompt was persisted: %s", entry.Raw())
		}
	}
}

// The inverse order also loses the completed prompt when navigation rebuilds the context.
func TestNavigationIsRejectedDuringAnAwaitedInputHook(t *testing.T) {
	sess := newTreeTestSession(t)
	started, released := make(chan struct{}), make(chan struct{})
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"input": {func(args ...any) (any, error) {
			close(started)
			<-released
			return nil, nil
		}},
	})
	target := seedTreeHistory(t, sess)
	prompt := make(chan error, 1)
	go func() {
		prompt <- sess.Prompt(t.Context(), "racing prompt", &PromptOptions{Source: extension.InputSourceRPC})
	}()
	waitFor(t, started, "the input hook")
	_, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{})
	close(released)
	<-prompt
	if err == nil || err.Error() != navigationDuringPromptError {
		t.Fatalf("navigation error = %v, want %q", err, navigationDuringPromptError)
	}
	if result, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{}); err != nil || result.Cancelled {
		t.Fatalf("navigation after the prompt = %+v, %v", result, err)
	}
}

func TestNavigationIsAllowedAfterAPromptIsHandledOrRejectedDuringPreflight(t *testing.T) {
	for _, outcome := range []string{"handled", "rejected"} {
		t.Run(outcome, func(t *testing.T) {
			sess := newTreeTestSession(t)
			if outcome == "handled" {
				withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
					"input": {func(args ...any) (any, error) { return extension.InputEventResultHandled{}, nil }},
				})
			}
			target := seedTreeHistory(t, sess)
			err := sess.Prompt(t.Context(), "not sent to the model")
			if (outcome == "handled") != (err == nil) {
				t.Fatalf("prompt error = %v", err)
			}
			if result, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{}); err != nil || result.Cancelled {
				t.Fatalf("navigation = %+v, %v", result, err)
			}
		})
	}
}

func TestNavigationStaysBlockedWhenAnotherPromptFinishesPreflightFirst(t *testing.T) {
	sess := newTreeTestSession(t)
	started, released := make(chan struct{}), make(chan struct{})
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"input": {func(args ...any) (any, error) {
			if args[0].(extension.InputEvent).Text == "waiting prompt" {
				close(started)
				<-released
			}
			return extension.InputEventResultHandled{}, nil
		}},
	})
	target := seedTreeHistory(t, sess)
	waiting := make(chan error, 1)
	go func() { waiting <- sess.Prompt(t.Context(), "waiting prompt") }()
	waitFor(t, started, "the waiting prompt's input hook")
	if err := sess.Prompt(t.Context(), "handled immediately"); err != nil {
		t.Fatal(err)
	}
	_, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{})
	close(released)
	<-waiting
	if err == nil || err.Error() != navigationDuringPromptError {
		t.Fatalf("navigation error = %v", err)
	}
	if result, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{}); err != nil || result.Cancelled {
		t.Fatalf("navigation after = %+v, %v", result, err)
	}
}

func TestNavigationStartedFromSessionTreeKeepsItsOwnState(t *testing.T) {
	sess := newTreeTestSession(t)
	target := seedTreeHistory(t, sess)
	nextTarget := *sess.inner.Load().GetLeafID()
	started, released := make(chan struct{}), make(chan struct{})
	continuation := make(chan error, 1)
	var once, startedOnce = new(bool), new(bool)
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_before_tree": {func(args ...any) (any, error) {
			if args[0].(extension.SessionBeforeTreeEvent).Preparation.TargetID == nextTarget && !*startedOnce {
				*startedOnce = true
				close(started)
				<-released
			}
			return nil, nil
		}},
		"session_tree": {func(args ...any) (any, error) {
			if !*once {
				*once = true
				go func() {
					_, err := sess.NavigateTree(context.WithoutCancel(t.Context()), nextTarget, NavigateTreeOptions{})
					continuation <- err
				}()
			}
			return nil, nil
		}},
	})
	if _, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, started, "the second navigation")
	if !sess.IsCompacting() {
		t.Fatal("IsCompacting() = false during the second navigation")
	}
	if err := sess.Prompt(t.Context(), "must wait for the second navigation"); err == nil || !strings.Contains(err.Error(), "compaction is in progress") {
		t.Fatalf("prompt error = %v", err)
	}
	close(released)
	if err := <-continuation; err != nil {
		t.Fatal(err)
	}
	if got := sess.inner.Load().GetLeafID(); got == nil || *got != nextTarget || sess.IsCompacting() {
		t.Fatalf("leaf = %v, compacting = %v", got, sess.IsCompacting())
	}
}

// pi: packages/coding-agent/test/suite/regressions/11451-thinking-level-after-tree-navigation.test.ts (7860ff528, #11888)
func TestThinkingLevelIsRecordedOnTheNewBranchBeforeTheNextPrompt(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{reasoning: true}, fauxReply("one", ai.StopReasonStop, 0), fauxReply("two", ai.StopReasonStop, 0), fauxReply("three", ai.StopReasonStop, 0))
	s := h.session
	if err := s.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	promptHarness(t, h, "one")
	firstAnswer := *s.inner.Load().GetLeafID()
	if err := s.SetThinkingLevel(ai.ThinkingLow); err != nil {
		t.Fatal(err)
	}
	promptHarness(t, h, "two")
	if _, err := s.NavigateTree(t.Context(), firstAnswer, NavigateTreeOptions{}); err != nil {
		t.Fatal(err)
	}
	if level, _ := icodingagent.GetSessionContextSettings(s.inner.Load().GetContextBranch()); level != "high" {
		t.Fatalf("thinking level of the new branch = %q, want high", level)
	}
	promptHarness(t, h, "three")
	if s.ThinkingLevel() != ai.ThinkingLow {
		t.Fatalf("session thinking level = %q", s.ThinkingLevel())
	}
	// Resume restores the thinking level from this context.
	if level, _ := icodingagent.GetSessionContextSettings(s.inner.Load().GetContextBranch()); level != "low" {
		t.Fatalf("recorded thinking level = %q, want low", level)
	}
}

func TestThinkingLevelThatOnlyDiffersFromTheBranchByClampingIsNotRecorded(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{reasoning: true}, fauxReply("one", ai.StopReasonStop, 0))
	s := h.session
	if err := s.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	// As after resuming a session that requested xhigh on a model without it: the session runs at high.
	if _, err := s.inner.Load().AppendThinkingLevelChange("xhigh"); err != nil {
		t.Fatal(err)
	}
	promptHarness(t, h, "one")
	entries := h.entries("thinking_level_change")
	var last struct {
		ThinkingLevel string `json:"thinkingLevel"`
	}
	if err := json.Unmarshal(entries[len(entries)-1].Raw(), &last); err != nil || last.ThinkingLevel != "xhigh" {
		t.Fatalf("last thinking_level_change = %+v, %v", last, err)
	}
}

// Pi's "allows a prompt from session_tree after the selected context is installed": a session_tree handler sends a user message, and
// the message runs on the selected branch.
func TestPromptFromSessionTreeRunsOnTheSelectedBranch(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{}, fauxReply("continued", ai.StopReasonStop, 0))
	sess := h.session
	target := seedTreeHistory(t, sess)
	var sendErr error
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_tree": {func(args ...any) (any, error) {
			sendErr = sess.SendUserMessage(t.Context(), "continue on the selected branch", nil)
			return nil, nil
		}},
	})
	if _, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{}); err != nil {
		t.Fatal(err)
	}
	if sendErr != nil {
		t.Fatalf("sendUserMessage from session_tree: %v", sendErr)
	}
	var users []string
	for _, message := range sess.Messages() {
		if message.User != nil {
			users = append(users, messageText(message))
		}
	}
	if want := []string{"first prompt", "continue on the selected branch"}; !slices.Equal(users, want) {
		t.Fatalf("user messages = %q, want %q", users, want)
	}
	if got := lastAssistantText(sess.Messages()); got != "continued" {
		t.Fatalf("last assistant text = %q", got)
	}
}

// Pi's "allows navigation from an extension %s": a command handler and an agent_settled handler navigate the tree.
func TestNavigationIsAllowedFromAnExtensionCommandAndAgentSettledHandler(t *testing.T) {
	for _, event := range []string{"command", "agent_settled"} {
		t.Run(event, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
			sess := h.session
			target := seedTreeHistory(t, sess)
			navigated := false
			navigate := func() error {
				_, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{})
				navigated = err == nil
				return err
			}
			var registered extension.Extension
			prompt := "finish then rewind"
			if event == "command" {
				registered.Commands = map[string]extension.RegisteredCommand{"rewind": {Name: "rewind", Handler: func(context.Context, string) error { return navigate() }}}
				prompt = "/rewind"
			} else {
				registered.Handlers = map[string][]extension.HandlerFn{"agent_settled": {func(args ...any) (any, error) { return nil, navigate() }}}
			}
			sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{registered}, t.TempDir()))
			if err := sess.Prompt(t.Context(), prompt, nil); err != nil {
				t.Fatal(err)
			}
			if got := sess.inner.Load().GetLeafID(); !navigated || got == nil || *got != target {
				t.Fatalf("navigated = %v, leaf = %v, want %s", navigated, got, target)
			}
		})
	}
}
