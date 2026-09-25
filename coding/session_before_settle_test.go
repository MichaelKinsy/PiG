package coding

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestAgentBeforeSettleCommitsCustomMessageAndContinuesOnce(t *testing.T) {
	var calls atomic.Int32
	ext := extension.Extension{Path: "boundary", Handlers: map[string][]extension.HandlerFn{
		"agent_before_settle": {func(args ...any) (any, error) {
			event := args[0].(*extension.AgentBeforeSettleEvent)
			if event.Outcome != extension.AgentActivityCompleted {
				t.Fatalf("outcome = %q, want completed", event.Outcome)
			}
			if calls.Add(1) != 1 {
				return nil, nil
			}
			if len(event.Context.ContextMessages) == 0 || event.Context.CanContinue {
				t.Fatalf("initial context = %+v, want settled assistant context", event.Context)
			}
			entries := []extension.SessionBoundaryDraft{{
				Type: "custom_message", CustomType: "test-continuation", Content: "continue now", Display: false,
			}}
			continued := true
			return extension.BoundaryResult{Entries: &entries, Continue: &continued}, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext},
		fauxReply("first", "stop", 0),
		fauxReply("second", "stop", 0),
	)
	if _, err := h.session.Send(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	if got := h.provider.callCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2", got)
	}
	if len(h.provider.requests) < 2 || !strings.Contains(h.provider.requests[1], "continue now") {
		t.Fatalf("second request = %v, want boundary context", h.provider.requests)
	}
	if got := len(h.entries("custom_message")); got != 1 {
		t.Fatalf("custom_message entries = %d, want 1", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("boundary calls = %d, want one per settled low-level run", got)
	}
}
