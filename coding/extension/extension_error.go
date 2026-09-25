package extension

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

	// Stack is the optional stack trace. In Go this is captured via
	// `debug.Stack()` at the dispatch site when a handler returns a
	// non-nil error. Empty when the runner was unable to capture one
	// (rare). Upstream uses `err.stack` which is also optional.
	Stack string `json:"stack,omitempty"`
}

// ErrorListener is the callback signature registered via
// Runner.AddErrorListener. Listeners run synchronously in the
// dispatch goroutine; long-running work should be moved to a
// dedicated goroutine by the listener itself.
//
// upstream: runner.ts:469 (private errorListeners: Set<ErrorListener>)
type ErrorListener = func(*ExtensionError)
