package extension

import "context"

// BashOperationsExecOptions mirrors the options upstream BashOperations.exec
// receives (core/tools/bash.ts). Cancellation is the context.
type BashOperationsExecOptions struct {
	// OnData receives raw output bytes (stdout and stderr) as they arrive,
	// from one goroutine at a time. The callee may keep the slice.
	OnData func(data []byte)
	// Timeout is the requested timeout in seconds; nil means none.
	Timeout *float64
	// Env replaces the inherited environment when non-nil.
	Env []string
}

// BashOperationsResult mirrors upstream { exitCode: number | null }: a signal
// termination reports 128 + the signal number, and nil is a failed command.
type BashOperationsResult struct {
	ExitCode *int
}

// BashOperations mirrors upstream BashOperations (core/tools/bash.ts):
// pluggable command execution, local by default and remote (for example SSH)
// when an extension supplies it, such as from a user_bash handler's
// { operations } result. Exec returns an error whose message is "aborted"
// when ctx was cancelled and "timeout:<seconds>" when the timeout fired, as
// upstream's callers match on those messages.
type BashOperations interface {
	Exec(ctx context.Context, command, cwd string, options BashOperationsExecOptions) (BashOperationsResult, error)
}
