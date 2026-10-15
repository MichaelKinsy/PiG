package breakdown

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
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
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
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

// TestProofClassReadsThePinnedLedger: the report counts the closed rows of the effective pin's mapping. A commit pin's ledger is
// mapping-v<release>+<commit[:8]>.json, which a release-only file pattern skips in favour of the release's mapping.
func TestProofClassReadsThePinnedLedger(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	var mapping struct {
		Mappings []struct {
			Disposition string `json:"disposition"`
		} `json:"mappings"`
	}
	data, err := os.ReadFile(filepath.Join(root, "test/parity/interfaces", "mapping-v"+pigversion.UpstreamVersion+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &mapping); err != nil {
		t.Fatal(err)
	}
	closed := 0
	for _, row := range mapping.Mappings {
		if row.Disposition == "ported" || row.Disposition == "designed-out" {
			closed++
		}
	}
	report, code := run(t, filepath.Join(t.TempDir(), "o"))
	if want := fmt.Sprintf(" of %d closed rows", closed); code != 0 || !strings.Contains(report, want) {
		t.Fatalf("the report must count the %d closed rows of mapping-v%s.json, got code %d:\n%s", closed, pigversion.UpstreamVersion, code, report)
	}
}
