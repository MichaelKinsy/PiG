//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSignalEmitsSessionShutdownToExtensions pins the contract that a
// termination signal gives extensions their cleanup event.
//
// Upstream's interactive signal handler calls shutdown({fromSignal: true}),
// which disposes the runtime: emitting session_shutdown: BEFORE touching the
// terminal, precisely so extension teardown cannot be skipped
// (interactive-mode.ts registerSignalHandlers).
//
// pig regressed this for SIGTERM: the process-wide handler cancelled the root
// context, and because extension subprocesses are spawned from that context,
// they were killed before the event could be delivered. Extensions silently
// never cleaned up, and shutdown also took ~6s instead of ~1s. SIGHUP was
// unaffected because it ran its own handler first.
func TestSignalEmitsSessionShutdownToExtensions(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH:", err)
	}
	bin := buildBinary(t)

	for _, tc := range []struct {
		name   string
		signal syscall.Signal
	}{
		{"SIGTERM", syscall.SIGTERM},
		{"SIGHUP", syscall.SIGHUP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "shutdown-marker")
			extPath := filepath.Join(dir, "shutdown-probe.mjs")
			if err := os.WriteFile(extPath, []byte(shutdownProbeExtension), 0o600); err != nil {
				t.Fatalf("write probe extension: %v", err)
			}

			session := "pig-sig-" + randID()
			// `exec` replaces the pane's shell with pig, so pane_pid is pig
			// itself and the signal cannot land on a wrapper process.
			command := strings.Join([]string{
				"cd", dir, "&&", "exec",
				"env", "PIG_HOME=" + dir, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_QUIET_STARTUP=1",
				"PIG_SHUTDOWN_MARKER=" + marker,
				bin, "--model", "test-faux/faux-1", "-e", extPath,
			}, " ")
			if out, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "120", "-y", "34", command).CombinedOutput(); err != nil {
				t.Fatalf("tmux new-session: %v\n%s", err, out)
			}
			t.Cleanup(func() { _ = tmuxCommand("kill-session", "-t", session).Run() })

			pid := waitForPanePID(t, session)
			// The footer paints before extension initialization. Wait for this extension's session_start status before sending a termination signal.
			waitForPaneText(t, session, "shutdown-probe-ready", 30*time.Second)

			if err := syscall.Kill(pid, tc.signal); err != nil {
				t.Fatalf("signal %s: %v", tc.signal, err)
			}

			// Upstream shuts down promptly; a multi-second wait here would hide
			// the teardown stall the ordering bug also caused.
			if !waitFor(3*time.Second, func() bool { return processExited(pid) }) {
				t.Fatalf("pig still running 3s after %s", tc.name)
			}
			if !waitFor(2*time.Second, func() bool {
				data, err := os.ReadFile(marker)
				return err == nil && strings.Contains(string(data), "session_shutdown")
			}) {
				data, _ := os.ReadFile(marker)
				t.Fatalf("extension never received session_shutdown on %s; marker=%q", tc.name, string(data))
			}
		})
	}
}

const shutdownProbeExtension = `import { appendFileSync } from "node:fs";
export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setStatus("shutdown-probe", "shutdown-probe-ready");
  });
  pi.on("session_shutdown", (event) => {
    try {
      appendFileSync(process.env.PIG_SHUTDOWN_MARKER, "session_shutdown reason=" + (event?.reason ?? "?") + "\n");
    } catch {}
  });
}
`

// TestInteractiveSigintTerminatesAndRestoresTerminal pins pig's SIGINT
// contract: terminate like upstream, but hand the terminal back first.
//
// Upstream registers no general SIGINT handler (interactive-mode.ts takes
// SIGINT only to ignore it while suspended), so an external SIGINT terminates
// pi. pig matches that. Ctrl+C never reaches this path: pig holds the terminal
// in raw mode, so \x03 is consumed by the keymap.
//
// Node's default handler exits without unwinding pi's terminal restore, which
// leaves ISIG off; measured against pi 0.84.0, `kill -INT` leaves the shell
// without a working Ctrl+C. pig restores the captured cooked state before
// exiting, which is the difference recorded as D51.
func TestInteractiveSigintTerminatesAndRestoresTerminal(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH:", err)
	}
	if _, err := exec.LookPath("stty"); err != nil {
		t.Skip("stty not on PATH:", err)
	}
	bin := buildBinary(t)
	dir := t.TempDir()

	session := "pig-sigint-" + randID()
	if out, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "120", "-y", "34").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = tmuxCommand("kill-session", "-t", session).Run() })

	// Keep a shell as the pane owner so the pty outlives pig and its terminal
	// state can be read after the signal.
	shellPID := waitForPanePID(t, session)
	tty := paneTTY(t, session)
	// A detached tmux server can seed a new pane from terminal flags left by
	// an earlier session. Prove the fixture starts raw, then establish the
	// cooked baseline whose restoration this test measures.
	if err := setSttyMode(tty, "raw"); err != nil {
		t.Fatalf("set raw terminal fixture: %v", err)
	}
	if isigEnabled(t, tty) {
		t.Fatal("stty raw fixture should disable ISIG")
	}
	if err := setSttyMode(tty, "sane"); err != nil {
		t.Fatalf("set sane terminal fixture: %v", err)
	}
	if !isigEnabled(t, tty) {
		settings, _ := sttyAll(tty)
		t.Fatalf("expected a sane terminal before pig starts; %s reports:\n%s", tty, settings)
	}

	launch := strings.Join([]string{
		"cd", dir, "&&",
		// The shell can change line discipline while reading this command. Set
		// the captured baseline immediately before Pig enters raw mode.
		"stty", "sane", "&&",
		"PIG_HOME=" + dir, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_QUIET_STARTUP=1",
		bin, "--model", "test-faux/faux-1", "--no-extensions",
	}, " ")
	if out, err := tmuxCommand("send-keys", "-t", session, launch, "Enter").CombinedOutput(); err != nil {
		t.Fatalf("launch pig: %v\n%s", err, out)
	}
	waitForPaneText(t, session, interactiveReadyMarker, 30*time.Second)

	pid := childOf(t, shellPID)
	if isigEnabled(t, tty) {
		t.Fatal("pig should hold the terminal in raw mode with ISIG disabled")
	}

	if err := syscall.Kill(pid, syscall.SIGINT); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}

	if !waitFor(5*time.Second, func() bool { return processExited(pid) }) {
		t.Fatal("pig survived SIGINT; upstream terminates, so pig must too")
	}
	if out, err := tmuxCommand("send-keys", "-t", session, "sleep 30", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("launch shell interrupt probe: %v\n%s", err, out)
	}
	sleepPID := childOf(t, shellPID)
	if out, err := tmuxCommand("send-keys", "-t", session, "C-c").CombinedOutput(); err != nil {
		t.Fatalf("send shell Ctrl+C: %v\n%s", err, out)
	}
	if !waitFor(5*time.Second, func() bool { return processExited(sleepPID) }) {
		t.Fatal("Pig exited on SIGINT without returning a working Ctrl+C to the shell")
	}
}

// paneTTY reports the pane's terminal device. tmux already knows it, so asking
// tmux avoids ps, whose -o tty= -p spelling a reduced ps does not accept.
func paneTTY(t *testing.T, session string) string {
	t.Helper()
	out, err := tmuxCommand("display-message", "-p", "-t", session, "#{pane_tty}").Output()
	if err != nil {
		t.Fatalf("resolve pane tty: %v", err)
	}
	tty := strings.TrimSpace(string(out))
	if tty == "" {
		t.Fatal("tmux reported an empty pane tty")
	}
	return tty
}

func childOf(t *testing.T, parent int) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("pgrep", "-P", fmt.Sprint(parent)).Output()
		if err == nil {
			fields := strings.Fields(string(out))
			if len(fields) > 0 {
				var pid int
				if _, scanErr := fmt.Sscanf(fields[0], "%d", &pid); scanErr == nil && pid > 0 {
					return pid
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no child of %d appeared", parent)
	return 0
}

func setSttyMode(tty, mode string) error {
	var firstErr error
	for _, flag := range []string{"-F", "-f"} {
		if err := exec.Command("stty", flag, tty, mode).Run(); err == nil {
			return nil
		} else if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// isigEnabled reports whether the terminal delivers Ctrl+C as a signal.
// sttyAll reads a terminal's settings. GNU coreutils selects the device with
// -F and BSD/macOS with -f, and neither accepts the other's spelling, so this
// test could not run on Linux while it hardcoded -f.
func sttyAll(tty string) ([]byte, error) {
	var firstErr error
	for _, flag := range []string{"-F", "-f"} {
		out, err := exec.Command("stty", flag, tty, "-a").Output()
		if err == nil {
			return out, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

func isigEnabled(t *testing.T, tty string) bool {
	t.Helper()
	out, err := sttyAll(tty)
	if err != nil {
		t.Fatalf("stty %s: %v", tty, err)
	}
	for _, field := range strings.Fields(string(out)) {
		if field == "-isig" {
			return false
		}
		if field == "isig" {
			return true
		}
	}
	t.Fatalf("stty output did not report isig:\n%s", out)
	return false
}

func waitForPanePID(t *testing.T, session string) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, err := tmuxCommand("list-panes", "-t", session, "-F", "#{pane_pid}").Output()
		if err == nil {
			var pid int
			if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid); scanErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("never resolved the pane pid")
	return 0
}

func waitForPaneText(t *testing.T, session, needle string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		out, err := tmuxCommand("capture-pane", "-p", "-t", session).Output()
		if err == nil {
			last = string(out)
			if strings.Contains(last, needle) {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("pane never showed %q; last capture:\n%s", needle, last)
}

func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

// processExited reports whether pid has exited. A process that has exited but
// that its parent has not yet reaped is a zombie: kill(pid, 0) still succeeds
// on it. pig runs as the tmux pane's process, so tmux does the reaping, and on
// a loaded runner that can lag the exit by seconds. Count a zombie as exited.
func processExited(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", fmt.Sprint(pid)).Output()
	if err != nil {
		// ps exits non-zero once the pid is gone.
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
