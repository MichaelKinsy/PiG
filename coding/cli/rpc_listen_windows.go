//go:build windows

package cli

import (
	"errors"
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The callback variants of the access-allowed and access-denied entries start like the plain ones: the header, the mask, then the trustee's SID.
const (
	accessAllowedCallbackACEType = 9
	accessDeniedCallbackACEType  = 10
)

// listenPrivateUnixSocket binds the --listen socket and refuses it when its access list lets any user but the current one (or SYSTEM and Administrators) connect: Windows keeps no mode bits on a socket file, so the file inherits the access list of its directory, and a directory such as C:\Temp would serve a full session to every local user. The listener is closed and the socket removed before any connection is accepted. Go's Unix listener unlinks the bound path on Close; the server removes path itself at exit (removeOwnSocket), so the listener must not. pig additive (D232).
func listenPrivateUnixSocket(path string) (net.Listener, error) {
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if unix, ok := listener.(*net.UnixListener); ok {
		unix.SetUnlinkOnClose(false)
	}
	created := lstatPinned(path)
	if err := requireOwnerOnlySocket(path); err != nil {
		_ = listener.Close()
		removeOwnSocket(path, created)
		return nil, err
	}
	return listener, nil
}

// requireOwnerOnlySocket fails closed: a user or an access list it cannot read refuses the socket as an open one does.
func requireOwnerOnlySocket(path string) error {
	const advice = "serve from a directory only you can access, for example one under %LOCALAPPDATA%"
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("cannot read the current user to check the socket's access list: %w; %s", err, advice)
	}
	dacl, err := readSocketDACL(path)
	if err != nil {
		return fmt.Errorf("cannot read the access list of the socket: %w; %s", err, advice)
	}
	if reason := listenSocketRefusal(token.User.Sid.String(), dacl); reason != "" {
		return fmt.Errorf("%s; %s", reason, advice)
	}
	return nil
}

// readSocketDACL reads the access list of the file at path.
func readSocketDACL(path string) (windowsDACL, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return windowsDACL{}, err
	}
	acl, _, err := sd.DACL()
	if errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) || (err == nil && acl == nil) {
		return windowsDACL{Null: true}, nil
	}
	if err != nil {
		return windowsDACL{}, err
	}
	entries := make([]windowsACE, 0, acl.AceCount)
	for i := range uint32(acl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return windowsDACL{}, err
		}
		var allow bool
		switch ace.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE, accessAllowedCallbackACEType:
			allow = true
		case windows.ACCESS_DENIED_ACE_TYPE, accessDeniedCallbackACEType:
		default:
			return windowsDACL{}, fmt.Errorf("entry %d has the type %d, which this check does not read", i, ace.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		entries = append(entries, windowsACE{SID: sid.String(), Allow: allow, Flags: ace.Header.AceFlags, Mask: uint32(ace.Mask)})
	}
	return windowsDACL{Entries: entries}, nil
}
