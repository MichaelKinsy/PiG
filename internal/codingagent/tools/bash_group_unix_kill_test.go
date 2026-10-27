//go:build unix

package tools

import (
	"errors"
	"reflect"
	"syscall"
	"testing"
)

type killCall struct {
	pid int
	sig syscall.Signal
}

// Ports packages/coding-agent/test/shell-kill-process-tree.test.ts with the same pids, signals and call order.
func TestKillProcessTree(t *testing.T) {
	stopGroup := killCall{-1234, syscall.SIGSTOP}
	killGroup := killCall{-1234, syscall.SIGKILL}
	killShell := killCall{1234, syscall.SIGKILL}
	for _, tc := range []struct {
		name   string
		darwin bool
		fail   func(killCall) bool
		want   []killCall
	}{
		{"stops the macOS process group before killing it without resuming it", true, func(killCall) bool { return false }, []killCall{stopGroup, killGroup}},
		{"still kills the group when stopping fails", true, func(c killCall) bool { return c == stopGroup }, []killCall{stopGroup, killGroup}},
		{"falls back to killing the shell when the group kill fails", true, func(c killCall) bool { return c == killGroup }, []killCall{stopGroup, killGroup, killShell}},
		{"tolerates failure of both group signals and the fallback", true, func(killCall) bool { return true }, []killCall{stopGroup, killGroup, killShell}},
		{"keeps Linux group termination unchanged", false, func(killCall) bool { return false }, []killCall{killGroup}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []killCall
			kill := func(pid int, sig syscall.Signal) error {
				c := killCall{pid, sig}
				calls = append(calls, c)
				if tc.fail(c) {
					return errors.New("signal failed")
				}
				return nil
			}
			_ = killProcessTree(kill, tc.darwin, 1234)
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("calls %v, want %v", calls, tc.want)
			}
		})
	}
}
