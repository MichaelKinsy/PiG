package harness

import (
	"sync"

	"github.com/MichaelKinsy/PiG/internal/entered"
)

// promiseAll is Pi's `await Promise.all([call1(), call2(), ...])` in a test port: JavaScript evaluates the array in
// order, running each call synchronously up to its first await before it starts the next. Each call here starts on
// its own goroutine once the previous one entered (returned or waits, internal/entered), and promiseAll returns when
// every call returned.
func promiseAll(calls ...func()) {
	var wg sync.WaitGroup
	for _, call := range calls {
		wg.Add(1)
		wait := entered.Go(func() {
			defer wg.Done()
			call()
		})
		wait()
	}
	wg.Wait()
}

// callAsync is a call to one of Pi's async functions whose Promise the test awaits later: JavaScript runs the call
// synchronously up to its first await, so by the time it returns, a waitForTask() has taken its place on the Session line
// and no commit the test starts next can overtake it. call runs on its own goroutine, and callAsync returns once it has
// entered (returned or waits, internal/entered); the test collects what call produces itself.
func callAsync(call func()) {
	wait := entered.Go(call)
	wait()
}
