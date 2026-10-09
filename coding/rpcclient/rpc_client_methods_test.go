package rpcclient

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// PiG's own tests for RpcClient members that upstream's rpc.test.ts and rpc-client tests never call directly. Each
// drives the real `pig --mode rpc` subprocess on the hermetic test-faux provider and observes the effect through a
// second command, as upstream's rpc.test.ts does for the commands it covers.

func TestRpcClientSetModesAndAutoRetryAreVisibleInState(t *testing.T) {
	f := newFauxFixture(t)
	if err := f.client.SetSteeringMode("all"); err != nil {
		t.Fatal(err)
	}
	if err := f.client.SetFollowUpMode("all"); err != nil {
		t.Fatal(err)
	}
	state, err := f.client.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.SteeringMode != "all" || state.FollowUpMode != "all" {
		t.Fatalf("state modes = %q, %q, want all, all", state.SteeringMode, state.FollowUpMode)
	}
	if err := f.client.SetSteeringMode("one-at-a-time"); err != nil {
		t.Fatal(err)
	}
	if err := f.client.SetFollowUpMode("one-at-a-time"); err != nil {
		t.Fatal(err)
	}
	if state, err = f.client.GetState(); err != nil || state.SteeringMode != "one-at-a-time" || state.FollowUpMode != "one-at-a-time" {
		t.Fatalf("state after reset = %+v, %v", state, err)
	}
	if err := f.client.SetAutoRetry(false); err != nil {
		t.Fatal(err)
	}
	if err := f.client.SetAutoRetry(true); err != nil {
		t.Fatal(err)
	}
}

func TestRpcClientAbortCommandsAreAcceptedWhileIdle(t *testing.T) {
	f := newFauxFixture(t)
	for name, abort := range map[string]func() error{"abort": f.client.Abort, "abort_bash": f.client.AbortBash, "abort_retry": f.client.AbortRetry} {
		if err := abort(); err != nil {
			t.Fatalf("%s while idle: %v", name, err)
		}
	}
	if state, err := f.client.GetState(); err != nil || state.IsStreaming {
		t.Fatalf("state after aborts = %+v, %v", state, err)
	}
}

// mutation-checked: zeroing the results of RpcClient.PromptAndWait fails it
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:513 (promptAndWait)
func TestRpcClientPromptAndWaitReturnsEventsThroughSettled(t *testing.T) {
	f := newFauxFixture(t)
	events, err := f.client.PromptAndWait("reply with exactly: pong", nil, DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	if !slices.Contains(types, "agent_end") || types[len(types)-1] != "agent_settled" {
		t.Fatalf("event types = %v, want agent_end and a final agent_settled", types)
	}
}

// Like upstream's waitForIdle, WaitForIdle resolves on the next agent_settled event; it does not inspect the state.
// packages/coding-agent/src/modes/rpc/rpc-client.ts:471 waitForIdle resolves on agent_settled (rpc-client.ts:479) and otherwise
// rejects with "Timeout waiting for agent to become idle." (rpc-client.ts:475).
func TestRpcClientWaitForIdleResolvesOnTheNextSettledEventAndTimesOutWithoutOne(t *testing.T) {
	f := newFauxFixture(t)
	if err := f.client.WaitForIdle(100 * time.Millisecond); err == nil || !strings.HasPrefix(err.Error(), "Timeout waiting for agent to become idle.") {
		t.Fatalf("WaitForIdle with no run = %v, want the idle timeout", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- f.client.WaitForIdle(DefaultTimeout) }()
	time.Sleep(200 * time.Millisecond)
	if _, err := f.client.Prompt("reply with exactly: idle", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("WaitForIdle = %v", err)
		}
	case <-time.After(DefaultTimeout):
		t.Fatal("WaitForIdle did not return")
	}
}

// packages/coding-agent/src/modes/rpc/rpc-client.ts:491 collectEvents gathers events until agent_settled, which it includes
// (rpc-client.ts:501).
func TestRpcClientCollectEventsGathersTheNextRunThroughSettled(t *testing.T) {
	f := newFauxFixture(t)
	type collected struct {
		events []JsonAgentSessionEvent
		err    error
	}
	done := make(chan collected, 1)
	go func() {
		events, err := f.client.CollectEvents(DefaultTimeout)
		done <- collected{events, err}
	}()
	// The collector registers its listener before the prompt starts the run it is waiting for.
	time.Sleep(200 * time.Millisecond)
	if _, err := f.client.Prompt("reply with exactly: collected", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || len(got.events) == 0 || got.events[len(got.events)-1].Type != "agent_settled" {
			t.Fatalf("collected %d events, err %v", len(got.events), got.err)
		}
	case <-time.After(DefaultTimeout):
		t.Fatal("CollectEvents did not return")
	}
}

// packages/coding-agent/src/modes/rpc/rpc-client.ts:491 collectEvents rejects with "Timeout collecting events." (rpc-client.ts:496).
func TestRpcClientCollectEventsTimesOutWithoutARun(t *testing.T) {
	f := newFauxFixture(t)
	_, err := f.client.CollectEvents(100 * time.Millisecond)
	if err == nil || !strings.HasPrefix(err.Error(), "Timeout collecting events.") {
		t.Fatalf("err = %v, want the collection timeout", err)
	}
}

// mutation-checked: zeroing the results of RpcClient.Fork, RpcClient.GetForkMessages fails it
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:394 (fork)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:411 (getForkMessages)
func TestRpcClientForkMessagesForkAndSwitchSession(t *testing.T) {
	f := newFauxFixture(t)
	f.promptAndWait(t, "reply with exactly: first")
	messages, err := f.client.GetForkMessages()
	if err != nil || len(messages) != 1 || messages[0].Text != "reply with exactly: first" {
		t.Fatalf("fork messages = %+v, %v", messages, err)
	}
	before, err := f.client.GetState()
	if err != nil || before.SessionFile == nil {
		t.Fatalf("state = %+v, %v", before, err)
	}
	forked, err := f.client.Fork(messages[0].EntryID)
	if err != nil || forked.Cancelled || forked.Text != "reply with exactly: first" {
		t.Fatalf("fork = %+v, %v", forked, err)
	}
	after, err := f.client.GetState()
	if err != nil || after.SessionFile == nil || *after.SessionFile == *before.SessionFile {
		t.Fatalf("a fork must switch to a new session file: before %v after %+v, %v", *before.SessionFile, after, err)
	}
	switched, err := f.client.SwitchSession(*before.SessionFile)
	if err != nil || switched.Cancelled {
		t.Fatalf("switch = %+v, %v", switched, err)
	}
	if state, err := f.client.GetState(); err != nil || state.SessionFile == nil || *state.SessionFile != *before.SessionFile || state.MessageCount == 0 {
		t.Fatalf("state after switching back = %+v, %v", state, err)
	}
}

func TestRpcClientCycleModelReportsNoOtherModelWithASingleModel(t *testing.T) {
	f := newFauxFixture(t)
	result, err := f.client.CycleModel()
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("cycle with one available model = %+v, want nil (upstream's null)", result)
	}
}
