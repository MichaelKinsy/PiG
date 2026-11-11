package sdk

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func answerYield(t *testing.T, host *conn, errMessage string) envelope {
	t.Helper()
	call := nextLifetimeCall(t, host)
	msg := &callResultMsg{Result: json.RawMessage(`{}`)}
	if errMessage != "" {
		msg = &callResultMsg{Error: &errorInfo{Message: errMessage}}
	}
	if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: msg}); err != nil {
		t.Fatal(err)
	}
	return call
}

func TestContextYieldSendsYieldCallWithParentRequestID(t *testing.T) {
	_, _, host, ctx, finish := retainedContextHarness(t)
	defer finish()
	done := make(chan error, 1)
	go func() { done <- ctx.Yield() }()
	call := answerYield(t, host, "")
	if call.Call.Method != "yield" || call.Call.ParentRequestID != "origin" || string(call.Call.Args) != "{}" {
		t.Fatalf("yield call = %+v, want method yield, parent origin, args {}", call.Call)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestContextSleepYieldsBeforeItSleeps(t *testing.T) {
	_, _, host, ctx, finish := retainedContextHarness(t)
	defer finish()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- ctx.Sleep(150 * time.Millisecond) }()
	call := answerYield(t, host, "")
	answered := time.Since(start)
	if call.Call.Method != "yield" || call.Call.ParentRequestID != "origin" {
		t.Fatalf("sleep's first call = %+v, want yield with parent origin", call.Call)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 150*time.Millisecond || answered >= 150*time.Millisecond {
		t.Fatalf("sleep returned after %v (yield answered at %v), want yield first, then the 150ms sleep", time.Since(start), answered)
	}
}

func TestContextYieldAndSleepPropagateTheCallError(t *testing.T) {
	_, _, host, ctx, finish := retainedContextHarness(t)
	defer finish()
	for name, run := range map[string]func() error{
		"yield": ctx.Yield,
		"sleep": func() error { return ctx.Sleep(time.Hour) },
	} {
		done := make(chan error, 1)
		go func() { done <- run() }()
		answerYield(t, host, "host refused")
		if err := <-done; err == nil || !strings.Contains(err.Error(), "host refused") {
			t.Fatalf("%s error = %v, want the host's error without sleeping", name, err)
		}
	}
}
