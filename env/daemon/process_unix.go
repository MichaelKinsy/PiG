//go:build !windows

package daemon

// Ports packages/env/daemon/src/sys/unix.rs

import (
	"os/exec"
	"slices"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// programAttributes starts the program as the leader of a new session, so a kill reaches every descendant that stays in
// its group (sys/unix.rs configure).
func programAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// startCommand starts cmd with an empty blocked-signal mask, as libuv starts programs.
func startCommand(cmd *exec.Cmd) error {
	return withEmptySignalMask(func() error { return nodespawn.Start(cmd) })
}

// killTree kills the command's session: its process group (or the process if the group is gone), then every other group
// in the session. Commands start in a session of their own (`setsid`), and a shell with job control puts each
// background job in a group of its own, so a terminal's `sleep 9 &` is reached too; only what left the session
// (`setsid`) is not. Call it only while `pid` is not reaped: until then it names no other process, and no other session
// has its id.
func killTree(pid int) {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	// A member may fork while the others are killed: again until none is found, or only the same ones (killed, not
	// reaped yet).
	var last []int
	for range 8 {
		members := sessionMembers(pid)
		if len(members) == 0 || slices.Equal(members, last) {
			return
		}
		for _, member := range members {
			killMember(member, pid)
		}
		last = members
	}
}

// sessionMembers returns the processes of session sid, by their session id.
func sessionMembers(sid int) []int {
	pids := allPids()
	var members []int
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		got, err := unix.Getsid(pid)
		if err == nil && got == sid {
			members = append(members, pid)
		}
	}
	slices.Sort(members)
	return members
}
