//go:build linux

package plugins

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// pi: libuv's `uv__to_stat` (deps/uv/src/unix/fs.c, libuv 1.52.1): where statx is refused the birth time Node reports is the ctime,
// so Pi's `identityOf` (watcher.ts) is inode and ctime there.

// refuseStatx makes statx answer ENOSYS for the test, as a kernel without it does.
func refuseStatx(t *testing.T) {
	t.Helper()
	saved := statxCall
	statxCall = func(int, string, int, int, *unix.Statx_t) error { return unix.ENOSYS }
	t.Cleanup(func() { statxCall = saved })
}

func TestLstatIdentityIsTheInodeAndTheCtime(t *testing.T) {
	dir := filepath.Join(realOf(t, tempRoot(t)), "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var stat unix.Stat_t
	if err := unix.Lstat(dir, &stat); err != nil {
		t.Fatal(err)
	}
	ino, birth, ok := lstatIdentity(dir)
	if want := int64(stat.Ctim.Sec)*1e9 + int64(stat.Ctim.Nsec); !ok || ino != stat.Ino || birth != want || birth == 0 {
		t.Fatalf("lstatIdentity = %d, %d, %v; want inode %d, ctime %d", ino, birth, ok, stat.Ino, want)
	}
	if _, _, ok := lstatIdentity(filepath.Join(dir, "none")); ok {
		t.Fatal("an entry that is not there has an identity")
	}
}

// With statx refused a directory removed and made again is another identity (its ctime is new, whether or not the inode number is
// reused), and so is the same directory after a chmod: ctime moves on an attribute change, which is Pi's identity there.
func TestIdentityWithStatxRefusedIsInodeAndCtime(t *testing.T) {
	refuseStatx(t)
	dir := filepath.Join(realOf(t, tempRoot(t)), "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	first := identityOf(dir)
	if !first.present || first.birth == 0 {
		t.Fatalf("identity = %+v, want a present entry with its ctime as birth", first)
	}
	if again := identityOf(dir); again != first {
		t.Fatalf("identity moved with nothing changed: %+v then %+v", first, again)
	}
	time.Sleep(20 * time.Millisecond) // past the kernel's coarse timestamp tick
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if after := identityOf(dir); after == first {
		t.Fatalf("identity %+v unchanged by a chmod: Pi's is inode and ctime here", first)
	}
	before := identityOf(dir)
	time.Sleep(20 * time.Millisecond)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if after := identityOf(dir); after == before {
		t.Fatalf("identity %+v of a directory made again is the old one's", after)
	}
}
