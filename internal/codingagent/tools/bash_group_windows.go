//go:build windows

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/jobgroup"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// setProcessGroup starts the shell suspended, so attachProcessGroup can put it
// in a job object before it runs and before it can start any descendant
// (Git for Windows' bin/bash.exe starts usr/bin/bash.exe at once). Upstream
// spawns bash with detached:false on win32; the tree is reaped by
// killProcessGroup via taskkill and the job.
// Ports packages/coding-agent/src/utils/shell.ts
func setProcessGroup(cmd *exec.Cmd) { jobgroup.Suspend(cmd) }

// exitKillTimeout bounds the whole Windows exit-path kill (taskkill, job
// termination, and waiting on job members) so a wedged child or taskkill cannot
// block shutdown past Windows's ~5 s console-close window (Pi 31b407dc63, #11889).
// upstream: packages/coding-agent/src/utils/shell.ts:killTrackedDetachedChildren
const exitKillTimeout = 2000 * time.Millisecond

// windowsTaskkillCommand detaches the hidden cleanup process from the caller's
// console, as upstream's spawn with stdio "ignore", detached: true, and
// windowsHide: true does.
func windowsTaskkillCommand(pid int) *exec.Cmd {
	return configureWindowsTaskkill(newTaskkillCommand(pid))
}

// windowsTaskkillCommandContext configures a bounded multi-PID taskkill for
// the exit path (Pi 31b407dc63, #11889).
func windowsTaskkillCommandContext(ctx context.Context, pids ...int) *exec.Cmd {
	return configureWindowsTaskkill(newTaskkillCommandContext(ctx, pids...))
}

func configureWindowsTaskkill(command *exec.Cmd) *exec.Cmd {
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
	return terminateAndWaitBashJob(p.Pid)
}

// killTrackedProcessGroups ends every tracked shell tree on the exit path
// within one shared exitKillTimeout budget: one bounded taskkill across all
// PIDs, then TerminateJobObject for every tracked job, then bounded waits on
// the job members using the remaining budget. When the deadline expires, exit
// proceeds as in Pi (Pi 31b407dc63, #11889).
// upstream: packages/coding-agent/src/utils/shell.ts:killTrackedDetachedChildren
func killTrackedProcessGroups(processes []*os.Process) {
	killTrackedProcessGroupsWithTimeout(processes, exitKillTimeout, (*exec.Cmd).Run, windows.WaitForSingleObject)
}

func killTrackedProcessGroupsWithTimeout(
	processes []*os.Process,
	budget time.Duration,
	run func(*exec.Cmd) error,
	waitHandle func(windows.Handle, uint32) (uint32, error),
) {
	if len(processes) == 0 {
		return
	}
	//portlint:allow clock exitKillTimeout bounds the synchronous Windows shutdown kill against the ~5 s OS console-close deadline (Pi 31b407dc63, #11889)
	deadline := time.Now().Add(budget)
	pids := make([]int, len(processes))
	for i, p := range processes {
		pids[i] = p.Pid
	}
	//portlint:allow ctxbackground KillTrackedDetachedChildren is a synchronous shutdown exit hook with no caller context; its deadline is Pi's 2000 ms bound
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	runTaskkill(windowsTaskkillCommandContext(ctx, pids...), run)
	// Always issue TerminateJobObject for every tracked job (it is immediate),
	// even if taskkill consumed the deadline, and collect member handles first.
	var allMembers []windows.Handle
	defer func() {
		for _, h := range allMembers {
			_ = windows.CloseHandle(h)
		}
	}()
	for _, pid := range pids {
		v, ok := bashJobs.Load(pid)
		if !ok {
			continue
		}
		job := v.(windows.Handle)
		members, _ := openJobMembers(job)
		allMembers = append(allMembers, members...)
		_ = windows.TerminateJobObject(job, 1)
	}
	for _, h := range allMembers {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		// Round up partial milliseconds so a sub-millisecond remainder does not spin as 0.
		ms := uint32((remaining + time.Millisecond - 1) / time.Millisecond)
		event, err := waitHandle(h, ms)
		if err != nil || event == uint32(windows.WAIT_TIMEOUT) {
			if time.Until(deadline) <= 0 {
				return
			}
		}
	}
}

func terminateAndWaitBashJob(pid int) error {
	v, ok := bashJobs.Load(pid)
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
var bashJobs = jobgroup.Jobs

// attachProcessGroup puts the shell, started suspended, in a job object so all its descendants join the job, then resumes it. A failure leaves taskkill
// as the only reaper.
func attachProcessGroup(p *os.Process) { jobgroup.Attach(p) }

// releaseProcessGroup closes the job handle. Closing it does not kill the job.
func releaseProcessGroup(p *os.Process) { jobgroup.Release(p) }

// processGroupMayHoldOutput reports whether a process of the shell's job is still alive, and so may still hold the output pipe.
func processGroupMayHoldOutput(p *os.Process) bool { return jobgroup.MayHoldOutput(p) }

// shellExitCode returns the process exit code. Windows processes end with an
// exit code; upstream's fallback for a missing one is 1.
func shellExitCode(state *os.ProcessState) int {
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
