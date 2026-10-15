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
