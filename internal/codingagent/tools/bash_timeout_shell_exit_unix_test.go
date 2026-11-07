//go:build !windows

package tools

import (
	"math"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/childwait"
)

// .upstream/main 06aa6563e packages/coding-agent/test/suite/regressions/11613-bash-timeout-after-shell-exit.test.ts
//
// The first case is about a writer that outlives the shell and keeps the pipe busy until the deadline, so the tool is still waiting when the timeout
// hits. Neither the 100 ms grace nor the deadline can be real clocks here: `sleep 0.02` in a loop is a fork and exec per line, and a macOS runner
// that runs the test beside the heavy ai package takes 50 to 100 ms or more per line (the failing runs' output holds two to four ticks in the shell's
// first 200 ms), so the writer's own gaps outrun the grace and the wait ends as a quiet pipe, which is the grace doing what Pi's does, not what this
// case examines. The grace never elapses (the writer is, by assumption, never quiet for 100 ms), and the deadline falls once the shell has exited,
// which is when childwait.Wait asks for its grace timer; the deadline falls 300 ms after that. Pi's pattern for the seconds is 0.x, which a loaded host can
// overrun, and a pattern that accepts any number would accept the deadline's time too: the test bounds the reported seconds by its own measurement of when
// the shell had exited instead, so a report of the deadline's time fails while load can only widen the margin.
func TestBashTimeoutAfterShellExit11613(t *testing.T) {
	t.Run("reports the shell's exit code when background processes kept writing until the timeout", func(t *testing.T) {
		var (
			fire     func()
			spawned  time.Time
			exitedBy time.Duration
		)
		testHookAfterShellStart = func() { spawned = time.Now() }
		testHookTimeoutTimer = func(time.Duration) (<-chan time.Time, func()) {
			timeout := make(chan time.Time, 1)
			fire = func() { timeout <- time.Now() }
			return timeout, func() {}
		}
		// The shell has exited when Wait asks for its grace timer. The deadline falls later, so the seconds reported must be the shell's run time
		// (at most exitedBy, which starts before and ends after the product's own measurement), not the deadline's. The grace never elapses.
		testHookStdioGraceTimer = func() childwait.IdleTimer {
			exitedBy = time.Since(spawned)
			time.AfterFunc(300*time.Millisecond, fire)
			return childwait.IdleTimer{Expired: make(chan time.Time), Rearm: func() {}, Stop: func() {}}
		}
		t.Cleanup(func() { testHookAfterShellStart, testHookTimeoutTimer, testHookStdioGraceTimer = nil, nil, nil })
		result := runFileTool(t, &BashTool{CWD: t.TempDir()}, t.Context(), map[string]any{
			"command": "(while true; do echo tick; sleep 0.02; done) & sleep 0.2; echo boom >&2; exit 3",
			"timeout": 1,
		})
		assertBashPortError(t, result, `boom[\s\S]*Command timed out after 1 seconds\. The shell exited with code 3 after \d+(?:\.\d)? seconds, but background processes it started kept writing output until the timeout\.$`)
		after, _ := strconv.ParseFloat(regexp.MustCompile(`after (\d+(?:\.\d)?) seconds, but`).FindStringSubmatch(result.Text())[1], 64)
		if bound := math.Round(float64(exitedBy.Milliseconds())/100) / 10; after > bound {
			t.Fatalf("reported the shell exited after %v seconds; it had exited %v seconds after the spawn", after, bound)
		}
	})
	t.Run("keeps the plain timeout message when the shell was still running at the deadline", func(t *testing.T) {
		// The deadline falls once the shell has printed, as in TestBashTimeoutKeepsTheOutputWrittenBeforeTheKill: a host too loaded to start the shell
		// within 0.3 s would time the command out before it wrote anything.
		var fire func()
		testHookTimeoutTimer = func(time.Duration) (<-chan time.Time, func()) {
			timeout := make(chan time.Time, 1)
			fire = func() { timeout <- time.Now() }
			return timeout, func() {}
		}
		t.Cleanup(func() { testHookTimeoutTimer = nil })
		tool := &BashTool{CWD: t.TempDir(), Operations: firstOutputOperations{BashOperations: CreateLocalBashOperations(nil), onFirstOutput: func() { fire() }}}
		result := runFileTool(t, tool, t.Context(), map[string]any{"command": "echo started; sleep 30", "timeout": 0.3})
		assertBashPortError(t, result, `started\s+Command timed out after 0\.3 seconds$`)
	})
}
