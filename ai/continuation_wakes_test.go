package ai

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/trace"
	"slices"
	"strings"
	"testing"

	xtrace "golang.org/x/exp/trace"
)

// continuationHandOffWakes streams events from a provider turn to a consumer that iterates on another goroutine, as a provider response and the agent loop do, and returns the goroutine wake-ups, the raw Waiting->Runnable transitions of the run and why each wake happened. Each event costs the provider one extra await (suspendContinuation), so execution changes hands at least twice per event.
func continuationHandOffWakes(t *testing.T, events int) (wakes, unblocks int, why wakeReasons) {
	t.Helper()
	executor := &continuationExecutor{}
	stream := NewAssistantMessageEventStream()
	stream.executor = executor
	var buf bytes.Buffer
	if err := trace.Start(&buf); err != nil {
		t.Skipf("runtime/trace unavailable: %v", err)
	}
	producer := executor.newTurn()
	produced := make(chan struct{})
	go func() {
		defer close(produced)
		producer.run(func(turn *continuationTurn) {
			partial := pushTestStart(t, stream)
			for range events {
				if err := stream.Push(TextDeltaEvent{Delta: "x", Partial: partial}); err != nil {
					t.Error(err)
					return
				}
				suspendContinuation(turn)
			}
			pushTestDone(t, stream)
		})
	}()
	delivered := 0
	for range stream.Events(t.Context()) {
		delivered++
	}
	<-produced
	trace.Stop()
	if want := events + 2; delivered != want {
		t.Fatalf("consumer saw %d events, want %d", delivered, want)
	}
	return goroutineWakes(t, &buf)
}

// wakeReasons says what the wakes goroutineWakes counted were: each key is the block reason of the goroutine that was woken, where it was created and where it blocked, so a run that counts more wakes than the hand-offs need (the count covers every goroutine of the process, the runtime's included) names who woke.
type wakeReasons map[string]int

// String lists the most frequent reasons first.
func (reasons wakeReasons) String() string {
	keys := make([]string, 0, len(reasons))
	for key := range reasons {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int {
		if byCount := reasons[b] - reasons[a]; byCount != 0 {
			return byCount
		}
		return strings.Compare(a, b)
	})
	var out strings.Builder
	for i, key := range keys {
		if i == 8 {
			fmt.Fprintf(&out, "\n    ... %d more kinds", len(keys)-i)
			break
		}
		fmt.Fprintf(&out, "\n    %4d  %s", reasons[key], key)
	}
	if out.Len() == 0 {
		return "\n    none"
	}
	return out.String()
}

// stackSite is the innermost frame of stack outside the runtime and sync packages ("pkg.func file:line"), or the innermost frame when there is none.
func stackSite(stack xtrace.Stack) string {
	first, site := "", ""
	for frame := range stack.Frames() {
		where := fmt.Sprintf("%s %s:%d", frame.Func, frame.File[strings.LastIndexByte(frame.File, '/')+1:], frame.Line)
		if first == "" {
			first = where
		}
		if !strings.HasPrefix(frame.Func, "runtime.") && !strings.HasPrefix(frame.Func, "sync.") {
			site = where
			break
		}
	}
	if site == "" {
		site = first
	}
	if site == "" {
		site = "no stack"
	}
	return site
}

// goroutineWakes counts the goroutines a trace shows made runnable from waiting (unblocks counts every Waiting->Runnable transition, switches included). A wake is a hand-off through the scheduler (a channel send, a close, a mutex, netpoll or timer wake-up). A coroutine switch (iter.Pull) parses the same way, as three events at one timestamp (x/exp/trace order.go advanceGoSwitch): the unblock of the next goroutine, the switching goroutine blocking with no reason (a scheduler block always has one) or exiting, and the next goroutine starting. It never enters a run queue, wakes no P and costs about 30 ns, so it is not counted.
func goroutineWakes(t *testing.T, trace io.Reader) (wakes, unblocks int, why wakeReasons) {
	t.Helper()
	reader, err := xtrace.NewReader(trace)
	if err != nil {
		t.Fatal(err)
	}
	// candidate is an unblock that may open a switch; step counts the switch's events seen after it.
	var candidate struct {
		by, next xtrace.GoID
		time     xtrace.Time
		step     int
	}
	open := false
	why = wakeReasons{}
	created := map[xtrace.GoID]string{}
	blocked := map[xtrace.GoID]string{}
	// wake counts one wake of goroutine id and records why it was blocked.
	wake := func(id xtrace.GoID) {
		wakes++
		reason := blocked[id]
		if reason == "" {
			reason = "(not seen blocking)"
		}
		origin := created[id]
		if origin == "" {
			origin = "before the trace started"
		}
		why[fmt.Sprintf("%s; created at %s", reason, origin)]++
	}
	for {
		event, err := reader.ReadEvent()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind() != xtrace.EventStateTransition {
			continue
		}
		transition := event.StateTransition()
		if transition.Resource.Kind != xtrace.ResourceGoroutine {
			continue
		}
		from, to := transition.Goroutine()
		id := transition.Resource.Goroutine()
		switch {
		case from == xtrace.GoNotExist:
			created[id] = stackSite(transition.Stack)
		case to == xtrace.GoWaiting:
			blocked[id] = fmt.Sprintf("blocked on %q at %s", transition.Reason, stackSite(transition.Stack))
		}
		if open {
			// The reader keeps timestamps strictly ordered by bumping the three events' shared one a nanosecond each.
			sameTime := event.Time()-candidate.time <= 2
			switch {
			case candidate.step == 0 && sameTime && id == candidate.by && from == xtrace.GoRunning && (to == xtrace.GoWaiting && transition.Reason == "" || to == xtrace.GoNotExist):
				candidate.step = 1
				continue
			case candidate.step == 1 && sameTime && id == candidate.next && from == xtrace.GoRunnable && to == xtrace.GoRunning:
				open = false
				continue
			}
			wake(candidate.next)
			open = false
		}
		if from == xtrace.GoWaiting && to == xtrace.GoRunnable {
			unblocks++
			candidate.by, candidate.next, candidate.time, candidate.step = event.Goroutine(), id, event.Time(), 0
			open = true
		}
	}
	if open {
		wake(candidate.next)
	}
	return wakes, unblocks, why
}

// Handing execution from one turn to another is a coroutine switch: a provider that awaits between pushes and a consumer that iterates on another goroutine do not wake a goroutine per event. The channel baton at 4cfaf24153 woke one goroutine per event here, each wake 2-2.5 us of process CPU on a multi-core host. GOMAXPROCS=1 makes every hand-off find its receiver parked, so the count is the number of hops and does not depend on timing.
func TestContinuationHandOffWakesPerEvent(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const n = 500
	small, smallUnblocks, smallWhy := continuationHandOffWakes(t, n)
	large, largeUnblocks, largeWhy := continuationHandOffWakes(t, 2*n)
	perEvent := float64(large-small) / n
	t.Logf("wakes: %d at %d events, %d at %d events (%.2f per event); every unblock, coroutine switches included: %.2f per event", small, n, large, 2*n, perEvent, float64(largeUnblocks-smallUnblocks)/n)
	if perEvent > 0.05 {
		t.Fatalf("%.2f goroutine wakes per streamed event, want none; the wakes of each run by the block reason of the goroutine woken, where it was created and where it blocked (a hand-off between the producer and the consumer is a chan receive in serve; anything else woke a goroutine the hand-offs do not need):\n  %d events, %d wakes:%s\n  %d events, %d wakes:%s", perEvent, n, small, smallWhy, 2*n, large, largeWhy)
	}
}
