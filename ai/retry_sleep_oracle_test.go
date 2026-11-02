package ai

// pi: packages/coding-agent/src/utils/sleep.ts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type codingSleepProbe struct {
	MS           int    `json:"ms"`
	AbortBefore  bool   `json:"abortBefore,omitempty"`
	AbortAfterMs *int   `json:"abortAfterMs,omitempty"`
	Label        string `json:"label"`
}

// The retry backoff of the coding agent (agent-session.ts awaits utils/sleep.ts) against Pi's compiled sleep: it rejects with Error("Aborted") at once on an aborted
// signal (a zero delay included), rejects the same way when aborted during the wait, resolves after the delay, and waits one millisecond for a delay below 1 or above
// 2^31-1 as setTimeout does, so a late abort of such a wait finds it already resolved.
func TestRetrySleepMatchesPiSleep(t *testing.T) {
	after := func(ms int) *int { return &ms }
	probes := []codingSleepProbe{
		{MS: 0, Label: "zero delay"},
		{MS: 5, Label: "short delay"},
		{MS: -5, Label: "negative delay"},
		{MS: 1e10, Label: "delay above 2^31-1"},
		{MS: 0, AbortBefore: true, Label: "zero delay on an aborted signal"},
		{MS: 1000, AbortBefore: true, Label: "aborted before the wait"},
		{MS: 1000, AbortAfterMs: after(10), Label: "aborted during the wait"},
		{MS: 1e10, AbortAfterMs: after(500), Label: "abort after an overflowing delay already resolved"},
	}
	var want []string
	pioracle.Run(t, `
const { sleep } = await load("pi-coding-agent/utils/sleep.js");
const outcomes = [];
for (const probe of input) {
  const controller = new AbortController();
  if (probe.abortBefore) controller.abort();
  const started = sleep(probe.ms, controller.signal);
  if (probe.abortAfterMs !== undefined) setTimeout(() => controller.abort(), probe.abortAfterMs);
  outcomes.push(await started.then(() => "resolved", (reason) => "rejected:" + String(reason)));
}
emit(outcomes);`, probes, &want)

	for i, probe := range probes {
		ctx, cancel := context.WithCancel(context.Background())
		if probe.AbortBefore {
			cancel()
		}
		if probe.AbortAfterMs != nil {
			time.AfterFunc(time.Duration(*probe.AbortAfterMs)*time.Millisecond, cancel)
		}
		settled := make(chan error, 1)
		go func() { settled <- sleepContext(ctx, retryTimerDuration(probe.MS)) }()
		got := "still sleeping after 3s"
		select {
		case err := <-settled:
			got = "resolved"
			if err != nil {
				got = "rejected:Error: " + err.Error()
			}
		case <-time.After(3 * time.Second):
		}
		cancel()
		if got != want[i] {
			t.Errorf("%s: %+v = %q, Pi %q", probe.Label, probe, got, want[i])
		}
	}
	if !errors.Is(sleepContext(canceledContext(), time.Hour), errRetrySleepAborted) {
		t.Error("an aborted signal does not reject with the Aborted error")
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
