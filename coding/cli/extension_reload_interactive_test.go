//go:build linux

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// An extension's `await ctx.reload()` in the TUI of the real binary: the same log as the headless modes (TestExtensionReloadIsWiredInEveryHeadlessMode), where the old instance's command resolves after the reload and the new instance's session_start sees the reloaded state. Interactive quits through its own shutdown, so the log ends at "returned".
func TestExtensionReloadResolvesInInteractiveMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	binary := buildPigBinaryForSignalTest(t)
	for _, language := range []string{"node", "go"} {
		t.Run(language, func(t *testing.T) {
			extension := writeReloadProbe(t, language)
			home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "pig")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(t.TempDir(), "reload.log")
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1", "RELOAD_PROBE_LOG=" + logPath, "TERM=xterm-256color"}
			master, slave := openPTY(t, 40, 160)
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension, "--no-session", "--approve")
			cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			output := &ptyOutput{}
			go func() {
				buffer := make([]byte, 4096)
				for {
					n, err := master.Read(buffer)
					if n > 0 {
						_, _ = output.Write(buffer[:n])
					}
					if err != nil {
						return
					}
				}
			}()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = slave.Close()
			done := make(chan struct{})
			go func() { defer close(done); _ = cmd.Wait() }()
			t.Cleanup(func() { cancel(); <-done; _ = master.Close() })
			output.waitQuiet(0, []byte("faux-1"), 200*time.Millisecond, testbudget.Wait(t))
			if _, err := master.Write([]byte("/reloadme\r")); err != nil {
				t.Fatal(err)
			}
			want := strings.Join(wantReloadLog[:len(wantReloadLog)-1], "\n")
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				if data, _ := os.ReadFile(logPath); strings.TrimRight(string(data), "\n") == want {
					return
				}
				if time.Now().After(deadline) {
					data, _ := os.ReadFile(logPath)
					t.Fatalf("extension log:\n%s\nwant:\n%s\nscreen:\n%s", data, want, output.since(0))
				}
				select {
				case <-done:
					data, _ := os.ReadFile(logPath)
					t.Fatalf("pig exited early; log:\n%s\nscreen:\n%s", data, output.since(0))
				case <-time.After(50 * time.Millisecond):
				}
			}
		})
	}
}
