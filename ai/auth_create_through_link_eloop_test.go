package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's ensureFileExists is existsSync(path) then writeFileSync(path, "{}", {mode: 0o600}) (auth-storage.ts:75-79): open(O_WRONLY|O_CREAT|O_TRUNC), where the kernel follows the whole link chain. A chain within the kernel's limit creates its target; a longer chain, a cycle, or a loop through a directory link throws `ELOOP: too many symbolic links encountered, open '<path>'` and creates nothing. Red without the kernel's verdict: a chain past the limit had its target created (the walk followed it) and a loop through a directory link reported a synthesized `sub/sub/…` path.
func TestAuthStorageCreateThroughSymlinkChainFollowsTheKernelsLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the kernel's symlink limit and ELOOP are POSIX")
	}
	// MAXSYMLINKS: 40 on Linux, 32 on macOS.
	limit := 40
	if runtime.GOOS == "darwin" {
		limit = 32
	}
	// chain makes links auth.json -> c1 -> … -> c(n-1) -> target, n symlinks in all, with target missing.
	chain := func(t *testing.T, dir string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			name := "auth.json"
			if i > 0 {
				name = fmt.Sprintf("c%d", i)
			}
			next := "target"
			if i < n-1 {
				next = fmt.Sprintf("c%d", i+1)
			}
			testenv.Symlink(t, next, filepath.Join(dir, name))
		}
	}
	tests := []struct {
		name  string
		build func(t *testing.T, dir string)
		want  string // "" = the target is created
	}{
		{"dangling 1", func(t *testing.T, dir string) { chain(t, dir, 1) }, ""},
		{"chain 5", func(t *testing.T, dir string) { chain(t, dir, 5) }, ""},
		{"chain at the limit", func(t *testing.T, dir string) { chain(t, dir, limit) }, ""},
		{"chain past the limit", func(t *testing.T, dir string) { chain(t, dir, limit+1) }, "eloop"},
		{"chain 100", func(t *testing.T, dir string) { chain(t, dir, 100) }, "eloop"},
		{"self loop", func(t *testing.T, dir string) { testenv.Symlink(t, "auth.json", filepath.Join(dir, "auth.json")) }, "eloop"},
		{"2-cycle", func(t *testing.T, dir string) {
			testenv.Symlink(t, "b", filepath.Join(dir, "auth.json"))
			testenv.Symlink(t, "auth.json", filepath.Join(dir, "b"))
		}, "eloop"},
		{"tail into a 3-cycle", func(t *testing.T, dir string) {
			testenv.Symlink(t, "t1", filepath.Join(dir, "auth.json"))
			testenv.Symlink(t, "x", filepath.Join(dir, "t1"))
			testenv.Symlink(t, "y", filepath.Join(dir, "x"))
			testenv.Symlink(t, "z", filepath.Join(dir, "y"))
			testenv.Symlink(t, "x", filepath.Join(dir, "z"))
		}, "eloop"},
		{"loop through a directory link", func(t *testing.T, dir string) {
			if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
				t.Fatal(err)
			}
			testenv.Symlink(t, ".", filepath.Join(dir, "sub", "sub"))
			testenv.Symlink(t, filepath.Join("sub", "auth.json"), filepath.Join(dir, "auth.json"))
			testenv.Symlink(t, filepath.Join("sub", "auth.json"), filepath.Join(dir, "sub", "auth.json"))
		}, "eloop"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.build(t, dir)
			path := filepath.Join(dir, "auth.json")
			err := (&AuthStorage{path: path}).ensureFileExists()
			target := filepath.Join(dir, "target")
			_, targetErr := os.Stat(target)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ensureFileExists = %v; want the target created", err)
				}
				if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "{}" {
					t.Fatalf("target = %q, %v; want {}", data, readErr)
				}
				return
			}
			want := "ELOOP: too many symbolic links encountered, open '" + path + "'"
			if err == nil || err.Error() != want {
				t.Fatalf("ensureFileExists = %v; want %s", err, want)
			}
			if targetErr == nil {
				t.Fatal("the target was created")
			}
		})
	}
}
