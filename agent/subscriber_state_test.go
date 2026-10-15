package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/agent/src/agent.ts:561-574 appends only message_end, before
// awaiting subscribers. Neither future prompt messages nor an unfinished
// assistant response belongs to the public transcript at message_start.
func TestAgentSubscribersObserveCompletedTranscript(t *testing.T) {
	for _, withTool := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "tool"}[withTool], func(t *testing.T) {
			provider := &scriptedProvider{respond: replyText("done")}
			var tools []AgentTool
			if withTool {
				provider.respond = toolCallsThenText(toolCall("call", "noop", nil))
				tools = []AgentTool{noopTool()}
			}
			a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: tools})
			var observed []string
			a.Subscribe(func(_ context.Context, event AgentEvent) error {
				switch e := event.(type) {
				case MessageStartEvent:
					observed = append(observed, "start:"+e.Message.Role()+":"+strings.Join(roles(a.MessagesSnapshot()), ","))
				case MessageEndEvent:
					observed = append(observed, "end:"+e.Message.Role()+":"+strings.Join(roles(a.MessagesSnapshot()), ","))
				}
				return nil
			})
			mustSend(t, a, "hello")
			want := []string{"start:user:", "end:user:user", "start:assistant:user", "end:assistant:user,assistant"}
			if withTool {
				want = []string{"start:user:system", "end:user:system,user", "start:assistant:system,user", "end:assistant:system,user,assistant", "start:toolResult:system,user,assistant", "end:toolResult:system,user,assistant,toolResult", "start:assistant:system,user,assistant,toolResult", "end:assistant:system,user,assistant,toolResult,assistant"}
			}
			if !reflect.DeepEqual(observed, want) {
				t.Fatalf("subscriber transcript = %q, want %q", observed, want)
			}
		})
	}
}

// Pi packages/agent/src/agent.ts:381-408 rejects invalid continuation before
// runWithLifecycle clears runtime error state (agent.ts:503-517).
func TestAgentRejectedContinuationPreservesErrorState(t *testing.T) {
	for _, tail := range []string{"assistant", "empty", "system-only"} {
		t.Run(tail, func(t *testing.T) {
			a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return errorStream(ai.StopReasonError) }})})
			mustSend(t, a, "fail")
			switch tail {
			case "empty":
				a.SetMessages(nil)
			case "system-only":
				a.SetMessages([]AgentMessage{{System: &ai.SystemMessage{Content: ai.SystemText("system")}}})
			}
			if _, err := a.ContinueMessages(t.Context()); err == nil {
				t.Fatal("invalid continuation succeeded")
			}
			if a.ErrorMessage() != "error" {
				t.Fatalf("rejected continuation cleared error: %q", a.ErrorMessage())
			}
		})
	}
}
