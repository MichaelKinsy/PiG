//go:build linux

package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Issue #165: an interactive session exited on its own and left no trace. Every ending that no user action asked for must
// leave a line in <agent dir>/exit.log (or a crash record), and a session that dies where no code can run is reported by
// the next start. These tests drive the real binary on a pseudo-terminal.

type exitRecordSession struct {
	t        *testing.T
	cmd      *exec.Cmd
	master   *os.File
	output   *ptyOutput
	exited   chan struct{}
	waitErr  error
	agentDir string
}

// startExitRecordSession starts pig on a pseudo-terminal in a fresh session of its own. With controlling, the terminal
// is the session's controlling terminal, so closing its master hangs the session up; without, a closed master shows
// only as a failed or ended read of standard input.
func startExitRecordSession(t *testing.T, binary, agentDir string, controlling bool, extraEnv ...string) *exitRecordSession {
	t.Helper()
	root := t.TempDir()
	seedFirstRunDone(t, agentDir)
	master, slave := openPTY(t, 40, 120)
	stdout := slave
	var otherMaster *os.File
	if !controlling {
		// Output goes to a second live terminal, so only the input side is gone when the first master closes.
		var otherSlave *os.File
		otherMaster, otherSlave = openPTY(t, 40, 120)
		stdout = otherSlave
		t.Cleanup(func() { _ = otherSlave.Close() })
	}
	cmd := exec.CommandContext(t.Context(), binary, "--no-session", "--no-extensions", "--offline")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PIG_HOME="+root, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_OFFLINE=1", "TERM=xterm-256color")
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, stdout, stdout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: controlling, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	s := &exitRecordSession{t: t, cmd: cmd, master: master, output: &ptyOutput{}, exited: make(chan struct{}), agentDir: agentDir}
	go func() { s.waitErr = cmd.Wait(); close(s.exited) }()
	drain := master
	if otherMaster != nil {
		drain = otherMaster
	}
	copied, stopCopy := make(chan struct{}), make(chan struct{})
	go func() { defer close(copied); capturePTY(drain, s.output, stopCopy) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-s.exited
		close(stopCopy)
		<-copied
		_ = master.Close()
		if otherMaster != nil {
			_ = otherMaster.Close()
		}
	})
	s.waitReady()
	return s
}

// waitReady waits until the session has drawn its screen and written its marker.
func (s *exitRecordSession) waitReady() {
	s.t.Helper()
	s.output.waitQuiet(0, []byte("PiG"), 200*time.Millisecond, testbudget.Wait(s.t))
	if !bytes.Contains(s.output.since(0), []byte("PiG")) {
		s.t.Fatalf("interactive screen did not draw: %q", s.output.since(0))
	}
	if len(s.markers()) != 1 {
		s.t.Fatalf("running session marker files = %v", s.markers())
	}
}

// capturePTY copies terminal output until stop closes or the terminal is closed. A blocking read on a terminal master cannot be
// interrupted by closing it, so it polls. Each poll and read runs under the file's RawConn, which holds the descriptor for that
// step; the step after a Close fails, and the capture ends. The descriptor's bare number does neither: when a test closed the
// master under a running capture (TestInteractiveExitOnAClosedTerminalLeavesALine), the capture went on to poll and read
// whichever file the process opened next on that number. In CI that file was a connection the loopback server of
// TestRPCAzureOpenAIResponsesObservation had just accepted: the capture read the model request before net/http did, and the run
// ended in "timeout after 0 Responses request(s)" with the request received and no handler entered.
func capturePTY(master *os.File, output *ptyOutput, stop <-chan struct{}) {
	conn, err := master.SyscallConn()
	if err != nil {
		return
	}
	buffer := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return
		default:
		}
		ready, n := 0, 0
		var pollErr, readErr error
		if conn.Read(func(fd uintptr) bool {
			if ready, pollErr = unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 20); pollErr == nil && ready != 0 {
				n, readErr = unix.Read(int(fd), buffer)
			}
			return true
		}) != nil {
			return
		}
		if errors.Is(pollErr, syscall.EINTR) {
			continue
		}
		if pollErr != nil {
			return
		}
		if ready == 0 {
			continue
		}
		if n > 0 {
			_, _ = output.Write(buffer[:n])
		}
		if readErr != nil || n == 0 {
			return
		}
	}
}

// A capture ends with its terminal. TestInteractiveExitOnAClosedTerminalLeavesALine closes the master under a running capture, and
// a capture that kept only the descriptor's number went on to poll and read whichever file took that number next. Here a socket
// takes it at once, as an accepted connection did in CI: what its peer writes must still be there to read.
func TestCapturePTYEndsWithItsTerminal(t *testing.T) {
	master, slave := openPTY(t, 40, 120)
	output := &ptyOutput{}
	stop, copied := make(chan struct{}), make(chan struct{})
	go func() { defer close(copied); capturePTY(master, output, stop) }()
	var opened []int
	defer func() {
		close(stop)
		<-copied
		for _, fd := range opened {
			_ = unix.Close(fd)
		}
		_ = master.Close()
		_ = slave.Close()
	}()
	if _, err := slave.WriteString("drawn"); err != nil {
		t.Fatal(err)
	}
	output.waitQuiet(0, []byte("drawn"), 0, testbudget.Wait(t))
	if !bytes.Contains(output.since(0), []byte("drawn")) {
		t.Fatalf("the capture is not copying: %q", output.since(0))
	}
	number := int(master.Fd())
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	// The kernel hands out the lowest free number, so the descriptors opened next reach the master's as soon as it is free.
	victim, peer := -1, -1
	for victim < 0 && len(opened) < 32 {
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		opened = append(opened, pair[0], pair[1])
		switch number {
		case pair[0]:
			victim, peer = pair[0], pair[1]
		case pair[1]:
			victim, peer = pair[1], pair[0]
		}
	}
	const sent = "bytes of another file"
	if victim >= 0 {
		if _, err := unix.Write(peer, []byte(sent)); err != nil {
			t.Fatal(err)
		}
	}
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(testbudget.Wait(t))
	for ended := false; !ended; {
		if bytes.Contains(output.since(0), []byte(sent)) {
			t.Fatalf("the capture read %q from the descriptor that took its closed terminal's number %d", sent, number)
		}
		select {
		case <-copied:
			ended = true
		case <-deadline:
			t.Fatal("the capture still runs after its terminal was closed")
		case <-tick.C:
		}
	}
	if victim < 0 {
		// The master's number was still held when the capture ended, so no other file could have been read through it.
		return
	}
	buffer := make([]byte, len(sent)+1)
	if n, err := unix.Read(victim, buffer); err != nil || string(buffer[:n]) != sent {
		t.Fatalf("the descriptor that took the terminal's number holds %q (%v), want %q", buffer[:max(n, 0)], err, sent)
	}
}

func (s *exitRecordSession) markers() []string {
	entries, _ := os.ReadDir(codingagent.CrashOutputDir(s.agentDir))
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func (s *exitRecordSession) exitCode() int {
	s.t.Helper()
	select {
	case <-s.exited:
	case <-time.After(testbudget.Wait(s.t)):
		s.t.Fatalf("session did not exit\n%s", s.output.since(0))
	}
	if exit, ok := errors.AsType[*exec.ExitError](s.waitErr); ok {
		return exit.ExitCode()
	}
	if s.waitErr != nil {
		s.t.Fatal(s.waitErr)
	}
	return 0
}

func (s *exitRecordSession) exitLog() string {
	data, _ := os.ReadFile(codingagent.ExitLogPath(s.agentDir))
	return string(data)
}

func TestInteractiveExitBySignalLeavesALine(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
		code int
		line string
	}{
		{"SIGHUP", syscall.SIGHUP, 0, "received SIGHUP"},
		{"SIGTERM", syscall.SIGTERM, 0, "received SIGTERM"},
		{"SIGINT", syscall.SIGINT, 130, "received SIGINT (exit 130)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := startExitRecordSession(t, binary, t.TempDir(), true)
			if err := s.cmd.Process.Signal(tc.sig); err != nil {
				t.Fatal(err)
			}
			if code := s.exitCode(); code != tc.code {
				t.Fatalf("exit code = %d, want %d\n%s", code, tc.code, s.output.since(0))
			}
			if got := s.exitLog(); !strings.Contains(got, tc.line) || !strings.Contains(got, "pid=") {
				t.Fatalf("exit.log = %q, want a line with %q", got, tc.line)
			}
			if left := s.markers(); len(left) != 0 {
				t.Fatalf("an exit that left its own trace keeps no marker: %v", left)
			}
		})
	}
}

// The closed terminal Pi also exits on without a trace. Whether Linux reports it as a hang-up, an I/O error or the end of
// input, the line must say so.
func TestInteractiveExitOnAClosedTerminalLeavesALine(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name        string
		controlling bool
		wants       []string
	}{
		{"hang-up", true, []string{"received SIGHUP", "terminal gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := startExitRecordSession(t, binary, t.TempDir(), tc.controlling)
			if err := s.master.Close(); err != nil {
				t.Fatal(err)
			}
			code := s.exitCode()
			log := s.exitLog()
			if !slicesAnyContains(log, tc.wants) {
				t.Fatalf("exit code %d, exit.log = %q, want one of %q", code, log, tc.wants)
			}
			if left := s.markers(); len(left) != 0 {
				t.Fatalf("marker files: %v", left)
			}
			t.Logf("closed terminal (controlling=%v): exit %d, exit.log %q", tc.controlling, code, log)
		})
	}
}

// Pi registers no end handler on standard input, so a session whose input ends while its terminal lives keeps running; it
// exits 0 only when nothing else holds its event loop open. Issue #165: PiG used to exit 1 after printing EOF. The session
// reports in its debug log that it saw the end of input, and only a signal ends it afterwards.
func TestInteractiveKeepsRunningWhenInputEndsOnALiveTerminal(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	tmp := t.TempDir()
	s := startExitRecordSession(t, binary, t.TempDir(), false, "PIG_DEBUG=1", "TMPDIR="+tmp)
	if err := s.master.Close(); err != nil {
		t.Fatal(err)
	}
	debugLog := filepath.Join(tmp, "pig-debug.log")
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		if data, _ := os.ReadFile(debugLog); bytes.Contains(data, []byte("terminal input ended (EOF); the session keeps running without input")) {
			break
		}
		select {
		case <-s.exited:
			t.Fatalf("session exited after its input ended (exit.log %q)\n%s", s.exitLog(), s.output.since(0))
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session never saw the end of its input\n%s", s.output.since(0))
		}
	}
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("session gone before SIGTERM (exit.log %q): %v", s.exitLog(), err)
	}
	if code := s.exitCode(); code != 0 {
		t.Fatalf("exit code after SIGTERM = %d\n%s", code, s.output.since(0))
	}
	if got := strings.TrimSpace(s.exitLog()); strings.Count(got, "\n") != 0 || !strings.Contains(got, "received SIGTERM") {
		t.Fatalf("exit.log = %q, want only the SIGTERM line", got)
	}
}

func slicesAnyContains(text string, wants []string) bool {
	for _, want := range wants {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}

// SIGKILL is the exit no process can observe, as when macOS ends a session under memory pressure. The next start reports it.
func TestInteractiveKilledSessionIsReportedByTheNextStart(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	agentDir := t.TempDir()
	first := startExitRecordSession(t, binary, agentDir, true)
	pid := first.cmd.Process.Pid
	if err := first.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	first.exitCode()
	if len(first.markers()) != 1 || first.exitLog() != "" {
		t.Fatalf("after SIGKILL: markers=%v exit.log=%q", first.markers(), first.exitLog())
	}
	second := startExitRecordSession(t, binary, agentDir, true)
	if got := second.exitLog(); !strings.Contains(got, "pid="+itoa(pid)+" ") || !strings.Contains(got, "ended without recording an exit") {
		t.Fatalf("exit.log after the next start = %q", got)
	}
	if err := second.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	second.exitCode()
	if _, err := os.Stat(filepath.Join(agentDir, "crash-output", itoa(pid)+".log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the dead session's marker was not consumed: %v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
