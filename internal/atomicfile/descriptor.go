package atomicfile

import (
	"errors"
	"slices"
	"syscall"
)

// What a replacement must keep of a file's security descriptor is decided here, on every platform, so that a test can drive every branch without Windows.
// The Win32 calls that read and set a descriptor are in descriptor_windows.go.

// Errors of Windows (winerror.h) that a token gets for an owner or a group it may not give a file. They are numbers so that the decision is the same code on
// every platform and a test can inject them.
const (
	errAccessDenied        = syscall.Errno(5)    // ERROR_ACCESS_DENIED: the file cannot be opened for WRITE_OWNER
	errInvalidOwner        = syscall.Errno(1307) // ERROR_INVALID_OWNER
	errInvalidPrimaryGroup = syscall.Errno(1308) // ERROR_INVALID_PRIMARY_GROUP
	errPrivilegeNotHeld    = syscall.Errno(1314) // ERROR_PRIVILEGE_NOT_HELD
)

// errAccessDiffers is what a carry of a security descriptor returns when the access of the file is not the access it was to be given.
var errAccessDiffers = errors.New("the DACL's entries or SE_DACL_PROTECTED are not those of the old file")

// access is the part of a security descriptor that decides who may open a file, and the flag that records how the DACL was set.
type access struct {
	absent        bool     // the descriptor has no DACL: the file system keeps none
	null          bool     // a DACL that is present but NULL, which grants everyone full access
	protected     bool     // SE_DACL_PROTECTED
	autoInherited bool     // SE_DACL_AUTO_INHERITED
	entries       []string // every ACE as the bytes of the DACL hold it: type, flags, size, mask and trustee
}

// sameAccess reports whether a and b let the same principals open the file: the same entries with the same flags in the same order, and the same
// SE_DACL_PROTECTED. SE_DACL_AUTO_INHERITED is not part of it.
func (a access) sameAccess(b access) bool {
	return a.absent == b.absent && a.null == b.null && a.protected == b.protected && slices.Equal(a.entries, b.entries)
}

// carryVerdict is the result of a carry of the access wanted, read back as got: nil when they are equal, [errAutoInheritedDiffers] when only
// SE_DACL_AUTO_INHERITED is not, [errAccessDiffers] when the access is not.
func carryVerdict(want, got access) error {
	if !want.sameAccess(got) {
		return errAccessDiffers
	}
	if want.autoInherited != got.autoInherited {
		return errAutoInheritedDiffers
	}
	return nil
}

// ignoreOwnerRefusal drops the errors a token gets for an owner or a group it may not set, as an EPERM from chown is dropped on Unix: the file then belongs to
// whom it belonged. Any other error is returned.
func ignoreOwnerRefusal(err error) error {
	for _, refusal := range []error{errAccessDenied, errInvalidOwner, errInvalidPrimaryGroup, errPrivilegeNotHeld} {
		if errors.Is(err, refusal) {
			return nil
		}
	}
	return err
}
