package invocation

// LifecycleError preserves an invocation failure whose diagnostic belongs to the subprocess lifecycle handler. Callers still receive an error; the runner must not emit a second notification for the same failed connection.
type LifecycleError struct {
	Err error
}

func (e *LifecycleError) Error() string { return e.Err.Error() }
func (e *LifecycleError) Unwrap() error { return e.Err }
