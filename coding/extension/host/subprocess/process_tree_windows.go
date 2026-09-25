//go:build windows

package subprocess

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processTree struct {
	process       *os.Process
	job           windows.Handle
	mu            sync.Mutex
	once          sync.Once
	err           error
	killAttempted chan struct{}
	killAttempt   sync.Once
}

type windowsProcessTreeOps struct {
	openProcess   func(uint32, bool, uint32) (windows.Handle, error)
	assign        func(windows.Handle, windows.Handle) error
	resume        func(windows.Handle) error
	afterStart    func()
	killAttempted chan struct{}
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func defaultWindowsProcessTreeOps() windowsProcessTreeOps {
	return windowsProcessTreeOps{
		openProcess: windows.OpenProcess,
		assign:      windows.AssignProcessToJobObject,
		resume: func(process windows.Handle) error {
			status, _, _ := ntResumeProcess.Call(uintptr(process))
			if status != 0 {
				return windows.NTStatus(status)
			}
			return nil
		},
	}
}

func startProcessTree(cmd *exec.Cmd) (*processTree, error) {
	return startProcessTreeWithWindowsOps(cmd, defaultWindowsProcessTreeOps())
}

func startProcessTreeWithWindowsOps(cmd *exec.Cmd, ops windowsProcessTreeOps) (*processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), //nolint:gosec // G103: SetInformationJobObject reads the JOBOBJECT_EXTENDED_LIMIT_INFORMATION it is given, and x/sys takes that buffer as uintptr.
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	tree := &processTree{job: job, killAttempted: ops.killAttempted}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.Cancel = tree.Kill

	// Hold launch ownership until the suspended child belongs to the job and is
	// resumed. A concurrent context cancellation blocks in Kill instead of
	// consuming the job handle while it is still empty.
	tree.mu.Lock()
	if err = cmd.Start(); err != nil {
		tree.mu.Unlock()
		_ = tree.Close()
		return nil, err
	}
	tree.process = cmd.Process
	if ops.afterStart != nil {
		ops.afterStart()
	}
	processHandle, err := ops.openProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(cmd.Process.Pid),
	)
	if err == nil {
		err = ops.assign(job, processHandle)
	}
	if err == nil {
		err = ops.resume(processHandle)
	}
	if processHandle != 0 {
		_ = windows.CloseHandle(processHandle)
	}
	if err != nil {
		// Assignment failures happen before resume, so the child cannot have
		// spawned descendants. Kill it directly; tree.Kill cannot be used while
		// launch ownership is held and must never be allowed to no-op here.
		_ = cmd.Process.Kill()
		tree.mu.Unlock()
		_ = cmd.Wait()
		_ = tree.Close()
		return nil, err
	}
	tree.mu.Unlock()
	return tree, nil
}

func (tree *processTree) Kill() error {
	if tree == nil {
		return nil
	}
	if tree.killAttempted != nil {
		tree.killAttempt.Do(func() { close(tree.killAttempted) })
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.once.Do(func() {
		if tree.job != 0 {
			tree.err = windows.TerminateJobObject(tree.job, 1)
			if errors.Is(tree.err, windows.ERROR_ACCESS_DENIED) && tree.process != nil {
				tree.err = tree.process.Kill()
			}
			_ = windows.CloseHandle(tree.job)
			tree.job = 0
			return
		}
		if tree.process != nil {
			tree.err = tree.process.Kill()
		}
	})
	if errors.Is(tree.err, os.ErrProcessDone) {
		return nil
	}
	return tree.err
}

func (tree *processTree) Close() error { return tree.Kill() }
