package coding

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestTurnErrorAtTheSendBoundary proves the contract at the seam embedded
// engines rely on: a provider failure makes Send return a nil error, and the
// turn's outcome is only recoverable through the returned messages —
// agent.TurnError surfaces it as a typed error.
func TestTurnErrorAtTheSendBoundary(t *testing.T) {
	errorMessage := sessionTestMessage("fake", "", ai.StopReasonError, "429 Too Many Requests")
	failing := staticProviderForSessionTests{events: []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: errorMessage},
		ai.ErrorEvent{Reason: ai.StopReasonError, Error: errorMessage},
	}}
	healthy := staticProviderForSessionTests{events: []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: sessionTestMessage("fake", "", ai.StopReasonPending, "")},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: sessionTestMessage("fake", "all good", ai.StopReasonStop, "")},
	}}

	disableRetry := false
	for name, provider := range map[string]staticProviderForSessionTests{
		"provider-failure": failing,
		"healthy-turn":     healthy,
	} {
		t.Run(name, func(t *testing.T) {
			// Retries off: the scripted failure fails once, instantly, instead
			// of burning the default three backoff retries (~14s).
			svcs := newTestServicesWithSettings(t, icodingagent.Settings{
				Retry: &icodingagent.RetrySettingsJSON{Enabled: &disableRetry},
			})
			sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sess.Close() }()

			msgs, sendErr := sess.Send(t.Context(), "hi")
			// The contract: a provider failure never becomes a Go error.
			if sendErr != nil {
				t.Fatalf("Send returned a Go error for the turn outcome: %v", sendErr)
			}
			turnErr := agent.TurnError(msgs)
			if name == "provider-failure" {
				if turnErr == nil {
					t.Fatal("TurnError(nil) on a failed turn — the false-green path")
				}
				var failure *agent.TurnFailure
				if !errors.As(turnErr, &failure) {
					t.Fatalf("TurnError is %T, want *agent.TurnFailure", turnErr)
				}
				if !strings.Contains(failure.Message, "429") {
					t.Fatalf("TurnFailure.Message = %q, want the provider's 429 text", failure.Message)
				}
			} else if turnErr != nil {
				t.Fatalf("TurnError = %v on a healthy turn, want nil", turnErr)
			}
		})
	}
}
