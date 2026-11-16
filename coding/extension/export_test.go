package extension

import (
	"context"
	"os"

	"github.com/MichaelKinsy/PiG/internal/childwait"
)

// WithExecDrainHooks returns ctx carrying hooks for the ExecCommand call that
// receives it: a test decides when the post-exit grace elapses and sees what
// the call's readers have consumed.
func WithExecDrainHooks(ctx context.Context, hooks *childwait.Hooks) context.Context {
	return context.WithValue(ctx, execDrainHooksKey{}, hooks)
}

// WithExecBeforeSignal returns ctx carrying a function for the ExecCommand call that receives it: the call runs it once, just before it signals the
// command, with the read ends of the command's stdout and stderr.
func WithExecBeforeSignal(ctx context.Context, beforeSignal func(stdout, stderr *os.File)) context.Context {
	return context.WithValue(ctx, execBeforeSignalKey{}, beforeSignal)
}

// ExecForceKillDelay is core/exec.ts FORCE_KILL_DELAY_MS.
const ExecForceKillDelay = execForceKillDelay

// SetExecMaxStringLength replaces the runtime string limit (buffer.constants.MAX_STRING_LENGTH) the way exec-runtime-limit.test.ts mocks it, and returns the restore.
func SetExecMaxStringLength(n int64) (restore func()) {
	previous := execMaxStringLength
	execMaxStringLength = n
	return func() { execMaxStringLength = previous }
}

// ExecDefaultMaxBuffer is the cap a call without maxBuffer gets.
func ExecDefaultMaxBuffer() int64 { return execDefaultMaxBuffer() }
