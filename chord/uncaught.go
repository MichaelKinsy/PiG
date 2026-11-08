package chord

import (
	"fmt"
	"os"
	"sync/atomic"
)

// AggregateError is JavaScript's AggregateError(errors, message): Error() is the message alone and the causes are
// reachable through Unwrap.
type AggregateError struct {
	Message string
	Errors  []error
}

func (err *AggregateError) Error() string   { return err.Message }
func (err *AggregateError) Unwrap() []error { return err.Errors }

// NewAggregateError builds an AggregateError over failures in order.
func NewAggregateError(message string, failures ...error) *AggregateError {
	return &AggregateError{Message: message, Errors: failures}
}

var uncaughtReporter atomic.Pointer[func(error)]

// exitOnUncaught is the default receiver of unhandled failures: it prints the failure to standard error and exits with
// status 1, as Node does for an uncaught exception.
var exitOnUncaught = func(err error) {
	fmt.Fprintf(os.Stderr, "%s\n", err)
	os.Exit(1)
}

func init() {
	report := func(err error) {
		//portlint:allow golifetime the goroutine ends the process, as an uncaught throw does; nothing outlives it
		go exitOnUncaught(err) //portlint:allow asyncorder the uncaught throw comes after the reporting call and ends the process; nothing is ordered after it
	}
	uncaughtReporter.Store(&report)
}

// SetUncaughtErrorReporter replaces the receiver of failures nobody handled: the Go form of the process's
// uncaught-exception handler. The default ends the process as an unhandled throw does, from its own goroutine so the
// reporting call is not interrupted. It returns the previous receiver and is safe to call concurrently.
func SetUncaughtErrorReporter(report func(error)) (previous func(error)) {
	return *uncaughtReporter.Swap(&report)
}
