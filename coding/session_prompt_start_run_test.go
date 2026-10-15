package coding

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// startingUserPrompt runs Prompt("hello") on a harness whose first before_agent_start handler call suspends until release, as
// agent-session.ts _startRun awaits _prepareRun. It returns the harness, the prompt's result and the release function.
func startingUserPrompt(t *testing.T, responses ...scriptedResponse) (*recoveryHarness, <-chan error, func()) {
	t.Helper()
	handlerStarted, released := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		if args[0].(extension.BeforeAgentStartEvent).MessageType != extension.BeforeAgentStartUser {
			return nil, nil
		}
		suspend := false
		once.Do(func() { suspend = true })
		if suspend {
			close(handlerStarted)
			<-released
		}
		return nil, nil
	}}}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext, compatStream: true}, responses...)
	prompt := make(chan error, 1)
	go func() { prompt <- h.session.Prompt(t.Context(), "hello") }()
	<-handlerStarted
	release := sync.OnceFunc(func() { close(released) })
	t.Cleanup(release)
	return h, prompt, release
}

func requestJSON(h *recoveryHarness, index int) string {
	h.provider.mu.Lock()
	defer h.provider.mu.Unlock()
	if index >= len(h.provider.requests) {
		return ""
	}
	return h.provider.requests[index]
}

// Pi 1.2.0 agent-session.ts:1832-1849 (_startRun): a user prompt's run counts as busy from the call on (_isAgentRunActive before
// _prepareRun), so while its compaction check and before_agent_start handlers run the Session is not idle, messages sent meanwhile join the
// starting run, a prompt or sendUserMessage must name its delivery, held messages are appended after the handlers, and an abort aborts
// the run at agent_start (:1106). Pi 1.1.0 (prompt :2028-2110) prepared the prompt idle and only marked the run busy in _runAgentPrompt.
func TestUserPromptRunIsBusyWhileItStarts(t *testing.T) {
	t.Run("the Session is busy while before_agent_start runs", func(t *testing.T) {
		h, prompt, release := startingUserPrompt(t, fauxReply("done", ai.StopReasonStop, 0))
		if h.session.IsIdle() || !h.session.IsStreaming() {
			t.Errorf("isIdle=%v isStreaming=%v while the user run's before_agent_start runs, want busy", h.session.IsIdle(), h.session.IsStreaming())
		}
		release()
		if err := <-prompt; err != nil {
			t.Fatal(err)
		}
		if !h.session.IsIdle() {
			t.Error("the Session is busy after the run settled")
		}
	})

	t.Run("a triggerTurn custom message sent meanwhile joins the starting run", func(t *testing.T) {
		h, prompt, release := startingUserPrompt(t, fauxReply("handled both", ai.StopReasonStop, 0), fauxReply("a second run", ai.StopReasonStop, 0))
		if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "note", Content: "BACKGROUND-NOTE", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)}); err != nil {
			t.Fatal(err)
		}
		release()
		if err := <-prompt; err != nil {
			t.Fatal(err)
		}
		if err := h.session.WaitForIdle(t.Context()); err != nil {
			t.Fatal(err)
		}
		if n := h.provider.callCount(); n != 1 {
			t.Fatalf("provider calls = %d, want the one starting run", n)
		}
		request := requestJSON(h, 0)
		if !strings.Contains(request, "hello") || !strings.Contains(request, "BACKGROUND-NOTE") {
			t.Errorf("the starting run's request lacks the prompt or the note: %.500s", request)
		}
	})

	t.Run("sendUserMessage must name deliverAs", func(t *testing.T) {
		h, prompt, release := startingUserPrompt(t, fauxReply("first answer", ai.StopReasonStop, 0), fauxReply("follow-up answer", ai.StopReasonStop, 0))
		err := h.session.SendUserMessage(t.Context(), "UNQUEUED", nil)
		if err == nil || err.Error() != errAgentAlreadyProcessing.Error() {
			t.Errorf("sendUserMessage without deliverAs = %v, want %q", err, errAgentAlreadyProcessing)
		}
		if err := h.session.SendUserMessage(t.Context(), "FOLLOW-UP", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp}); err != nil {
			t.Fatalf("sendUserMessage with deliverAs followUp = %v", err)
		}
		release()
		if err := <-prompt; err != nil {
			t.Fatal(err)
		}
		if n := h.provider.callCount(); n != 2 {
			t.Fatalf("provider calls = %d, want the prompt and its follow-up", n)
		}
		if strings.Contains(requestJSON(h, 0), "FOLLOW-UP") || !strings.Contains(requestJSON(h, 1), "FOLLOW-UP") {
			t.Error("the follow-up was not delivered after the first response")
		}
		if projectionContains(h, "UNQUEUED") {
			t.Error("the rejected sendUserMessage reached the session")
		}
	})

	t.Run("a prompt must name its streamingBehavior and a steer joins the first request", func(t *testing.T) {
		h, prompt, release := startingUserPrompt(t, fauxReply("answer", ai.StopReasonStop, 0), fauxReply("unexpected", ai.StopReasonStop, 0))
		if err := h.session.Prompt(t.Context(), "SECOND"); err == nil || err.Error() != errAgentAlreadyProcessing.Error() {
			t.Errorf("a second prompt without streamingBehavior = %v, want %q", err, errAgentAlreadyProcessing)
		}
		if err := h.session.Prompt(t.Context(), "STEERED", &PromptOptions{StreamingBehavior: extension.DeliverAsSteer}); err != nil {
			t.Fatalf("a steered prompt = %v", err)
		}
		release()
		if err := <-prompt; err != nil {
			t.Fatal(err)
		}
		if n := h.provider.callCount(); n != 1 {
			t.Fatalf("provider calls = %d, want 1", n)
		}
		request := requestJSON(h, 0)
		if !strings.Contains(request, "STEERED") || strings.Contains(request, "SECOND") {
			t.Errorf("the first request = %.500s, want the steer and not the rejected prompt", request)
		}
	})

	t.Run("an abort aborts the run at agent_start", func(t *testing.T) {
		h, prompt, release := startingUserPrompt(t, fauxReply("should not complete", ai.StopReasonStop, 0))
		h.session.RequestAbort()
		aborted := make(chan error, 1)
		go func() { aborted <- h.session.WaitForIdle(t.Context()) }()
		release()
		if err := <-prompt; err != nil {
			t.Fatalf("prompt = %v, want it to resolve as Pi's does", err)
		}
		if err := <-aborted; err != nil {
			t.Fatal(err)
		}
		if err := h.session.FlushEvents(t.Context()); err != nil {
			t.Fatal(err)
		}
		messages := h.session.Messages()
		roles := make([]string, len(messages))
		for i, message := range messages {
			roles[i] = message.Role()
		}
		// The run's system prompt patch, the prompt, and the response the abort ended.
		if !slices.Equal(roles, []string{"system", agent.RoleUser, agent.RoleAssistant}) || messages[2].Assistant.StopReason != ai.StopReasonAborted {
			t.Fatalf("messages = %v (last %+v), want the prompt and an aborted response", roles, messages[len(messages)-1].Assistant)
		}
		if projectionContains(h, "should not complete") {
			t.Error("the aborted run completed a request")
		}
		h.mu.Lock()
		var settled []agent.AgentSettledEvent
		for _, event := range h.events {
			if e, ok := event.(agent.AgentSettledEvent); ok {
				settled = append(settled, e)
			}
		}
		h.mu.Unlock()
		if len(settled) != 1 || !settled[0].Aborted {
			t.Errorf("agent_settled events = %+v, want one aborted", settled)
		}
		if !h.session.IsIdle() {
			t.Error("the Session is not idle after the aborted run")
		}
	})

	t.Run("a bash result recorded meanwhile is appended after the handlers, before the prompt", func(t *testing.T) {
		h, prompt, release := startingUserPrompt(t, fauxReply("done", ai.StopReasonStop, 0))
		if err := h.session.RecordBashResult("echo held", BashResult{Output: "HELD-OUTPUT", ExitCode: new(0)}, nil); err != nil {
			t.Fatal(err)
		}
		if len(h.session.Messages()) != 0 {
			t.Error("the bash result was appended while the run's handlers ran")
		}
		release()
		if err := <-prompt; err != nil {
			t.Fatal(err)
		}
		messages := h.session.Messages()
		roles := make([]string, len(messages))
		for i, message := range messages {
			roles[i] = message.Role()
		}
		if !slices.Equal(roles, []string{agent.RoleBashExecution, "system", agent.RoleUser, agent.RoleAssistant}) {
			t.Errorf("messages = %v, want the bash result before the prompt", roles)
		}
		if !strings.Contains(requestJSON(h, 0), "HELD-OUTPUT") {
			t.Error("the starting run's request lacks the held bash result")
		}
	})
}
