//go:build windows

package daemon

// Ports packages/env/daemon/src/sys/windows.rs (Group)

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// newGroup puts a program just started into a job object of its own: what is left in it is killed once the program exits, and
// when the daemon ends (the job is closed with it). It is nested in the daemon's job. The program already runs: a child it starts before this
// assignment stays outside the job. Starting it suspended would close that gap.
func newGroup(cmd *exec.Cmd) (*programGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	// No breakaway: a child started after the assignment stays in this job, even if the daemon's job allows breakaway.
	var information windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	//nolint:gosec // G103: the information struct is the one the call reads, sized as the call expects.
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	ending := func() {
		// Terminating the job kills what is left in it, the program too if it still runs; the handle is not needed after.
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	}
	return &programGroup{pid: cmd.Process.Pid, live: true, ending: ending}, nil
}

// waitForExit waits until the program has exited; the open handle of the program keeps its pid from naming another process.
func (g *programGroup) waitForExit() error {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(g.pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(process) }()
	_, err = windows.WaitForSingleObject(process, windows.INFINITE)
	return err
}
