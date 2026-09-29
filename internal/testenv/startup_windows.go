//go:build windows

package testenv

import (
	"encoding/json"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StartupHelper is the environment variable that makes a test binary whose
// TestMain calls ReportStartupIfRequested report how it was started.
const StartupHelper = "PIG_TEST_STARTUP_HELPER"

// Startup is how a Windows process observes the way it was started.
type Startup struct {
	// ShowWindow is STARTUPINFO.wShowWindow when dwFlags has
	// STARTF_USESHOWWINDOW, and -1 otherwise.
	ShowWindow int `json:"showWindow"`
	// OwnConsole reports that the process is the only one attached to its
	// console, as CREATE_NO_WINDOW leaves a console program.
	OwnConsole bool `json:"ownConsole"`
	// ConsoleWindow reports that the process's console has a window.
	ConsoleWindow bool `json:"consoleWindow"`
}

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
)

// CurrentStartup reports how the current process was started.
func CurrentStartup() Startup {
	var info windows.StartupInfo
	_ = windows.GetStartupInfo(&info)
	show := -1
	if info.Flags&windows.STARTF_USESHOWWINDOW != 0 {
		show = int(info.ShowWindow)
	}
	window, _, _ := procGetConsoleWindow.Call()
	var processes [16]uint32
	count, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&processes[0])), uintptr(len(processes))) //nolint:gosec // G103: x/sys has no wrapper; the API fills at most len(processes) IDs of the fixed local array, which stays live for the call, and no CLI-supplied pointer reaches Windows.
	return Startup{ShowWindow: show, OwnConsole: count == 1, ConsoleWindow: window != 0}
}

// ReportStartupIfRequested writes CurrentStartup to stdout as JSON and exits
// when StartupHelper is "1". A TestMain calls it first, so the test binary
// can stand in for a spawned program.
func ReportStartupIfRequested() {
	if os.Getenv(StartupHelper) != "1" {
		return
	}
	if err := json.NewEncoder(os.Stdout).Encode(CurrentStartup()); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
