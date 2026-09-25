//go:build windows

package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// On Windows an owner-only secret file has a protected DACL (nothing
// inherited) whose only entry grants the current user access.
func TestOwnerOnlySecretFileHasAProtectedCurrentUserDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	writeOwnerOnlySecret(t, path, []byte("value"))
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("secret file DACL inherits entries from its directory")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("secret file DACL = %v (err %v), want exactly one entry", dacl, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// Windows may add the auto-inherited (AI) flag; the protected flag is
	// checked above, so compare the entries.
	entries := strings.TrimPrefix(strings.TrimPrefix(descriptor.String(), "D:PAI"), "D:P")
	if want := "(A;;FA;;;" + user.User.Sid.String() + ")"; entries != want {
		t.Fatalf("secret file DACL = %s, want only %s", descriptor.String(), want)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("owner cannot read its secret file: %v", err)
	}
}
