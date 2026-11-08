//go:build !windows

package env

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// stdioHolders is a daemon process whose stdin and stdout (and stderr, which shares stdout's open file description, as
// under 2>&1) are pipes the test also holds: a second holder of each open file description, as a parent shell or a
// wrapper is. The daemon moves its stdin and stdout to the runtime's poller while it runs, which makes the descriptions
// non-blocking for every holder; it must give them back however it ends.
type stdioHolders struct {
	t      *testing.T
	cmd    *exec.Cmd
	input  *os.File // the client's end of stdin; closing it ends the daemon's input
	holdIn int      // a duplicate of the daemon's stdin
	// holdOut is a duplicate of the daemon's stdout, which is also its stderr.
	holdOut int
}

func startStdioHolders(t *testing.T, environment ...string) *stdioHolders {
	t.Helper()
	var in, out [2]int
	for _, fds := range []*[2]int{&in, &out} {
		if err := unix.Pipe(fds[:]); err != nil {
			t.Fatal(err)
		}
		unix.CloseOnExec(fds[0])
		unix.CloseOnExec(fds[1])
	}
	daemonIn, daemonOut := os.NewFile(uintptr(in[0]), "daemon stdin"), os.NewFile(uintptr(out[1]), "daemon stdout")
	h := &stdioHolders{t: t, input: os.NewFile(uintptr(in[1]), "client"), holdIn: dup(t, in[0]), holdOut: dup(t, out[1])}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h.cmd = exec.Command(executable, "serve", "--token", "tok")
	h.cmd.Stdin, h.cmd.Stdout, h.cmd.Stderr = daemonIn, daemonOut, daemonOut
	h.cmd.Env = append(os.Environ(), environment...)
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = daemonIn.Close()
	_ = daemonOut.Close()
	reader := os.NewFile(uintptr(out[0]), "client output")
	t.Cleanup(func() {
		_ = h.cmd.Process.Kill()
		_ = h.cmd.Wait()
		_ = h.input.Close()
		_ = reader.Close()
		_ = unix.Close(h.holdIn)
		_ = unix.Close(h.holdOut)
	})
	_ = reader.SetReadDeadline(time.Now().Add(30 * time.Second))
	if line, err := bufio.NewReader(reader).ReadString('\n'); err != nil || line != "PI-ENV tok\n" {
		t.Fatalf("sync line %q: %v", line, err)
	}
	return h
}

func dup(t *testing.T, fd int) int {
	t.Helper()
	next, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func nonblocking(t *testing.T, fd int) bool {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	return flags&unix.O_NONBLOCK != 0
}

// requireModes checks both descriptions' mode as the other holder sees it.
func (h *stdioHolders) requireModes(when string, want bool) {
	h.t.Helper()
	if got := nonblocking(h.t, h.holdIn); got != want {
		h.t.Errorf("%s: stdin's open file description is non-blocking=%v, want %v", when, got, want)
	}
	if got := nonblocking(h.t, h.holdOut); got != want {
		h.t.Errorf("%s: stdout's open file description is non-blocking=%v, want %v", when, got, want)
	}
}

// TestDaemonGivesBackBlockingStdioOnEveryExit: the daemon waits on its inherited pipes through the runtime poller, which
// sets O_NONBLOCK on open file descriptions that a parent shell, a wrapper or a 2>&1 stderr also holds. The mode is back
// when the process ends by input EOF, by SIGTERM (os.Exit skips defers) and by the silence limit.
func TestDaemonGivesBackBlockingStdioOnEveryExit(t *testing.T) {
	for _, test := range []struct {
		name        string
		environment []string
		end         func(*stdioHolders)
		wantCode    int
	}{
		{"input ends", nil, func(h *stdioHolders) { _ = h.input.Close() }, 0},
		{"SIGTERM", nil, func(h *stdioHolders) { _ = h.cmd.Process.Signal(syscall.SIGTERM) }, 128 + int(syscall.SIGTERM)},
		{"silence", []string{"PI_ENV_TEST_SILENCE_MS=300"}, func(*stdioHolders) {}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := startStdioHolders(t, test.environment...)
			// The premise: while the daemon runs, the other holder sees the descriptions non-blocking.
			h.requireModes("running", true)
			test.end(h)
			done := make(chan error, 1)
			go func() { done <- h.cmd.Wait() }()
			select {
			case err := <-done:
				code := 0
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					code = exit.ExitCode()
				} else if err != nil {
					t.Fatal(err)
				}
				if code != test.wantCode {
					t.Fatalf("exit code %d, want %d", code, test.wantCode)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the daemon did not end")
			}
			h.requireModes("after exit", false)
		})
	}
}
