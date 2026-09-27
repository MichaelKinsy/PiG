//go:build integration && windows

package integration

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// processRunning reports whether p has not exited. os.Process on Windows
// accepts no signal but Kill, so the test waits on the process with no delay.
func processRunning(p *os.Process) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(p.Pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	event, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && event == uint32(windows.WAIT_TIMEOUT)
}

// prepareTermination starts cmd in its own process group, so
// requestTermination can send it a console control event without reaching
// the test process or its console's other processes.
func prepareTermination(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// requestTermination sends Ctrl+Break to p's process group. Go reports Ctrl+C
// and Ctrl+Break as SIGINT, and a new process group receives only Ctrl+Break,
// so this is the interrupt a user's Ctrl+C delivers. It returns the exit
// status pig reports for it: 128 plus SIGINT's number.
func requestTermination(p *os.Process) (int, error) {
	return 128 + int(syscall.SIGINT), windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(p.Pid))
}
