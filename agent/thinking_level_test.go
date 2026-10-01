package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Regression guards for the assistant message's thinkingLevel. Upstream
// 0.99.1 records the level the loop requested on the final response of every
// request, whichever stream function answered
// (.upstream/v0.99.1/packages/agent/src/agent-loop.ts:408-409, 445, 460), and
// leaves the partial messages of message_start and message_update alone. The
// upstream suite does not assert it; the published package was probed for the
// key order (thinkingLevel is appended after timestamp) and the "off" default.

func assistantEnds(events []AgentEvent) []*AssistantMessage {
	var ends []*AssistantMessage
	for _, event := range events {
		if end, ok := event.(MessageEndEvent); ok && end.Message.Assistant != nil {
			ends = append(ends, end.Message.Assistant)
		}
	}
	return ends
}

func TestAgentLoop_RecordsRequestedThinkingLevelOnFinalAssistantMessage(t *testing.T) {
	for _, level := range []ai.ThinkingLevel{"", ai.ThinkingOff, ai.ThinkingMinimal, ai.ThinkingHigh, ai.ThinkingXHigh} {
		want := level
		if want == "" {
			want = ai.ThinkingOff
		}
		provider := &scriptedProvider{respond: replyText("ok")}
		rec := newEventRecorder(nil)
		a := NewAgent(AgentOptions{Model: scriptedModel(provider), ThinkingLevel: level, EventCh: rec.ch})

		msgs := mustSend(t, a, "hello")

		events := rec.stop()
		ends := assistantEnds(events)
		if len(ends) != 1 || ends[0].ThinkingLevel != want {
			t.Fatalf("level %q: message_end thinkingLevel = %+v, want %q", level, ends, want)
		}
		last := msgs[len(msgs)-1].Assistant
		if last == nil || last.ThinkingLevel != want || a.Messages()[len(a.Messages())-1].Assistant.ThinkingLevel != want {
			t.Fatalf("level %q: transcript thinkingLevel = %+v, want %q", level, last, want)
		}
		for _, event := range events {
			if start, ok := event.(MessageStartEvent); ok && start.Message.Assistant != nil && start.Message.Assistant.ThinkingLevel != "" {
				t.Fatalf("level %q: message_start already carries thinkingLevel %q", level, start.Message.Assistant.ThinkingLevel)
			}
		}
	}
}

func TestAgentLoop_RecordsThinkingLevelOnErrorAndAbortedResponses(t *testing.T) {
	for _, reason := range []ai.StopReason{ai.StopReasonError, ai.StopReasonAborted} {
		provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return errorStream(reason) }}
		a := NewAgent(AgentOptions{Model: scriptedModel(provider), ThinkingLevel: ai.ThinkingMedium})

		msgs := mustSend(t, a, "hello")

		last := msgs[len(msgs)-1].Assistant
		if last == nil || last.StopReason != reason || last.ThinkingLevel != ai.ThinkingMedium {
			t.Fatalf("%s response = %+v, want thinkingLevel medium", reason, last)
		}
	}
}

// upstream: a stream function whose request setup fails answers with an error
// event (ai/src/api/lazy.ts:52-58), and streamAssistantResponse records the
// requested level on that result too (agent-loop.ts:409, 445). PiG's stream
// function returns the setup error instead, and the loop synthesizes the same
// final response, so it must record the level as well.
func TestAgentLoop_RecordsThinkingLevelWhenTheStreamFunctionFailsBeforeStreaming(t *testing.T) {
	for _, level := range []ai.ThinkingLevel{"", ai.ThinkingMedium} {
		want := recordedThinkingLevel(level)
		rec := newEventRecorder(nil)
		a := NewAgent(AgentOptions{
			Model: scriptedModel(&scriptedProvider{respond: replyText("unused")}), ThinkingLevel: level, EventCh: rec.ch,
			StreamFn: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return nil, errors.New("no API key")
			},
		})

		msgs := mustSend(t, a, "hello")

		last := msgs[len(msgs)-1].Assistant
		if last == nil || last.StopReason != ai.StopReasonError || last.ThinkingLevel != want {
			t.Fatalf("level %q: response = %+v, want an error response with thinkingLevel %q", level, last, want)
		}
		events := rec.stop()
		if ends := assistantEnds(events); len(ends) != 1 || ends[0].ThinkingLevel != want {
			t.Fatalf("level %q: message_end = %+v, want thinkingLevel %q", level, ends, want)
		}
		// Without a start event, upstream's message_start copies the final message.
		for _, event := range events {
			if start, ok := event.(MessageStartEvent); ok && start.Message.Assistant != nil && start.Message.Assistant.ThinkingLevel != want {
				t.Fatalf("level %q: message_start thinkingLevel = %q, want %q", level, start.Message.Assistant.ThinkingLevel, want)
			}
		}
	}
}

// upstream: streamAssistantResponse reads config.reasoning after prepareRequest replaced it.
func TestAgentLoop_RecordsTheThinkingLevelPrepareRequestSelected(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	high := ai.ThinkingHigh
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		PrepareRequest: func(context.Context, PrepareRequestContext) (*AgentRequestUpdate, error) {
			return &AgentRequestUpdate{ThinkingLevel: &high}, nil
		},
	})

	msgs := mustSend(t, a, "hello")

	if got := msgs[len(msgs)-1].Assistant.ThinkingLevel; got != ai.ThinkingHigh {
		t.Fatalf("thinkingLevel = %q, want high (requested %q)", got, provider.request(1).opts.Thinking)
	}
}

func TestAssistantMessageThinkingLevelJSONFollowsTimestampAndRoundTrips(t *testing.T) {
	message := AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, Timestamp: 7, StopReason: ai.StopReasonStop, ThinkingLevel: ai.ThinkingHigh, Usage: &ai.Usage{}}}

	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(encoded), `"timestamp":7,"thinkingLevel":"high"}`) {
		t.Fatalf("encoded = %s, want thinkingLevel appended after timestamp", encoded)
	}
	var decoded AgentMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Assistant == nil || decoded.Assistant.ThinkingLevel != ai.ThinkingHigh {
		t.Fatalf("decoded = %+v, want thinkingLevel high", decoded.Assistant)
	}
	absent := AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Usage: &ai.Usage{}}}
	if encoded, _ := json.Marshal(absent); strings.Contains(string(encoded), "thinkingLevel") {
		t.Fatalf("a legacy message encodes thinkingLevel: %s", encoded)
	}
}

func TestAssistantMessageLLMMessageCarriesThinkingLevel(t *testing.T) {
	message := &AssistantMessage{Role: RoleAssistant, ThinkingLevel: ai.ThinkingLow}
	if got := message.LLMMessage().ThinkingLevel; got != ai.ThinkingLow {
		t.Fatalf("LLMMessage thinkingLevel = %q, want low", got)
	}
}
