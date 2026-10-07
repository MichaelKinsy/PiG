//go:build windows

package tools

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// setProcessGroup starts the shell suspended, so attachProcessGroup can put it
// in a job object before it runs and before it can start any descendant
// (Git for Windows' bin/bash.exe starts usr/bin/bash.exe at once). Upstream
// spawns bash with detached:false on win32; the tree is reaped by
// killProcessGroup via taskkill and the job.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

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
	// Open every member before the kill so a recycled PID cannot be waited on.
	// A PID that is already gone fails OpenProcess and needs no wait.
	members, listErr := openJobMembers(job)
	defer func() {
		for _, h := range members {
			_ = windows.CloseHandle(h)
		}
	}()
	if err := windows.TerminateJobObject(job, 1); err != nil {
		return fmt.Errorf("terminate bash job object: %w", err)
	}
	// TerminateJobObject guarantees that each member exits.
	for _, h := range members {
		if _, err := windows.WaitForSingleObject(h, windows.INFINITE); err != nil {
			return fmt.Errorf("wait for bash job member: %w", err)
		}
	}
	return listErr
}

// jobProcessIDList is JOBOBJECT_BASIC_PROCESS_ID_LIST with room for ids.
type jobProcessIDList struct {
	assigned, listed uint32
	ids              [1]uintptr
}

// openJobMembers opens, with SYNCHRONIZE, each process now in the job. It
// grows the buffer to the count the job reports until the list fits.
func openJobMembers(job windows.Handle) ([]windows.Handle, error) {
	capacity := uint32(1)
	for {
		size := uint32(unsafe.Sizeof(jobProcessIDList{})) + (capacity-1)*uint32(unsafe.Sizeof(uintptr(0)))
		buf := make([]uintptr, (size+uint32(unsafe.Sizeof(uintptr(0)))-1)/uint32(unsafe.Sizeof(uintptr(0))))
		list := (*jobProcessIDList)(unsafe.Pointer(&buf[0])) //nolint:gosec // G103: buf is sized for a JOBOBJECT_BASIC_PROCESS_ID_LIST with capacity ids, the layout jobProcessIDList mirrors.
		// The goroutine stack can move while the x/sys wrapper runs, before the system call, and the wrapper takes the buffer as a uintptr, so the buffer is pinned, which places it on the heap.
		var pinner runtime.Pinner
		pinner.Pin(&buf[0])
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(list)), size, nil) //nolint:gosec // G103: QueryInformationJobObject fills the pinned JOBOBJECT_BASIC_PROCESS_ID_LIST buffer, and x/sys takes it as uintptr.
		pinner.Unpin()
		if errors.Is(err, windows.ERROR_MORE_DATA) {
			capacity = max(list.assigned, capacity+1)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list bash job members: %w", err)
		}
		ids := unsafe.Slice(&list.ids[0], list.listed) //nolint:gosec // G103: the API wrote listed ids after the header, inside buf.
		var members []windows.Handle
		for _, id := range ids {
			if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(id)); err == nil {
				members = append(members, h)
			}
		}
		return members, nil
	}
}

// bashJobs maps a shell's pid to the job object that holds its descendants.
var bashJobs sync.Map

// attachProcessGroup puts the shell, started suspended, in a job object so
// all its descendants join the job, then resumes it. A failure leaves taskkill
// as the only reaper.
func attachProcessGroup(p *os.Process) {
	defer func() {
		// A shell left suspended would hang the command forever; kill it so the run fails instead.
		if !resumeProcess(p.Pid) {
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
	bashJobs.Store(p.Pid, job)
}

// resumeProcess resumes every thread of the suspended process pid (its main
// thread, the only one a CREATE_SUSPENDED start creates). It reports whether
// it resumed one.
func resumeProcess(pid int) bool {
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

// releaseProcessGroup closes the job handle. Closing it does not kill the job.
func releaseProcessGroup(p *os.Process) {
	if v, ok := bashJobs.LoadAndDelete(p.Pid); ok {
		_ = windows.CloseHandle(v.(windows.Handle))
	}
}

// processGroupMayHoldOutput reports whether a process of the shell's job is
// still alive, and so may still hold the output pipe. Every descendant joins
// the job and cannot break away (the job does not allow it), so an empty job
// means every write end is closed. Without a job it reports true.
func processGroupMayHoldOutput(p *os.Process) bool {
	v, ok := bashJobs.Load(p.Pid)
	if !ok {
		return true
	}
	// The goroutine stack can move while the x/sys wrapper runs, before the system call, and the wrapper takes the buffer as a uintptr, so the buffer is pinned, which places it on the heap.
	info := new(jobBasicAccounting)
	var pinner runtime.Pinner
	pinner.Pin(info)
	defer pinner.Unpin()
	if err := windows.QueryInformationJobObject(v.(windows.Handle), windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(info)), uint32(unsafe.Sizeof(*info)), nil); err != nil { //nolint:gosec // G103: QueryInformationJobObject fills the pinned JOBOBJECT_BASIC_ACCOUNTING_INFORMATION jobBasicAccounting mirrors, and x/sys takes it as uintptr.
		return true
	}
	return info.ActiveProcesses != 0
}

// jobBasicAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobBasicAccounting struct {
	TotalUserTime, TotalKernelTime                     int64
	ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses                uint32
	ActiveProcesses, TotalTerminatedProcesses          uint32
}

// shellExitCode returns the process exit code. Windows processes end with an
// exit code; upstream's fallback for a missing one is 1.
func shellExitCode(state *os.ProcessState) int {
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
