package coding

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// suspendedRun starts a run of the given type whose first before_agent_start handler suspends until release, so a message held while the
// run starts can be recorded first. start begins the run on the harness and returns its result.
func suspendedRun(t *testing.T, messageType extension.BeforeAgentStartMessageType, start func(*recoveryHarness) error, responses ...scriptedResponse) (*recoveryHarness, <-chan error, func()) {
	t.Helper()
	handlerStarted, released := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		if args[0].(extension.BeforeAgentStartEvent).MessageType != messageType {
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
	run := make(chan error, 1)
	go func() { run <- start(h) }()
	<-handlerStarted
	release := sync.OnceFunc(func() { close(released) })
	t.Cleanup(release)
	return h, run, release
}

// agent-session.ts:1845-1848 and 1924-1925: _startRun flushes the messages held while the run started after _prepareRun has taken the
// pending nextTurn messages, so a listener that sends deliverAs "nextTurn" while the held custom messages are emitted leaves its message for
// the run after this one. The held message's message_end reaches Session listeners only (_flushPendingCustomMessages emits it with _emit).
func TestNextTurnMessageSentWhileHeldMessagesFlushWaitsForTheNextRun(t *testing.T) {
	for _, tc := range []struct {
		name        string
		messageType extension.BeforeAgentStartMessageType
		start       func(*recoveryHarness) error
	}{
		{"a user prompt's run", extension.BeforeAgentStartUser, func(h *recoveryHarness) error { return h.session.Prompt(t.Context(), "hello") }},
		{"a custom message's run", extension.BeforeAgentStartCustom, func(h *recoveryHarness) error {
			return h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "first", Content: "FIRST-MESSAGE", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, run, release := suspendedRun(t, tc.messageType, tc.start, fauxReply("first answer", ai.StopReasonStop, 0), fauxReply("second answer", ai.StopReasonStop, 0))
			if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "held", Content: "HELD-MESSAGE", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(false)}); err != nil {
				t.Fatal(err)
			}
			var sent sync.Once
			unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
				end, ok := event.(agent.MessageEndEvent)
				if !ok || end.Message.Custom["customType"] != "held" {
					return
				}
				sent.Do(func() {
					err := h.session.SendCustomMessage(context.Background(), extension.CustomMessageRef{CustomType: "late", Content: "LATE-NOTE", Display: true}, &extension.SendMessageOptions{DeliverAs: extension.DeliverAsNextTurn})
					if err != nil {
						t.Errorf("sendCustomMessage from the listener = %v", err)
					}
				})
			})
			defer unsubscribe()
			release()
			if err := <-run; err != nil {
				t.Fatal(err)
			}
			if err := h.session.WaitForIdle(t.Context()); err != nil {
				t.Fatal(err)
			}
			if n := h.provider.callCount(); n != 1 {
				t.Fatalf("provider calls = %d, want the starting run", n)
			}
			if !strings.Contains(requestJSON(h, 0), "HELD-MESSAGE") {
				t.Fatalf("the starting run's request lacks the held message: %.500s", requestJSON(h, 0))
			}
			if strings.Contains(requestJSON(h, 0), "LATE-NOTE") {
				t.Errorf("a nextTurn message sent while the held messages flushed joined the run that was starting: %.500s", requestJSON(h, 0))
			}
			if err := h.session.Prompt(t.Context(), "again"); err != nil {
				t.Fatal(err)
			}
			if n := h.provider.callCount(); n != 2 {
				t.Fatalf("provider calls = %d, want the starting run and the next one", n)
			}
			if got := strings.Count(requestJSON(h, 1), "LATE-NOTE"); got != 1 {
				t.Errorf("the next run's request holds the late nextTurn message %d times, want once: %.500s", got, requestJSON(h, 1))
			}
		})
	}
}
