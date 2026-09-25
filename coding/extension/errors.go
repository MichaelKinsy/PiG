package extension

import "errors"

// Sentinel errors surfaced by extension hosts (the inproc dispatch runner
// and subprocess transport adapter) to extensions. Authors compare with
// errors.Is.
var (
	// ErrStaleContext is returned by API calls made through an
	// ExtensionContext whose runner has been replaced (typically after
	// /reload or a session swap). Mirrors upstream's invalidate() sentinel
	// in core/extensions/runner.ts.
	ErrStaleContext = errors.New("extension: context is stale (runner replaced)")

	// ErrBusy is returned when an action is rejected because the agent is
	// streaming or compacting. Mirrors upstream's reload guard in
	// modes/interactive/interactive-mode.ts handleReloadCommand.
	ErrBusy = errors.New("extension: agent is busy (streaming or compacting)")

	// ErrCapabilityNotDeclared is returned when an extension calls an API
	// surface it didn't declare in its manifest capabilities list.
	ErrCapabilityNotDeclared = errors.New("extension: capability not declared in manifest")

	// ErrUnknownEvent is returned when a host attempts to dispatch an event
	// name that no registered handler understands. Hosts may treat this as
	// an info-level no-op; it exists for tests and diagnostics.
	ErrUnknownEvent = errors.New("extension: unknown event")
)
