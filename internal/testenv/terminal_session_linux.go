//go:build linux

package testenv

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// terminalSessionEnv names the test that a child of the test binary runs alone, as the leader of a terminal session (InTerminalSession).
const terminalSessionEnv = "PIG_TEST_TERMINAL_SESSION"

// terminalSessionCopyDelay holds the copy of the terminal's output back for this long. It is zero except in the test that forces the order the helper must
// survive: the child has written everything and exited before the first read of its output.
var terminalSessionCopyDelay time.Duration

// InTerminalSession reports whether the calling top-level test runs as PiG does in a user's terminal: as the leader of a session whose controlling terminal
// is a pseudo-terminal, with its own process group in the terminal's foreground. Called in the package's test process, it runs the test again in such a
// process (the same test binary, only that test, within the parent's deadline), fails if the child fails or does not run it, skips if the child skipped, and
// returns false.
//
// A test that starts a process that could take the terminal (an interactive shell makes its own process group the foreground group) calls it first and
// ends with RequireTerminalForeground. Without a controlling terminal, such a process fails to open /dev/tty and the test proves nothing.
func InTerminalSession(t *testing.T) bool {
	t.Helper()
	name := t.Name()
	if strings.Contains(name, "/") {
		t.Fatalf("InTerminalSession runs top-level tests only, not %s", name)
	}
	if os.Getenv(terminalSessionEnv) == name {
		return true
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^" + regexp.QuoteMeta(name) + "$", "-test.count=1", "-test.v"}
	if deadline, ok := t.Deadline(); ok {
		// The child times out first, so its goroutine dump reaches the failure instead of the parent's timeout panic.
		left := time.Until(deadline)
		args = append(args, "-test.timeout="+(left-min(left/10, 5*time.Second)).String())
	}
	master, slave := openTerminalSessionPTY(t)
	//nolint:gosec // G204 (class b): program is executable from os.Executable; args are -test.* flags computed from t.Name and the deadline.
	cmd := exec.Command(executable, args...)
	cmd.Env = append(os.Environ(), terminalSessionEnv+"="+name)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	child, err := Start(t, cmd)
	_ = slave.Close()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	copied := make(chan struct{})
	go func() {
		time.Sleep(terminalSessionCopyDelay)
		_, _ = io.Copy(&output, master)
		close(copied)
	}()
	waitErr := child.Wait()
	// The child's output is in the terminal's buffer when it exits, and the copy reads it until the terminal reports its end (every process that held the
	// slave is gone). Closing the master before that discards what the copy has not read yet, which under a loaded scheduler was all of it. A process that
	// keeps the slave open is waited for the test's budget, and then reported.
	bound := time.NewTimer(testbudget.Wait(t))
	copyEnded := false
	select {
	case <-copied:
		copyEnded = true
	case <-bound.C:
	}
	bound.Stop()
	_ = master.Close()
	<-copied
	switch {
	case waitErr != nil:
		t.Fatalf("%s in a terminal session: %v\n%s", name, waitErr, output.String())
	case !copyEnded:
		t.Fatalf("%s in a terminal session: the child ended and the terminal's output did not end within the test budget (a process it started still holds the terminal); what was read:\n%s", name, output.String())
	case bytes.Contains(output.Bytes(), []byte("--- SKIP: "+name+" ")):
		t.Skipf("%s skipped in a terminal session:\n%s", name, output.String())
	case !bytes.Contains(output.Bytes(), []byte("--- PASS: "+name+" ")):
		t.Fatalf("%s did not run in a terminal session:\n%s", name, output.String())
	}
	t.Logf("%s in a terminal session:\n%s", name, output.String())
	return false
}

// RequireTerminalForeground fails t when the foreground process group of the terminal on standard input is not the test process's own group: a process the
// test started took the terminal and kept it, so the process's next read from the terminal raises SIGTTIN. Call it in a test that InTerminalSession runs in
// a terminal session.
func RequireTerminalForeground(t testing.TB) {
	t.Helper()
	foreground, err := ioctlGetInt(0, unix.TIOCGPGRP)
	if err != nil {
		t.Fatalf("the test process has no terminal; call InTerminalSession first: %v", err)
	}
	if own := syscall.Getpgrp(); foreground != own {
		t.Errorf("the terminal's foreground process group is %d, not the test process's group %d: the next read from the terminal raises SIGTTIN", foreground, own)
	}
}

// openTerminalSessionPTY opens a pseudo-terminal pair.
func openTerminalSessionPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	// The ioctls go through SyscallConn, not Fd: Fd turns the descriptor blocking, and a Close could then no longer end a read of it that waits for
	// a terminal a stray process keeps open.
	raw, err := master.SyscallConn()
	if err != nil {
		t.Fatalf("pseudo-terminal descriptor: %v", err)
	}
	var number int
	var ioctlErr error
	if err := raw.Control(func(fd uintptr) {
		if ioctlErr = ioctlSetPointerInt(int(fd), unix.TIOCSPTLCK, 0); ioctlErr != nil {
			ioctlErr = fmt.Errorf("unlock pseudo-terminal: %w", ioctlErr)
			return
		}
		if number, ioctlErr = ioctlGetInt(int(fd), unix.TIOCGPTN); ioctlErr != nil {
			ioctlErr = fmt.Errorf("pseudo-terminal number: %w", ioctlErr)
		}
	}); err != nil {
		t.Fatalf("pseudo-terminal descriptor: %v", err)
	}
	if ioctlErr != nil {
		t.Fatal(ioctlErr)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave: %v", err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}
