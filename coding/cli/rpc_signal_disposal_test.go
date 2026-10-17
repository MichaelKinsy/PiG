//go:build unix

package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's RPC signal handler kills tracked children and awaits runtimeHost.dispose on its one event loop
// (rpc-mode.ts:366-376,726-744). The killed bash call's exit is I/O, so the run reaches its tool result only if a
// session_shutdown handler awaits a timer or I/O first. A throwing handler never yields: Pi exits with the tool call
// unanswered and sends no second provider request. A slow handler yields, so the run persists the tool result and
// sends the next request while the handler waits. Pi 1.1.0 probe: throw → 1 request, 6 session lines; slow → 2 requests.
func TestRPCSignalDisposalHoldsTheRunLikePisEventLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	extensions := t.TempDir()
	throwing := filepath.Join(extensions, "throwing-shutdown.mjs")
	slow := filepath.Join(extensions, "slow-shutdown.mjs")
	for path, source := range map[string]string{
		throwing: `export default function (pi) { pi.on("session_shutdown", () => { throw new Error("shutdown boom"); }); }`,
		slow:     `export default function (pi) { pi.on("session_shutdown", () => new Promise((resolve) => setTimeout(resolve, 1500))); }`,
	} {
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A Python handler reports no window; its waitForIdle is a Promise-shaped host call, where a Node handler would
	// await and Pi's loop would turn. The call releases the hold, so the run reaches its tool result and the call returns.
	// Without that the held run never ends and SIGTERM never exits the process.
	waitsForIdle := filepath.Join(extensions, "waits_for_idle")
	if err := os.MkdirAll(waitsForIdle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(waitsForIdle, "waits_for_idle.py"), []byte(`import pig_sdk


def new_extension() -> pig_sdk.Extension:
    e = pig_sdk.Extension("waits_for_idle")
    e.on_event("session_shutdown", lambda ctx, data: ctx.wait_for_idle())
    return e
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, extension string
		wantToolResult  bool
	}{
		{"throwing handler", throwing, false},
		{"slow handler", slow, true},
		{"python handler waiting for idle", waitsForIdle, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir, agentDir, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
			cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-extensions", "-e", tc.extension, "--mode", "rpc", "--session-dir", sessionDir)
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stdin.Close() }()
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}
			t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
			if _, err := stdin.Write([]byte(`{"id":"1","type":"prompt","message":"Run: sleep for a while"}` + "\n")); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(workDir, ai.TestFauxToolStartedMarker)
			deadline := time.Now().Add(testbudget.Wait(t))
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("faux tool never started; stderr:\n%s", stderr.String())
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatalf("signal: %v", err)
			}
			waited := make(chan error, 1)
			go func() { waited <- cmd.Wait() }()
			select {
			case err = <-waited:
			case <-time.After(testbudget.Wait(t)):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-waited
				t.Fatalf("pig did not exit after SIGTERM; stderr:\n%s", stderr.String())
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 143 {
				t.Fatalf("wait = %v, want exit status 143; stderr:\n%s", err, stderr.String())
			}
			files, _ := filepath.Glob(filepath.Join(sessionDir, "*.jsonl"))
			if len(files) != 1 {
				t.Fatalf("session files = %v", files)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(data), `"role":"toolResult"`); got != tc.wantToolResult {
				t.Fatalf("session has a tool result = %v, want %v:\n%s\nstdout:\n%s", got, tc.wantToolResult, data, stdout.String())
			}
		})
	}
}
