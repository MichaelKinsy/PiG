//go:build linux || darwin

package atomicfile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

const standInAttribute = "user.pig-test"

// setAttribute puts an extended attribute on path, or skips the test where the file system keeps none (a temporary directory on a file system without user attributes).
func setAttribute(t *testing.T, path, name string, value []byte) {
	t.Helper()
	if err := unix.Setxattr(path, name, value, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EPERM) {
			t.Skipf("this file system takes no %s attribute: %v", name, err)
		}
		t.Fatal(err)
	}
}

func attribute(t *testing.T, path, name string) []byte {
	t.Helper()
	buf := make([]byte, 4096)
	n, err := unix.Getxattr(path, name, buf)
	if err != nil {
		t.Fatalf("attribute %s of %s: %v", name, path, err)
	}
	return buf[:n]
}

// Extended attributes are part of what an in-place write keeps: the replacement carries them over. (Stand-in for the attributes of macOS and for any user attribute on Linux.)
func TestWriteFileCarriesExtendedAttributesOver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	setAttribute(t, path, standInAttribute, []byte("kept"))
	before := inode(t, path)
	reported := reportedPaths(t)
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := attribute(t, path, standInAttribute); string(got) != "kept" {
		t.Fatalf("attribute after the write = %q, want kept", got)
	}
	if inode(t, path) == before {
		t.Fatal("a file with attributes the helper can carry was written in place")
	}
	if len(*reported) != 0 {
		t.Fatalf("reported = %v for an atomic replacement", *reported)
	}
	noTemporaryFile(t, dir)
}

// A POSIX access ACL (the system.posix_acl_access attribute on Linux) survives a write: the file keeps the ACL entry that lets a second user read it.
func TestWriteFileKeepsAPosixACL(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("POSIX ACLs are the system.posix_acl_access attribute of Linux")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	// ACL_USER_OBJ rw-, ACL_USER uid 12345 r--, ACL_GROUP_OBJ r--, ACL_MASK r--, ACL_OTHER ---, in the version 2 xattr form.
	acl := binary.LittleEndian.AppendUint32(nil, 2)
	for _, entry := range []struct {
		tag, perm uint16
		id        uint32
	}{{0x01, 6, ^uint32(0)}, {0x02, 4, 12345}, {0x04, 4, ^uint32(0)}, {0x10, 4, ^uint32(0)}, {0x20, 0, ^uint32(0)}} {
		acl = binary.LittleEndian.AppendUint16(acl, entry.tag)
		acl = binary.LittleEndian.AppendUint16(acl, entry.perm)
		acl = binary.LittleEndian.AppendUint32(acl, entry.id)
	}
	setAttribute(t, path, "system.posix_acl_access", acl)
	infoBefore, _ := os.Stat(path)
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := attribute(t, path, "system.posix_acl_access"); !bytes.Equal(got, acl) {
		t.Fatalf("the ACL after the write is % x, want % x", got, acl)
	}
	if infoAfter, _ := os.Stat(path); infoAfter.Mode() != infoBefore.Mode() {
		t.Fatalf("mode after the write = %v, want %v", infoAfter.Mode(), infoBefore.Mode())
	}
}

// An attribute that cannot be carried over must never be dropped silently: the file is written in place, which keeps every attribute, and the write is reported.
func TestWriteFileWritesInPlaceWhenAnAttributeCannotBeCarried(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	setAttribute(t, path, standInAttribute, []byte("kept"))
	before := inode(t, path)
	previous := copyAttributes
	copyAttributes = func(*os.File, string) error { return syscall.EPERM }
	t.Cleanup(func() { copyAttributes = previous })
	reported := reportedPaths(t)
	if err := WriteFile(path, []byte("new bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new bytes" {
		t.Fatalf("content = %q", data)
	}
	if inode(t, path) != before {
		t.Fatal("the file was replaced although its attributes could not be carried over")
	}
	if got := attribute(t, path, standInAttribute); string(got) != "kept" {
		t.Fatalf("attribute = %q", got)
	}
	if len(*reported) != 1 {
		t.Fatalf("reported = %v, want one line", *reported)
	}
	noTemporaryFile(t, dir)
}

// A file system without extended attributes has nothing to carry: the replacement stays atomic.
func TestWriteFileReplacesWhereTheFileSystemHasNoAttributes(t *testing.T) {
	for _, unsupported := range []error{unix.ENOTSUP, unix.EOPNOTSUPP} {
		dir := t.TempDir()
		path := filepath.Join(dir, "auth.json")
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		before := inode(t, path)
		previous := listAttributes
		listAttributes = func(string) ([]string, error) { return nil, unsupported }
		reported := reportedPaths(t)
		err := WriteFile(path, []byte("new"), 0o600)
		listAttributes = previous
		if err != nil {
			t.Fatal(err)
		}
		if inode(t, path) == before || len(*reported) != 0 {
			t.Fatalf("with %v from the attribute listing the write went in place (reported %v)", unsupported, *reported)
		}
	}
}
