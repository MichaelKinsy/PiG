package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

// runtimePolicyChildEnv makes the test binary run the command line as a child: "1" runs Main(--version) and prints, as it exits, the
// GOMAXPROCS it started with, the GOMAXPROCS it exits with and its GC percent; "rpc" runs Main(--mode rpc).
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

// D174: an rpc run caps GOMAXPROCS at procpolicy.RPCCap when the environment does not set it, and a user's GOMAXPROCS still wins. The
// child is the real command line in rpc mode; stdin ends at once, so it exits through exitProcess, which writes the PIG_PROFILE=metrics
// record with the width at exit. At 0c3963388f the unset case exits at the general cap, 2.
func TestRPCRunsOnTheRPCCap(t *testing.T) {
	if os.Getenv(runtimePolicyChildEnv) == "rpc" {
		Main([]string{"--mode", "rpc"}, nil)
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	var environment []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOMAXPROCS=") && !strings.HasPrefix(kv, "PIG_PROFILE") {
			environment = append(environment, kv)
		}
	}
	run := func(t *testing.T, extra ...string) int {
		t.Helper()
		home, profiles := t.TempDir(), t.TempDir()
		command := exec.Command(os.Args[0], "-test.run=^TestRPCRunsOnTheRPCCap$")
		command.Env = append(append(append([]string(nil), environment...), runtimePolicyChildEnv+"=rpc", "HOME="+home, "USERPROFILE="+home, "PIG_HOME="+filepath.Join(home, ".pig"),
			"PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_OFFLINE=1", "PIG_PROFILE=metrics", "PIG_PROFILE_DIR="+profiles), extra...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
		files, err := filepath.Glob(filepath.Join(profiles, "*-metrics.json"))
		if err != nil || len(files) != 1 {
			t.Fatalf("metrics files %v, %v; want one", files, err)
		}
		data, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			GOMAXPROCS int `json:"gomaxprocs"`
		}
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		return record.GOMAXPROCS
	}
	t.Run("unset", func(t *testing.T) {
		if got, want := run(t), min(procpolicy.RPCCap, runtime.NumCPU()); got != want {
			t.Errorf("rpc run without GOMAXPROCS exited at %d Ps, want %d", got, want)
		}
	})
	t.Run("user-set", func(t *testing.T) {
		if got := run(t, "GOMAXPROCS=7"); got != 7 {
			t.Errorf("rpc run with GOMAXPROCS=7 exited at %d Ps, want 7", got)
		}
	})
}
