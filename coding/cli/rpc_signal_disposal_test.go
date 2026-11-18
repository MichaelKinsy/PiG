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

// Pi's RPC signal handler kills tracked children and awaits runtimeHost.dispose (rpc-mode.ts:383-391,744-751), which
// awaits session.abort() before it emits session_shutdown (agent-session-runtime.ts:409-417; Pi main 7a53c310a). The
// killed bash call's exit is I/O that the abort's wait lets the event loop see, so the run reaches and persists its tool
// result before any session_shutdown handler runs, whether the handler throws, waits on a timer or waits for idle.
// Pi at the pin (c23b9ec1), print mode's identical dispose (scenario print/09): the session holds the tool result and
// the aborted assistant message.
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
	// A Python handler reports no window. A handler may not call waitForIdle (a command-context member, Pi runner.ts:1016),
	// so it makes another Promise-shaped host call, exec, which returns at once: the run settled before session_shutdown,
	// and the call is served while the signal's disposal waits for the handler. The handler must end without an extension
	// error.
	makesHostCall := filepath.Join(extensions, "makes_host_call")
	if err := os.MkdirAll(makesHostCall, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(makesHostCall, "makes_host_call.py"), []byte(`import pig_sdk


def shutdown(ctx, data):
    done = ctx.exec("true")
    if done.exit_code != 0:
        raise RuntimeError("exec true exited with %d" % done.exit_code)


def new_extension() -> pig_sdk.Extension:
    e = pig_sdk.Extension("makes_host_call")
    e.on_event("session_shutdown", shutdown)
    return e
`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, extension    string
		wantExtensionError bool
	}{
		{"throwing handler", throwing, true},
		{"slow handler", slow, false},
		{"python handler making a host call", makesHostCall, false},
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
			if got := strings.Contains(stdout.String(), `"type":"extension_error"`); got != tc.wantExtensionError {
				t.Fatalf("stdout has an extension_error event = %v, want %v:\nstdout:\n%s\nstderr:\n%s", got, tc.wantExtensionError, stdout.String(), stderr.String())
			}
			files, _ := filepath.Glob(filepath.Join(sessionDir, "*.jsonl"))
			if len(files) != 1 {
				t.Fatalf("session files = %v", files)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"role":"toolResult"`) {
				t.Fatalf("session has no tool result; the run the signal's disposal settled persists it:\n%s\nstdout:\n%s", data, stdout.String())
			}
		})
	}
}
