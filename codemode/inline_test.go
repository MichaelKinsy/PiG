package codemode_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/codemode"
)

// Ports packages/codemode/test/portable.test.ts, describe("InlineCodemodeSandbox") (Pi 1.2.0), with its scripts and
// expectations. Go mechanics: timeoutMs Infinity is math.Inf(1), and the wasm option is the embedded module.

func newInlineSandbox(t *testing.T, options codemode.InlineSandboxOptions) *codemode.InlineSandbox {
	t.Helper()
	sandbox, err := codemode.NewInlineSandbox(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	return sandbox
}

// Pi 1.2.0 packages/codemode/test/portable.test.ts:37 describe("InlineCodemodeSandbox") "fails a script that computes past its interrupt budget as a timeout"
func TestInlineSandboxFailsAScriptThatComputesPastItsInterruptBudgetAsATimeout(t *testing.T) {
	sandbox := newInlineSandbox(t, codemode.InlineSandboxOptions{InterruptBudget: 1_000})
	for _, code := range []string{"while (true) {}", "while (true) await null"} {
		result := run(t, sandbox.Sandbox, code, codemode.ExecuteOptions{TimeoutMs: math.Inf(1)})
		if e := wantFailure(t, result, codemode.ErrorTimeout); e.Message != "Execution exceeded its interrupt budget of 1000" {
			t.Errorf("%s: message = %q", code, e.Message)
		}
	}
}

// inline.ts DEFAULT_INTERRUPT_BUDGET is 100_000 and an inline sandbox given no interruptBudget uses it: the timeout message
// names the budget that ran out, so a changed default shows in what a caller sees.
func TestInlineSandboxWithoutABudgetUsesTheDefaultOf100000(t *testing.T) {
	if codemode.DefaultInterruptBudget != 100_000 {
		t.Errorf("DefaultInterruptBudget = %d, want 100000", codemode.DefaultInterruptBudget)
	}
	sandbox := newInlineSandbox(t, codemode.InlineSandboxOptions{})
	result := run(t, sandbox.Sandbox, "while (true) {}", codemode.ExecuteOptions{TimeoutMs: math.Inf(1)})
	if e := wantFailure(t, result, codemode.ErrorTimeout); e.Message != "Execution exceeded its interrupt budget of 100000" {
		t.Errorf("message = %q", e.Message)
	}
}

// Pi 1.2.0 packages/codemode/test/portable.test.ts:48 describe("InlineCodemodeSandbox") "counts the budget across waits on tools"
func TestInlineSandboxCountsTheBudgetAcrossWaitsOnTools(t *testing.T) {
	tick := codemode.Tool{Name: "tick", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) { return js("1"), nil }}
	sandbox := newInlineSandbox(t, codemode.InlineSandboxOptions{InterruptBudget: 1_000, SandboxOptions: codemode.SandboxOptions{Tools: []codemode.Tool{tick}}})
	result := run(t, sandbox.Sandbox, "for (;;) { for (let i = 0; i < 1e5; i++); await tools.tick(); }")
	wantFailure(t, result, codemode.ErrorTimeout)
	if len(result.Calls) <= 1 {
		t.Errorf("calls = %d, want more than one: the budget must carry across tool waits", len(result.Calls))
	}
}

// The deadline of an in-thread VM is checked between the turns of the event loop, not while a turn computes. Pi's host arms the
// deadline before the launcher runs, and Node runs a timer only between tasks, so a segment that computes past the deadline
// still delivers its result first; probed against live Pi ea448f45 (InlineCodemodeSandbox, interruptBudget 10_000_000):
//   - spin 250 ms, wait 200 ms on a tool, deadline 300 ms: timeout (the first segment counts toward the deadline);
//   - wait 10 ms, spin 500 ms, deadline 300 ms: ok (the deadline fires after the segment delivered its result);
//   - spin 500 ms, deadline 300 ms: ok.
//
// The durations are scaled so that a loaded host cannot move a deadline across a segment boundary. spin is a busy loop on the
// wall clock.
func TestInlineSandboxChecksTheDeadlineBetweenTurnsLikePi(t *testing.T) {
	const spin = "const spin = (ms) => { const t = Date.now(); while (Date.now() - t < ms) {} };"
	wait := codemode.Tool{Name: "wait", Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		ms, _ := strconv.Atoi(string(args))
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return args, nil
	}}
	for _, tc := range []struct {
		name, code string
		timeoutMs  float64
		wantOK     bool
		wantValue  string
	}{
		{"the first segment counts toward the deadline", spin + "spin(400); await tools.wait(400); return 1", 600, false, ""},
		{"a deadline that expires while a later segment computes waits for its result", spin + "await tools.wait(10); spin(800); return 2", 400, true, "2"},
		{"a deadline that expires during the first segment waits for its result", spin + "spin(800); return 3", 400, true, "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sandbox := newInlineSandbox(t, codemode.InlineSandboxOptions{InterruptBudget: 10_000_000, SandboxOptions: codemode.SandboxOptions{Tools: []codemode.Tool{wait}}})
			wantOK(t, run(t, sandbox.Sandbox, "return 0"), "0")
			result := run(t, sandbox.Sandbox, tc.code, codemode.ExecuteOptions{TimeoutMs: tc.timeoutMs})
			if tc.wantOK {
				wantOK(t, result, tc.wantValue)
				return
			}
			wantFailure(t, result, codemode.ErrorTimeout)
		})
	}
}

// The worker backend preempts a computing script on all three shapes, as Pi's does.
func TestWorkerSandboxPreemptsAComputingScriptOnTheDeadline(t *testing.T) {
	const spin = "const spin = (ms) => { const t = Date.now(); while (Date.now() - t < ms) {} };"
	sandbox := newSandbox(t, 10_000)
	wantOK(t, run(t, sandbox, "return 0"), "0")
	for _, code := range []string{spin + "spin(800); return 3", spin + "await null; spin(800); return 2"} {
		wantFailure(t, run(t, sandbox, code, codemode.ExecuteOptions{TimeoutMs: 400}), codemode.ErrorTimeout)
	}
}

// sandbox.test.ts:829-836 "reports a failing wasm module as a sandbox error", on the inline sandbox (InlineCodemodeSandboxOptions.wasm,
// packages/codemode/src/runtime/inline.ts:49): the loader supplies the module the VM runs on, and a loader that fails is the
// sandbox error Failed to load QuickJS.
func TestInlineSandboxUsesItsWasmLoader(t *testing.T) {
	loads := 0
	ok := newInlineSandbox(t, codemode.InlineSandboxOptions{SandboxOptions: codemode.SandboxOptions{Wasm: func() ([]byte, error) { loads++; return codemode.QuickJSWasm(), nil }}})
	result, err := ok.Execute(t.Context(), "return 1 + 1", codemode.ExecuteOptions{})
	if err != nil || !result.OK || string(result.Value) != "2" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if loads != 1 {
		t.Fatalf("the loader ran %d times, want 1", loads)
	}
	failing := newInlineSandbox(t, codemode.InlineSandboxOptions{SandboxOptions: codemode.SandboxOptions{Wasm: func() ([]byte, error) { return nil, errors.New("no wasm") }}})
	result, err = failing.Execute(t.Context(), "return 1", codemode.ExecuteOptions{})
	if err != nil || result.OK || result.Error == nil || result.Error.Kind != codemode.ErrorSandbox || result.Error.Message != "Failed to load QuickJS: no wasm" {
		t.Fatalf("result = %+v (%+v), err = %v", result, result.Error, err)
	}
}

// The tool table and the lifecycle of CodemodeSandboxBase (packages/codemode/src/runtime/execution.ts:363-370) are what InlineCodemodeSandbox
// (packages/codemode/src/runtime/inline.ts:121) inherits: tools registered after construction are the script's tools.<name>, an unregistered
// one is gone, and close() rejects later executions.
func TestInlineSandboxHasTheToolTableOfTheBase(t *testing.T) {
	sandbox := newInlineSandbox(t, codemode.InlineSandboxOptions{})
	echo := codemode.Tool{Name: "echo", Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) { return args, nil }}
	// The promoted methods are those of the InlineSandbox type itself, as the base class members are those of the subclass.
	if err := (*codemode.InlineSandbox).RegisterTool(sandbox, echo); err != nil {
		t.Fatal(err)
	}
	if tools := sandbox.Tools(); len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	result, err := sandbox.Execute(t.Context(), "return await tools.echo({ n: 1 })", codemode.ExecuteOptions{})
	if err != nil || !result.OK || string(result.Value) != `{"n":1}` {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if !sandbox.UnregisterTool("echo") || len(sandbox.Tools()) != 0 {
		t.Fatal("echo was not unregistered")
	}
	result, err = sandbox.Execute(t.Context(), "return await tools.echo({})", codemode.ExecuteOptions{})
	if err != nil || result.OK || result.Error == nil || result.Error.Kind != codemode.ErrorScript || !strings.Contains(result.Error.Message, "echo") {
		t.Fatalf("after unregister: %+v (%+v), err = %v", result, result.Error, err)
	}
	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
	// execution.ts:416: execute() on a closed sandbox rejects with Error("Sandbox is closed").
	if result, err = sandbox.Execute(t.Context(), "return 1", codemode.ExecuteOptions{}); err == nil || err.Error() != "Sandbox is closed" {
		t.Fatalf("a closed sandbox answered %+v, err = %v, want the error Sandbox is closed", result, err)
	}
}
