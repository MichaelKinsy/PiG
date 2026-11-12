package subprocess_test

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// The installed oracle carries the post-release backports (automation/pin/oracle.py), so vendoring from it must verify them, never
// apply them again: running the generator on the committed tree is a no-op, however many times it runs.
func TestVendorPiDistIsIdempotentOnTheCommittedTree(t *testing.T) {
	t.Parallel()
	shims, err := filepath.Abs(filepath.Join("runtime-node", "shims"))
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "automation", "gen", "vendor-pi-dist.sh"))
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(t.TempDir(), "shims")
	if log, err := exec.CommandContext(t.Context(), "cp", "-R", shims, copied).CombinedOutput(); err != nil {
		t.Fatalf("copy the shims: %v\n%s", err, log)
	}
	want := bundleDigests(t, shims)
	for run := 1; run <= 2; run++ {
		command := exec.CommandContext(t.Context(), "bash", script)
		command.Env = append(command.Environ(), "PIG_VENDOR_SHIMS="+copied)
		if log, err := command.CombinedOutput(); err != nil {
			t.Fatalf("vendor-pi-dist.sh run %d (run make parity-deps): %v\n%s", run, err, log)
		}
		if got := bundleDigests(t, copied); !reflect.DeepEqual(got, want) {
			t.Fatalf("vendor-pi-dist.sh run %d changed the committed shims: run automation/gen/vendor-pi-dist.sh", run)
		}
	}
}
