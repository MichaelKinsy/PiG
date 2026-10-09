package codemode_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/codemode"
)

// host.ts Execution.finish: `for (const pending of this.pending.values()) { if (pending.record) pending.record.durationMs =
// now - pending.startedAt; pending.controller.abort(); }`. A tool call still running when the execution ends keeps the
// "cancelled" status and records how long it ran, not the 0 it started with.
func TestACallCancelledByTheEndOfTheExecutionRecordsHowLongItRan(t *testing.T) {
	const ran = 60 * time.Millisecond
	started := make(chan struct{}, 1)
	hang := codemode.Tool{Name: "hang", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	sandbox := newSandbox(t, 10_000, hang)
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan codemode.Result, 1)
	go func() {
		result, _ := sandbox.Execute(ctx, "await tools.hang(); return 'never'", codemode.ExecuteOptions{})
		results <- result
	}()
	receive(t, started)
	time.Sleep(ran)
	cancel()
	result := receive(t, results)
	wantFailure(t, result, codemode.ErrorAborted)
	if len(result.Calls) != 1 || result.Calls[0].Name != "hang" || result.Calls[0].Status != codemode.CallCancelled {
		t.Fatalf("calls = %+v", result.Calls)
	}
	if got := result.Calls[0].DurationMs; got < float64(ran/time.Millisecond) {
		t.Fatalf("cancelled call durationMs = %v, want at least the %v it ran before the abort", got, ran)
	}
}

// host.ts Execution: `if (Number.isFinite(options.timeoutMs)) this.timer = setTimeout(...)`. NaN and -Infinity are not
// finite, so they arm no deadline, as +Infinity does not.
func TestRunsWithoutADeadlineWhenTimeoutIsNotFinite(t *testing.T) {
	wait := codemode.Tool{Name: "wait", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		select {
		case <-time.After(50 * time.Millisecond):
			return json.RawMessage(`"late"`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	sandbox := newSandbox(t, 10_000, wait)
	for _, timeoutMs := range []float64{math.NaN(), math.Inf(-1)} {
		result, err := sandbox.Execute(context.Background(), "return await tools.wait()", codemode.ExecuteOptions{TimeoutMs: timeoutMs})
		if err != nil {
			t.Fatal(err)
		}
		if !result.OK || string(result.Value) != `"late"` {
			t.Fatalf("timeoutMs %v: result = %+v, want the script's value without a deadline", timeoutMs, result)
		}
	}
}
