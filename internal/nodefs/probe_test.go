package nodefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The probe's path is path.join of a clean directory and plain names, and Exists answers for that path.
func TestPathProbeJoinsAndProbes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	var probe PathProbe
	for _, tc := range []struct {
		dir   string
		elems []string
	}{
		{dir, []string{".agents", "skills"}},
		{dir, []string{".git"}},
		{filepath.Dir(dir), nil},
		{string(filepath.Separator), []string{"x"}},
		{"", []string{"x", "y"}},
	} {
		want := filepath.Join(append([]string{tc.dir}, tc.elems...)...)
		if !probe.Set(tc.dir, tc.elems...).Is(want) {
			t.Errorf("probe path of %q %q is not %q", tc.dir, tc.elems, want)
		}
		if got, exists := probe.Exists(), Exists(want); got != exists {
			t.Errorf("probe of %q exists = %v, Exists gives %v", want, got, exists)
		}
	}
	if !probe.Set(dir, ".agents", "skills").Exists() || probe.Set(dir, ".git").Exists() {
		t.Error("the probe does not see the directory that exists, or sees one that does not")
	}
}

// A probe of a missing path builds no path string and no error: on Unix only access(2)'s copy of the path is allocated.
func TestPathProbeOfAMissingPathAllocatesOnlyTheSystemCallCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Exists is os.Stat on Windows")
	}
	dir := t.TempDir()
	var probe PathProbe
	probe.Set(dir, ".agents", "skills").Exists()
	if allocs := testing.AllocsPerRun(100, func() { probe.Set(dir, ".agents", "skills").Exists() }); allocs > 1 {
		t.Errorf("a probe of a missing path allocates %v times, want at most 1", allocs)
	}
}
