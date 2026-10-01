package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The grouped test run fails when a package leaves anything in its scratch temporary directory. The shared Node compile caches are not leaks. A go-build* directory is the work directory of one go command (GOTMPDIR), not a shared cache: go removes it on exit, so one that remains belongs to a killed build and is a leak.
func TestAssertCleanTmpFlagsLeaksAndAllowsSharedCaches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the grouped test scripts run under bash on Unix runners")
	}
	script := filepath.Join(repoRoot(t), "automation", "ci", "assert-clean-tmp.sh")
	scratch := t.TempDir()
	for _, cache := range []string{"node-compile-cache", "v8-compile-cache-1000"} {
		if err := os.Mkdir(filepath.Join(scratch, cache), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command(script, scratch).CombinedOutput(); err != nil {
		t.Fatalf("shared caches reported as leaks: %v\n%s", err, out)
	}
	for _, leak := range []string{"pig-ext-sdk-fixture-1.log", "pig-packed-packed-node-abc-2.log", "go-build123456"} {
		if err := os.WriteFile(filepath.Join(scratch, leak), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(scratch, "pig-cli-inventory-x"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(script, scratch).CombinedOutput()
	if err == nil {
		t.Fatal("leaked pig-* entries were accepted")
	}
	for _, name := range []string{"pig-ext-sdk-fixture-1.log", "pig-packed-packed-node-abc-2.log", "go-build123456", "pig-cli-inventory-x"} {
		if !strings.Contains(string(out), name) {
			t.Errorf("leak %s missing from report:\n%s", name, out)
		}
	}
}
