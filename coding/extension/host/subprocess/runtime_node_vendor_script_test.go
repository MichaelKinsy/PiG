package subprocess_test

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// The installed oracle is the pinned Pi: vendoring from it is a function of the pin alone, so running the generator on the committed tree is a no-op, however many times it runs.
// What it proves is a property of the committed tree (it is what the generator writes), and the tree is proven on the platforms where generation
// runs: make generate and vendor-pi-dist-drift are Unix commands. On Windows the bash script gives Node its checkout path in MSYS form
// (/d/a/PiG/PiG/...), which Node reads as D:\d\a\PiG\PiG\..., so the installed oracle is never found; the Windows jobs do not run the generator.
func TestVendorPiDistIsIdempotentOnTheCommittedTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("vendor-pi-dist.sh hands Node its MSYS checkout path, which Windows Node reads as a drive-letter path that does not exist; the generator runs on Unix, where this test proves the committed tree")
	}
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
			t.Fatalf("vendor-pi-dist.sh run %d changed the committed shims: run make generate (or make vendor-pi-dist, then make node-runtime) and commit the result", run)
		}
	}
}
