package subprocess_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The installed oracle carries the post-release backports (automation/pin/oracle.py), so vendoring from it must verify them, never
// apply them again: running the generator on the committed tree is a no-op, however many times it runs.
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
	// The generator gets its shims directory through a directory link, so the path it is given and the path esbuild resolves differ on every
	// platform (macOS /tmp is a link to /private/tmp, which is where this showed first).
	real := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	testenv.RequireDirectoryLink(t, real, linked)
	copied := filepath.Join(real, "shims")
	if log, err := exec.CommandContext(t.Context(), "cp", "-R", shims, copied).CombinedOutput(); err != nil {
		t.Fatalf("copy the shims: %v\n%s", err, log)
	}
	want := bundleDigests(t, shims)
	for run := 1; run <= 2; run++ {
		command := exec.CommandContext(t.Context(), "bash", script)
		command.Env = append(command.Environ(), "PIG_VENDOR_SHIMS="+filepath.Join(linked, "shims"))
		if log, err := command.CombinedOutput(); err != nil {
			t.Fatalf("vendor-pi-dist.sh run %d (run make parity-deps): %v\n%s", run, err, log)
		}
		if got := bundleDigests(t, copied); !reflect.DeepEqual(got, want) {
			t.Fatalf("vendor-pi-dist.sh run %d changed the committed shims: run automation/gen/vendor-pi-dist.sh", run)
		}
	}
}
