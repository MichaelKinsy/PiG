//go:build windows

package tools

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// setProcessGroup is a no-op on Windows. Upstream spawns bash with
// detached:false on win32; the tree is reaped by killProcessGroup via taskkill.
func setProcessGroup(_ *exec.Cmd) {}

// windowsTaskkillCommand detaches the hidden cleanup process from the caller's
// console, as upstream's spawn with stdio "ignore", detached: true, and
// windowsHide: true does.
func windowsTaskkillCommand(pid int) *exec.Cmd {
	command := newTaskkillCommand(pid)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
	nodespawn.HideWindow(command, nodespawn.Ignore, nodespawn.Ignore, nodespawn.Ignore)
	return command
}

// killProcessGroup uses the trusted System32 taskkill and consumes failures without switching to a single-process kill.
// upstream: packages/coding-agent/src/utils/shell.ts:killProcessTree
//
// taskkill /T only walks the parent links that still exist when it runs, and
// it returns before the killed processes have released their handles. A job
// object holds every descendant, so after taskkill the job is terminated and
// killProcessGroup returns only once the job is empty. Otherwise a surviving
// grandchild keeps its working directory open after the tool returns.
func killProcessGroup(p *os.Process) error {
	runTaskkill(windowsTaskkillCommand(p.Pid), (*exec.Cmd).Run)
	v, ok := bashJobs.Load(p.Pid)
	if !ok {
		return nil
	}
	job := v.(windows.Handle)
	_ = windows.TerminateJobObject(job, 1)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var info jobAccounting
		if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil || info.ActiveProcesses == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION, which x/sys lacks.
type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

// bashJobs maps a shell's pid to the job object that holds its descendants.
var bashJobs sync.Map

// attachProcessGroup puts the started shell in a job object so its
// descendants join the job. A failure leaves taskkill as the only reaper.
func attachProcessGroup(p *os.Process) {
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
	bashJobs.Store(p.Pid, job)
}

// releaseProcessGroup closes the job handle. Closing it does not kill the job.
func releaseProcessGroup(p *os.Process) {
	if v, ok := bashJobs.LoadAndDelete(p.Pid); ok {
		_ = windows.CloseHandle(v.(windows.Handle))
	}
}

// shellExitCode returns the process exit code. Windows processes end with an
// exit code; upstream's fallback for a missing one is 1.
func shellExitCode(state *os.ProcessState) int {
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
