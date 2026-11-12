//go:build unix

package extension_test

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Regression tests for Pi issue #12088 (core/exec.ts, a83b75a07): a command that a signal ended has no exit code, and Pi reports 128 + the
// signal number, as a shell does, so a timeout, an abort or a crash does not read as success. Pi's tests (test/exec.test.ts, "signal-terminated
// commands") are skipped on Windows; the Windows rule is TestExecCommand_Timeout and TestExecCommandTimeoutSignalsOnlyTheLeader, where libuv
// reports the signal a kill sent.

func TestExecCommandReportsSIGTERMForATimeout(t *testing.T) {
	command, args := helper(t, "sleep", "10000")
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), command, args, &extension.ExecOptions{Timeout: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 143 || !result.Killed || result.Truncated {
		t.Fatalf("result = %+v, want code 143, killed, not truncated", result)
	}
}

func TestExecCommandReportsSIGTERMForAnAbort(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	defer abort()
	time.AfterFunc(100*time.Millisecond, abort)
	command, args := helper(t, "sleep", "10000")
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), command, args, &extension.ExecOptions{Signal: signal})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 143 || !result.Killed || result.Truncated {
		t.Fatalf("result = %+v, want code 143, killed, not truncated", result)
	}
}

// Not killed by pi, so killed is false and only the code tells the command apart from a success. Pi's own case is SIGKILL (137).
func TestExecCommandReportsTheSignalOfACommandKilledByAnother(t *testing.T) {
	for _, tc := range []struct {
		signal string
		want   int
	}{{"KILL", 137}, {"TERM", 143}, {"HUP", 129}, {"INT", 130}} {
		t.Run(tc.signal, func(t *testing.T) {
			result, err := extension.ExecCommand(context.Background(), t.TempDir(), "sh", []string{"-c", "kill -s " + tc.signal + " $$"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != tc.want || result.Killed || result.Truncated {
				t.Fatalf("result = %+v, want code %d, not killed, not truncated", result, tc.want)
			}
		})
	}
}
