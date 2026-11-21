package extension_test

// pi: packages/coding-agent/src/core/exec.ts

import (
	"context"
	"os"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// What exec.ts does with the command's output pipes when it signals the command, by what asks for the signal:
//
//   - The chunk that crosses maxBuffer: proc.stdout.destroy(), proc.stderr.destroy() and then killProcess() (exec.ts:111-113). Node's destroy() closes
//     the descriptor before it returns, so the command's next write meets a closed pipe before the SIGTERM reaches it.
//   - A timeout or an abort: killProcess() alone (exec.ts:88-98). The pipes stay open until the command has exited and its output has ended or fallen
//     idle (waitForChildProcess), so a command's handler for the SIGTERM can still write what it has to say.
//
// The code a cut-off command reports (141 for the SIGPIPE it meets, 143 for the SIGTERM) shows the order only as a share of runs, which
// TestExecCommandMatchesPi compares with Pi's. These tests ask the call itself, once per run: ExecCommand tells a test the state of the read ends at
// the moment it signals (extension.WithExecBeforeSignal), which no command's scheduling can change.

// signalSight is what one call's read ends were when it signalled the command.
type signalSight struct {
	mu                         sync.Mutex
	calls                      int
	stdoutClosed, stderrClosed bool
}

// closedFile reports that f was closed. RawControl takes a reference to the descriptor without waiting for a reader, and fails only on a closed file:
// Stat does not tell a closed file on Windows, and a read of it would wait for the reader of the pipe.
func closedFile(f *os.File) bool {
	conn, err := f.SyscallConn()
	return err != nil || conn.Control(func(uintptr) {}) != nil
}

// execFloodSeenAtTheSignal runs the `flood` helper, which writes on both pipes until it is ended, through ExecCommand with opts, and returns the result
// and what the call's read ends were at its signals.
func execFloodSeenAtTheSignal(ctx context.Context, t *testing.T, opts *extension.ExecOptions) (extension.ExecResult, *signalSight) {
	t.Helper()
	sight := &signalSight{}
	ctx = extension.WithExecBeforeSignal(ctx, func(stdout, stderr *os.File) {
		sight.mu.Lock()
		defer sight.mu.Unlock()
		sight.calls++
		sight.stdoutClosed, sight.stderrClosed = closedFile(stdout), closedFile(stderr)
	})
	command, args := helper(t, "flood")
	result, err := extension.ExecCommand(ctx, t.TempDir(), command, args, opts)
	if err != nil {
		t.Fatal(err)
	}
	sight.mu.Lock()
	defer sight.mu.Unlock()
	return result, sight
}

// exec.ts:111-113: the chunk that crosses the cap destroys both pipes and then signals.
func TestExecCommandClosesBothPipesBeforeItSignalsAtTheCap(t *testing.T) {
	result, sight := execFloodSeenAtTheSignal(testbudget.Context(t), t, maxBuffer(10))
	if !result.Truncated || !result.Killed {
		t.Fatalf("result = %+v, want truncated and killed", result)
	}
	wantClosed := runtime.GOOS != "windows"
	if sight.calls != 1 || sight.stdoutClosed != wantClosed || sight.stderrClosed != wantClosed {
		t.Errorf("the call signalled %d times, with stdout closed %v and stderr closed %v, want one signal with both closed=%v", sight.calls, sight.stdoutClosed, sight.stderrClosed, wantClosed)
	}
}

// exec.ts:96-98: the timer calls killProcess and nothing else; the pipes close when the command has exited and its output has ended.
func TestExecCommandSignalsWithBothPipesOpenOnATimeout(t *testing.T) {
	result, sight := execFloodSeenAtTheSignal(testbudget.Context(t), t, &extension.ExecOptions{Timeout: 1})
	if result.Truncated || !result.Killed {
		t.Fatalf("result = %+v, want killed and not truncated", result)
	}
	if sight.calls != 1 || sight.stdoutClosed || sight.stderrClosed {
		t.Errorf("the call signalled %d times, with stdout closed %v and stderr closed %v, want one signal with both open", sight.calls, sight.stdoutClosed, sight.stderrClosed)
	}
}

// exec.ts:88-94: an aborted signal calls killProcess and nothing else.
func TestExecCommandSignalsWithBothPipesOpenOnAnAbort(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	abort()
	result, sight := execFloodSeenAtTheSignal(testbudget.Context(t), t, &extension.ExecOptions{Signal: signal})
	if result.Truncated || !result.Killed {
		t.Fatalf("result = %+v, want killed and not truncated", result)
	}
	if sight.calls != 1 || sight.stdoutClosed || sight.stderrClosed {
		t.Errorf("the call signalled %d times, with stdout closed %v and stderr closed %v, want one signal with both open", sight.calls, sight.stdoutClosed, sight.stderrClosed)
	}
}
