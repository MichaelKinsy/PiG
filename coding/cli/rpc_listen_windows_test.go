//go:build windows

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// listenDirectory is a new directory whose access list is its own (it inherits nothing from its parent) and names only the current user, and Users when sharedWithUsers is true. A socket bound inside it inherits that list. These tests run on a Windows machine only (the Windows cli CI jobs of the PR); the table in TestListenSocketRefusal runs everywhere.
func listenDirectory(t *testing.T, sharedWithUsers bool) string {
	t.Helper()
	dir := shortTempDir(t)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	grant := func(sid *windows.SID, kind windows.TRUSTEE_TYPE) windows.EXPLICIT_ACCESS {
		return windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  kind,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}
	entries := []windows.EXPLICIT_ACCESS{grant(user.User.Sid, windows.TRUSTEE_IS_USER)}
	if sharedWithUsers {
		users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, grant(users, windows.TRUSTEE_IS_GROUP))
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestListenRefusesASocketInADirectoryUsersCanReach: a directory whose access list grants Users access gives the socket that access, so the bind is refused with the SID, the way out is named, and the socket this server created is removed (D232).
func TestListenRefusesASocketInADirectoryUsersCanReach(t *testing.T) {
	path := filepath.Join(listenDirectory(t, true), "s.sock")
	listener, err := listenPrivateUnixSocket(path)
	if err == nil {
		_ = listener.Close()
		t.Fatal("the socket in a directory that grants Users access was served")
	}
	for _, want := range []string{"S-1-5-32-545", "Users", "LOCALAPPDATA"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not contain %q", err, want)
		}
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused socket is still there: %v", err)
	}
}

// TestListenServesASocketInAnOwnerOnlyDirectory: a directory only the current user can access gives a socket the check accepts.
func TestListenServesASocketInAnOwnerOnlyDirectory(t *testing.T) {
	path := filepath.Join(listenDirectory(t, false), "s.sock")
	listener, err := listenPrivateUnixSocket(path)
	if err != nil {
		t.Fatalf("the socket in an owner-only directory was refused: %v", err)
	}
	created := lstatPinned(path)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	removeOwnSocket(path, created)
}
