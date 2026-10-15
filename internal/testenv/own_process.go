package testenv

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ownProcessEnv names the test that a child of the test binary runs alone (InOwnProcess).
const ownProcessEnv = "PIG_TEST_OWN_PROCESS"

// InOwnProcess reports whether the calling top-level test runs in a process of its own. Called in the package's test process, it runs the test again in a child (the same test binary, only that test, within the parent's deadline), fails if the child fails or does not run it, skips if the child skipped, and returns false.
//
// A test that reads a process-wide runtime counter (runtime/metrics, runtime/trace) calls it first: in the package's process, goroutines that earlier tests left running create goroutines, wake up, allocate and collect during the measurement.
func InOwnProcess(t *testing.T) bool {
	t.Helper()
	name := t.Name()
	if strings.Contains(name, "/") {
		t.Fatalf("InOwnProcess runs top-level tests only, not %s", name)
	}
	if os.Getenv(ownProcessEnv) == name {
		return true
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^" + regexp.QuoteMeta(name) + "$", "-test.count=1", "-test.v"}
	if deadline, ok := t.Deadline(); ok {
		// The child times out first, so its goroutine dump reaches the failure instead of the parent's timeout panic.
		left := time.Until(deadline)
		args = append(args, "-test.timeout="+(left-min(left/10, 5*time.Second)).String())
	}
	cmd := exec.CommandContext(t.Context(), executable, args...)
	cmd.Env = append(os.Environ(), ownProcessEnv+"="+name)
	output, err := cmd.CombinedOutput()
	switch {
	case err != nil:
		t.Fatalf("%s in its own process: %v\n%s", name, err, output)
	case bytes.Contains(output, []byte("--- SKIP: "+name+" ")):
		t.Skipf("%s skipped in its own process:\n%s", name, output)
	case !bytes.Contains(output, []byte("--- PASS: "+name+" ")):
		t.Fatalf("%s did not run in its own process:\n%s", name, output)
	}
	t.Logf("%s in its own process:\n%s", name, output)
	return false
}
