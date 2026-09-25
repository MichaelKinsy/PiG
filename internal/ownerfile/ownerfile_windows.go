//go:build windows

package ownerfile

import (
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CreateNew creates the file at path, which must not exist, for reading and
// writing, with a protected DACL that grants the current user full access and
// no one else anything: the Windows form of mode 0600. The DACL is part of the
// creation, so no other principal can open the file before it is protected; a
// DACL applied after creation cannot revoke a handle already opened through
// the directory's inherited entries.
func CreateNew(path string) (*os.File, error) {
	// pig divergence (D68): Windows owner-only files use a DACL, not mode bits.
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &attributes, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// CreateTemp creates a new temporary file in dir with CreateNew's protection.
// Like os.CreateTemp, the last "*" in pattern, or its end, is replaced by a
// random string, and an empty dir means os.TempDir().
func CreateTemp(dir, pattern string) (*os.File, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if strings.ContainsAny(pattern, `/\`) {
		return nil, &os.PathError{Op: "createtemp", Path: pattern, Err: errors.New("pattern contains path separator")}
	}
	prefix, suffix := pattern, ""
	if i := strings.LastIndex(pattern, "*"); i >= 0 {
		prefix, suffix = pattern[:i], pattern[i+1:]
	}
	for range 10000 {
		file, err := CreateNew(filepath.Join(dir, prefix+strconv.FormatUint(uint64(rand.Uint32()), 10)+suffix))
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return file, err
	}
	return nil, &os.PathError{Op: "createtemp", Path: filepath.Join(dir, prefix+"*"+suffix), Err: os.ErrExist}
}

// OwnerOnly reports whether the DACL of the file at path grants access to no
// one but the file's owner, SYSTEM, and Administrators.
func OwnerOnly(path string, _ os.FileInfo) (bool, error) {
	// pig divergence (D68): Windows has no mode bits; the file's DACL decides.
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return false, err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return false, err
	}
	if dacl == nil {
		// A NULL DACL grants everyone full access.
		return false, nil
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false, err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false, err
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return false, err
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			// The trustee SID starts at SidStart inside the ACE that GetAce
			// returned from the security descriptor Windows allocated.
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) //nolint:gosec // G103: reads the documented ACCESS_ALLOWED_ACE layout in a Windows-owned buffer.
			if sid.Equals(owner) || sid.Equals(system) || sid.Equals(administrators) {
				continue
			}
			return false, nil
		default:
			// Object and callback entries carry conditions this check does not
			// evaluate, so they never count as owner-only.
			return false, nil
		}
	}
	return true, nil
}
