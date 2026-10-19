//go:build windows

package testenv

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobBasicAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which x/sys/windows does not declare.
type jobBasicAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// tree is a Job Object that kills its processes when it closes.
type tree struct {
	mu  sync.Mutex
	job windows.Handle
}

func startTree(cmd *exec.Cmd) (*tree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	// The buffer is pinned because x/sys takes it as a uintptr.
	info := new(windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION)
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	var pinner runtime.Pinner
	pinner.Pin(info)
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(info)), //nolint:gosec // G103: the call reads the pinned info struct; x/sys takes it as uintptr.
		uint32(unsafe.Sizeof(*info)))
	pinner.Unpin()
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	tr := &tree{job: job}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// The child starts suspended and joins the job before it runs, so it cannot start a descendant outside the job.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if cmd.Cancel != nil { // exec accepts a Cancel only on a command made by CommandContext.
		cmd.Cancel = func() error { tr.stop(0); return nil }
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, handle)
	}
	if err == nil {
		status, _, _ := ntResumeProcess.Call(uintptr(handle))
		if status != 0 {
			err = windows.NTStatus(status)
		}
	}
	if handle != 0 {
		_ = windows.CloseHandle(handle)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return tr, nil
}

func (tr *tree) active() uint32 {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.job == 0 {
		return 0
	}
	// x/sys takes the buffer as a uintptr, which keeps neither a stack variable in place nor a heap one alive; the pinned
	// heap buffer stays put for the call.
	info := new(jobBasicAccounting)
	var pinner runtime.Pinner
	pinner.Pin(info)
	defer pinner.Unpin()
	if err := windows.QueryInformationJobObject(tr.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(info)), //nolint:gosec // G103: the call fills the pinned info struct; x/sys takes it as uintptr.
		uint32(unsafe.Sizeof(*info)), nil); err != nil {
		return 0
	}
	return info.ActiveProcesses
}

// stop ends every process of the job at once; Windows has no graceful group signal for a console-less child. It reports
// whether any member was still running after the grace period. The job counts a member until the kernel has torn it down,
// which can trail the exit that signalled Wait (a console host, or the child itself, under load), so a member that
// disappears within grace is an exit in progress, and only one that outlasts it is a descendant the child left behind.
func (tr *tree) stop(grace time.Duration) bool {
	left := tr.active() > 0
	for deadline := time.Now().Add(grace); left && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		left = tr.active() > 0
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.job != 0 {
		_ = windows.TerminateJobObject(tr.job, 1)
		_ = windows.CloseHandle(tr.job)
		tr.job = 0
	}
	return left
}
