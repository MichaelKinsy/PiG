//go:build windows

package workers

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processTree is the job object a worker is placed in: Windows has no process groups to signal, so killing the job kills
// everything the worker spawned.
type processTree struct{ job windows.Handle }

// detach gives cmd a process group of its own, as libuv does for a detached child.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}

// treeOf places the started cmd in a job object that kills its processes when closed.
func treeOf(cmd *exec.Cmd) (*processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
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
	return &processTree{job: job}, nil
}

// kill terminates the job's processes; false when there is no job.
func (tree *processTree) kill() bool {
	if tree == nil || tree.job == 0 {
		return false
	}
	return windows.TerminateJobObject(tree.job, 1) == nil
}

func (tree *processTree) close() {
	if tree != nil && tree.job != 0 {
		_ = windows.CloseHandle(tree.job)
		tree.job = 0
	}
}

// exitOf is how a process ended: Windows has no signals.
func exitOf(state *os.ProcessState) Exit {
	if state == nil {
		return Exit{}
	}
	return Exit{Code: intPointer(state.ExitCode())}
}
