package subprocess

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestTurnBoundaryPackedRowRecordsTheIsolatedReference: the packed row compares with what the isolated row recorded, and `-run '/python/packed'` selects no isolated
// row. The packed row used to fail with "isolated=<nil>", a diff against nothing; it must record the isolated reference itself and pass.
func TestTurnBoundaryPackedRowRecordsTheIsolatedReference(t *testing.T) {
	if testing.Short() {
		t.Skip("loads Python extension processes")
	}
	t.Parallel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx := testbudget.DeadlineContext(t)
	output, err := exec.CommandContext(ctx, executable, "-test.run=^TestTurnBoundaryPackedFactoriesMatchIsolated$/^python$/^packed$", "-test.count=1", "-test.v").CombinedOutput()
	if expired := testbudget.Expired(ctx); expired != "" {
		t.Fatalf("%s:\n%s", expired, output)
	}
	if err != nil || !strings.Contains(string(output), "--- PASS: TestTurnBoundaryPackedFactoriesMatchIsolated/python/packed ") || strings.Contains(string(output), "--- SKIP:") ||
		!strings.Contains(string(output), "isolated reference row has not recorded") {
		t.Fatalf("selecting only the packed row: %v\n%s", err, output)
	}
}
