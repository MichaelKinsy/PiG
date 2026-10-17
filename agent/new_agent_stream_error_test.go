package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

const noDefaultStreamMessage = "No default stream function configured. Pass streamFn explicitly or call setDefaultStreamFn()."

type providerStub struct{}

func (*providerStub) ID() string { return "stub" }
func (*providerStub) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return doneStream(textMessage("provider")), nil
}
func (*providerStub) Close() error { return nil }

func piStream(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return doneStream(textMessage("ok")), nil
}

// Pi 1.0.4 packages/agent/src/agent.ts:236: `this.streamFunction = runtimeOptions.streamFn ?? getDefaultStreamFn()`, and
// stream-fn.ts getDefaultStreamFn throws when no default is configured. The constructor fails; it does not fall back to a provider.
func TestNewAgentFailsWithoutAStreamFunction(t *testing.T) {
	SetDefaultStreamFn(nil)
	t.Cleanup(func() { SetDefaultStreamFn(nil) })

	agent, err := NewAgent(AgentOptions{})
	if agent != nil || err == nil || err.Error() != noDefaultStreamMessage {
		t.Fatalf("NewAgent() = %v, %v; want nil and %q", agent, err, noDefaultStreamMessage)
	}

	// A model that carries its own provider does not stand in for the stream function.
	withProvider := &ai.Model{ID: "native", ProviderMeta: ai.ProviderMetadata{ProviderID: "openai", API: ai.APIOpenAIResponses}, Provider: &providerStub{}}
	agent, err = NewAgent(AgentOptions{Model: withProvider})
	if agent != nil || err == nil {
		t.Fatalf("NewAgent(model with provider) = %v, %v; want the same failure", agent, err)
	}
	if !errors.Is(err, ErrNoDefaultStreamFunction) {
		t.Fatalf("error %v is not ErrNoDefaultStreamFunction", err)
	}
}

func TestNewAgentAcceptsEachStreamFunctionSource(t *testing.T) {
	SetDefaultStreamFn(nil)
	t.Cleanup(func() { SetDefaultStreamFn(nil) })

	if agent, err := NewAgent(AgentOptions{StreamFn: piStream}); err != nil || agent == nil {
		t.Fatalf("explicit streamFn: %v, %v", agent, err)
	}
	if agent, err := NewAgent(AgentOptions{DefaultStreamFn: piStream}); err != nil || agent == nil {
		t.Fatalf("host default option: %v, %v", agent, err)
	}
	SetDefaultStreamFn(piStream)
	agent, err := NewAgent(AgentOptions{})
	if err != nil || agent == nil {
		t.Fatalf("configured default: %v, %v", agent, err)
	}
	// The configured default is read when the agent is built: clearing it later does not break this agent.
	SetDefaultStreamFn(nil)
	if _, err := agent.Send(t.Context(), "hi"); err != nil {
		t.Fatalf("Send after the default was cleared: %v", err)
	}
}

// With no stream function left (an override reset to nil on an agent built without a default), a run reports the failure instead of dereferencing nil.
func TestSendWithoutAStreamFunctionReportsTheFailure(t *testing.T) {
	SetDefaultStreamFn(nil)
	agent, err := NewAgent(AgentOptions{StreamFn: piStream})
	if err != nil {
		t.Fatal(err)
	}
	agent.SetStreamFunction(nil)
	if _, err := agent.Send(t.Context(), "hi"); !errors.Is(err, ErrNoDefaultStreamFunction) {
		t.Fatalf("Send = %v, want ErrNoDefaultStreamFunction", err)
	}
}
