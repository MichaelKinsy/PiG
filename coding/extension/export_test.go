package extension

import (
	"context"

	"github.com/MichaelKinsy/PiG/internal/childwait"
)

// WithExecDrainHooks returns ctx carrying hooks for the ExecCommand call that
// receives it: a test decides when the post-exit grace elapses and sees what
// the call's readers have consumed.
func WithExecDrainHooks(ctx context.Context, hooks *childwait.Hooks) context.Context {
	return context.WithValue(ctx, execDrainHooksKey{}, hooks)
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
