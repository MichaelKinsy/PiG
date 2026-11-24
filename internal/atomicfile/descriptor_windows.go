//go:build windows

package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Pi writes a file in place, so a write leaves the whole security descriptor and file attributes of the file as they were. A replacement has to give the
// temporary file the old file's descriptor and user-settable attributes before moving it into place, and Windows has three kinds of part in a security descriptor:
//   - the access: the DACL's entries with each entry's flags, and SE_DACL_PROTECTED. They decide who may open the file, so a replacement that changes
//     them changes the security of the file, and a replacement that cannot keep them is not made (see [carryDescriptor]);
//   - the bookkeeping: SE_DACL_AUTO_INHERITED, which only says that the entries were set by a program that keeps inherited entries up to date. A file system may
//     set it whatever the caller asked for, so a carry that leaves only that flag different is a success ([errAutoInheritedDiffers]);
//   - the owner and the group, which are carried as far as the caller's token may set them, as on Unix.
//
// In addition, user-settable Win32 file attributes (HIDDEN, SYSTEM, NOT_CONTENT_INDEXED) that MoveFileExW does not copy from the destination are
// carried onto the temporary file before the move. FILE_ATTRIBUTE_ARCHIVE is intentionally not copied from a cleared old file: a newly written file
// keeps ARCHIVE set so backup software sees that its content changed, matching an in-place write.

// descriptorParts are the parts of a security descriptor a replacement carries.
const descriptorParts = windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION

// carriedFileAttributes are the user-settable Win32 file attributes that a MoveFileExW replacement carries from the existing file onto the temporary file.
// Read-only files are already rejected by WriteFile's write probe; FILE_ATTRIBUTE_ARCHIVE stays set on the new content as an in-place write leaves it;
// COMPRESSED and ENCRYPTED require FSCTL/EFS APIs and are documented in D651.
const carriedFileAttributes = windows.FILE_ATTRIBUTE_HIDDEN |
	windows.FILE_ATTRIBUTE_SYSTEM |
	windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED

// accessOf reads the access of a descriptor GetNamedSecurityInfo returned.
func accessOf(descriptor *windows.SECURITY_DESCRIPTOR) (access, error) {
	control, _, err := descriptor.Control()
	if err != nil {
		return access{}, err
	}
	state := access{
		protected:     control&windows.SE_DACL_PROTECTED != 0,
		autoInherited: control&windows.SE_DACL_AUTO_INHERITED != 0,
	}
	dacl, _, err := descriptor.DACL()
	switch {
	case errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND):
		state.absent = true
		return state, nil
	case err != nil:
		return access{}, err
	case dacl == nil:
		state.null = true
		return state, nil
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return access{}, err
		}
		//nolint:gosec // G103: the ACE lies in the DACL's buffer, which the descriptor owns, and its header says how long it is.
		state.entries = append(state.entries, string(unsafe.Slice((*byte)(unsafe.Pointer(ace)), ace.Header.AceSize)))
	}
	return state, nil
}

// carryAttributes gives the temporary file the access (and, as far as the token may, the owner and group) and user-settable file attributes
// (HIDDEN, SYSTEM, NOT_CONTENT_INDEXED) of the file it will replace, before any data is written to it and before it replaces anything.
// It is the Windows form of copying the extended attributes: a temporary file that cannot take the access is an error, which WriteFile answers with
// an in-place write, as Pi writes, so that the file never has other access than it had.
func carryAttributes(temp *os.File, path string) error {
	if err := carryDescriptor(path, temp.Name()); err != nil && !errors.Is(err, errAutoInheritedDiffers) {
		return err
	}
	return carryFileAttributes(path, temp.Name())
}

// carryFileAttributes copies HIDDEN, SYSTEM and NOT_CONTENT_INDEXED from from onto to without clearing FILE_ATTRIBUTE_ARCHIVE on to.
func carryFileAttributes(from, to string) error {
	srcPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	srcAttrs, err := windows.GetFileAttributes(srcPtr)
	if err != nil {
		return err
	}
	want := srcAttrs & carriedFileAttributes
	dstPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	dstAttrs, err := windows.GetFileAttributes(dstPtr)
	if err != nil {
		return err
	}
	next := (dstAttrs &^ carriedFileAttributes) | want
	if next == dstAttrs {
		return nil
	}
	if next == 0 {
		next = windows.FILE_ATTRIBUTE_NORMAL
	}
	return windows.SetFileAttributes(dstPtr, next)
}

// carryDescriptor gives the temporary file at to the security descriptor of the file at from and reads the descriptor of to back ([carryAccess] decides
// which calls). It returns nil when the access and SE_DACL_AUTO_INHERITED are as from's, [errAutoInheritedDiffers] when only SE_DACL_AUTO_INHERITED is
// not, and any other error when the access is not: whatever the calls themselves reported, the read-back decides. The owner and the group are not read back:
// a token that may not set them (ERROR_INVALID_OWNER, ERROR_INVALID_PRIMARY_GROUP, ERROR_PRIVILEGE_NOT_HELD, or no WRITE_OWNER on the file) leaves them as
// they are, as chown does on Unix. A file system that keeps no security descriptor (the file at from has none: ERROR_NOT_SUPPORTED, ERROR_INVALID_FUNCTION)
// has nothing to carry.
func carryDescriptor(from, to string) error {
	source, err := windows.GetNamedSecurityInfo(from, windows.SE_FILE_OBJECT, descriptorParts)
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return nil
	}
	if err != nil {
		return err
	}
	want, err := accessOf(source)
	if err != nil {
		return err
	}
	if want.absent {
		return nil
	}
	calls := carryCalls{
		exact: func() error { return setAccess(to, source, want) },
		asked: func() error { return setAccessAsked(from, to, want) },
		named: func() error { return setAccessNamed(to, source, want) },
		read:  func() (access, error) { return readAccess(to) },
	}
	ownerErr := setOwnerAndGroup(to, source)
	got, setErr, err := carryAccess(want, calls)
	setErr = errors.Join(ownerErr, setErr)
	if err != nil {
		return errors.Join(err, setErr)
	}
	verdict := carryVerdict(want, got)
	if verdict == nil {
		return nil
	}
	err = fmt.Errorf("%s -> %s: %w: %s", from, to, verdict, describeDifference(source, to))
	if errors.Is(verdict, errAccessDiffers) {
		err = errors.Join(err, setErr)
	}
	return err
}

// readAccess is the access of the file at path.
func readAccess(path string) (access, error) {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return access{}, err
	}
	return accessOf(descriptor)
}

// describeDifference prints the descriptor that was to be carried and the descriptor of to, in SDDL, for an error message.
func describeDifference(source *windows.SECURITY_DESCRIPTOR, to string) string {
	have := "unreadable"
	if descriptor, err := windows.GetNamedSecurityInfo(to, windows.SE_FILE_OBJECT, descriptorParts); err == nil {
		have = descriptor.String()
	}
	return "wanted " + source.String() + ", have " + have
}

// daclInformation names the DACL and says whether it is protected, for the calls that set it.
func daclInformation(want access) windows.SECURITY_INFORMATION {
	if want.protected {
		return windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION
}

// setAccess sets the DACL of the file at to to the DACL of source, with the protection source has: through the file's handle, opened for WRITE_DAC alone,
// with SetKernelObjectSecurity, which hands the descriptor to the file system as it is. Microsoft documents that function for kernel objects and points to
// SetFileSecurity for files, which opens the file by name and calls the same system service. SHOWN BY RUN 43699097748: it keeps the entries and
// SE_DACL_PROTECTED of every starting descriptor of the matrix (a source that is not protected and has no entries other than the temporary file's leaves the
// temporary file protected), and leaves SE_DACL_AUTO_INHERITED off whatever source has.
func setAccess(to string, source *windows.SECURITY_DESCRIPTOR, want access) error {
	handle, err := openSecurityHandle(to, windows.WRITE_DAC)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	return windows.SetKernelObjectSecurity(handle, daclInformation(want), source)
}

// setAccessAsked is setAccess with the request for auto-inheritance in the control of the descriptor given (SE_DACL_AUTO_INHERIT_REQ and
// SE_DACL_AUTO_INHERITED), on a copy of the source read again from from: the one route NOT SHOWN BY RUN, which the matrix's probe prints. The file system
// takes the flag from the control of the descriptor it is given when it is asked for auto-inheritance (SetPrivateObjectSecurityEx's SEF_DACL_AUTO_INHERIT is
// the same request); that it does so through this call is a reading of the documentation, and the read-back after the call decides.
func setAccessAsked(from, to string, want access) error {
	source, err := windows.GetNamedSecurityInfo(from, windows.SE_FILE_OBJECT, descriptorParts)
	if err != nil {
		return err
	}
	if err := source.SetControl(windows.SE_DACL_AUTO_INHERIT_REQ|windows.SE_DACL_AUTO_INHERITED, windows.SE_DACL_AUTO_INHERIT_REQ|windows.SE_DACL_AUTO_INHERITED); err != nil {
		return err
	}
	handle, err := openSecurityHandle(to, windows.WRITE_DAC)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	return windows.SetKernelObjectSecurity(handle, daclInformation(want), source)
}

// setAccessNamed is SetNamedSecurityInfo with the DACL and the protection of source. SHOWN BY RUN: it sets SE_DACL_AUTO_INHERITED, and it runs inheritance:
// the parent's inheritable entries are added to an unprotected DACL that lacks them, and the inherited flag of the entries of a protected DACL is cleared.
func setAccessNamed(to string, source *windows.SECURITY_DESCRIPTOR, want access) error {
	dacl, _, err := source.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(to, windows.SE_FILE_OBJECT, daclInformation(want), nil, nil, dacl, nil)
}

// setOwnerAndGroup sets the owner and the group of the file at to to source's where they differ and the token may set them. An owner that the token may
// not give a file is not an error (the file then belongs to whom it belonged), as an EPERM from chown is not on Unix.
func setOwnerAndGroup(to string, source *windows.SECURITY_DESCRIPTOR) error {
	owner, _, ownerErr := source.Owner()
	group, _, groupErr := source.Group()
	if ownerErr != nil || groupErr != nil {
		return errors.Join(ownerErr, groupErr)
	}
	current, err := windows.GetNamedSecurityInfo(to, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	haveOwner, _, _ := current.Owner()
	haveGroup, _, _ := current.Group()
	var info windows.SECURITY_INFORMATION
	if owner != nil && (haveOwner == nil || !owner.Equals(haveOwner)) {
		info |= windows.OWNER_SECURITY_INFORMATION
	}
	if group != nil && (haveGroup == nil || !group.Equals(haveGroup)) {
		info |= windows.GROUP_SECURITY_INFORMATION
	}
	if info == 0 {
		return nil
	}
	handle, err := openSecurityHandle(to, windows.WRITE_OWNER)
	if err != nil {
		return ignoreOwnerRefusal(err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	return ignoreOwnerRefusal(windows.SetKernelObjectSecurity(handle, info, source))
}

// openSecurityHandle opens the file at path for the access to its security descriptor given, which asks for no data access, so that it conflicts with no
// handle another process (or the writer's own temporary file) has open.
func openSecurityHandle(path string, rights uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, rights, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
}
