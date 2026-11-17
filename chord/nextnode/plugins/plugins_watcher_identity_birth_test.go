package plugins

// Ports packages/chord/src/node/plugins/watcher.ts `identityOf`

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// birthSeconds is the birth time of path in seconds as the system's own `stat` prints it (0: the file system has none), and whether
// this system has such a command here.
func birthSeconds(t *testing.T, path string) (seconds int64, known bool) {
	t.Helper()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		command = exec.Command("stat", "-c", "%W", path)
	case "darwin":
		command = exec.Command("stat", "-f", "%B", path)
	default:
		return 0, false
	}
	out, err := command.Output()
	if err != nil {
		t.Fatalf("%v: %v", command.Args, err)
	}
	seconds, err = strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatalf("%v printed %q", command.Args, out)
	}
	return seconds, true
}

// fileSystemOf is the file system type of path as `stat -f` names it on Linux, for the log.
func fileSystemOf(path string) string {
	if runtime.GOOS != "linux" {
		return runtime.GOOS
	}
	out, err := exec.Command("stat", "-f", "-c", "%T", path).Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// D540 claim 1: the identity of a directory carries its birth time where the system has one (statx `btime` on Linux, `birthtimespec`
// on macOS), and a directory removed and made again is another identity (Pi's watcher renews its watches on a changed identity). The
// birth time is compared with what the system's own `stat` says for the directory; where `stat` has none (a tmpfs) the identity has
// none either. The directories are made in two places: Go's temporary directory (a tmpfs in some images) and beside the test, in the
// working tree (the file system of the checkout), and the file system of each is logged.
func TestIdentityOfADirectoryCarriesItsBirthTimeWhereTheSystemHasOneAndARemadeDirectoryDiffers(t *testing.T) {
	tree, err := os.MkdirTemp(".", ".identity-birth-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tree) })
	tree, err = filepath.Abs(tree)
	if err != nil {
		t.Fatal(err)
	}
	for name, base := range map[string]string{"temporary directory": t.TempDir(), "working tree": tree} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(base, "d")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			first := identityOf(dir)
			seconds, known := birthSeconds(t, dir)
			t.Logf("%s on %s (%s): birth %d ns, stat says %d s (known %v)", base, runtime.GOOS, fileSystemOf(dir), first.birth, seconds, known)
			if known {
				if seconds != 0 && (first.birth == 0 || first.birth/1e9 != seconds) {
					t.Fatalf("birth time = %d ns, the system's stat says %d s", first.birth, seconds)
				}
				if seconds == 0 && first.birth != 0 {
					t.Fatalf("birth time = %d ns where the file system has none", first.birth)
				}
			}
			if runtime.GOOS == "darwin" && first.birth == 0 {
				t.Fatal("macOS has a birth time: it is 0")
			}
			if first.birth == 0 {
				return // inode only: the identity of a directory made again is the inode alone (D540 claim 2 for such a file system)
			}
			// Removed and made again once the clock is past the old birth time (the kernel stamps with its coarse clock): another identity,
			// whether or not the file system gave the new directory the old inode number.
			if err := os.Remove(dir); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Until(time.Unix(0, first.birth).Add(50 * time.Millisecond)))
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			again := identityOf(dir)
			t.Logf("inode %d then %d, birth %d then %d", first.ino, again.ino, first.birth, again.birth)
			if !again.present || again == first {
				t.Fatalf("identity of the directory made again = %+v, the same as before %+v", again, first)
			}
			if again.birth <= first.birth {
				t.Fatalf("birth time of the directory made again = %d, not after %d", again.birth, first.birth)
			}
		})
	}
}
