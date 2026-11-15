//go:build integration && (linux || darwin)

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// releaseReaderOnClose makes master.Close close the descriptor in the kernel while a goroutine is blocked reading it. openHangupPTY calls master.Fd, which
// puts the descriptor in blocking mode; Close of a blocking file only marks it closed and leaves the descriptor open until the blocked Read returns, so on a
// Piglet Binary that is idle and writes nothing the terminal was never hung up and the process went on. With the descriptor non-blocking again the Read waits in
// the poller, which Close wakes.
func releaseReaderOnClose(t *testing.T, master *os.File) {
	t.Helper()
	raw, err := master.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var setErr error
	if err := raw.Control(func(fd uintptr) { setErr = unix.SetNonblock(int(fd), true) }); err != nil {
		t.Fatal(err)
	}
	if setErr != nil {
		t.Fatal(setErr)
	}
}

// listTemp is the names in dir, sorted.
func listTemp(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}

// fileHolds reports whether the file at path holds every needle.
func fileHolds(path string, needles ...string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, needle := range needles {
		if !strings.Contains(string(data), needle) {
			return false
		}
	}
	return true
}

// TestFusedExtensionLogInTempDirectory starts a Piglet Binary that fuses a Go extension on a pseudo-terminal, with a temporary directory of its own, and ends
// the session each way a session ends. The log that the fused extensions' SDK writes its diagnostics to (PIG_EXT_LOG, coding/extension/host/subprocess ext_log.go)
// is not in the temporary directory until a handler fails, holds the stack of the failure, and does not outlive the process: Pi writes no such file, and a
// listing of the temporary directory during a session, and after one that was closed with its terminal, showed one more file. The paths that end in os.Exit (a
// hung-up terminal, SIGINT) skipped the removal that the orderly shutdown does.
func TestFusedExtensionLogInTempDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a Piglet Binary")
	}
	binary := uiViewPigletBinary(t, "fused.yaml")
	ends := []struct {
		name string
		// exit is the status the session ends with, or -1 where the cell does not check it.
		exit int
		end  func(t *testing.T, cmd *exec.Cmd, master *os.File)
	}{
		{"hung-up terminal", 129, func(t *testing.T, _ *exec.Cmd, master *os.File) {
			if err := master.Close(); err != nil {
				t.Fatal(err)
			}
		}},
		{"SIGINT", -1, func(t *testing.T, cmd *exec.Cmd, _ *os.File) {
			if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
				t.Fatal(err)
			}
		}},
		{"SIGTERM", -1, func(t *testing.T, cmd *exec.Cmd, _ *os.File) {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
		}},
		{"/quit", -1, func(t *testing.T, _ *exec.Cmd, master *os.File) {
			if _, err := master.WriteString("/quit\r"); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range ends {
		t.Run(tc.name, func(t *testing.T) {
			home := uiViewShortHome(t)
			agent := filepath.Join(home, "agent")
			if err := os.MkdirAll(agent, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(uiViewModels), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"quietStartup":true}`), 0o644); err != nil {
				t.Fatal(err)
			}
			scratch := t.TempDir()
			master, slave, err := openHangupPTY()
			if err != nil {
				t.Fatalf("open pseudo-terminal: %v", err)
			}
			releaseReaderOnClose(t, master)
			defer func() { _ = master.Close() }()
			out := &syncBuffer{}
			readerDone := make(chan struct{})
			go func() {
				defer close(readerDone)
				buf := make([]byte, 32<<10)
				for {
					n, err := master.Read(buf)
					_, _ = out.Write(buf[:n])
					if err != nil {
						return
					}
				}
			}()

			cmd := exec.Command(binary, "--model", "test-faux/faux-1")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_UI_VIEW_FIXTURE_MOUNTS=1",
				"PIG_CODING_AGENT_DIR=", "PIG_CODING_AGENT_SESSION_DIR=", "PI_CODING_AGENT_DIR=", "PI_CODING_AGENT_SESSION_DIR=", "TMPDIR="+scratch)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			before := listTemp(t, scratch)
			if err := cmd.Start(); err != nil {
				_ = slave.Close()
				t.Fatalf("start the Piglet Binary: %v", err)
			}
			_ = slave.Close()
			waited := make(chan error, 1)
			go func() { waited <- cmd.Wait() }()
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					<-waited
				}
				<-readerDone
			}()

			// The fused extension is loaded and serving once its command answers. The command is sent again until the answer shows, because a key typed before the
			// editor takes input is lost.
			if !waitFor(startupReadyWait, func() bool { return out.contains("faux-1") }) {
				t.Fatal("the Piglet Binary never showed its footer")
			}
			if !waitFor(2*time.Minute, func() bool {
				if out.contains("viewfx-pong") {
					return true
				}
				_, _ = master.WriteString("/vping\r")
				time.Sleep(time.Second)
				return false
			}) {
				t.Fatal("the fused extension never answered its command")
			}
			// A session in which no handler failed has put nothing into the temporary directory, as Pi's puts nothing.
			if during := listTemp(t, scratch); !slices.Equal(during, before) {
				t.Errorf("the temporary directory held %q before the session and %q during it, with no handler failed", before, during)
			}
			// A failing handler makes the SDK create the log, which holds the stack that Pi never writes to the console.
			if _, err := master.WriteString("/vpanic\r"); err != nil {
				t.Fatal(err)
			}
			var logs []string
			if !waitFor(time.Minute, func() bool {
				logs, _ = filepath.Glob(filepath.Join(scratch, "pig-ext-fused-*.log"))
				return len(logs) == 1 && fileHolds(logs[0], "viewfx-probe-boom", "goroutine ")
			}) {
				t.Errorf("after a handler panicked the temporary directory holds %q, want one fused log with the stack", listTemp(t, scratch))
			}

			// The failed command has been handled once a later command is answered.
			if _, err := master.WriteString("/vping :after\r"); err != nil {
				t.Fatal(err)
			}
			if !waitFor(time.Minute, func() bool { return out.contains("viewfx-pong:after") }) {
				t.Fatal("the fused extension did not answer a command after the failed one")
			}

			tc.end(t, cmd, master)
			select {
			case <-waited:
			case <-time.After(30 * time.Second):
				t.Fatal("the Piglet Binary still running 30s after the session was ended")
			}
			if tc.exit >= 0 && cmd.ProcessState.ExitCode() != tc.exit {
				t.Errorf("the Piglet Binary exited with status %d, want %d", cmd.ProcessState.ExitCode(), tc.exit)
			}
			if after := listTemp(t, scratch); !slices.Equal(after, before) {
				t.Errorf("the temporary directory held %q before the session and %q after it", before, after)
			}
		})
	}
}
