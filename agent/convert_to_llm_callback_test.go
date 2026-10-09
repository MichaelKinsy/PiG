package agent

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent-loop.ts awaits convertToLlm before opening the provider stream;
// agent.ts runWithLifecycle handles a rejected converter as a run failure.
func TestConvertToLlmWaitsAndReportsFailureWithoutCallingProvider(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		provider := &scriptedProvider{respond: replyText("unexpected")}
		a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), ConvertToLlm: func([]AgentMessage) ([]ai.Message, error) { <-release; return nil, errors.New("conversion failed") }})
		done := sendAsync(t, a, "hello")
		synctest.Wait()
		if !a.IsStreaming() || provider.calls() != 0 {
			t.Fatalf("streaming=%v provider=%d", a.IsStreaming(), provider.calls())
		}
		close(release)
		synctest.Wait()
		<-done
		messages := a.Messages()
		if provider.calls() != 0 || len(messages) != 2 || messages[1].Assistant == nil || messages[1].Assistant.ErrorMessage != "conversion failed" || a.ErrorMessage() != "conversion failed" {
			t.Fatalf("provider=%d messages=%+v state error=%q", provider.calls(), messages, a.ErrorMessage())
		}
	})
}
