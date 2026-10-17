package codemode

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The in-thread launcher (packages/codemode/src/runtime/inline.ts): messages are delivered after the VM call returned and
// never while the host is inside another delivery, and stop is safe to repeat.

func launchInlineVM(t *testing.T, code string, budget int, events vmEvents) vmChannel {
	t.Helper()
	data := VmStartData{Code: code, Tools: []VmStartTool{{Name: "t", JSName: "t"}}, Globals: []VmStartGlobal{}, Store: map[string]string{}}
	channel, err := inlineLauncher(wasmEngineLoader(nil, ""), PreludeSource, budget)(t.Context(), data, events)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(channel.stop)
	return channel
}

// A host that answers a call from inside the message handler, as a blocking driver does, does not nest deliveries: the
// post only enqueues while the goroutine that is pumping delivers.
func TestAnInlineHostThatAnswersInsideTheHandlerIsNeverReentered(t *testing.T) {
	const calls = 300
	var (
		channel vmChannel
		depth   int
		deepest int
		done    = make(chan vmMessage, 1)
	)
	events := vmEvents{
		message: func(message vmMessage) {
			depth++
			deepest = max(deepest, depth)
			defer func() { depth-- }()
			switch message.kind {
			case vmMessageCall:
				channel.post(callResult{id: message.id, ok: true, payload: strconv.Itoa(message.id), defined: true})
			case vmMessageDone, vmMessageCrash:
				done <- message
			}
		},
		failure: func(failure *Error) { t.Errorf("failure: %+v", failure) },
	}
	channel = launchInlineVM(t, "let sum = 0; for (let i = 0; i < "+strconv.Itoa(calls)+"; i++) sum += await tools.t(i); return sum", DefaultInterruptBudget, events)
	channel.start()
	message := <-done
	if !message.ok || message.value != strconv.Itoa(calls*(calls+1)/2) {
		t.Fatalf("done = %+v, want the sum of the call ids 1..%d", message, calls)
	}
	if deepest != 1 {
		t.Fatalf("the host handler nested %d deep, want 1: a post from inside the handler must only enqueue", deepest)
	}
}

// inline.ts: stop disposes the VM once, however often and however concurrently it is called, and nothing is delivered
// after it.
func TestStoppingAnInlineChannelIsIdempotentAndSilencesIt(t *testing.T) {
	var mu sync.Mutex
	var delivered []vmMessage
	events := vmEvents{
		message: func(message vmMessage) {
			mu.Lock()
			delivered = append(delivered, message)
			mu.Unlock()
		},
		failure: func(failure *Error) { t.Errorf("failure: %+v", failure) },
	}
	before := liveVMs.Load()
	channel := launchInlineVM(t, "text('queued'); await tools.t(1); text('never')", DefaultInterruptBudget, events)
	if live := liveVMs.Load(); live != before+1 {
		t.Fatalf("live VMs = %d, want %d: the VM exists as soon as launch returns", live, before+1)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(channel.stop)
	}
	wg.Wait()
	channel.stop()
	if live := liveVMs.Load(); live != before {
		t.Fatalf("live VMs = %d after stop, want %d", live, before)
	}
	channel.start()
	channel.post(callResult{id: 1, ok: true, payload: "1", defined: true})
	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 0 {
		t.Fatalf("delivered %+v after stop, want nothing", delivered)
	}
}

// inline.ts: only the first verdict is reported; the crash that interrupting a settled VM causes is dropped, and an
// interrupt that ends the VM after the budget ran out is reported as a timeout instead of the crash.
func TestAnInlineChannelReportsOnlyTheFirstVerdict(t *testing.T) {
	var messages []vmMessage
	var failures []*Error
	events := vmEvents{
		message: func(message vmMessage) { messages = append(messages, message) },
		failure: func(failure *Error) { failures = append(failures, failure) },
	}
	channel := launchInlineVM(t, "text('a'); return 1", DefaultInterruptBudget, events)
	channel.start()
	if len(failures) != 0 || len(messages) != 2 || messages[0].kind != vmMessageOutput || messages[1].kind != vmMessageDone || !messages[1].ok {
		t.Fatalf("messages = %+v, failures = %+v, want the output then one done", messages, failures)
	}

	messages, failures = nil, nil
	channel = launchInlineVM(t, "while (true) {}", 50, events)
	channel.start()
	if len(messages) != 0 || len(failures) != 1 || failures[0].Kind != ErrorTimeout || failures[0].Message != "Execution exceeded its interrupt budget of 50" {
		t.Fatalf("messages = %+v, failures = %+v, want one budget timeout", messages, failures)
	}
}

// inline.ts: once the VM reported its verdict the result is decided, so it is interrupted at the next poll instead of running
// on until the budget ends and being reported as a timeout. The script keeps printing after the output limit failed it.
func TestAnInlineVMIsInterruptedOnceItReportedItsVerdict(t *testing.T) {
	var messages []vmMessage
	var failures []*Error
	events := vmEvents{
		message: func(message vmMessage) { messages = append(messages, message) },
		failure: func(failure *Error) { failures = append(failures, failure) },
	}
	channel := launchInlineVM(t, `const s = "x".repeat(1 << 20); for (;;) { try { text(s); } catch {} }`, DefaultInterruptBudget, events)
	channel.start()
	if len(failures) != 0 {
		t.Fatalf("failures = %+v, want none: the script ended with its output-limit error before the budget did", failures)
	}
	verdicts := 0
	for _, message := range messages {
		if terminal(message) {
			verdicts++
		}
	}
	if last := messages[len(messages)-1]; verdicts != 1 || last.kind != vmMessageDone || last.ok {
		t.Fatalf("%d verdicts, last message %+v, want one failed done last", verdicts, last)
	}
}

// Node runs the microtasks a segment queued before any timer, so the result of a first segment that computes past the
// deadline is delivered before the deadline (inline.ts post: queueMicrotask; execution.ts: the timer is a macrotask). The
// first segment's messages are delivered by the channel's start, after the launcher returned: the turn must last until
// they are delivered, or a deadline that waited for the segment ends the execution first. The launcher here returns its
// channel late, as the host goroutine descheduled after the segment would.
func TestAnInlineFirstSegmentDeliversItsResultBeforeAnExpiredDeadline(t *testing.T) {
	sandbox, err := NewInlineSandbox(InlineSandboxOptions{InterruptBudget: 10_000_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	if r, _ := sandbox.Execute(t.Context(), "return 0", ExecuteOptions{}); !r.OK {
		t.Fatalf("warm-up: %+v", r.Error)
	}
	inline := sandbox.launcher
	sandbox.launcher = func(ctx context.Context, data VmStartData, events vmEvents) (vmChannel, error) {
		channel, err := inline(ctx, data, events)
		time.Sleep(100 * time.Millisecond)
		return channel, err
	}
	result, err := sandbox.Execute(t.Context(), "const t = Date.now(); while (Date.now() - t < 300) {} return 3", ExecuteOptions{TimeoutMs: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || string(result.Value) != "3" {
		t.Fatalf("result = %+v (%+v), want ok 3: Pi delivers the segment's result before the expired deadline", result, result.Error)
	}
}
