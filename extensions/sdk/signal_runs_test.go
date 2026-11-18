package sdk

import (
	"context"
	"testing"
)

type uncomparableSignal struct {
	context.Context
	_ []int
}

func runOf(t *testing.T, answer any) (run uint64, aborted, ended bool) {
	t.Helper()
	m, ok := answer.(map[string]any)
	if !ok {
		t.Fatalf("answer = %#v, want an object", answer)
	}
	run, _ = m["run"].(uint64)
	aborted, _ = m["aborted"].(bool)
	ended, _ = m["endedAborted"].(bool)
	return run, aborted, ended
}

// The wire answer names the run by the signal object (Pi: one AbortController per run): one token for one object however often it is read, a new token for another object, endedAborted when the object that was answered last had aborted.
func TestSignalRunsTokenFollowsTheSignalObject(t *testing.T) {
	var runs signalRuns
	var none context.Context
	if got := runs.wire(none); got != nil {
		t.Fatalf("no signal: %#v, want nil", got)
	}
	first, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	run1, aborted, ended := runOf(t, runs.wire(first))
	if run1 == 0 || aborted || ended {
		t.Fatalf("run 1 = %d aborted %v ended %v", run1, aborted, ended)
	}
	for range 100 {
		if run, _, _ := runOf(t, runs.wire(first)); run != run1 {
			t.Fatalf("the same signal answered token %d, want %d", run, run1)
		}
	}
	second, cancelSecond := context.WithCancel(t.Context())
	defer cancelSecond()
	run2, aborted, ended := runOf(t, runs.wire(second))
	if run2 == run1 || aborted || ended {
		t.Fatalf("run 2 = %d (run 1 %d) aborted %v ended %v, want a new token, live, ended live", run2, run1, aborted, ended)
	}
	cancelSecond()
	if run, aborted, _ := runOf(t, runs.wire(second)); run != run2 || !aborted {
		t.Fatalf("run 2 aborted = %d aborted %v", run, aborted)
	}
	third, cancelThird := context.WithCancel(t.Context())
	defer cancelThird()
	run3, aborted, ended := runOf(t, runs.wire(third))
	if run3 == run2 || aborted || !ended {
		t.Fatalf("run 3 = %d aborted %v ended %v, want a new token, live, ended aborted", run3, aborted, ended)
	}
	// Another function of the process never shares a token.
	var other signalRuns
	if run, _, _ := runOf(t, other.wire(first)); run == run1 || run == run2 || run == run3 {
		t.Fatalf("another getSignal function answered %d, a token of this one", run)
	}
}

// A signal of a type that cannot be compared has no identity: each answer is a new run, and the comparison does not panic.
func TestSignalRunsUncomparableSignalIsANewRunEachTime(t *testing.T) {
	var runs signalRuns
	signal := uncomparableSignal{Context: t.Context()}
	a, _, _ := runOf(t, runs.wire(signal))
	b, _, _ := runOf(t, runs.wire(signal))
	if a == b {
		t.Fatalf("an uncomparable signal answered the token %d twice", a)
	}
}

var answerSink any

// The answer is built on every read, so the token is boxed once per run and not once per answer: a read of a held run allocates what the map of its members costs, and no box for the token.
func TestSignalRunsBoxTheTokenOncePerRun(t *testing.T) {
	var runs signalRuns
	signalRunCounter.Add(1000) // past the small integers Go boxes without allocating
	signal, cancel := context.WithCancel(t.Context())
	defer cancel()
	runs.wire(signal)
	box := any(uint64(1000))
	want := testing.AllocsPerRun(100, func() { answerSink = map[string]any{"aborted": false, "run": box} })
	got := testing.AllocsPerRun(100, func() { answerSink = runs.wire(signal) })
	if got > want {
		t.Fatalf("a read of a held run allocates %v times, the answer's map alone %v: the token is boxed again on every read", got, want)
	}
}
