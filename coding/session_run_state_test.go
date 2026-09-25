package coding

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// runStateHarness is a recovery harness whose extension handlers can reach
// the session they belong to.
func runStateHarness(t *testing.T, handlers map[string]func(*Session), responses ...scriptedResponse) *recoveryHarness {
	t.Helper()
	var sessionRef atomic.Pointer[Session]
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{}}
	for event, handler := range handlers {
		ext.Handlers[event] = []extension.HandlerFn{func(...any) (any, error) {
			handler(sessionRef.Load())
			return nil, nil
		}}
	}
	h := newRecoveryHarness(t, harnessOptions{extension: ext}, responses...)
	sessionRef.Store(h.session)
	return h
}

func lastAssistantText(messages []agent.AgentMessage) string {
	if last := lastAssistantMessage(messages); last != nil {
		return assistantText(last)
	}
	return ""
}

// Upstream's agent awaits agent_end listeners before _handlePostAgentRun, so
// a follow-up an agent_end handler queues continues the same run. The
// headless modes left it stranded: the run settled with the message queued.
func TestSendUserMessageFollowUpFromAgentEndContinuesRun(t *testing.T) {
	var once sync.Once
	h := runStateHarness(t, map[string]func(*Session){
		"agent_end": func(s *Session) {
			once.Do(func() {
				// A slow handler: post-run handling must still wait for it.
				time.Sleep(20 * time.Millisecond)
				if err := s.SendUserMessage("continue please", extension.DeliverAsFollowUp); err != nil {
					t.Error(err)
				}
			})
		},
	}, fauxReply("first", ai.StopReasonStop, 0), fauxReply("second", ai.StopReasonStop, 0))

	messages, err := h.session.Send(context.Background(), "start")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.provider.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2 (the follow-up continues the run)", got)
	}
	if got := lastAssistantText(messages); got != "second" {
		t.Fatalf("run ended with %q, want the follow-up's answer", got)
	}
	if h.session.HasPendingMessages() {
		t.Fatal("follow-up still queued after the run settled")
	}
}

// Upstream defers a prompt sent while agent_settled is dispatched and runs it
// before the original prompt() resolves, so print mode waits for it.
func TestSendUserMessageFromAgentSettledRunsBeforeSendReturns(t *testing.T) {
	var once sync.Once
	h := runStateHarness(t, map[string]func(*Session){
		"agent_settled": func(s *Session) {
			once.Do(func() {
				if s.IsStreaming() {
					t.Error("session still streaming while agent_settled is dispatched")
				}
				if err := s.SendUserMessage("next task", ""); err != nil {
					t.Error(err)
				}
			})
		},
	}, fauxReply("first", ai.StopReasonStop, 0), fauxReply("second", ai.StopReasonStop, 0))

	if _, err := h.session.Send(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	if got := h.provider.callCount(); got != 2 {
		t.Fatalf("model calls when Send returned = %d, want 2", got)
	}
	if got := lastAssistantText(h.session.Messages()); got != "second" {
		t.Fatalf("last answer = %q, want second", got)
	}
}

// Upstream prompt() throws "Agent is already processing" for a message sent
// during a run without a delivery mode; sendUserMessage reports it through
// runner.emitError with extensionPath "<runtime>".
func TestSendUserMessageWithoutDeliveryModeWhileStreamingReportsError(t *testing.T) {
	h := runStateHarness(t, map[string]func(*Session){
		"agent_start": func(s *Session) {
			if err := s.SendUserMessage("too early", ""); err != nil {
				t.Error(err)
			}
		},
	}, fauxReply("only", ai.StopReasonStop, 0))
	var mu sync.Mutex
	var reported []*extension.ExtensionError
	h.session.currentRunner().AddErrorListener(func(err *extension.ExtensionError) {
		mu.Lock()
		reported = append(reported, err)
		mu.Unlock()
	})

	if _, err := h.session.Send(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 || reported[0].ExtensionPath != "<runtime>" || reported[0].Event != "send_user_message" || reported[0].Error != errAgentAlreadyProcessing.Error() {
		t.Fatalf("reported errors = %+v, want one <runtime> send_user_message %q", reported, errAgentAlreadyProcessing)
	}
	if h.provider.callCount() != 1 {
		t.Fatalf("model calls = %d, want 1", h.provider.callCount())
	}
}

// blockingProvider streams nothing until its request is cancelled.
type blockingProvider struct{ started chan struct{} }

func (blockingProvider) ID() string   { return "faux" }
func (blockingProvider) Close() error { return nil }
func (p blockingProvider) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	stream := ai.NewAssistantMessageEventStream()
	partial := &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonPending}
	_ = stream.Push(ai.StartEvent{Partial: partial})
	go func() {
		close(p.started)
		<-ctx.Done()
		aborted := &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonAborted, ErrorMessage: "aborted"}
		_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: aborted})
	}()
	return stream, nil
}

// Upstream session.abort() stops whatever run is active and waits for idle.
// A run an extension started while idle had no cancel in the headless modes,
// and isIdle stayed true while it ran.
func TestAbortStopsExtensionStartedRun(t *testing.T) {
	provider := blockingProvider{started: make(chan struct{})}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{
		Model:            &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}},
		SkipBuiltinTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range session.Events() { //nolint:revive // drain
		}
	}()
	t.Cleanup(func() { _ = session.Close(); <-drained })

	if err := session.SendUserMessage("background work", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("extension-started run never reached the provider")
	}
	if !session.IsStreaming() || session.IsIdle() {
		t.Fatalf("streaming=%v idle=%v during the extension-started run", session.IsStreaming(), session.IsIdle())
	}
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	if err := session.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if session.IsStreaming() || !session.IsIdle() {
		t.Fatalf("streaming=%v idle=%v after Abort", session.IsStreaming(), session.IsIdle())
	}
	if err := session.WaitForIdle(ctx); err != nil {
		t.Fatalf("WaitForIdle after Abort: %v", err)
	}
}

// RunAgentPrompt is upstream _runAgentPrompt for interactive mode: the run is
// streaming (so input handlers see the delivery mode) until it returns, and
// Abort reaches it like any other run.
func TestRunAgentPromptMarksTheRunActive(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	var streamingDuringStart bool
	aborted := make(chan struct{})
	go func() {
		for !h.session.IsStreaming() {
			time.Sleep(time.Millisecond)
		}
		_ = h.session.Abort(context.Background())
		close(aborted)
	}()
	_, err := h.session.RunAgentPrompt(context.Background(), func(ctx context.Context) ([]agent.AgentMessage, error) {
		streamingDuringStart = h.session.IsStreaming()
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if !streamingDuringStart {
		t.Fatal("IsStreaming was false inside RunAgentPrompt")
	}
	if err == nil {
		t.Fatal("RunAgentPrompt returned without the abort's cancellation")
	}
	select {
	case <-aborted:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Abort did not return after the run ended")
	}
	if h.session.IsStreaming() {
		t.Fatal("IsStreaming still true after RunAgentPrompt returned")
	}
}
