package runtime

import "sync/atomic"

// Cached is a native getter (HarnessOf, WorkerOf) that keeps its first success: the natives do not change once open, and the
// registry value behind them may be read only inside a section of the host, so the first call is made in one (a handler's
// synchronous part, an `OnLive` routine's) and later ones, off the loop, take the kept value.
func Cached[T any](get func() (*T, error)) func() (*T, error) {
	var kept atomic.Pointer[T]
	return func() (*T, error) {
		if value := kept.Load(); value != nil {
			return value, nil
		}
		value, err := get()
		if err == nil {
			kept.Store(value)
		}
		return value, err
	}
}
