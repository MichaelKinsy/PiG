//go:build darwin || linux

package cli

import (
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// runPigOnTerminalStdinToFile runs pig the way `pig "q" > out.txt` runs from a
// terminal: stdin is a pseudo-terminal and stdout is the file at outPath.
func runPigOnTerminalStdinToFile(t *testing.T, bin, outPath string, args ...string) (string, int) {
	t.Helper()
	master, slave := openModePTY(t)
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	go func() { _, _ = io.Copy(io.Discard, master) }()
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runPigForModeTest(t, bin, slave, out, args...)
	_ = out.Close()
	return stderr, code
}

// startPigOnTerminal starts cmd with a pseudo-terminal as its controlling
// terminal and standard streams. It returns the terminal's master side and a
// function that kills the process and releases the terminal.
func startPigOnTerminal(t *testing.T, cmd *exec.Cmd) (io.ReadWriter, func()) {
	t.Helper()
	master, slave := openModePTY(t)
	cmd.Stdin, cmd.Stdout = slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		_ = master.Close()
		_ = slave.Close()
		t.Fatal(err)
	}
	return master, func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = slave.Close()
		_ = master.Close()
	}
}
