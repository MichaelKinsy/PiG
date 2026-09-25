package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/test/suite/regressions/6904-dns-transport-retry.test.ts.
func TestSendRetriesDNSLookupFailure(t *testing.T) {
	const wrappedDNSLookupError = "The pending stream has been canceled (caused by: getaddrinfo ENOTFOUND bedrock-runtime.us-east-1.amazonaws.com)"
	harness := newRecoveryHarness(t, harnessOptions{
		settings: `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}}`,
	}, fauxError(wrappedDNSLookupError), fauxReply("recovered after DNS retry", ai.StopReasonStop, 0))

	if _, err := harness.session.Send(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	if calls := harness.provider.callCount(); calls != 2 {
		t.Fatalf("provider calls = %d, want 2", calls)
	}

	var starts []agent.AutoRetryStartEvent
	var ends []agent.AutoRetryEndEvent
	for _, event := range harness.settle(t) {
		switch event := event.(type) {
		case agent.AutoRetryStartEvent:
			starts = append(starts, event)
		case agent.AutoRetryEndEvent:
			ends = append(ends, event)
		}
	}
	if len(starts) != 1 || starts[0].ErrorMessage != wrappedDNSLookupError {
		t.Fatalf("auto retry starts = %#v", starts)
	}
	if len(ends) != 1 || !ends[0].Success {
		t.Fatalf("auto retry ends = %#v", ends)
	}
}
