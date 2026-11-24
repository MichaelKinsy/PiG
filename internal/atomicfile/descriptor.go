package atomicfile

import (
	"context"
	"errors"
	"slices"
	"syscall"
)

// What a replacement must keep of a file's security descriptor and how the Windows replacement orders the descriptor carry and the move are decided here,
// on every platform, so that tests can drive every branch without Windows. The Win32 calls live in descriptor_windows.go and replace_windows.go.

// Errors of Windows (winerror.h) that a token gets for an owner or a group it may not give a file. They are numbers so that the decision is the same code on
// every platform and a test can inject them.
const (
	errAccessDenied        = syscall.Errno(5)    // ERROR_ACCESS_DENIED: the file cannot be opened for WRITE_OWNER
	errInvalidOwner        = syscall.Errno(1307) // ERROR_INVALID_OWNER
	errInvalidPrimaryGroup = syscall.Errno(1308) // ERROR_INVALID_PRIMARY_GROUP
	errPrivilegeNotHeld    = syscall.Errno(1314) // ERROR_PRIVILEGE_NOT_HELD
)

// MoveFileExW flags used by the Windows replacement (winbase.h). Defined here as constants so the step logic and its cross-platform tests verify the exact
// flags passed to MoveFileExW without needing a Windows build.
const (
	moveReplaceExisting = 0x1 // MOVEFILE_REPLACE_EXISTING
	moveWriteThrough    = 0x8 // MOVEFILE_WRITE_THROUGH
)

// errAutoInheritedDiffers is what a carry of a security descriptor returns when the entries of the DACL and SE_DACL_PROTECTED, which decide who may open the
// file, are as the old file's but SE_DACL_AUTO_INHERITED is not: a file system may set that bookkeeping flag whatever the caller asked for. It is not a
// failure; a carry that returns any other error did not give the file the old access.
var errAutoInheritedDiffers = errors.New("the security descriptor has the access of the old file but not its SE_DACL_AUTO_INHERITED flag")

// errAccessDiffers is what a carry of a security descriptor returns when the access of the file is not the access it was to be given.
var errAccessDiffers = errors.New("the DACL's entries or SE_DACL_PROTECTED are not those of the old file")

// windowsMoveSteps are the calls a Windows replacement performs: carrying the existing file's security descriptor onto the temporary file (when the target
// existed), then moving the temporary file over the target in one MoveFileExW call with MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH, retried under
// fsretry.Retry while another process holds the target open.
type windowsMoveSteps struct {
	carry func(from, to string) error
	move  func(from, to string, flags uint32) error
	retry func(ctx context.Context, op func() error) error
}

// replaceWithMove carries the existing target's security descriptor onto temp (tolerating only a difference in SE_DACL_AUTO_INHERITED) and then moves temp
// over path in a single MoveFileExW call with MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH, retried through steps.retry. Because ReplaceFileW is not
// used, path is never left without a file between two renames; if the carry or move fails, path still holds the old file intact and the caller removes temp.
func replaceWithMove(ctx context.Context, temp, path string, existed bool, steps windowsMoveSteps) error {
	if existed {
		if err := steps.carry(path, temp); err != nil && !errors.Is(err, errAutoInheritedDiffers) {
			return err
		}
	}
	return steps.retry(ctx, func() error {
		return steps.move(temp, path, moveReplaceExisting|moveWriteThrough)
	})
}

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

// carryCalls are the Win32 calls that give a file the access of another, and the call that reads it back (descriptor_windows.go builds them; a test fakes them).
// Each set call may fail or do something other than its name says: the read-back after it decides.
type carryCalls struct {
	// exact sets the entries and SE_DACL_PROTECTED as they are given, through the low-level call that runs no inheritance. The Windows job showed that it
	// clears SE_DACL_AUTO_INHERITED whatever the descriptor given had, and keeps the entries of every starting descriptor of the matrix.
	exact func() error
	// asked is the one route the Windows job has not shown: the same low-level call with the request for auto-inheritance (SE_DACL_AUTO_INHERIT_REQ and
	// SE_DACL_AUTO_INHERITED) in the control of the descriptor given. It is tried only for a descriptor that has SE_DACL_AUTO_INHERITED.
	asked func() error
	// named is SetNamedSecurityInfo, which the Windows job showed sets SE_DACL_AUTO_INHERITED but also runs inheritance, which can add entries to an
	// unprotected DACL and rewrites the inherited flag of the entries of a protected one.
	named func() error
	read  func() (access, error)
}

// carryAccess gives a file the access want and reads it back, and returns what it read last and the errors the set calls returned. A file that already has
// want's access and flag is not touched: nothing is called. Otherwise exact goes first, so that the access is right whatever the other calls do (it is skipped
// when the access already is want's and only the flag is missing, so that it does not clear what is right). When want has SE_DACL_AUTO_INHERITED and the file
// has not, asked and then named (when it is not nil) are tried in that order, each followed by a read-back: the first that leaves want's access and the flag
// ends it, and a call that leaves other access is undone by exact again, which leaves the flag off. So the result has want's access whenever exact gave it,
// and the flag when a call set it without touching the access.
func carryAccess(want access, calls carryCalls) (got access, callErrs, err error) {
	apply := func(call func() error) error {
		callErrs = errors.Join(callErrs, call())
		got, err = calls.read()
		return err
	}
	if got, err = calls.read(); err != nil || carryVerdict(want, got) == nil {
		return got, nil, err
	}
	if !want.autoInherited || !want.sameAccess(got) {
		if apply(calls.exact) != nil || !want.autoInherited || !want.sameAccess(got) || got.autoInherited {
			return got, callErrs, err
		}
	}
	for _, call := range []func() error{calls.asked, calls.named} {
		if call == nil {
			continue
		}
		if apply(call) != nil || carryVerdict(want, got) == nil {
			return got, callErrs, err
		}
		if !want.sameAccess(got) && (apply(calls.exact) != nil || !want.sameAccess(got)) {
			return got, callErrs, err
		}
	}
	return got, callErrs, err
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
