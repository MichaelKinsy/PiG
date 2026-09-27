//go:build windows

package runtimecell_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemThreadInformation is SYSTEM_THREAD_INFORMATION, the record that
// follows a SYSTEM_PROCESS_INFORMATION entry once for each of its threads.
type systemThreadInformation struct {
	KernelTime      int64
	UserTime        int64
	CreateTime      int64
	WaitTime        uint32
	StartAddress    uintptr
	UniqueProcess   uintptr
	UniqueThread    uintptr
	Priority        int32
	BasePriority    int32
	ContextSwitches uint32
	ThreadState     uint32
	WaitReason      uint32
}

// KTHREAD_STATE and KWAIT_REASON names for the values a stuck process shows.
var (
	threadStates = map[uint32]string{0: "initialized", 1: "ready", 2: "running", 3: "standby", 4: "terminated", 5: "waiting", 6: "transition", 7: "deferred-ready"}
	waitReasons  = map[uint32]string{0: "Executive", 4: "DelayExecution", 5: "Suspended", 6: "UserRequest", 12: "WrSuspended", 13: "WrUserRequest", 15: "WrQueue", 16: "WrLpcReceive", 17: "WrLpcReply", 22: "WrTerminated", 36: "WrRundown", 37: "WrAlertByThreadId"}
)

// describeProcess reports what the process pid is doing: whether it is still
// running, each thread's state and wait reason, and its child processes. A
// thread Suspended with a WerFault.exe child means Windows Error Reporting is
// holding a crashed process.
func describeProcess(pid int) string {
	var parts []string
	if handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid)); err == nil {
		// A process can exit with 259, the STILL_ACTIVE value, so the
		// handle's signaled state decides whether the process runs.
		if event, err := windows.WaitForSingleObject(handle, 0); err == nil {
			var code uint32
			switch {
			case event == uint32(windows.WAIT_TIMEOUT):
				parts = append(parts, "still active")
			case windows.GetExitCodeProcess(handle, &code) == nil:
				parts = append(parts, fmt.Sprintf("exited with code %#x", code))
			}
		}
		_ = windows.CloseHandle(handle)
	}
	parts = append(parts, "threads: "+describeThreads(pid))
	parts = append(parts, "children: "+describeChildren(pid))
	return strings.Join(parts, "; ")
}

// querySystemInformation is NtQuerySystemInformation; a test replaces it.
var querySystemInformation = windows.NtQuerySystemInformation

// sizeAttempts bounds describeThreads' retries after
// STATUS_INFO_LENGTH_MISMATCH. It runs from cmd.Cancel before the kill, and
// the process list can grow between calls.
const sizeAttempts = 4

func describeThreads(pid int) string {
	size := uint32(1 << 20)
	for range sizeAttempts {
		buffer := make([]byte, size)
		var needed uint32
		err := querySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buffer[0]), size, &needed) //nolint:gosec // G103: NtQuerySystemInformation fills the caller's buffer, which x/sys takes as unsafe.Pointer.
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			size = max(needed+1<<16, 2*size)
			continue
		}
		if err != nil {
			return "unavailable: " + err.Error()
		}
		for offset := uint32(0); ; {
			process := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buffer[offset])) //nolint:gosec // G103: the kernel wrote a SYSTEM_PROCESS_INFORMATION record at this offset.
			if int(process.UniqueProcessID) == pid {
				threads := unsafe.Slice((*systemThreadInformation)(unsafe.Pointer(&buffer[offset+uint32(unsafe.Sizeof(*process))])), process.NumberOfThreads) //nolint:gosec // G103: NumberOfThreads thread records follow the process record.
				var described []string
				for _, thread := range threads {
					state := threadStates[thread.ThreadState]
					if thread.ThreadState == 5 {
						reason := waitReasons[thread.WaitReason]
						if reason == "" {
							reason = fmt.Sprint(thread.WaitReason)
						}
						state += "/" + reason
					}
					described = append(described, fmt.Sprintf("%d %s", thread.UniqueThread, state))
				}
				return fmt.Sprintf("%d [%s]", len(threads), strings.Join(described, ", "))
			}
			if process.NextEntryOffset == 0 {
				return "process not found"
			}
			offset += process.NextEntryOffset
		}
	}
	return fmt.Sprintf("unavailable: the process list outgrew %d attempts", sizeAttempts)
}

func describeChildren(pid int) string {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "unavailable: " + err.Error()
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var children []string
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if int(entry.ParentProcessID) == pid {
			children = append(children, fmt.Sprintf("%s %d", windows.UTF16ToString(entry.ExeFile[:]), entry.ProcessID))
		}
	}
	if len(children) == 0 {
		return "none"
	}
	return strings.Join(children, ", ")
}

// waitExited waits, through a handle of its own, until the process pid exits.
// The handle keeps the exited process's object, and its exit code, until the
// test ends.
func waitExited(t *testing.T, pid int) {
	t.Helper()
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
	if event, err := windows.WaitForSingleObject(handle, 10_000); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("process %d did not exit: event %#x, %v", pid, event, err)
	}
}

// A process can exit with 259, the value GetExitCodeProcess also returns for
// a running process, so exit code 259 alone does not mean "still active".
func TestDescribeProcessTellsExitCode259FromRunning(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit 259")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitExited(t, cmd.Process.Pid)
	got := describeProcess(cmd.Process.Pid)
	_ = cmd.Wait()
	if strings.Contains(got, "still active") || !strings.Contains(got, "exited with code 0x103") {
		t.Fatalf("describeProcess of a process that exited with 259 = %q", got)
	}
}

// describeThreads runs from cmd.Cancel before the kill, so a process list that
// keeps growing must not hold the kill back.
func TestDescribeThreadsBoundsSizeRetries(t *testing.T) {
	calls := 0
	querySystemInformation = func(_ int32, _ unsafe.Pointer, size uint32, needed *uint32) error {
		calls++
		*needed = size + 1
		return windows.STATUS_INFO_LENGTH_MISMATCH
	}
	t.Cleanup(func() { querySystemInformation = windows.NtQuerySystemInformation })
	described := make(chan string, 1)
	go func() { described <- describeThreads(os.Getpid()) }()
	select {
	case got := <-described:
		if !strings.HasPrefix(got, "unavailable") || calls != sizeAttempts {
			t.Fatalf("describeThreads = %q after %d queries, want unavailable after %d", got, calls, sizeAttempts)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("describeThreads kept retrying; cmd.Cancel would not reach the kill")
	}
}

// When the runner exits on its own just as its deadline passes, the kill
// fails, and the test must report the runner's own exit error, not a kill.
func TestRunnerDeadlineKeepsAnExitBeforeTheKill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", "exit 3")
	var deadline runnerDeadline
	cmd.Cancel = deadline.cancel(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitExited(t, cmd.Process.Pid)
	cancel() // the deadline passes after the exit, before Wait
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if deadline.killed || !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("killed=%t wait=%v; want no kill and the runner's exit status 3", deadline.killed, err)
	}
}
