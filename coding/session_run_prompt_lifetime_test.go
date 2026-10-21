package coding

import (
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session.ts:1874-1881 clears the run's prompt options (`_runSystemPromptOptions = undefined`) before it flushes the bash and custom messages held during the run, so a listener or handler that reads the system prompt while those messages end sees the base prompt, not the run's forced one.
// The message is sent from agent_end, after the last turn boundary has flushed, so it stays held until the run's tail flush.
func TestHeldCustomMessageFlushedAtRunEndSeesTheBaseSystemPrompt(t *testing.T) {
	var session *Session
	var mu sync.Mutex
	var promptAtFlush string
	flushed := false
	noTurn := false
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"before_agent_start": {func(...any) (any, error) {
			return &extension.BeforeAgentStartEventResult{SystemPrompt: new("RUN-FORCED-PROMPT")}, nil
		}},
		"agent_end": {func(...any) (any, error) {
			return nil, session.SendMessage(extension.CustomMessageRef{CustomType: "held", Content: "held", Display: true}, &extension.SendMessageOptions{TriggerTurn: &noTurn})
		}},
	}}}, fauxReply("done", ai.StopReasonStop, 0))
	session = h.session
	session.Subscribe(func(event agent.AgentEvent) {
		if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Custom != nil {
			mu.Lock()
			defer mu.Unlock()
			flushed, promptAtFlush = true, session.SystemPrompt()
		}
	})
	if err := session.Prompt(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !flushed {
		t.Fatal("the held custom message never reached message_end")
	}
	if strings.Contains(promptAtFlush, "RUN-FORCED-PROMPT") || promptAtFlush == "" {
		t.Fatalf("system prompt while the held message ended = %q, want the base prompt", promptAtFlush)
	}
}
