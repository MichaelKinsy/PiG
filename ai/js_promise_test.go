package ai

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

func waitForGoroutines(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > want {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want at most %d", runtime.NumGoroutine(), want)
		}
		runtime.Gosched()
	}
}

func assertJSExecutorDrained(t *testing.T, executor *continuationExecutor) {
	t.Helper()
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.ready != nil || executor.external != nil || executor.active != nil || executor.draining {
		t.Fatal("executor retained queue entries or execution ownership")
	}
}

// A disposed suspended generator releases its coroutine. Deferred code runs; a deferred await does not resume, because the JavaScript generator is unreachable and never continues.
func TestJSAsyncGeneratorDisposeReleasesCoroutine(t *testing.T) {
	baseline := runtime.NumGoroutine()
	executor := &continuationExecutor{}
	var deferred, resumed int
	executor.run(func(*continuationTurn) {
		for range 100 {
			generator := newJSAsyncGenerator(executor, func(b *jsGeneratorBody[int]) (int, error) {
				defer func() {
					deferred++
					_, _ = jsAwait(b, jsValue(1))
					resumed++
				}()
				return 0, b.yield(jsValue(1))
			})
			generator.next()
			generator.dispose()
			generator.dispose()
		}
	})
	waitForGoroutines(t, baseline)
	if deferred != 100 || resumed != 0 {
		t.Fatalf("deferred = %d, resumed = %d, want 100 and 0", deferred, resumed)
	}
	assertJSExecutorDrained(t, executor)
}

// A generator that never receives a request holds no goroutine, and disposing it is a no-op.
func TestJSAsyncGeneratorUnstartedHoldsNoCoroutine(t *testing.T) {
	baseline := runtime.NumGoroutine()
	executor := &continuationExecutor{}
	executor.run(func(*continuationTurn) {
		for range 100 {
			newJSAsyncGenerator(executor, func(*jsGeneratorBody[int]) (int, error) { return 0, nil }).dispose()
		}
	})
	if got := runtime.NumGoroutine(); got > baseline {
		t.Fatalf("goroutines = %d, want at most %d", got, baseline)
	}
}

func TestJSRunAsyncOutsideExecutorPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("starting an async function outside its executor did not panic")
		}
	}()
	runJSAsync(&continuationExecutor{}, func(*jsCoroutine) (jsOperand[int], error) { return jsValue(1), nil })
}

// A panic in a body reaches the goroutine that resumed it instead of being swallowed by the coroutine.
func TestJSCoroutinePanicPropagates(t *testing.T) {
	executor := &continuationExecutor{}
	var recovered any
	executor.run(func(*continuationTurn) {
		defer func() { recovered = recover() }()
		runJSAsync(executor, func(*jsCoroutine) (jsOperand[int], error) { panic("body failure") })
	})
	if recovered != "body failure" {
		t.Fatalf("recovered = %v", recovered)
	}
}

// A long generator chain completes on one executor with bounded queues and no retained coroutine.
func TestJSAsyncGeneratorStreamsManyValues(t *testing.T) {
	const count = 10000
	baseline := runtime.NumGoroutine()
	executor := &continuationExecutor{}
	generator := newJSAsyncGenerator(executor, func(b *jsGeneratorBody[int]) (int, error) {
		for i := 1; i <= count; i++ {
			if err := b.yield(jsValue(i)); err != nil {
				return 0, err
			}
		}
		return 0, nil
	})
	sum := 0
	executor.run(func(turn *continuationTurn) {
		for {
			result, err := jsAwait(turn, jsPromiseOperand(generator.next()))
			if err != nil {
				t.Error(err)
				return
			}
			if result.done {
				return
			}
			sum += result.value
		}
	})
	if want := count * (count + 1) / 2; sum != want {
		t.Fatalf("sum = %d, want %d", sum, want)
	}
	if generator.queue != nil || generator.state != jsGeneratorCompleted {
		t.Fatalf("generator retained requests or state %d", generator.state)
	}
	waitForGoroutines(t, baseline)
	assertJSExecutorDrained(t, executor)
}

func TestJSAsyncGeneratorRejectionSurfacesAsError(t *testing.T) {
	executor := &continuationExecutor{}
	failure := errors.New("provider failed")
	generator := newJSAsyncGenerator(executor, func(*jsGeneratorBody[int]) (int, error) { return 0, failure })
	var got error
	executor.run(func(turn *continuationTurn) {
		_, got = jsAwait(turn, jsPromiseOperand(generator.next()))
	})
	if !errors.Is(got, failure) {
		t.Fatalf("error = %v", got)
	}
}

func BenchmarkJSAsyncGeneratorYield(b *testing.B) {
	b.ReportAllocs()
	executor := &continuationExecutor{}
	generator := newJSAsyncGenerator(executor, func(body *jsGeneratorBody[int]) (int, error) {
		for i := range b.N {
			if err := body.yield(jsValue(i)); err != nil {
				return 0, err
			}
		}
		return 0, nil
	})
	executor.run(func(turn *continuationTurn) {
		for {
			result, _ := jsAwait(turn, jsPromiseOperand(generator.next()))
			if result.done {
				return
			}
		}
	})
}
