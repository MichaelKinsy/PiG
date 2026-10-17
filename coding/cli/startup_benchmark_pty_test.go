//go:build linux

package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// upstream: packages/coding-agent/src/main.ts:964-978. PI_STARTUP_BENCHMARK runs interactiveMode.init(), waits 150 ms and
// calls interactiveMode.stop() with no argument, so the fullscreenExitOutput setting decides whether the fullscreen exit
// prints the transcript (interactive-mode.ts:7090, stopInteractiveTui :881-887). The process exits by itself: init
// rendered the session's messages (interactive-mode.ts:1110) and no input loop ran.
func TestStartupBenchmarkInitsAndStopsInteractiveMode(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	const leaveAlternateScreen = "\x1b[?1049l"
	for _, tc := range []struct {
		name, tuiMode, settings string
		transcriptAfterExit     bool
	}{
		{"regular", "regular", `{}`, false},
		{"fullscreen-transcript", "fullscreen", `{"fullscreenExitOutput":"transcript"}`, true},
		{"fullscreen-resume-hint", "fullscreen", `{"fullscreenExitOutput":"resume-hint"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session := writeResizeTestSession(t, 2)
			master, slave := openPTY(t, 30, 100)
			defer func() { _ = master.Close() }()
			cmd := exec.CommandContext(t.Context(), binary, "--model", "test-faux/faux-1", "--session", session, "--tui-mode", tc.tuiMode)
			cmd.Dir = t.TempDir()
			pigHome := t.TempDir()
			// An existing settings.json skips the first-time setup dialog (D88).
			if err := os.MkdirAll(filepath.Join(pigHome, "agent"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pigHome, "agent", "settings.json"), []byte(tc.settings), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(os.Environ(), "PIG_HOME="+pigHome, "PIG_CODING_AGENT_DIR="+filepath.Join(pigHome, "agent"),
				"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "TERM=xterm-256color", "PI_STARTUP_BENCHMARK=1")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}
			_ = slave.Close()
			output := &ptyOutput{}
			copied := make(chan struct{})
			go func() {
				_, _ = io.Copy(output, master)
				close(copied)
			}()
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			select {
			case err := <-exited:
				if err != nil {
					t.Fatalf("benchmark exit = %v, want success; output %q", err, output.since(0))
				}
			case <-time.After(testbudget.Wait(t)):
				_ = cmd.Process.Kill()
				<-exited
				t.Fatalf("the startup benchmark did not exit by itself; output %q", output.since(0))
			}
			<-copied
			got := string(output.since(0))
			if !strings.Contains(got, "Answer marker 1.") {
				t.Fatalf("init did not render the session messages: %q", got)
			}
			if i := strings.LastIndex(got, "\x1b[?25h"); i < strings.LastIndex(got, "Answer marker 1.") {
				t.Errorf("stop did not show the cursor after the last render: %q", got)
			}
			if tc.tuiMode == "fullscreen" {
				exit := strings.LastIndex(got, leaveAlternateScreen)
				if exit < 0 {
					t.Fatalf("stop did not leave the alternate screen: %q", got)
				}
				if after := strings.Contains(got[exit:], "Answer marker 1."); after != tc.transcriptAfterExit {
					t.Errorf("transcript printed after the fullscreen exit = %v, want %v: %q", after, tc.transcriptAfterExit, got[exit:])
				}
			}
		})
	}
}

// upstream: packages/coding-agent/src/main.ts:932-936. PI_STARTUP_BENCHMARK outside interactive mode is a red error on stderr
// and exit status 1, before any prompt runs.
func TestStartupBenchmarkRejectsNonInteractiveModes(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, args := range [][]string{{"-p", "hello"}, {"--mode", "json", "hello"}, {"--mode", "rpc"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			pigHome := t.TempDir()
			cmd := exec.CommandContext(t.Context(), binary, append([]string{"--model", "test-faux/faux-1", "--no-session"}, args...)...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "PIG_HOME="+pigHome, "PIG_CODING_AGENT_DIR="+filepath.Join(pigHome, "agent"),
				"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PI_STARTUP_BENCHMARK=yes")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr.ExitCode() != 1 {
				t.Fatalf("exit = %v, want status 1; stderr %q", err, stderr.String())
			}
			if want := "Error: PI_STARTUP_BENCHMARK only supports interactive mode\n"; stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", stdout.String())
			}
		})
	}
}
