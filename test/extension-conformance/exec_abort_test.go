package extensionconformance

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// execAbortHelperLifetime outlives every wait of the tests below: a helper that is still running after its call returned, or that no
// abort ended, is the failure they report.
const execAbortHelperLifetime = 90 * time.Second

// execAbortCallTimeout bounds how long an SDK's call may take after the abort. An SDK that never forwards the abort waits the helper's whole lifetime.
const execAbortCallTimeout = 30 * time.Second

// execAbortWant is what pi.exec resolves with when its signal aborts a running command (core/exec.ts): the output written so far,
// killed, and code 0, because a command that a signal ended has no exit code and `code ?? 0` resolves it as 0.
const execAbortWant = "exec-abort:killed=true;code=0;stdout=started"

// TestExecAbortHelperProcess is the command the tests below run through pi.exec. It writes "started", records its pid, then waits for a
// signal. Run as an ordinary test it returns at once.
func TestExecAbortHelperProcess(t *testing.T) {
	if flag.NArg() != 1 {
		return
	}
	pidFile := flag.Arg(0)
	_, _ = os.Stdout.WriteString("started\n")
	// The pid file appears whole: a reader that sees it has the pid, and the output is already written.
	if err := os.WriteFile(pidFile+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pidFile+".tmp", pidFile); err != nil {
		t.Fatal(err)
	}
	time.Sleep(execAbortHelperLifetime)
	os.Exit(3)
}

func execAbortProbe(t *testing.T, pidFile string, pre bool) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	probe, err := json.Marshal(map[string]any{
		"command": exe,
		"args":    []string{"-test.run=^TestExecAbortHelperProcess$", "--", pidFile},
		"pidFile": pidFile,
		"pre":     pre,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(probe)
}

// readExecAbortPid returns the pid the helper recorded, or 0 when it did not get that far.
func readExecAbortPid(pidFile string) int {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// Pi's pi.exec(command, args, { signal }) kills the running command when the signal aborts (core/exec.ts:46-52): SIGTERM to the
// command alone, and the call resolves, never rejects, with the output so far, killed set and code 0. The abort comes from the
// extension's own AbortSignal, not from the request, so every SDK must carry it to the host, and the command must be gone when the
// call returns: an SDK that does not forward the abort leaves the call waiting for the command's whole lifetime.
//
// The `pre` rows pass a signal that is aborted before the call. Pi spawns the command and kills it at once (exec.ts:42-48),
// so the call resolves killed with code 0 and the command is gone, and nothing is left running.
func TestExecAbortAcrossSDKs(t *testing.T) {
	t.Run("reference", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "pid")
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, abort := context.WithCancel(t.Context())
		defer abort()
		go func() {
			for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && readExecAbortPid(pidFile) == 0; time.Sleep(10 * time.Millisecond) {
			}
			abort()
		}()
		result, err := extension.ExecCommand(ctx, t.TempDir(), exe, []string{"-test.run=^TestExecAbortHelperProcess$", "--", pidFile}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := "exec-abort:killed=" + strconv.FormatBool(result.Killed) + ";code=" + strconv.Itoa(result.Code) + ";stdout=" + strings.TrimSpace(result.Stdout)
		if got != execAbortWant {
			t.Fatalf("reference outcome = %q, want %q", got, execAbortWant)
		}
		if pid := readExecAbortPid(pidFile); pid == 0 || !processGone(pid) {
			t.Fatalf("the command (pid %d) is still running after the abort", pid)
		}
	})
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "rust", "python"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Fatal("subprocess harness has no bridge")
			}
			cwd := t.TempDir()
			h.bridge.SetHostAction("exec", func(ctx context.Context, command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
				return extension.ExecCommand(ctx, cwd, command, args, opts)
			})
			command, ok := findCommand(h.runner, "exec-abort-probe")
			if !ok {
				t.Fatal("exec-abort-probe command missing")
			}
			for _, pre := range []bool{false, true} {
				name := "aborted-while-running"
				if pre {
					name = "aborted-before-the-call"
				}
				pidFile := filepath.Join(t.TempDir(), "pid")
				*h.notify = nil
				probe := execAbortProbe(t, pidFile, pre)
				done := make(chan error, 1)
				go func() { done <- command.Handler(h.runner.DispatchContext(t.Context()), probe) }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
				case <-time.After(execAbortCallTimeout):
					if pid := readExecAbortPid(pidFile); pid != 0 {
						killProcess(pid)
					}
					t.Fatalf("%s: the call was still waiting %v after the abort: the SDK left the command running", name, execAbortCallTimeout)
				}
				got := ""
				if len(*h.notify) == 1 {
					got = strings.TrimSuffix((*h.notify)[0], ":info")
				}
				if pre {
					// Pi spawns the command and kills it at once, so what it wrote before the kill is not fixed.
					if !strings.HasPrefix(got, "exec-abort:killed=true;code=0;stdout=") {
						t.Errorf("%s: outcome = %q, want killed with code 0", name, *h.notify)
					}
				} else if !slices.Equal(*h.notify, []string{execAbortWant + ":info"}) {
					t.Errorf("%s: outcome = %q, want %q", name, *h.notify, execAbortWant)
				}
				pid := readExecAbortPid(pidFile)
				if !pre && pid == 0 {
					t.Fatalf("%s: the command never recorded its pid", name)
				}
				if pid != 0 && !processGone(pid) {
					killProcess(pid)
					t.Errorf("%s: the command (pid %d) is still running after the call returned", name, pid)
				}
			}
		})
	}
}
