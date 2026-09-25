//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// openPTY allocates a Linux pseudo-terminal pair with the given size.
func openPTY(t *testing.T, rows, cols uint16) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pseudo-terminal: %v", err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pseudo-terminal number: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave: %v", err)
	}
	setPTYSize(t, master, rows, cols)
	return master, slave
}

func setPTYSize(t *testing.T, master *os.File, rows, cols uint16) {
	t.Helper()
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
		t.Fatalf("set pseudo-terminal size: %v", err)
	}
}

// ptyOutput collects everything the child writes to its terminal.
type ptyOutput struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	updated time.Time
}

func (o *ptyOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.updated = time.Now()
	return o.buf.Write(p)
}

func (o *ptyOutput) mark() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Len()
}

func (o *ptyOutput) since(mark int) []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return bytes.Clone(o.buf.Bytes()[mark:])
}

// waitQuiet returns once output after mark contains needle and has then stayed
// idle for quiet, or at deadline. Waiting for needle first keeps a pause in a
// slow render on a loaded machine from passing for the end of the render.
func (o *ptyOutput) waitQuiet(mark int, needle []byte, quiet, deadline time.Duration) {
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		o.mu.Lock()
		idle := bytes.Contains(o.buf.Bytes()[mark:], needle) && time.Since(o.updated) >= quiet
		o.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func writeResizeTestSession(t *testing.T, exchanges int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var lines bytes.Buffer
	encode := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		lines.Write(data)
		lines.WriteByte('\n')
	}
	encode(map[string]any{"type": "session", "version": 3, "id": "resize-test-session", "timestamp": "2026-09-23T00:00:00Z", "cwd": t.TempDir()})
	var parent any
	for i := range exchanges {
		userID, assistantID := fmt.Sprintf("u%07d", i), fmt.Sprintf("a%07d", i)
		encode(map[string]any{"type": "message", "id": userID, "parentId": parent, "timestamp": "2026-09-23T00:00:01Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Question %d", i)}}, "timestamp": 1790000000000 + 2*i}})
		encode(map[string]any{"type": "message", "id": assistantID, "parentId": userID, "timestamp": "2026-09-23T00:00:02Z", "message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Answer marker %d.\n\nA second paragraph keeps the transcript taller than the terminal.", i)}},
			"api": "faux", "provider": "test-faux", "model": "faux-1", "stopReason": "stop", "timestamp": 1790000000001 + 2*i,
			"usage": map[string]any{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
		}})
		parent = assistantID
	}
	if err := os.WriteFile(path, lines.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Pi's main.ts writes chalk.dim("No session selected") with console.log after
// selectSession resolves null. Keep stderr separate so a PTY cannot hide a
// wrong output stream, and wait for process exit to prove cancellation is final.
//
// chalk.dim emits SGR only when chalk's stdout color level is non-zero, so
// FORCE_COLOR=0 and TERM=dumb print plain text. NO_COLOR is not consulted by
// the pinned chalk 6.0.0 supports-color and keeps the faint codes.
func TestStartupResumeCancelUsesStdoutAndFaintStyle(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"color", []string{"TERM=xterm-256color"}, "\x1b[2mNo session selected\x1b[22m\r\n"},
		{"force-color-0", []string{"TERM=xterm-256color", "FORCE_COLOR=0"}, "No session selected\r\n"},
		{"term-dumb", []string{"TERM=dumb"}, "No session selected\r\n"},
		{"no-color", []string{"TERM=xterm-256color", "NO_COLOR=1"}, "\x1b[2mNo session selected\x1b[22m\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
				k, _, _ := strings.Cut(kv, "=")
				return slices.Contains([]string{"TERM", "FORCE_COLOR", "NO_COLOR", "CI", "COLORTERM", "TERM_PROGRAM", "TF_BUILD"}, k)
			})
			startupResumeCancel(t, binary, append(env, tc.env...), tc.want)
		})
	}
}

func startupResumeCancel(t *testing.T, binary string, env []string, want string) {
	home, agentDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	master, slave := openPTY(t, 35, 100)
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	cmd := exec.CommandContext(t.Context(), binary, "--no-extensions", "--session-dir", t.TempDir(), "--resume")
	cmd.Dir = t.TempDir()
	cmd.Env = append(slices.Clone(env), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir)
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var waitErr error
	exited := make(chan struct{})
	go func() {
		waitErr = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	output := &ptyOutput{}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, master)
		close(copied)
	}()
	t.Cleanup(func() {
		_ = master.Close()
		<-copied
	})
	ready := []byte("No sessions in current folder.")
	output.waitQuiet(0, ready, 100*time.Millisecond, testbudget.Wait(t))
	if !bytes.Contains(output.since(0), ready) {
		t.Fatalf("startup picker did not render: %q", output.since(0))
	}
	if _, err := master.Write([]byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Escape did not exit the startup picker")
	}
	<-copied
	if waitErr != nil {
		t.Fatalf("cancel exit = %v, want success; stderr = %q", waitErr, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("cancel stderr = %q, want empty", stderr.String())
	}
	if !bytes.HasSuffix(output.since(0), []byte(want)) {
		t.Errorf("cancel stdout = %q, want suffix %q", output.since(0), want)
	}
}

// Pi's TUI.start passes requestRender() as the terminal resize callback, and
// the main-screen renderer clears scrollback and replays the buffer only when
// the width or height changed. SIGWINCH without a geometry change (tmux
// window switches, terminal focus changes) must leave scrollback alone; a
// real height change still repaints fully.
func TestInteractiveSameSizeResizeKeepsScrollback(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	session := writeResizeTestSession(t, 60)
	master, slave := openPTY(t, 30, 100)
	defer func() { _ = master.Close() }()

	cmd := exec.Command(binary, "--model", "test-faux/faux-1", "--session", session)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	output := &ptyOutput{}
	go func() { _, _ = io.Copy(output, master) }()

	output.waitQuiet(0, []byte("Answer marker 59."), 500*time.Millisecond, testbudget.Wait(t))
	if startup := output.since(0); !bytes.Contains(startup, []byte("Answer marker 59.")) {
		t.Fatalf("the Session transcript did not render; last output: %q", startup[max(0, len(startup)-400):])
	}

	mark := output.mark()
	for range 3 {
		if err := cmd.Process.Signal(syscall.SIGWINCH); err != nil {
			t.Fatalf("send SIGWINCH: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// A same-size SIGWINCH should produce no repaint, so allow a fixed settle
	// period far longer than the 16 ms render throttle.
	time.Sleep(1500 * time.Millisecond)
	if same := output.since(mark); bytes.Contains(same, []byte("\x1b[3J")) || bytes.Contains(same, []byte("\x1b[2J")) {
		t.Fatalf("same-size SIGWINCH cleared the screen and replayed %d bytes", len(same))
	}

	mark = output.mark()
	setPTYSize(t, master, 24, 100)
	output.waitQuiet(mark, []byte("\x1b[3J"), 500*time.Millisecond, testbudget.Wait(t))
	if changed := output.since(mark); !bytes.Contains(changed, []byte("\x1b[3J")) {
		t.Fatalf("height change did not repaint the buffer; got %d bytes", len(changed))
	}
}
