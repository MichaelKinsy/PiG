//go:build windows

// Package jobgroup holds a child process and every descendant it starts in a Windows job object, so a caller can ask whether any of them is still
// alive, and so may still hold the child's output pipe. The bash tool and extension.ExecCommand share it.
package jobgroup

import (
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Jobs maps a child's pid to the job object that holds its descendants.
var Jobs = new(sync.Map)

// Suspend makes cmd start suspended, so Attach can put the child in a job object before it runs and before it can start any descendant (Git for
// Windows' bin/bash.exe starts usr/bin/bash.exe at once).
func Suspend(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

// Attach puts the child, started by Suspend, in a job object so all its descendants join the job, then resumes it. A failure leaves the child
// without a job: MayHoldOutput then reports true.
func Attach(p *os.Process) {
	defer func() {
		// A child left suspended would hang forever; kill it so the run fails instead.
		if !resume(p.Pid) {
			_ = p.Kill()
		}
	}()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, h)
		_ = windows.CloseHandle(h)
	}
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	Jobs.Store(p.Pid, job)
}

// resume resumes every thread of the suspended process pid (its main thread, the only one a CREATE_SUSPENDED start creates). It reports whether
// it resumed one.
func resume(pid int) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	resumed := false
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		h, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			continue
		}
		if _, resumeErr := windows.ResumeThread(h); resumeErr == nil {
			resumed = true
		}
		_ = windows.CloseHandle(h)
	}
	return resumed
}

// Release closes the job handle. Closing it does not kill the job.
func Release(p *os.Process) {
	if v, ok := Jobs.LoadAndDelete(p.Pid); ok {
		_ = windows.CloseHandle(v.(windows.Handle))
	}
}

// MayHoldOutput reports whether a process of the child's job is still alive, and so may still hold the output pipe. Every descendant joins the job
// and cannot break away (the job does not allow it), so an empty job means every write end is closed. Without a job it reports true.
func MayHoldOutput(p *os.Process) bool {
	v, ok := Jobs.Load(p.Pid)
	if !ok {
		return true
	}
	// The goroutine stack can move while the x/sys wrapper runs, before the system call, and the wrapper takes the buffer as a uintptr, so the buffer is pinned, which places it on the heap.
	info := new(basicAccounting)
	var pinner runtime.Pinner
	pinner.Pin(info)
	defer pinner.Unpin()
	if err := windows.QueryInformationJobObject(v.(windows.Handle), windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(info)), uint32(unsafe.Sizeof(*info)), nil); err != nil { //nolint:gosec // G103: QueryInformationJobObject fills the pinned JOBOBJECT_BASIC_ACCOUNTING_INFORMATION basicAccounting mirrors, and x/sys takes it as uintptr.
		return true
	}
	return info.ActiveProcesses != 0
}

// basicAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type basicAccounting struct {
	TotalUserTime, TotalKernelTime                     int64
	ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses                uint32
	ActiveProcesses, TotalTerminatedProcesses          uint32
}
