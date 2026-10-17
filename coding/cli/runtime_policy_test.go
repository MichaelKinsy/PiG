package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// runtimePolicyChildEnv makes the test binary run Main(--version) and print, as it exits, the GOMAXPROCS it started with, the GOMAXPROCS it
// exits with and its GC percent.
const runtimePolicyChildEnv = "PIG_TEST_RUNTIME_POLICY_CHILD"

// D174: when the environment does not set GOMAXPROCS, Main caps it at min(2, the runtime default) before anything else runs; a user's
// GOMAXPROCS and GOGC stay as set. 2 is the cap E12b chose (procpolicy.Cap). At a5f9b338cf the unset case exits at the runtime default.
func TestRuntimePolicyRespectsUserEnv(t *testing.T) {
	if os.Getenv(runtimePolicyChildEnv) == "1" {
		started := runtime.GOMAXPROCS(0)
		// exitProcess calls stopStartupExtensions on the way out; --version exits before startup assigns it.
		stopStartupExtensions = func() {
			gogc := debug.SetGCPercent(-1)
			debug.SetGCPercent(gogc)
			fmt.Printf("\nruntime-policy %d %d %d\n", started, runtime.GOMAXPROCS(0), gogc)
		}
		Main([]string{"--version"}, nil)
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	var environment []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOMAXPROCS=") && !strings.HasPrefix(kv, "GOGC=") {
			environment = append(environment, kv)
		}
	}
	run := func(t *testing.T, extra ...string) (started, exited, gogc int) {
		t.Helper()
		home := t.TempDir()
		command := exec.Command(os.Args[0], "-test.run=^TestRuntimePolicyRespectsUserEnv$")
		command.Env = append(append(append([]string(nil), environment...), runtimePolicyChildEnv+"=1", "HOME="+home, "USERPROFILE="+home, "PIG_OFFLINE=1"), extra...)
		out, err := command.Output()
		if err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
		for line := range strings.Lines(string(out)) {
			if rest, ok := strings.CutPrefix(line, "runtime-policy "); ok {
				if _, err := fmt.Sscan(rest, &started, &exited, &gogc); err != nil {
					t.Fatalf("child line %q: %v", line, err)
				}
				return started, exited, gogc
			}
		}
		t.Fatalf("child printed no runtime-policy line:\n%s", out)
		return 0, 0, 0
	}
	t.Run("user-set", func(t *testing.T) {
		if started, exited, gogc := run(t, "GOMAXPROCS=7", "GOGC=400"); started != 7 || exited != 7 || gogc != 400 {
			t.Errorf("GOMAXPROCS=7 GOGC=400: started at %d, exited at %d with GOGC %d; want 7, 7, 400", started, exited, gogc)
		}
	})
	t.Run("unset", func(t *testing.T) {
		started, exited, gogc := run(t)
		if want := min(2, started); exited != want || gogc != 100 {
			t.Errorf("unset: runtime default %d, exited at %d with GOGC %d; want %d, 100", started, exited, gogc, want)
		}
		if started <= 2 {
			t.Logf("the runtime default is %d here, so the cap leaves it unchanged", started)
		}
	})
}
