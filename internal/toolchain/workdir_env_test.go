package toolchain

import (
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestWorkDirEnvReplacesInheritedPWD(t *testing.T) {
	dir := t.TempDir()
	got := WorkDirEnv(dir, []string{"PWD=/elsewhere", "HOME=/h", "PWD=/again"})
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		if !slices.Equal(got, []string{"PWD=/elsewhere", "HOME=/h", "PWD=/again"}) {
			t.Fatalf("WorkDirEnv changed the environment where PWD is unused: %q", got)
		}
		return
	}
	if want := []string{"HOME=/h", "PWD=" + dir}; !slices.Equal(got, want) {
		t.Fatalf("WorkDirEnv = %q, want %q", got, want)
	}
	t.Chdir(dir)
	if got := WorkDirEnv("sub", nil); !slices.Equal(got, []string{"PWD=" + filepath.Join(dir, "sub")}) {
		t.Fatalf("relative dir: WorkDirEnv = %q", got)
	}
}
