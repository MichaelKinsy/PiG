package testenv

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// asyncPreemptionChildEnv marks the process RerunWithoutAsyncPreemption started.
const asyncPreemptionChildEnv = "PIG_TEST_WITHOUT_ASYNC_PREEMPTION_CHILD"

// RerunWithoutAsyncPreemption makes a test whose two ends of a FIFO are both blocking opens immune to macOS's signal-interrupted FIFO open. Call it first in such a test and return
// when it reports true.
//
// On macOS a signal that ends a blocking open of a FIFO makes the kernel (fifo_open in XNU's bsd/miscfs/fifofs/fifo_vnops.c) leave through fifo_close_internal, which counts the opener
// out and shuts the FIFO's send (or receive) side while the other end, whose open completed because it saw the opener, goes on: its write fails with EPIPE (the bytes are lost) or its
// read returns end of file at once while the retried open waits for a counterpart that is gone. The Go runtime's SIGURG preemption signals reach such opens in a process whose goroutines
// are busy. The runtime sends none with GODEBUG=asyncpreemptoff=1, so on macOS this runs the test again in a child test binary with it set and reports the child's verdict: true means the
// child ran the test and the caller must return, false means the caller is the child, or the host is not macOS (no other kernel has the race) and the test runs where it is. A subtest
// is selected by its whole name. SIGCHLD from a child that exits while an open sleeps is not covered, so a test that starts a child must wait for it to exit before its next FIFO open (readFIFO in env does).
func RerunWithoutAsyncPreemption(t *testing.T) bool {
	t.Helper()
	if runtime.GOOS != "darwin" || os.Getenv(asyncPreemptionChildEnv) != "" {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(t.Name(), "/")
	for i, part := range parts {
		parts[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	//nolint:gosec // G204 (class b): program is self from os.Executable; args are -test.run from t.Name() quoted with QuoteMeta plus fixed test flags.
	command := exec.CommandContext(testbudget.DeadlineContext(t), self, "-test.run="+strings.Join(parts, "/"), "-test.count=1", "-test.v")
	debug := "asyncpreemptoff=1"
	if inherited := os.Getenv("GODEBUG"); inherited != "" {
		debug = inherited + "," + debug
	}
	command.Env = append(os.Environ(), asyncPreemptionChildEnv+"=1", "GODEBUG="+debug)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, strings.TrimSpace(string(output)))
	}
	if strings.Contains(string(output), "--- SKIP: "+t.Name()) {
		t.Skip(strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "--- PASS: "+t.Name()) {
		t.Fatalf("the child did not run %s:\n%s", t.Name(), strings.TrimSpace(string(output)))
	}
	return true
}
