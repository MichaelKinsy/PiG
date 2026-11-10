package ai

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's ensureFileExists is existsSync(path) then writeFileSync(path, "{}", {mode: 0o600}) (auth-storage.ts:75-79): open(O_WRONLY|O_CREAT|O_TRUNC), where the kernel follows the whole link chain. A chain within the kernel's limit creates its target; a longer chain, a cycle, or a loop through a directory link throws `ELOOP: too many symbolic links encountered, open '<path>'` and creates nothing. Red without the kernel's verdict: a chain past the limit had its target created (the walk followed it) and a loop through a directory link reported a synthesized `sub/sub/…` path.
func TestAuthStorageCreateThroughSymlinkChainFollowsTheKernelsLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the kernel's symlink limit and ELOOP are POSIX")
	}
	// chain makes links auth.json -> c1 -> … -> c(n-1) -> target, n symlinks in all, with target missing.
	chain := func(t *testing.T, dir string, n int) {
		t.Helper()
		for i := range n {
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
	limit := symlinkChainLimit(t, chain)
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
			testenv.RequireDirectoryLink(t, ".", filepath.Join(dir, "sub", "sub"))
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

// symlinkChainLimit is the longest chain of links the kernel resolves from the test's temporary directory, found by stat on chains of growing length (a
// resolved chain to a missing target is ENOENT, one past the limit ELOOP): the kernel's limit (MAXSYMLINKS: 40 on Linux, 32 on macOS) less the links its
// path to that directory already crosses, as macOS's /var -> /private/var, which a test that assumed the nominal limit missed. The probe stats and never
// calls the code under test, so the chain at the limit stays the longest the kernel resolves.
func symlinkChainLimit(t *testing.T, chain func(t *testing.T, dir string, n int)) int {
	t.Helper()
	nominal := map[string]int{"linux": 40, "darwin": 32}[runtime.GOOS]
	limit := 0
	for n := 1; n <= 99; n++ {
		dir := t.TempDir()
		chain(t, dir, n)
		_, err := os.Stat(filepath.Join(dir, "auth.json"))
		if errors.Is(err, syscall.ELOOP) {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("stat of a chain of %d links = %v, want ENOENT or ELOOP", n, err)
		}
		limit = n
	}
	// The probe must have found a limit: a chain of 5 resolves (the test's own case), and one of 99 does not.
	if limit < 5 || limit >= 99 || (nominal != 0 && limit > nominal) {
		t.Fatalf("the longest chain the kernel resolves is %d links (nominal %d on %s), want 5 to 98 and at most the nominal limit", limit, nominal, runtime.GOOS)
	}
	t.Logf("the kernel resolves chains of up to %d links here", limit)
	return limit
}
