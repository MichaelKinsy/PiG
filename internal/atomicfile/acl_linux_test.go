//go:build linux

package atomicfile

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

const (
	aclAccess  = "system.posix_acl_access"
	aclDefault = "system.posix_acl_default"

	aclUserObj  = 0x01
	aclUser     = 0x02
	aclGroupObj = 0x04
	aclMask     = 0x10
	aclOther    = 0x20
	aclNoID     = ^uint32(0)

	// Two users that exist on no machine this runs on: neither owns a file here, so what the ACL gives them is exactly what its named entry and its mask give.
	sourceUser  = 12345
	inheritUser = 54321
)

type aclEntry struct {
	tag, perm uint16
	id        uint32
}

// posixACL is the version 2 xattr form of a POSIX ACL: a 4-byte version, then 8 bytes (tag, permission, id) per entry.
func posixACL(entries ...aclEntry) []byte {
	acl := binary.LittleEndian.AppendUint32(nil, 2)
	for _, entry := range entries {
		acl = binary.LittleEndian.AppendUint16(acl, entry.tag)
		acl = binary.LittleEndian.AppendUint16(acl, entry.perm)
		acl = binary.LittleEndian.AppendUint32(acl, entry.id)
	}
	return acl
}

// readACL returns the attribute's bytes, or nil where the file has none.
func readACL(t *testing.T, path, name string) []byte {
	t.Helper()
	buf := make([]byte, 4096)
	n, err := unix.Getxattr(path, name, buf)
	if errors.Is(err, unix.ENODATA) {
		return nil
	}
	if err != nil {
		t.Fatalf("%s of %s: %v", name, path, err)
	}
	return buf[:n]
}

// grantedTo is what the kernel lets the user uid (not the owner, not in the file's group) do to the file, from its mode and access ACL together, as
// POSIX 1003.1e says: a named user entry counts through the mask; with no named entry the user is "other".
func grantedTo(t *testing.T, path string, uid uint32) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	perm := uint16(info.Mode().Perm() & 7)
	if acl := readACL(t, path, aclAccess); acl != nil {
		var named, mask, other uint16 = 0, 7, perm
		hasNamed := false
		for entries := acl[4:]; len(entries) >= 8; entries = entries[8:] {
			tag, p, id := binary.LittleEndian.Uint16(entries), binary.LittleEndian.Uint16(entries[2:]), binary.LittleEndian.Uint32(entries[4:])
			switch {
			case tag == aclUser && id == uid:
				named, hasNamed = p, true
			case tag == aclMask:
				mask = p
			case tag == aclOther:
				other = p
			}
		}
		if hasNamed {
			perm = named & mask
		} else {
			perm = other
		}
	}
	return fmt.Sprintf("%c%c%c", "-r"[perm>>2&1], "-w"[perm>>1&1], "-x"[perm&1])
}

// setACL puts an ACL attribute on path or skips the test where the file system keeps none.
func setACL(t *testing.T, path, name string, acl []byte) {
	t.Helper()
	setAttribute(t, path, name, acl)
}

// The file a write replaces keeps exactly the access it had, mode and effective ACL, whether or not the directory carries a default ACL: a default ACL
// gives every new file in the directory its entries, and the temporary file is a new file.
func TestWriteFileKeepsExactlyTheAccessOfTheFileItReplaces(t *testing.T) {
	for _, cell := range []struct {
		name                    string
		sourceHasACL, dirHasACL bool
	}{
		{"source with ACL, directory with default ACL", true, true},
		{"source with ACL, directory without", true, false},
		{"source without ACL, directory with default ACL", false, true},
		{"source without ACL, directory without", false, false},
	} {
		t.Run(cell.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "auth.json")
			// The file exists before the directory gets its default ACL: it has no access ACL unless the test gives it one.
			if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o640); err != nil {
				t.Fatal(err)
			}
			if cell.sourceHasACL {
				setACL(t, path, aclAccess, posixACL(aclEntry{aclUserObj, 6, aclNoID}, aclEntry{aclUser, 4, sourceUser}, aclEntry{aclGroupObj, 4, aclNoID}, aclEntry{aclMask, 4, aclNoID}, aclEntry{aclOther, 0, aclNoID}))
			}
			if cell.dirHasACL {
				setACL(t, dir, aclDefault, posixACL(aclEntry{aclUserObj, 7, aclNoID}, aclEntry{aclUser, 4, inheritUser}, aclEntry{aclGroupObj, 5, aclNoID}, aclEntry{aclMask, 7, aclNoID}, aclEntry{aclOther, 0, aclNoID}))
			}
			infoBefore, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			aclBefore := readACL(t, path, aclAccess)
			sourceBefore, inheritBefore := grantedTo(t, path, sourceUser), grantedTo(t, path, inheritUser)

			if err := WriteFile(path, []byte("new"), 0o600); err != nil {
				t.Fatal(err)
			}

			infoAfter, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if infoAfter.Mode() != infoBefore.Mode() {
				t.Errorf("mode after the write = %v, want %v", infoAfter.Mode(), infoBefore.Mode())
			}
			if got := readACL(t, path, aclAccess); !bytes.Equal(got, aclBefore) {
				t.Errorf("access ACL after the write = % x, want % x", got, aclBefore)
			}
			if got := grantedTo(t, path, sourceUser); got != sourceBefore {
				t.Errorf("user %d is granted %s after the write, was granted %s", sourceUser, got, sourceBefore)
			}
			if got := grantedTo(t, path, inheritUser); got != inheritBefore {
				t.Errorf("user %d (named only in the directory's default ACL) is granted %s after the write, was granted %s", inheritUser, got, inheritBefore)
			}
			noTemporaryFile(t, dir)
		})
	}
}
