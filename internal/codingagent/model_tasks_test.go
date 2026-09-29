package codingagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestModelTasksRejectEarlyAndDrainOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := &ModelRegistry{}
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		releaseAll := func() { once.Do(func() { close(release) }) }
		t.Cleanup(func() { releaseAll(); registry.CloseModelTasks() })
		failure := errors.New("first failure")
		var sibling context.Context
		done := make(chan error, 1)
		go func() {
			done <- registry.AwaitModelTasks(t.Context(),
				func(context.Context) error { <-entered; return failure },
				func(ctx context.Context) error {
					sibling = ctx
					close(entered)
					<-release
					return errors.New("late failure")
				},
			)
		}()
		<-entered
		synctest.Wait()
		select {
		case err := <-done:
			if !errors.Is(err, failure) {
				t.Fatalf("first result=%v", err)
			}
		default:
			t.Fatal("first rejection waited for its sibling")
		}
		if sibling.Err() != nil {
			t.Fatal("rejection cancelled an unrelated Promise sibling")
		}
		closed := make(chan struct{})
		go func() { registry.CloseModelTasks(); close(closed) }()
		synctest.Wait()
		if !errors.Is(sibling.Err(), context.Canceled) {
			t.Fatal("owner close did not cancel admitted work")
		}
		select {
		case <-closed:
			t.Fatal("owner close returned before its non-cooperative sibling drained")
		default:
		}
		releaseAll()
		<-closed
		if registry.StartModelTask(context.Background(), func(context.Context) { t.Error("closed owner ran work") }) {
			t.Fatal("closed owner accepted background work")
		}
		if err := registry.AwaitModelTasks(t.Context(), func(context.Context) error { t.Error("closed owner ran foreground work"); return nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("closed owner result=%v", err)
		}
		registry.CloseModelTasks()
	})
}

func TestModelTaskContextsShareCancellationNotRequestValues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type requestKey struct{}
		registry := &ModelRegistry{}
		defer registry.CloseModelTasks()
		parent, cancel := context.WithCancelCause(t.Context())
		first := registry.ModelTaskContext(context.WithValue(parent, requestKey{}, "first"))
		second := registry.ModelTaskContext(context.WithValue(parent, requestKey{}, "second"))
		empty := registry.ModelTaskContext(context.WithValue(parent, requestKey{}, nil))
		if first.Done() != second.Done() || first.Value(requestKey{}) != "first" || second.Value(requestKey{}) != "second" || empty.Value(requestKey{}) != nil {
			t.Fatal("cancellation sharing leaked request values")
		}
		for range 100 {
			if err := registry.AwaitModelTasks(parent, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
		registry.modelTasks.mu.Lock()
		count := len(registry.modelTasks.bindings)
		registry.modelTasks.mu.Unlock()
		if count != 1 {
			t.Fatalf("one live parent signal retained %d cancellation bindings", count)
		}
		if first.Err() != nil {
			t.Fatal("settling work cancelled a retained observer")
		}
		cause := errors.New("caller cancelled")
		cancel(cause)
		<-first.Done()
		if !errors.Is(context.Cause(first), cause) || !errors.Is(context.Cause(second), cause) {
			t.Fatalf("caller cancellation cause lost: %v / %v", context.Cause(first), context.Cause(second))
		}
		synctest.Wait()
		registry.modelTasks.mu.Lock()
		count = len(registry.modelTasks.bindings)
		registry.modelTasks.mu.Unlock()
		if count != 0 {
			t.Fatal("completed parent retained a cancellation binding")
		}
	})
}

func TestModelTaskContextCannotBypassOwnerAfterWithoutCancel(t *testing.T) {
	registry := &ModelRegistry{}
	original := registry.ModelTaskContext(context.Background())
	detached, cancel := context.WithCancel(context.WithoutCancel(original))
	defer cancel()
	rebound := registry.ModelTaskContext(detached)
	registry.CloseModelTasks()
	if detached.Err() != nil {
		t.Fatal("closing Services cancelled a caller-owned context")
	}
	if !errors.Is(rebound.Err(), context.Canceled) {
		t.Fatal("inherited owner marker bypassed cancellation after WithoutCancel")
	}
}

func TestModelTaskRetainedSignalHonorsDeadlineAndOwnerAfterSettlement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := &ModelRegistry{}
		parent, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		var retained context.Context
		if err := registry.AwaitModelTasks(parent, func(ctx context.Context) error { retained = ctx; return nil }); err != nil {
			t.Fatal(err)
		}
		if retained.Err() != nil {
			t.Fatal("settlement cancelled retained signal")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if !errors.Is(retained.Err(), context.DeadlineExceeded) || !errors.Is(context.Cause(retained), context.DeadlineExceeded) {
			t.Fatalf("deadline lost: %v / %v", retained.Err(), context.Cause(retained))
		}
		background := registry.ModelTaskContext(context.Background())
		registry.CloseModelTasks()
		if !errors.Is(background.Err(), context.Canceled) {
			t.Fatal("owner close failed to cancel retained background signal")
		}
	})
}
