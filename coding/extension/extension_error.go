package extension

import "errors"

// ErrHandlerStopped marks a handler call the host itself cut short: it
// stopped the extension (shutdown, a termination signal, /reload) or
// cancelled the dispatch. The handler did not fail, so runners do not report
// it as an ExtensionError. Upstream never interrupts a running handler; its
// signal path disposes the runtime and exits without reporting the
// interrupted work.
var ErrHandlerStopped = errors.New("extension handler stopped by the host")

// ExtensionError is the structured error surfaced when a registered
// handler throws (TS) / returns a non-nil error (Go) during event
// dispatch. Hosts collect these without aborting the dispatch chain
// and surface them through `Runner.AddErrorListener`.
//
// upstream: types.ts:1537-1542
//
//	export interface ExtensionError {
//		extensionPath: string;
//		event: string;
//		error: string;
//		stack?: string;
//	}
//
// Wire format note. JSON tags match upstream camelCase exactly so the
// type round-trips cleanly through current and future extension transports.
type ExtensionError struct {
	// ExtensionPath is the resolved filesystem path of the extension
	// whose handler failed. Mirrors upstream `extensionPath`.
	ExtensionPath string `json:"extensionPath"`

	// Event is the event-type string the handler was registered for
	// (e.g. "tool_call", "session_start"). Mirrors upstream `event`.
	Event string `json:"event"`

	// Error is the human-readable message. In Go this is `err.Error()`;
	// upstream is `err.message`. Mirrors upstream `error`.
	Error string `json:"error"`

	// Stack is the optional stack of the failure itself, as upstream's
	// `err.stack`: the thrown error's stack for a subprocess extension, or
	// the panicking goroutine's stack for an in-process handler. It is empty
	// for a plain returned error, which carries no stack. See [ErrorStack].
	Stack string `json:"stack,omitempty"`
}

// StackError is an error that carries the stack of the failure it reports,
// such as a JavaScript error's `stack` or a recovered Go panic's stack.
type StackError interface {
	error
	ErrorStack() string
}

// ErrorStack returns the stack carried by err or an error it wraps, or "".
// Runners use it for [ExtensionError.Stack]: upstream reports the handler's
// `err.stack`, never the host's own dispatch stack.
func ErrorStack(err error) string {
	if carrier, ok := errors.AsType[StackError](err); ok {
		return carrier.ErrorStack()
	}
	return ""
}

// ErrorListener is the callback signature registered via
// Runner.AddErrorListener. Listeners run synchronously in the
// dispatch goroutine; long-running work should be moved to a
// dedicated goroutine by the listener itself.
//
// upstream: runner.ts:469 (private errorListeners: Set<ErrorListener>)
type ErrorListener = func(*ExtensionError)
