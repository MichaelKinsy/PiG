package execution_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/ai"
)

func requireEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustRegister(t *testing.T) func(func(), error) {
	t.Helper()
	return func(unsubscribe func(), err error) {
		t.Helper()
		requireNoError(t, err)
		t.Cleanup(unsubscribe)
	}
}

func userMessage(text string, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{
		Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: timestamp,
	}}
}

func runStartEvent(runID, lane string) agentharness.HarnessEvent {
	return agentharness.HarnessEvent{Payload: agentharness.RunStartPayload{RunID: runID, StartedAt: 1}, Lane: lane}
}

func queueUpdateEvent(entryID string, timestamp int64) agentharness.HarnessEvent {
	return agentharness.HarnessEvent{Payload: agentharness.QueueUpdatePayload{Queues: []agentharness.LaneQueuedItem{{
		EntryID: entryID, Kind: "nextRun", Type: "message", Message: userMessage(entryID, timestamp),
	}}}, Lane: "main"}
}

func newEffectGate(t *testing.T) *execution.Gate {
	t.Helper()
	gate, control := execution.CreateGate()
	t.Cleanup(func() { control.Close(errors.New("test cleanup")) })
	return gate
}

func deferred() (<-chan struct{}, func()) {
	done := make(chan struct{})
	return done, sync.OnceFunc(func() { close(done) })
}

func ignoreHookError(harness.Context, error, agentharness.HookName, string) error {
	return nil
}
