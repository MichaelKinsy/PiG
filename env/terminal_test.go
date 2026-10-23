//go:build !windows

package env

// Ports Pi 1.2.0 packages/env/test/terminal.test.ts (describe "openTerminal").
//
// The terminals are Unix only: Windows answers ENOTSUP, as the upstream suite skips them there.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const terminalTestTimeout = 20 * time.Second

func terminalConnection(t *testing.T) (*Connection, string) {
	t.Helper()
	connection := NewConnection(ConnectionOptions{Command: []string{terminalDaemon}})
	t.Cleanup(connection.Close)
	return connection, t.TempDir()
}

// collector collects output and waits for text to appear in it.
type collector struct {
	mu      sync.Mutex
	data    []byte
	changed chan struct{}
}

func newCollector() *collector { return &collector{changed: make(chan struct{})} }

func (c *collector) onData(data []byte) {
	c.mu.Lock()
	c.data = append(c.data, data...)
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
}

func (c *collector) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Clone(c.data)
}

func (c *collector) waitFor(t *testing.T, text string) {
	t.Helper()
	c.waitUntil(t, fmt.Sprintf("%q", text), func(data []byte) bool { return bytes.Contains(data, []byte(text)) })
}

// waitForAfter waits for text to appear in the output after the first occurrence of marker.
func (c *collector) waitForAfter(t *testing.T, marker, text string) {
	t.Helper()
	c.waitUntil(t, fmt.Sprintf("%q after %q", text, marker), func(data []byte) bool {
		at := bytes.Index(data, []byte(marker))
		return at >= 0 && bytes.Contains(data[at+len(marker):], []byte(text))
	})
}

// waitUntil waits for found to hold of the output so far; what names the awaited output in the failure.
func (c *collector) waitUntil(t *testing.T, what string, found func(data []byte) bool) {
	t.Helper()
	deadline := time.NewTimer(terminalTestTimeout)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		done := found(c.data)
		changed := c.changed
		c.mu.Unlock()
		if done {
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatalf("output never contained %s: %q", what, c.bytes())
		}
	}
}

// waitForPid waits for output matching pattern and returns the number in its first group.
func (c *collector) waitForPid(t *testing.T, pattern *regexp.Regexp) int {
	t.Helper()
	deadline := time.NewTimer(terminalTestTimeout)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		match := pattern.FindSubmatch(c.data)
		changed := c.changed
		c.mu.Unlock()
		if match != nil {
			pid, err := strconv.Atoi(string(match[1]))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatalf("output never matched %v: %q", pattern, c.bytes())
		}
	}
}

// waitExit waits for the terminal to end and returns what Exited reports.
func waitExit(t *testing.T, terminal *RemoteTerminal) (int, error) {
	t.Helper()
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := terminal.Exited()
		done <- result{code, err}
	}()
	select {
	case r := <-done:
		return r.code, r.err
	case <-time.After(terminalTestTimeout):
		t.Fatal("the terminal did not end")
		return 0, nil
	}
}

func remoteCode(err error) string {
	if remote, ok := errors.AsType[*RemoteError](err); ok {
		return remote.Code
	}
	return fmt.Sprint(err)
}

// Pi 1.2.0 packages/env/test/terminal.test.ts:51 "runs a program in a terminal with input, resizing and its exit code"
// Pi source: packages/env/src/terminal.ts:94 (openTerminal), packages/env/src/terminal.ts:31 (pid),
// packages/env/src/terminal.ts:33 (exited), packages/env/src/terminal.ts:56 (write), packages/env/src/terminal.ts:64 (resize) and the options at
// packages/env/src/terminal.ts:8-17 (cwd, argv, env, cols, rows, onData).
func TestOpenTerminalRunsAProgramInATerminalWithInputResizingAndItsExitCode(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		output := newCollector()
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			Cwd: cwd, Argv: []string{"/bin/sh"}, Env: map[string]string{"PS1": "$ ", "TERM": "xterm-256color"},
			Cols: 80, Rows: 24, OnData: output.onData,
		})
		if err != nil {
			t.Fatal(err)
		}
		if terminal.Pid <= 0 {
			t.Fatalf("pid = %d", terminal.Pid)
		}
		terminal.Write("test -t 0 && echo IS-A-TTY; stty size; echo $TERM\n")
		output.waitFor(t, "IS-A-TTY")
		output.waitFor(t, "24 80")
		output.waitFor(t, "xterm-256color")
		// The shell's next prompt, so the resize lands while the shell waits for input. macOS's /bin/sh is bash 3.2, and its readline
		// prepares the terminal before every prompt by reading the window size and writing the value it read back
		// (lib/readline/rltty.c set_winsize). A resize that arrives between that read and that write is overwritten with the old size
		// by the shell, not dropped by the daemon: the daemon's TIOCSWINSZ succeeds and the size reads old again. The prompt is
		// printed after the preparation (readline.c readline: rl_prep_terminal, then the prompt), so waiting for it ends the window.
		// Pi's test resizes as soon as the output arrives and has the same window.
		output.waitForAfter(t, "xterm-256color", "$ ")
		terminal.Resize(100, 30)
		terminal.Write("stty size\n")
		output.waitFor(t, "30 100")
		terminal.Write("exit 3\n")
		if code, err := waitExit(t, terminal); err != nil || code != 3 {
			t.Fatalf("exit = %d, %v", code, err)
		}
	})
}

// eventually polls what only the system can tell, with no event to wait for.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(terminalTestTimeout)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Pi 1.2.0 packages/env/test/terminal.test.ts:74 "reports the job in the foreground, not the shell at its prompt"
// Pi source: packages/env/src/terminal.ts:77 (foreground), packages/env/src/terminal.ts:22 (TerminalForeground.pid)
// and packages/env/src/terminal.ts:23 (TerminalForeground.name).
func TestOpenTerminalReportsTheJobInTheForegroundNotTheShellAtItsPrompt(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		output := newCollector()
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			Cwd: cwd,
			// Interactive, for job control: the shell puts each job in front of the terminal.
			Argv: []string{"/bin/sh", "-i"}, Env: map[string]string{"PS1": "$ ", "TERM": "xterm-256color"},
			Cols: 80, Rows: 24, OnData: output.onData,
		})
		if err != nil {
			t.Fatal(err)
		}
		terminal.Write("echo READY\n")
		output.waitFor(t, "READY")
		if foreground, err := terminal.Foreground(background); err != nil || foreground != nil {
			t.Fatalf("foreground at the prompt = %+v, %v", foreground, err)
		}
		terminal.Write("sleep 100\n")
		var foreground *TerminalForeground
		eventually(t, "the job coming to the foreground", func() bool {
			var err error
			foreground, err = terminal.Foreground(background)
			if err != nil {
				t.Fatal(err)
			}
			return foreground != nil
		})
		if foreground.Pid <= 0 || foreground.Pid == terminal.Pid {
			t.Fatalf("foreground = %+v, terminal pid %d", foreground, terminal.Pid)
		}
		// Pi checks the name on darwin and linux only. The shell's child takes the terminal (tcsetpgrp) before it
		// execs sleep, and pty.rs reads the name with proc_name or /proc/<pid>/comm, so for a moment the job's group
		// still names the shell (Pi's daemon and this one alike; seen here as "bash" from macOS's /bin/sh). Wait for
		// that same group to finish its exec, an observable condition, instead of racing it; a name other than
		// "sleep" at the deadline still fails.
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			job := foreground
			eventually(t, fmt.Sprintf("the foreground job %+v naming sleep", job), func() bool {
				var err error
				foreground, err = terminal.Foreground(background)
				if err != nil {
					t.Fatal(err)
				}
				if foreground == nil || foreground.Pid != job.Pid {
					t.Fatalf("foreground changed from %+v to %+v", job, foreground)
				}
				return foreground.Name == "sleep"
			})
		}
		terminal.Close()
		if _, err := waitExit(t, terminal); remoteCode(err) != "aborted" {
			t.Fatalf("exit error = %v", err)
		}
		if _, err := terminal.Foreground(background); remoteCode(err) != "EBADF" {
			t.Fatalf("foreground after the end: %v", err)
		}
	})
}

// Pi 1.2.0 packages/env/test/terminal.test.ts:103 "delivers raw bytes, not decoded text"
// Pi source: packages/env/src/terminal.ts:17 (onData), packages/env/src/terminal.ts:114 (output events).
func TestOpenTerminalDeliversRawBytesNotDecodedText(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		output := newCollector()
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			Cwd: cwd, Argv: []string{"sh", "-c", `printf 'a\377b'`}, Cols: 80, Rows: 24, OnData: output.onData,
		})
		if err != nil {
			t.Fatal(err)
		}
		if code, err := waitExit(t, terminal); err != nil || code != 0 {
			t.Fatalf("exit = %d, %v", code, err)
		}
		if got := output.bytes(); !bytes.Equal(got, []byte{0x61, 0xff, 0x62}) {
			t.Fatalf("output = %v", got)
		}
	})
}

// Pi 1.2.0 packages/env/test/terminal.test.ts:117 "keeps input in order"
// Pi source: packages/env/src/terminal.ts:56 (write).
func TestOpenTerminalKeepsInputInOrder(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			Cwd: cwd, Argv: []string{"sh", "-c", "head -c 40000 > input.txt"}, Cols: 80, Rows: 24, OnData: func([]byte) {},
		})
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for index := range 4000 {
			piece := fmt.Sprintf("%09d\n", index)
			text.WriteString(piece)
			terminal.Write(piece)
		}
		if code, err := waitExit(t, terminal); err != nil || code != 0 {
			t.Fatalf("exit = %d, %v", code, err)
		}
		written, err := os.ReadFile(filepath.Join(cwd, "input.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(written) != text.String() {
			t.Fatalf("input arrived out of order or short: %d bytes of %d", len(written), text.Len())
		}
	})
}

// Pi 1.2.0 packages/env/test/terminal.test.ts:136 "kills the program on close"
// Pi source: packages/env/src/terminal.ts:69 (close).
func TestOpenTerminalKillsTheProgramOnClose(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			Cwd: cwd, Argv: []string{"sleep", "100"}, Cols: 80, Rows: 24, OnData: func([]byte) {},
		})
		if err != nil {
			t.Fatal(err)
		}
		terminal.Close()
		if _, err := waitExit(t, terminal); remoteCode(err) != "aborted" {
			t.Fatalf("exit error = %v", err)
		}
	})
}

// Pi 1.2.0 packages/env/test/terminal.test.ts:149 "fails to open in a missing directory"
// Pi source: packages/env/src/terminal.ts:94 (openTerminal), packages/env/src/terminal.ts:125 (a failure before it started rejects the open).
func TestOpenTerminalFailsToOpenInAMissingDirectory(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		_, err := OpenTerminal(background, connection, TerminalOptions{Cwd: filepath.Join(cwd, "missing"), Cols: 80, Rows: 24, OnData: func([]byte) {}})
		if _, ok := errors.AsType[*RemoteError](err); !ok {
			t.Fatalf("error = %T %v", err, err)
		}
	})
}

// Pi 1.2.0 packages/env/test/terminal.test.ts:156 "rejects writes to a terminal that does not exist"
func TestPtyWriteRejectsATerminalThatDoesNotExist(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, _ := terminalConnection(t)
		_, err := connection.Request(background, "ptyWrite", Json{"target": 999}, RequestOptions{})
		if remoteCode(err) != "EBADF" {
			t.Fatalf("error = %v", err)
		}
	})
}

// Not in Pi's suite: the protocol's size limits, the default login shell, and `ptyWrite` to a missing target.
func TestPtyRequestsAreValidated(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		for name, request := range map[string]Json{
			"zero columns":      {"cwd": cwd, "cols": 0, "rows": 24},
			"too many rows":     {"cwd": cwd, "cols": 80, "rows": 4097},
			"missing size":      {"cwd": cwd},
			"empty argv":        {"cwd": cwd, "cols": 80, "rows": 24, "argv": []string{}},
			"argv with numbers": {"cwd": cwd, "cols": 80, "rows": 24, "argv": []any{"sh", 1}},
			"no cwd":            {"cols": 80, "rows": 24},
		} {
			if _, err := connection.Request(background, "pty", request, RequestOptions{}); remoteCode(err) != "EINVAL" {
				t.Errorf("%s: error = %v", name, err)
			}
		}
		for _, op := range []string{"ptyWrite", "ptyResize", "ptyForeground"} {
			if _, err := connection.Request(background, op, Json{"target": 12345, "cols": 80, "rows": 24}, RequestOptions{}); remoteCode(err) != "EBADF" {
				t.Errorf("%s: error = %v", op, err)
			}
			if _, err := connection.Request(background, op, Json{}, RequestOptions{}); remoteCode(err) != "EINVAL" || err.Error() != "missing field target" {
				t.Errorf("%s without target: error = %v", op, err)
			}
		}
		// A resize beyond the limits fails on a running terminal, which is not shut down by it.
		terminal, err := OpenTerminal(background, connection, TerminalOptions{Cwd: cwd, Argv: []string{"sleep", "100"}, Cols: 80, Rows: 24, OnData: func([]byte) {}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = connection.Request(background, "ptyResize", Json{"target": terminal.id, "cols": 0, "rows": 24}, RequestOptions{Session: terminal.session})
		if remoteCode(err) != "EINVAL" {
			t.Errorf("resize to zero columns: error = %v", err)
		}
		if foreground, err := terminal.Foreground(background); err != nil || foreground != nil {
			t.Errorf("terminal after the bad resize: %+v, %v", foreground, err)
		}
	})
}

// Pi source: packages/env/src/terminal.ts:10 (argv), packages/env/src/terminal.ts:12 (env) and packages/env/src/terminal.ts:13 (inheritEnv): without argv the daemon runs
// the shell as a login shell, and inheritEnv false leaves the program only the given environment.
func TestOpenTerminalRunsTheUsersShellAsALoginShellByDefault(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		output := newCollector()
		inherit := false
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			Cwd: cwd, Env: map[string]string{"SHELL": "/bin/sh", "PS1": "", "TERM": "dumb"}, InheritEnv: &inherit,
			Cols: 80, Rows: 24, OnData: output.onData,
		})
		if err != nil {
			t.Fatal(err)
		}
		terminal.Write("echo \"arg0=$0 term=$TERM home=${HOME-unset}\"; exit 5\n")
		// On macOS both daemons' login shell gets the user's HOME back (observed for Pi's and PiG's daemon alike); elsewhere it stays unset.
		home := "unset"
		if runtime.GOOS == "darwin" {
			home = os.Getenv("HOME")
		}
		output.waitFor(t, "arg0=-sh term=dumb home="+home)
		if code, err := waitExit(t, terminal); err != nil || code != 5 {
			t.Fatalf("exit = %d, %v", code, err)
		}
	})
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// The daemon kills the process group of a terminal on close and on a lost connection: a program the shell started
// does not outlive the terminal.
func TestOpenTerminalKillsEverythingInTheSessionOnCloseAndOnALostConnection(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		for _, via := range []string{"close", "connection"} {
			t.Run(via, func(t *testing.T) {
				connection, cwd := terminalConnection(t)
				output := newCollector()
				script := `sleep 100 & printf 'child:%s:\n' $!; wait`
				if via == "close" {
					// The child ignores SIGHUP, so the hangup of the shell's death does not end it: only the kill of the
					// group does. A lost connection may end the daemon before it kills anything; then only the hangup
					// ends the session, in Pi's daemon too.
					script = "trap '' HUP; " + script
				}
				terminal, err := OpenTerminal(background, connection, TerminalOptions{
					// Not interactive, so the child stays in the shell's process group, which is what the daemon kills.
					Cwd: cwd, Argv: []string{"/bin/sh", "-c", script}, Cols: 80, Rows: 24, OnData: output.onData,
				})
				if err != nil {
					t.Fatal(err)
				}
				child := output.waitForPid(t, regexp.MustCompile(`child:(\d+):`))
				if !processExists(child) || !processExists(terminal.Pid) {
					t.Fatal("the programs did not start")
				}
				if via == "close" {
					terminal.Close()
				} else {
					connection.Close()
				}
				_, _ = waitExit(t, terminal)
				eventually(t, "the shell and its child ending", func() bool {
					// A killed child is reparented and reaped by init, so it can linger as a zombie for a moment.
					return !processExists(terminal.Pid) && !runningProcess(child)
				})
			})
		}
	})
}

// pty.rs ends the request once the program exited and its terminal stayed silent for EXIT_GRACE (100 ms): a background
// job that still holds the terminal does not keep it running, and is not killed.
func TestOpenTerminalEndsWhenTheProgramExitsThoughABackgroundJobHoldsTheTerminal(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		output := newCollector()
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			// The job ignores the hangup the shell's exit sends, so it keeps the terminal open.
			Cwd: cwd, Argv: []string{"/bin/sh", "-c", `trap '' HUP; sleep 60 & printf 'job:%s:\n' $!; exit 3`}, Cols: 80, Rows: 24, OnData: output.onData,
		})
		if err != nil {
			t.Fatal(err)
		}
		job := output.waitForPid(t, regexp.MustCompile(`job:(\d+):`))
		t.Cleanup(func() { _ = syscall.Kill(job, syscall.SIGKILL) })
		started := time.Now()
		if code, err := waitExit(t, terminal); err != nil || code != 3 {
			t.Fatalf("exit = %d, %v", code, err)
		}
		if elapsed := time.Since(started); elapsed > 10*time.Second {
			t.Fatalf("the terminal ended %v after the program, with its job still running", elapsed)
		}
		if !runningProcess(job) {
			t.Fatal("the background job did not outlive the terminal")
		}
	})
}

// runningProcess is whether pid exists and is not a zombie waiting to be reaped.
func runningProcess(pid int) bool {
	if !processExists(pid) {
		return false
	}
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	fields := strings.Fields(string(status[strings.LastIndex(string(status), ")")+1:]))
	return len(fields) == 0 || fields[0] != "Z"
}

// Not in Pi's suite. Pi source: packages/env/src/terminal.ts:39 (the constructor), packages/env/src/terminal.ts:56 (write takes bytes or text).
func TestNewRemoteTerminalKeepsWhatItIsGivenAndWriteTakesBytesOrText(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		output := newCollector()
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			Cwd: cwd, Argv: []string{"/bin/sh"}, Env: map[string]string{"PS1": "", "TERM": "dumb"}, Cols: 80, Rows: 24, OnData: output.onData,
		})
		if err != nil {
			t.Fatal(err)
		}
		terminal.Write([]byte("echo BYTES-$((1+1))\n"))
		output.waitFor(t, "BYTES-2")
		terminal.Write("echo TEXT-é-$((2+2))\n")
		output.waitFor(t, "TEXT-é-4")
		terminal.Write(42) // not a string or bytes: nothing is sent
		terminal.Write("echo AFTER-$((3+3))\n")
		output.waitFor(t, "AFTER-6")
		if got := string(output.bytes()); strings.Contains(got, "42") {
			t.Fatalf("a value that is neither text nor bytes was sent: %q", got)
		}

		aborted := make(chan struct{})
		settled := make(chan TerminalExit, 1)
		settled <- TerminalExit{ExitCode: 7}
		rebuilt := NewRemoteTerminal(connection, terminal.id, terminal.session, terminal.Pid+1, settled, func() { close(aborted) })
		if rebuilt.Pid != terminal.Pid+1 {
			t.Errorf("Pid = %d, want the pid given", rebuilt.Pid)
		}
		for range 2 { // upstream's promise can be awaited again
			if code, err := rebuilt.Exited(); code != 7 || err != nil {
				t.Errorf("Exited() = %d, %v; want the settlement given", code, err)
			}
		}
		rebuilt.Close()
		select {
		case <-aborted:
		default:
			t.Error("Close did not call the abort it was given")
		}
		// The terminal built over the running request reaches it: its input arrives in the shell.
		rebuilt.Write("exit 9\n")
		if code, err := waitExit(t, terminal); err != nil || code != 9 {
			t.Fatalf("exit = %d, %v", code, err)
		}
	})
}

// Not in Pi's suite. Pi source: packages/env/src/terminal.ts:3 (WRITE_CHUNK) and packages/env/src/terminal.ts:56 (write): input larger than a frame
// (16 MiB, connection.ts and docs/protocol.md) goes to the daemon in several writes, which arrive whole and in order.
func TestOpenTerminalWriteOfMoreThanAFrameArrivesWholeAndInOrder(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		output := newCollector()
		size := maxFrame + 3*terminalWriteChunk/2 + 7 // more than one frame, not a multiple of the chunk
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			Cwd: cwd, Cols: 80, Rows: 24, OnData: output.onData,
			// Raw mode passes every byte, including the ones a cooked terminal treats as controls; READY says it is on.
			Argv: []string{"sh", "-c", fmt.Sprintf("stty raw -echo; echo READY; head -c %d > input.bin", size)},
		})
		if err != nil {
			t.Fatal(err)
		}
		output.waitFor(t, "READY")
		data := make([]byte, size)
		for index := range data {
			data[index] = byte(index*131 + index>>8) // every byte value, in a pattern a lost or swapped chunk breaks
		}
		terminal.Write(data)
		if code, err := waitExit(t, terminal); err != nil || code != 0 {
			t.Fatalf("exit = %d, %v", code, err)
		}
		written, err := os.ReadFile(filepath.Join(cwd, "input.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if len(written) != len(data) {
			t.Fatalf("%d bytes arrived of %d", len(written), len(data))
		}
		for index := range data {
			if written[index] != data[index] {
				t.Fatalf("byte %d is %#x, want %#x (chunk %d)", index, written[index], data[index], index/terminalWriteChunk)
			}
		}
	})
}

// Go addition: OpenTerminal takes a context, which openTerminal (packages/env/src/terminal.ts:93-127) does not. Cancelling
// it before the started event abandons the open with the context's error and cancels the pty request, so the daemon
// kills the program that was starting.
// mutation-checked: dropping cancel() from the ctx.Done branch of OpenTerminal leaves the program running and fails it.
func TestOpenTerminalCancelledBeforeItStartedAbandonsTheOpenAndKillsTheProgram(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		pidFile := filepath.Join(cwd, "pid")
		ctx, cancel := context.WithCancel(background)
		cancel()
		// The context is done before the request leaves, so it wins over a started event that needs a round trip.
		terminal, err := OpenTerminal(ctx, connection, TerminalOptions{
			Cwd: cwd, Argv: []string{"sh", "-c", `echo $$ > pid.tmp && mv pid.tmp pid && exec sleep 100`}, Cols: 80, Rows: 24, OnData: func([]byte) {},
		})
		if terminal != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("OpenTerminal = %v, %v; want nil, context.Canceled", terminal, err)
		}
		// The daemon may cancel the request before the program runs; then no pid file appears and nothing is left.
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if text, readErr := os.ReadFile(pidFile); readErr == nil {
				pid := must(strconv.Atoi(strings.TrimSpace(string(text))))
				eventually(t, "the program of the abandoned open is killed", func() bool { return !processExists(pid) })
				return
			}
		}
	})
}

// Go addition: a pty request that ends without a started event fails the open with `The terminal ended before it
// started`. In openTerminal (packages/env/src/terminal.ts:93-127) `started` only settles from the started event
// (:116) or a rejection of the request (:125), so a successful reply without started leaves the open pending forever.
// The scripted daemon answers the hello, then the pty request (id 2) with {"exitCode":0} and no event.
// mutation-checked: waiting on ctx (never done here) before returning the error in the call.done branch of OpenTerminal, as Pi's open stays pending, fails it.
func TestOpenTerminalFailsWhenTheRequestEndsWithoutStarting(t *testing.T) {
	ptyResult := `printf '\000\000\000\027\002\000\000\000\002\000\000\000\016{"exitCode":0}'; `
	connection := scriptedConnection(t, syncLine+helloResult+`sleep 1; `+ptyResult+`exec sleep 30`, nil)
	done := make(chan error, 1)
	go func() {
		terminal, err := OpenTerminal(background, connection, TerminalOptions{Cwd: "/", Cols: 80, Rows: 24, OnData: func([]byte) {}})
		if terminal != nil {
			err = fmt.Errorf("OpenTerminal returned a terminal %v with %w", terminal, err)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || err.Error() != "The terminal ended before it started" {
			t.Fatalf("OpenTerminal error = %v, want `The terminal ended before it started`", err)
		}
	case <-time.After(terminalTestTimeout):
		t.Fatal("OpenTerminal did not return after the request ended")
	}
}
