//go:build windows

package workers

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processTree is the job object a worker is placed in: Windows has no process groups to signal, so killing the job kills
// everything the worker spawned. The handle is closed when the worker ended, while a Kill may still come (a timer, a
// `fatal`): mu keeps a Kill from using a closed handle, whose value Windows may give to another object.
type processTree struct {
	mu  sync.Mutex
	job windows.Handle
}

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
	if tree == nil {
		return false
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if tree.job == 0 {
		return false
	}
	return windows.TerminateJobObject(tree.job, 1) == nil
}

// close closes the job's handle once; a kill after it finds no job.
func (tree *processTree) close() {
	if tree == nil {
		return
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if tree.job != 0 {
		_ = windows.CloseHandle(tree.job)
		tree.job = 0
	}
}

// exitOf is how a process ended: Windows has no signals.
func exitOf(state *os.ProcessState) Exit {
	if state == nil {
		return Exit{}
	}
	return Exit{Code: new(state.ExitCode())}
}

// killedExit is the exit of a process Kill ended: TerminateJobObject gives it an exit code, where libuv reports the signal
// it sent a child it killed, so Pi's public error reads `worker exited (SIGKILL)`.
func killedExit(Exit) Exit { return Exit{Signal: new("SIGKILL")} }
