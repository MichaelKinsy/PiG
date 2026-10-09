package agent

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// .upstream/v0.87.1/packages/agent/test/agent-loop.test.ts:87
// .upstream/v0.87.1/packages/agent/test/agent.test.ts:107
func TestConfiguredDefaultStreamFn(t *testing.T) {
	for _, entry := range []string{"agent", "agent-loop"} {
		t.Run(entry, func(t *testing.T) {
			calls := 0
			SetDefaultStreamFn(func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				calls++
				return doneStream(textMessage("fallback")), nil
			})
			t.Cleanup(func() { SetDefaultStreamFn(nil) })
			a := mustNewAgent(AgentOptions{})
			if entry == "agent" {
				mustSend(t, a, "Hello")
			} else {
				runPrompt(t, a, a.createLoopConfig(false), userMessage("Hello"))
			}
			if calls != 1 {
				t.Fatalf("fallback calls = %d, want 1", calls)
			}
		})
	}
}

// Go represents Pi's stock stream identity with a nil override; replacing and restoring it must preserve the host's configured path.
func TestAgentStreamFunctionOverrideAndRestore(t *testing.T) {
	var calls []string
	a := mustNewAgent(AgentOptions{DefaultStreamFn: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		calls = append(calls, "stock")
		return doneStream(textMessage("stock")), nil
	}})
	if a.StreamFunction() != nil {
		t.Fatal("stock stream reported as a caller override")
	}
	mustSend(t, a, "first")
	a.SetStreamFunction(func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		calls = append(calls, "custom")
		return doneStream(textMessage("custom")), nil
	})
	if a.StreamFunction() == nil {
		t.Fatal("missing current stream override")
	}
	mustSend(t, a, "second")
	a.SetStreamFunction(nil)
	if a.StreamFunction() != nil {
		t.Fatal("nil did not restore the stock stream")
	}
	mustSend(t, a, "third")
	if !slices.Equal(calls, []string{"stock", "custom", "stock"}) {
		t.Fatalf("stream calls = %v", calls)
	}
}

// Pi packages/agent/src/agent.ts:229 and agent-loop.ts:390 pass the model
// descriptor to streamFn; an injected stream does not need a provider object.
func TestStreamFnWithModelDescriptorOnly(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			called := false
			a := mustNewAgent(AgentOptions{Model: &ai.Model{ID: "mock", ProviderMeta: ai.ProviderMetadata{ProviderID: "openai", API: ai.APIOpenAIResponses}}, StreamFn: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				called = true
				if failed {
					return nil, errors.New("provider exploded")
				}
				return doneStream(textMessage("fallback")), nil
			}})
			messages, err := a.Send(t.Context(), "Hello")
			if err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("streamFn not invoked")
			}
			last := messages[len(messages)-1].Assistant
			if last == nil {
				t.Fatal("missing assistant")
			}
			if failed && (last.StopReason != ai.StopReasonError || last.ErrorMessage != "provider exploded") {
				t.Fatalf("failure = %+v", last)
			}
			if !failed && last.Content[0].(ai.TextContent).Text != "fallback" {
				t.Fatalf("response = %+v", last)
			}
		})
	}
}
