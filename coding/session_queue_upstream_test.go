package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestUpstreamSessionQueue(t *testing.T) {
	var records []any
	capture := func(t *testing.T, h *recoveryHarness) {
		t.Cleanup(func() {
			roles := []string{}
			for _, message := range h.session.Messages() {
				roles = append(roles, message.Role())
			}
			records = append(records, []any{queueUserTexts(h), queueAssistantTexts(h), roles, h.session.PendingMessageCount(), h.session.GetSteeringMessages(), h.session.GetFollowUpMessages(), max(0, len(h.provider.responses)-h.provider.callCount())})
		})
	}
	newHarness := func(t *testing.T, ext extension.Extension, tools []agent.AgentTool) *recoveryHarness {
		h := newQueueCharacterizationHarness(t, ext, tools)
		capture(t, h)
		return h
	}
	waitingHarness := func(t *testing.T, ext extension.Extension) queueWaitingHarness {
		waiting := createQueueWaitingHarness(t, ext)
		capture(t, waiting.h)
		return waiting
	}

	// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:69
	t.Run("dispatches extension commands immediately when prompted while idle", func(t *testing.T) {
		var commandRuns []string
		h := newHarness(t, queueCommandExtension(func(_ context.Context, args string) error { commandRuns = append(commandRuns, args); return nil }), nil)
		if err := h.session.Prompt(t.Context(), "/testcmd hello world", nil); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(commandRuns, []string{"hello world"}) {
			t.Fatalf("commands=%v", commandRuns)
		}
		if len(h.provider.responses)-h.provider.callCount() != 0 {
			t.Fatal("pending response count changed")
		}
		if len(h.session.Messages()) != 0 {
			t.Fatal("extension command entered history")
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:92
	t.Run("delivers extension-origin steering messages before the next LLM call", func(t *testing.T) {
		waiting := waitingHarness(t, extension.Extension{})
		h := waiting.h
		api := h.session.currentRunner().CreateCommandContext()
		waiting.setResponses(fauxToolCall("wait"), func(messages []ai.Message) *ai.AssistantMessage {
			text := "missing steer"
			if slices.Contains(queueProviderUserTexts(messages), "steer now") {
				text = "saw steer"
			}
			return fauxReply(text, ai.StopReasonStop, 0)(messages)
		})
		<-waiting.waitForToolStart
		if err := api.SendUserMessage("steer now", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsSteer}); err != nil {
			t.Fatal(err)
		}
		waiting.releaseToolExecution()
		waiting.join()
		if want := []string{"start", "steer now"}; !reflect.DeepEqual(queueUserTexts(h), want) {
			t.Fatalf("users=%v want=%v", queueUserTexts(h), want)
		}
		if !slices.Contains(queueAssistantTexts(h), "saw steer") {
			t.Fatalf("assistants=%v", queueAssistantTexts(h))
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:125
	t.Run("delivers follow-up messages only after the current run finishes", func(t *testing.T) {
		waiting := waitingHarness(t, extension.Extension{})
		h := waiting.h
		var assistantSeenBeforeFollowUp []string
		waiting.setResponses(fauxToolCall("wait"), func(messages []ai.Message) *ai.AssistantMessage {
			for _, message := range messages {
				if assistant, ok := message.(ai.AssistantMessage); ok {
					var parts []string
					for _, block := range assistant.Content {
						if text, ok := block.(ai.TextContent); ok {
							parts = append(parts, text.Text)
						}
					}
					assistantSeenBeforeFollowUp = append(assistantSeenBeforeFollowUp, strings.Join(parts, "\n"))
				}
			}
			return fauxReply("follow-up response", ai.StopReasonStop, 0)(messages)
		})
		<-waiting.waitForToolStart
		if _, err := h.session.FollowUp(t.Context(), "after current run", nil, nil); err != nil {
			t.Fatal(err)
		}
		waiting.releaseToolExecution()
		waiting.join()
		if want := []string{"start", "after current run"}; !reflect.DeepEqual(queueUserTexts(h), want) {
			t.Fatalf("users=%v", queueUserTexts(h))
		}
		if !slices.Contains(assistantSeenBeforeFollowUp, "") {
			t.Fatalf("assistants before follow-up=%v", assistantSeenBeforeFollowUp)
		}
		if !slices.Contains(queueAssistantTexts(h), "follow-up response") {
			t.Fatalf("assistants=%v", queueAssistantTexts(h))
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:159
	t.Run("runs direct steering and follow-up messages through input handlers", func(t *testing.T) {
		type inputRecord struct {
			Text              string
			Source            extension.InputSource
			StreamingBehavior string
		}
		var inputEvents []inputRecord
		waiting := waitingHarness(t, extension.Extension{Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) {
			event := args[0].(extension.InputEvent)
			inputEvents = append(inputEvents, inputRecord{event.Text, event.Source, event.StreamingBehavior})
			if strings.HasPrefix(event.Text, "handle") {
				return extension.InputEventResultHandled{}, nil
			}
			return extension.InputEventResultTransform{Text: "transformed: " + event.Text}, nil
		}}}})
		h := waiting.h
		waiting.setResponses(fauxToolCall("wait"), fauxReply("steered", ai.StopReasonStop, 0), fauxReply("followed up", ai.StopReasonStop, 0))
		<-waiting.waitForToolStart
		inputEvents = nil
		options := &QueueInputOptions{Source: extension.InputSourceRPC}
		for _, text := range []string{"steer me", "handle steer"} {
			if _, err := h.session.Steer(t.Context(), text, nil, options); err != nil {
				t.Fatal(err)
			}
		}
		for _, text := range []string{"follow me", "handle follow"} {
			if _, err := h.session.FollowUp(t.Context(), text, nil, options); err != nil {
				t.Fatal(err)
			}
		}
		want := []inputRecord{{"steer me", "rpc", "steer"}, {"handle steer", "rpc", "steer"}, {"follow me", "rpc", "followUp"}, {"handle follow", "rpc", "followUp"}}
		if !reflect.DeepEqual(inputEvents, want) {
			t.Fatalf("input=%v want=%v", inputEvents, want)
		}
		if got := h.session.GetSteeringMessages(); !reflect.DeepEqual(got, []string{"transformed: steer me"}) {
			t.Fatalf("steering=%v", got)
		}
		if got := h.session.GetFollowUpMessages(); !reflect.DeepEqual(got, []string{"transformed: follow me"}) {
			t.Fatalf("follow-up=%v", got)
		}
		waiting.releaseToolExecution()
		waiting.join()
	})
	for _, test := range []struct {
		name    string
		follow  bool
		all     bool
		queued  []string
		replies []string
		want    []string
	}{
		// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:206
		{"delivers multiple steering messages in order in one-at-a-time mode", false, false, []string{"steer 1", "steer 2"}, []string{"handled steer 1", "handled steer 2"}, []string{"", "handled steer 1", "handled steer 2"}},
		// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:227
		{"delivers multiple follow-up messages in order in one-at-a-time mode", true, false, []string{"follow-up 1", "follow-up 2"}, []string{"original turn complete", "handled follow-up 1", "handled follow-up 2"}, []string{"", "original turn complete", "handled follow-up 1", "handled follow-up 2"}},
		// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:254
		{"delivers all steering messages in one batch in all mode", false, true, []string{"steer 1", "steer 2"}, []string{"batched steer response"}, []string{"", "batched steer response"}},
		// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:281
		{"delivers all follow-up messages in one batch in all mode", true, true, []string{"follow-up 1", "follow-up 2"}, []string{"original turn complete", "batched follow-up response"}, []string{"", "original turn complete", "batched follow-up response"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			waiting := waitingHarness(t, extension.Extension{})
			h := waiting.h
			var batchedUserMessages []string
			responses := []scriptedResponse{fauxToolCall("wait")}
			for i, text := range test.replies {
				if test.all && i == len(test.replies)-1 {
					responses = append(responses, func(messages []ai.Message) *ai.AssistantMessage {
						batchedUserMessages = queueProviderUserTexts(messages)
						return fauxReply(text, ai.StopReasonStop, 0)(messages)
					})
				} else {
					responses = append(responses, fauxReply(text, ai.StopReasonStop, 0))
				}
			}
			if test.all {
				var err error
				if test.follow {
					err = h.session.SetFollowUpMode(agent.QueueModeAll)
				} else {
					err = h.session.SetSteeringMode(agent.QueueModeAll)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			waiting.setResponses(responses...)
			<-waiting.waitForToolStart
			for _, text := range test.queued {
				var err error
				if test.follow {
					_, err = h.session.FollowUp(t.Context(), text, nil, nil)
				} else {
					_, err = h.session.Steer(t.Context(), text, nil, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			waiting.releaseToolExecution()
			waiting.join()
			wantUsers := append([]string{"start"}, test.queued...)
			if test.all {
				if !reflect.DeepEqual(batchedUserMessages, wantUsers) {
					t.Fatalf("batch=%v want=%v", batchedUserMessages, wantUsers)
				}
			} else if !reflect.DeepEqual(queueUserTexts(h), wantUsers) {
				t.Fatalf("users=%v want=%v", queueUserTexts(h), wantUsers)
			}
			if got := queueAssistantTexts(h); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("assistants=%v want=%v", got, test.want)
			}
		})
	}
	for _, test := range []struct {
		name, content string
		mode          extension.DeliverAs
	}{
		// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:309
		{"queues custom messages with deliverAs steer while streaming", "steer custom", extension.DeliverAsSteer},
		// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:342
		{"queues custom messages with deliverAs followUp while streaming", "follow-up custom", extension.DeliverAsFollowUp},
	} {
		t.Run(test.name, func(t *testing.T) {
			waiting := waitingHarness(t, extension.Extension{})
			h := waiting.h
			sawCustomMessage := false
			responses := []scriptedResponse{fauxToolCall("wait")}
			if test.mode == extension.DeliverAsFollowUp {
				responses = append(responses, fauxReply("original turn complete", ai.StopReasonStop, 0))
			}
			responses = append(responses, func(messages []ai.Message) *ai.AssistantMessage {
				sawCustomMessage = queueProviderHasTextBlock(messages, test.content)
				return fauxReply("done", ai.StopReasonStop, 0)(messages)
			})
			waiting.setResponses(responses...)
			<-waiting.waitForToolStart
			if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "queue-test", Content: test.content, Display: true, Details: map[string]any{"value": 1}}, &extension.SendMessageOptions{DeliverAs: test.mode}); err != nil {
				t.Fatal(err)
			}
			waiting.releaseToolExecution()
			waiting.join()
			if !sawCustomMessage {
				t.Fatal("provider did not receive custom user content")
			}
			if !slices.ContainsFunc(h.session.Messages(), func(message agent.AgentMessage) bool {
				return message.Role() == "custom" && message.Custom["customType"] == "queue-test"
			}) {
				t.Fatal("custom message not retained")
			}
		})
	}
	// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:376
	t.Run("injects nextTurn custom messages into the next prompt", func(t *testing.T) {
		h := newHarness(t, extension.Extension{}, nil)
		sawCustomMessage := false
		if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "next-turn", Content: "carry this", Display: true, Details: map[string]any{}}, &extension.SendMessageOptions{DeliverAs: extension.DeliverAsNextTurn}); err != nil {
			t.Fatal(err)
		}
		h.provider.responses = []scriptedResponse{func(messages []ai.Message) *ai.AssistantMessage {
			sawCustomMessage = queueProviderHasTextBlock(messages, "carry this")
			return fauxReply("done", ai.StopReasonStop, 0)(messages)
		}}
		if err := h.session.Prompt(t.Context(), "normal prompt", nil); err != nil {
			t.Fatal(err)
		}
		if !sawCustomMessage {
			t.Fatal("nextTurn custom content missing")
		}
		var roles []string
		for _, message := range h.session.Messages() {
			roles = append(roles, message.Role())
		}
		if want := []string{"system", "user", "custom", "assistant"}; !reflect.DeepEqual(roles, want) {
			t.Fatalf("roles=%v want=%v", roles, want)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:409
	t.Run("updates pendingMessageCount and removes queued text before message_start is emitted", func(t *testing.T) {
		waiting := waitingHarness(t, extension.Extension{})
		h := waiting.h
		var counts []int
		waiting.setResponses(fauxToolCall("wait"), fauxReply("done", ai.StopReasonStop, 0))
		h.session.Subscribe(func(event agent.AgentEvent) {
			if start, ok := event.(agent.MessageStartEvent); ok && start.Message.User != nil && extractUserMessageText(start.Message.User.Content) == "queued" {
				counts = append(counts, h.session.PendingMessageCount())
			}
		})
		<-waiting.waitForToolStart
		if _, err := h.session.Steer(t.Context(), "queued", nil, nil); err != nil {
			t.Fatal(err)
		}
		if h.session.PendingMessageCount() != 1 {
			t.Fatal("queued count not 1")
		}
		waiting.releaseToolExecution()
		waiting.join()
		if !reflect.DeepEqual(counts, []int{0}) {
			t.Fatalf("counts at start=%v", counts)
		}
		if h.session.PendingMessageCount() != 0 {
			t.Fatal("queue not empty after completion")
		}
	})
	for _, mode := range []string{"steer", "followUp"} {
		// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:440 (steer), :458 (followUp)
		t.Run("throws when queueing an extension command with "+mode, func(t *testing.T) {
			h := newHarness(t, queueCommandExtension(func(context.Context, string) error { return nil }), nil)
			var err error
			if mode == "steer" {
				_, err = h.session.Steer(t.Context(), "/testcmd queued", nil, nil)
			} else {
				_, err = h.session.FollowUp(t.Context(), "/testcmd queued", nil, nil)
			}
			want := `Extension command "/testcmd" cannot be queued. Use prompt() or execute the command when not streaming.`
			if err == nil || err.Error() != want {
				t.Fatalf("error=%v want=%s", err, want)
			}
		})
	}
	// upstream: packages/coding-agent/test/suite/agent-session-queue.test.ts:476
	t.Run("delivers follow-ups queued during agent_end", func(t *testing.T) {
		sent := false
		var api *extension.CommandContext
		h := newHarness(t, extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_end": {func(...any) (any, error) {
			if sent {
				return nil, nil
			}
			sent = true
			return nil, api.SendUserMessage("conflict report", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp})
		}}}}, nil)
		api = h.session.currentRunner().CreateCommandContext()
		h.provider.responses = []scriptedResponse{fauxReply("reply", ai.StopReasonStop, 0), fauxReply("follow-up reply", ai.StopReasonStop, 0)}
		if err := h.session.Prompt(t.Context(), "hello", nil); err != nil {
			t.Fatal(err)
		}
		if err := h.session.WaitForIdle(t.Context()); err != nil {
			t.Fatal(err)
		}
		if want := []string{"hello", "conflict report"}; !reflect.DeepEqual(queueUserTexts(h), want) {
			t.Fatalf("users=%v want=%v", queueUserTexts(h), want)
		}
	})
	if os.Getenv("PIG_QUEUE_PROBE") == "1" {
		encoded, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("SESSION_QUEUE " + string(encoded))
	}
}
