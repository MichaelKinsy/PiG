package breakdown

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("python3", append([]string{"proofclass.py"}, args...)...)
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Dir = root
	cmd.Args[1] = "test/parity/interface-closure/breakdown/proofclass.py"
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return string(out), code
}

// TestProofClassRatchetOnlyGoesDown: the count of rows lacking their required behaviour proof is reported per package; a baseline below the
// current count is "ABOVE baseline" and fails only under --enforce, and a written baseline is a fixed point.
func TestProofClassRatchetOnlyGoesDown(t *testing.T) {
	out := t.TempDir()
	zero := filepath.Join(out, "zero.tsv")
	if err := os.WriteFile(zero, []byte("package\tunproven\nagent\t0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, code := run(t, filepath.Join(out, "o1"), "--baseline", zero)
	if code != 0 || !strings.Contains(report, "ABOVE baseline") || !strings.Contains(report, "unproven behaviour") {
		t.Fatalf("a report-only run reports the excess and exits 0, got code %d:\n%s", code, report)
	}
	if _, code = run(t, filepath.Join(out, "o2"), "--baseline", zero, "--enforce"); code != 1 {
		t.Fatalf("--enforce over a lower baseline must fail, got %d", code)
	}
	written := filepath.Join(out, "written.tsv")
	if _, code = run(t, filepath.Join(out, "o3"), "--baseline", written, "--update-baseline"); code != 0 {
		t.Fatalf("--update-baseline failed: %d", code)
	}
	report, code = run(t, filepath.Join(out, "o4"), "--baseline", written, "--enforce")
	if code != 0 || !strings.Contains(report, "no package above its baseline") {
		t.Fatalf("a freshly written baseline is a fixed point, got %d:\n%s", code, report)
	}
	tsv, err := os.ReadFile(filepath.Join(out, "o4", "unproven-behaviour.tsv"))
	if err != nil || !strings.HasPrefix(string(tsv), "id\tpackage\trequired class\treason\n") {
		t.Fatalf("the unproven list is written: %v", err)
	}
}
