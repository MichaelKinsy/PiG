//go:build windows

package runtimecell_test

import (
	"errors"
	"fmt"
	"strings"
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
	if handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)); err == nil {
		var code uint32
		if windows.GetExitCodeProcess(handle, &code) == nil {
			if code == 259 {
				parts = append(parts, "still active")
			} else {
				parts = append(parts, fmt.Sprintf("exit code %#x", code))
			}
		}
		_ = windows.CloseHandle(handle)
	}
	parts = append(parts, "threads: "+describeThreads(pid))
	parts = append(parts, "children: "+describeChildren(pid))
	return strings.Join(parts, "; ")
}

func describeThreads(pid int) string {
	size := uint32(1 << 20)
	for {
		buffer := make([]byte, size)
		var needed uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buffer[0]), size, &needed) //nolint:gosec // G103: NtQuerySystemInformation fills the caller's buffer, which x/sys takes as unsafe.Pointer.
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			size = needed + 1<<16
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
