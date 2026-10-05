//go:build windows

package tools

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
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
		list := (*jobProcessIDList)(unsafe.Pointer(&buf[0]))
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(list)), size, nil)
		if err == windows.ERROR_MORE_DATA {
			capacity = list.assigned
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list bash job members: %w", err)
		}
		ids := unsafe.Slice(&list.ids[0], list.listed)
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
