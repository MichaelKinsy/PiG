package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

// The host side of a Start run against a launcher that is not the in-process worker: what packages/codemode/src/runtime/
// execution.ts (Execution.forwardCall, Execution.deliver, CodemodeSandboxBase.start) does with the VM's messages and the
// caller's answers. The VM side is tested in deterministic_test.go.

type startHarness struct {
	t       *testing.T
	events  vmEvents
	data    VmStartData
	channel *fakeChannel
	mu      sync.Mutex
	calls   []HostCall
	started []string
	idles   [][2]int
	run     *Run
}

func newStartHarness(t *testing.T, options RunOptions) *startHarness {
	t.Helper()
	h := &startHarness{t: t, channel: &fakeChannel{}}
	launched := make(chan struct{})
	sandbox := sandboxWith(t, func(_ context.Context, data VmStartData, events vmEvents) (vmChannel, error) {
		// The launcher runs on the run's goroutine; closing launched publishes data and events to the test goroutine.
		h.data, h.events = data, events
		close(launched)
		return h.channel, nil
	})
	options.OnStarted = func(fingerprint string) { h.mu.Lock(); h.started = append(h.started, fingerprint); h.mu.Unlock() }
	options.OnCall = func(call HostCall) { h.mu.Lock(); h.calls = append(h.calls, call); h.mu.Unlock() }
	options.OnIdle = func(seq, issued int) { h.mu.Lock(); h.idles = append(h.idles, [2]int{seq, issued}); h.mu.Unlock() }
	run, err := sandbox.Start(t.Context(), "ignored", options)
	if err != nil {
		t.Fatal(err)
	}
	h.run = run
	waitLaunched(t, launched)
	return h
}

// waitLaunched waits until the fake launcher ran, which makes what it stored visible to the test goroutine.
func waitLaunched(t *testing.T, launched <-chan struct{}) {
	t.Helper()
	select {
	case <-launched:
	case <-time.After(5 * time.Second):
		t.Fatal("the launcher did not run")
	}
}

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not reached")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (h *startHarness) callCount() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.calls) }

func (h *startHarness) send(m vmMessage) { h.events.message(m) }

func toolCall(id int, name, args string) vmMessage {
	return vmMessage{kind: vmMessageCall, id: id, isTool: true, name: name, args: args, argsDefined: args != ""}
}

func TestStartForwardsTheStartDataAndTheCallsOfTheScript(t *testing.T) {
	h := newStartHarness(t, RunOptions{StartedAt: 1000, Seed: 7, Tools: []DeclaredTool{{Name: "my-tool", JSName: "renamed"}, {Name: "plain"}}, Globals: []DeclaredGlobal{{Name: "search", Spread: true}}})
	want := VmStartData{Code: "ignored", Tools: []VmStartTool{{"my-tool", "renamed", ""}, {"plain", "plain", ""}}, Globals: []VmStartGlobal{{"search", true}}, Store: map[string]string{}, Deterministic: &VmDeterministic{StartedAt: 1000, Seed: 7}}
	got, _ := json.Marshal(h.data)
	exp, _ := json.Marshal(want)
	if !bytes.Equal(got, exp) {
		t.Fatalf("start data %s, want %s", got, exp)
	}
	h.send(vmMessage{kind: vmMessageStarted, fingerprint: "fp"})
	h.send(toolCall(1, "plain", `{"a":1}`))
	h.send(vmMessage{kind: vmMessageCall, id: 2, name: "search", args: `["x",2]`, argsDefined: true})
	h.send(vmMessage{kind: vmMessageIdle, seq: 0, issued: 2})
	waitFor(t, func() bool { return h.callCount() == 2 })
	if h.started[0] != "fp" || h.idles[0] != [2]int{0, 2} {
		t.Fatalf("started %v idles %v", h.started, h.idles)
	}
	if c := h.calls[0]; c.ID != 1 || c.Target != "tool" || c.Name != "plain" || string(c.Args) != `{"a":1}` {
		t.Fatalf("call 1 = %+v", c)
	}
	if c := h.calls[1]; c.ID != 2 || c.Target != "global" || string(c.Args) != `["x",2]` {
		t.Fatalf("call 2 = %+v", c)
	}
	h.send(vmMessage{kind: vmMessageDone, ok: true, value: "1", valueDefined: true, writes: "[]", writesDefined: true, during: &During{Seq: 2, Issued: 2}})
	result := h.run.Result()
	if !result.OK || result.During == nil || *result.During != (During{2, 2}) {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Calls) != 1 || result.Calls[0].Name != "plain" || result.Calls[0].Status != CallCancelled {
		t.Fatalf("a call nobody answered stays cancelled, globals are not recorded: %+v", result.Calls)
	}
}

func TestDeliverPostsTheAnswerWithItsSequenceNumberAndClock(t *testing.T) {
	h := newStartHarness(t, RunOptions{Tools: []DeclaredTool{{Name: "echo"}}})
	h.send(toolCall(1, "echo", "1"))
	waitFor(t, func() bool { return h.callCount() == 1 })
	if err := h.run.Deliver(3, 5000, 1, Outcome{Message: "no", Properties: json.RawMessage(`{"code":7}`)}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { posted, _ := h.channel.state(); return len(posted) == 1 })
	posted, _ := h.channel.state()
	if r := posted[0]; r.id != 1 || r.ok || r.payload != "no" || r.seq != 3 || r.now != 5000 || r.properties != `{"code":7}` || !r.propertiesDefined {
		t.Fatalf("posted %+v", r)
	}
	h.send(vmMessage{kind: vmMessageDone, ok: true, writes: "[]", writesDefined: true})
	if result := h.run.Result(); len(result.Calls) != 1 || result.Calls[0].Status != CallError {
		t.Fatalf("an error answer records the call as an error: %+v", result.Calls)
	}
}

// execution.ts deliver: each refusal leaves the call waiting, so a later valid answer is accepted.
func TestDeliverRefusesWhatCannotBeAnsweredAndLeavesTheCallWaiting(t *testing.T) {
	h := newStartHarness(t, RunOptions{Tools: []DeclaredTool{{Name: "wait"}}})
	h.send(toolCall(1, "wait", "1"))
	h.send(toolCall(2, "wait", "2"))
	waitFor(t, func() bool { return h.callCount() == 2 })
	for _, c := range []struct {
		name    string
		deliver func() error
		want    string
	}{
		{"not finite", func() error { return h.run.Deliver(1, math.NaN(), 1, Outcome{OK: true}) }, "not a finite number"},
		{"infinite", func() error { return h.run.Deliver(1, math.Inf(1), 1, Outcome{OK: true}) }, "not a finite number"},
		{"unknown call", func() error { return h.run.Deliver(1, 0, 99, Outcome{OK: true}) }, "Call 99 is not waiting"},
		{"seq zero", func() error { return h.run.Deliver(0, 0, 2, Outcome{OK: true}) }, "used once"},
		{"invalid JSON", func() error { return h.run.Deliver(1, 0, 1, Outcome{OK: true, Value: json.RawMessage(`{`)}) }, "not valid JSON"},
	} {
		if err := c.deliver(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if posted, _ := h.channel.state(); len(posted) != 0 {
		t.Fatalf("a refused answer was posted: %+v", posted)
	}
	if err := h.run.Deliver(1, 0, 1, Outcome{OK: true}); err != nil {
		t.Fatal(err)
	}
	if err := h.run.Deliver(5, 0, 1, Outcome{OK: true}); err == nil || !strings.Contains(err.Error(), "Call 1 is not waiting") {
		t.Fatalf("an answered call is not waiting again: %v", err)
	}
	if err := h.run.Deliver(1, 0, 2, Outcome{OK: true}); err == nil || !strings.Contains(err.Error(), "used once") {
		t.Fatalf("a result number used twice: %v", err)
	}
	if err := h.run.Deliver(2, 0, 2, Outcome{OK: true}); err != nil {
		t.Fatal(err)
	}
}

// types.ts: the caller's abort ends the run with an error the script cannot catch, and a later answer is not an error.
func TestStartEndsAsAbortedWhenTheCallerCancels(t *testing.T) {
	h := &startHarness{t: t, channel: &fakeChannel{}}
	launched := make(chan struct{})
	sandbox := sandboxWith(t, func(_ context.Context, data VmStartData, events vmEvents) (vmChannel, error) {
		h.events = events
		close(launched)
		return h.channel, nil
	})
	ctx, cancel := context.WithCancelCause(t.Context())
	run, err := sandbox.Start(ctx, "ignored", RunOptions{Tools: []DeclaredTool{{Name: "wait"}}, OnCall: func(HostCall) {}})
	if err != nil {
		t.Fatal(err)
	}
	waitLaunched(t, launched)
	h.events.message(toolCall(1, "wait", ""))
	cancel(errors.New("diverged"))
	result := run.Result()
	if result.OK || result.Error.Kind != ErrorAborted || result.Error.Message != "diverged" {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	if err := run.Deliver(1, 0, 1, Outcome{OK: true}); err != nil {
		t.Fatalf("an answer after the run ended is ignored: %v", err)
	}
}

func TestStartRefusesWhatUpstreamRefuses(t *testing.T) {
	sandbox := sandboxWith(t, func(context.Context, VmStartData, vmEvents) (vmChannel, error) { return &fakeChannel{}, nil })
	for _, c := range []struct {
		name    string
		options RunOptions
		want    string
	}{
		{"negative start", RunOptions{StartedAt: -1}, "startedAt must be"},
		{"NaN start", RunOptions{StartedAt: math.NaN()}, "startedAt must be"},
		{"duplicate tool", RunOptions{Tools: []DeclaredTool{{Name: "a"}, {Name: "a"}}}, "Tool names must be unique"},
		{"shared script name", RunOptions{Tools: []DeclaredTool{{Name: "a-b"}, {Name: "a_b"}}}, "script names must be unique"},
		{"bad global", RunOptions{Globals: []DeclaredGlobal{{Name: "tools"}}}, "Invalid global name"},
		{"global and namespace", RunOptions{Globals: []DeclaredGlobal{{Name: "a"}, {Name: "a.b"}}}, "conflicts with the namespace"},
	} {
		if _, err := sandbox.Start(t.Context(), "", c.options); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	_ = sandbox.Close()
	if _, err := sandbox.Start(t.Context(), "", RunOptions{}); err == nil || err.Error() != "Sandbox is closed" {
		t.Fatalf("a closed sandbox: %v", err)
	}
}
