package extension

import (
	"context"
	"testing"
)

// pig divergence (D631): the kinds of the command's standard streams. Node gives a command sockets for stdout and stderr (libuv's socket pair) and /dev/null for stdin
// (stdio "ignore", core/exec.ts:58-62); ExecCommand gives it pipes for stdout and stderr and the same /dev/null. Both sides are asserted against the record's table, so a
// change of either shows here. (That a pipe can be opened again through /dev/stdout and a socket cannot is TestExecCommandReopensItsOutputWherePiCannot.)
func TestExecCommandStdioKindsAgainstPi(t *testing.T) {
	script := `for fd in 0 1 2; do stat -L -c "$fd %F" /proc/self/fd/$fd; done; if test -p /dev/stdin; then echo fifo; elif test -S /dev/stdin; then echo socket; elif test -c /dev/stdin; then echo char; else echo other; fi`
	scenario := execScenario{Command: "sh", Args: []string{"-c", script}, CWD: t.TempDir()}
	pi := piExecOutcomes(t, []execScenario{scenario})[0]
	if want := (execOutcome{Stdout: "0 character special file\n1 socket\n2 socket\nchar\n"}); pi != want {
		t.Errorf("Pi = %+v, want %+v", pi, want)
	}
	pig := pigExecOutcome(context.Background(), scenario)
	if want := (execOutcome{Stdout: "0 character special file\n1 fifo\n2 fifo\nchar\n"}); pig != want {
		t.Errorf("PiG = %+v, want %+v", pig, want)
	}
}
