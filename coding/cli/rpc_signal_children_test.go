//go:build linux

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/ai"
)

// rpcSignalChildrenHelperEnv names the signal the helper process sends to its RPC child.
const rpcSignalChildrenHelperEnv = "PIG_TEST_RPC_SIGNAL_CHILDREN"

// Pi's rpc-mode.ts signal handler calls killTrackedDetachedChildren before shutdown(128+signum): a bash call's shell
// runs in its own process group, and a SIGTERM shutdown neither flushes nor aborts the run, so without the kill the
// command outlives the RPC process. The faux bash call runs `touch <marker>; sleep 45`. A helper process that is a
// child subreaper adopts what is left of the call when PiG exits and reports how each adopted process ended: SIGKILL
// when PiG killed it, a normal exit after 45 seconds when it outlived PiG.
func TestRPCSignalKillsTheShellOfARunningBashCall(t *testing.T) {
	if name := os.Getenv(rpcSignalChildrenHelperEnv); name != "" {
		rpcSignalChildrenHelper(t, name)
		return
	}
	t.Parallel()
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			helper := exec.Command(os.Args[0], "-test.run=^TestRPCSignalKillsTheShellOfARunningBashCall$", "-test.v")
			helper.Env = append(os.Environ(), rpcSignalChildrenHelperEnv+"="+unix.SignalName(sig))
			output, err := helper.CombinedOutput()
			if err != nil {
				t.Fatalf("helper: %v\n%s", err, output)
			}
			var reports []string
			for line := range strings.SplitSeq(string(output), "\n") {
				if report, ok := strings.CutPrefix(line, "ADOPTED "); ok {
					reports = append(reports, report)
				}
			}
			if !strings.Contains(string(output), "SIGNALLED") {
				t.Fatalf("the helper did not signal a running bash call:\n%s", output)
			}
			// PiG reaps a shell it killed before it exits, so a killed call can leave nothing to adopt.
			for _, report := range reports {
				if report != "killed" {
					t.Fatalf("a process of the bash call outlived the RPC process (%s): %v", sig, reports)
				}
			}
		})
	}
}

func rpcSignalChildrenHelper(t *testing.T, name string) {
	sig := unix.SignalNum(name)
	if sig == 0 {
		t.Fatalf("unknown signal %q", name)
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := startRPCProcessAt(t, dir, []string{"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_HOME=" + t.TempDir()}, "--model", "test-faux/faux-1", "--no-extensions", "--no-session")
	p.send(`{"type":"prompt","message":"Run: sleep for a while"}`)
	marker := filepath.Join(dir, ai.TestFauxToolStartedMarker)
	deadline := time.Now().Add(p.budget)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the faux bash call never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	fmt.Println("SIGNALLED")
	err := p.cmd.Wait()
	p.exited = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 128+int(sig) {
		t.Fatalf("wait = %v, want %d", err, 128+int(sig))
	}
	// Every process left is a process of the bash call that this subreaper adopted.
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, 0, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.ECHILD) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if status.Signaled() && status.Signal() == syscall.SIGKILL {
			fmt.Println("ADOPTED killed")
		} else {
			fmt.Printf("ADOPTED %d survived: %v\n", pid, status)
		}
	}
}
