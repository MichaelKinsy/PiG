package coding

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi 0.87.1 agent-session.ts:1015-1028 publishes each durable recovery omission before continuing the retry.
func TestRecoveryPublishesContextEditEntry(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"retry":{"enabled":true,"maxRetries":1,"baseDelayMs":1},"compaction":{"enabled":false}}`}, fauxError("overloaded"), fauxReply("recovered", "stop", 0))
	if _, err := h.session.Send(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	events := h.settle(t)
	var got []any
	retryStart, appendIndex, continuation := -1, -1, -1
	for index, event := range events {
		switch event.(type) {
		case agent.AutoRetryStartEvent:
			retryStart = index
		case agent.AgentStartEvent:
			if retryStart >= 0 {
				continuation = index
			}
		}
		if appended, ok := event.(agent.EntryAppendedEvent); ok {
			appendIndex = index
			var entry any
			if err := json.Unmarshal(appended.Entry, &entry); err != nil {
				t.Fatal(err)
			}
			got = append(got, entry)
		}
	}
	if retryStart < 0 || appendIndex <= retryStart || continuation <= appendIndex {
		t.Fatalf("recovery ordering: start=%d append=%d continuation=%d", retryStart, appendIndex, continuation)
	}
	var want []any
	for _, entry := range h.entries("context_edit") {
		var value any
		if err := json.Unmarshal(entry.Raw(), &value); err != nil {
			t.Fatal(err)
		}
		want = append(want, value)
	}
	if len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("entry_appended=%v persisted=%v", got, want)
	}
}
