package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// RPC mode ends with os.Exit, as Pi's process.exit does, after it killed the extension processes; Runtime.Close and Host.Shutdown, which remove the Host's private socket directory, never run on that path. A pig that ends normally, on the end of its stdin or on SIGTERM, leaves nothing in the socket directory.
func TestAnRPCPigThatEndsNormallyLeavesNoSocketDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Python extensions")
	}
	for _, test := range []struct {
		name string
		end  func(t *testing.T, p *rpcProcess, wait time.Duration)
	}{
		{"stdin end", func(t *testing.T, p *rpcProcess, _ time.Duration) {
			p.closeAndWait("an RPC pig that ends with its stdin")
		}},
		{"SIGTERM", func(t *testing.T, p *rpcProcess, wait time.Duration) {
			if runtime.GOOS == "windows" {
				t.Skip("Windows has no SIGTERM to deliver")
			}
			if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			waited := make(chan error, 1)
			go func() { waited <- p.cmd.Wait() }()
			select {
			case <-waited:
				p.exited = true
			case <-time.After(wait):
				t.Fatalf("the RPC pig did not end after SIGTERM within %s\n%s", wait, p.stderr.String())
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range map[string]string{
				"lo_a.py":      pythonModuleStateProbe("lo_a", false, "lo_helper"),
				"lo_helper.py": "VERSION = \"v1\"\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			home, cwd, sessionDir, probeLog := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "pig")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			socketBase := testenv.ShortTempDir(t, "pig-sock-")
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1",
				"RELOAD_PROBE_LOG=" + probeLog, "XDG_RUNTIME_DIR=" + socketBase, "TMPDIR=" + socketBase, "TEMP=" + socketBase, "TMP=" + socketBase}
			wait := testbudget.Wait(t)
			process := startRPCProcessAt(t, cwd, env, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", filepath.Join(dir, "lo_a.py"))
			awaitPythonProbeLines(t, filepath.Join(probeLog, "lo_a.log"), 1, wait)
			socketRoot := filepath.Join(socketBase, "pig")
			if running := directoryNames(t, socketRoot); len(running) != 1 {
				t.Fatalf("the running pig has the socket directories %v, want one", running)
			}
			test.end(t, process, wait)
			if left := directoryNames(t, socketRoot); len(left) != 0 {
				t.Fatalf("the pig ended and left %v in its socket directory", left)
			}
		})
	}
}
