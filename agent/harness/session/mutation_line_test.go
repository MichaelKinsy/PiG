package session_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

func TestMutationLineSerializesEveryMutation(t *testing.T) {
	var line session.MutationLine
	gate := make(chan struct{})
	started := make(chan struct{})
	var mu sync.Mutex
	var order []string
	record := func(step string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, step)
	}
	first := line.Enqueue(func() (any, error) {
		record("first:start")
		close(started)
		<-gate
		record("first:end")
		return "first", nil
	})
	second := line.Enqueue(func() (any, error) {
		record("second")
		return "second", nil
	})
	<-started
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	if !reflect.DeepEqual(order, []string{"first:start"}) {
		t.Fatalf("order before release = %v", order)
	}
	mu.Unlock()
	close(gate)
	if value, err := first.Wait(); value != "first" || err != nil {
		t.Fatalf("first = %v, %v", value, err)
	}
	if value, err := second.Wait(); value != "second" || err != nil {
		t.Fatalf("second = %v, %v", value, err)
	}
	if !reflect.DeepEqual(order, []string{"first:start", "first:end", "second"}) {
		t.Fatalf("order = %v", order)
	}
}

func TestMutationLineContinuesAfterFailedJobPreservingTheFailure(t *testing.T) {
	var line session.MutationLine
	rejection := errors.New("mutation failed")
	if _, err := line.Run(func() (any, error) { return nil, rejection }); !errors.Is(err, rejection) {
		t.Fatalf("err = %v", err)
	}
	if value, err := line.Run(func() (any, error) { return "next", nil }); value != "next" || err != nil {
		t.Fatalf("next = %v, %v", value, err)
	}
}

func TestMutationLineSealsQueuedAndFutureJobsWhileDrainingTheRunningJob(t *testing.T) {
	var line session.MutationLine
	gate := make(chan struct{})
	started := make(chan struct{})
	running := line.Enqueue(func() (any, error) {
		close(started)
		<-gate
		return "running", nil
	})
	<-started
	queuedRan := false
	queued := line.Enqueue(func() (any, error) {
		queuedRan = true
		return "queued", nil
	})
	closed := errors.New("closed")
	drained := line.Seal(closed)
	if _, err := line.Run(func() (any, error) { return "late", nil }); !errors.Is(err, closed) {
		t.Fatalf("late err = %v", err)
	}
	select {
	case <-drained:
		t.Fatal("drained before the running job settled")
	default:
	}
	close(gate)
	if value, err := running.Wait(); value != "running" || err != nil {
		t.Fatalf("running = %v, %v", value, err)
	}
	if _, err := queued.Wait(); !errors.Is(err, closed) || queuedRan {
		t.Fatalf("queued err = %v ran = %v", err, queuedRan)
	}
	<-drained
	if second := line.Seal(errors.New("second")); second == nil {
		t.Fatal("second seal returned no drain")
	}
	if _, err := line.Run(func() (any, error) { return nil, nil }); !errors.Is(err, closed) {
		t.Fatalf("first sealing error must win, got %v", err)
	}
}

func TestMutationLineSealOnIdleLineDrainsImmediately(t *testing.T) {
	var line session.MutationLine
	select {
	case <-line.Seal(errors.New("closed")):
	case <-time.After(time.Second):
		t.Fatal("idle seal did not drain")
	}
}
