//go:build windows

package testenv

import (
	"testing"

	"golang.org/x/sys/windows"
)

// ReadOnlyDir lets the current user list and traverse dir but not add or
// remove its entries until the test ends: a protected, inherited DACL that
// grants the user only read and execute, the Windows form of mode 0555.
func ReadOnlyDir(t testing.TB, dir string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	if err := setProtectedDACL(dir, "D:P(A;OICI;FRFX;;;"+sid+")"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := setProtectedDACL(dir, "D:P(A;OICI;FA;;;"+sid+")"); err != nil {
			t.Errorf("restore %s: %v", dir, err)
		}
	})
}

func setProtectedDACL(path, sddl string) error {
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
