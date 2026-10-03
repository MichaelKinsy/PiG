// Test helpers: the Go form of a pending Promise.

package harness

// future is a running operation: the Go form of a pending Promise.
type future[T any] struct {
	done  chan struct{}
	value T
	err   error
}

func async[T any](run func() (T, error)) *future[T] {
	result := &future[T]{done: make(chan struct{})}
	go func() {
		defer close(result.done)
		result.value, result.err = run()
	}()
	return result
}

func asyncErr(run func() error) *future[struct{}] {
	return async(func() (struct{}, error) { return struct{}{}, run() })
}

func (result *future[T]) wait() (T, error) {
	<-result.done
	return result.value, result.err
}
