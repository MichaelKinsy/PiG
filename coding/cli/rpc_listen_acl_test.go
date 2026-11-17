package cli

import (
	"strings"
	"testing"
)

// TestListenSocketRefusal: the decision that keeps a --listen socket on Windows private to its user. It runs on every OS; the Windows build feeds it the socket file's access list (rpc_listen_windows_test.go proves that read on real directories).
func TestListenSocketRefusal(t *testing.T) {
	const (
		user     = "S-1-5-21-111-222-333-1001"
		domain   = "S-1-5-21-111-222-333-513" // Domain Users, a group the user belongs to
		other    = "S-1-5-21-111-222-333-1002"
		readData = 0x00000001
		write    = 0x00000002
		generic  = 0x80000000 // GENERIC_READ
		readAttr = 0x00000080 // FILE_READ_ATTRIBUTES
		syncOnly = 0x00100000 // SYNCHRONIZE
		full     = 0x001F01FF // FILE_ALL_ACCESS
		inherit  = aceInheritOnly | 0x03
	)
	allow := func(sid string, mask uint32) windowsACE { return windowsACE{SID: sid, Allow: true, Mask: mask} }
	tests := []struct {
		name string
		dacl windowsDACL
		want []string // every part must appear in the refusal; nil means the socket is private
	}{
		{"only the user", windowsDACL{Entries: []windowsACE{allow(user, full)}}, nil},
		{"user, SYSTEM and Administrators", windowsDACL{Entries: []windowsACE{allow(user, full), allow(sidSystem, full), allow(sidAdministrators, full)}}, nil},
		{"an empty list admits nobody", windowsDACL{}, nil},
		{"Everyone may read", windowsDACL{Entries: []windowsACE{allow(user, full), allow("S-1-1-0", readData)}}, []string{"S-1-1-0", "Everyone"}},
		{"Users may write", windowsDACL{Entries: []windowsACE{allow(user, full), allow("S-1-5-32-545", write)}}, []string{"S-1-5-32-545", "Users"}},
		{"Authenticated Users with a generic right", windowsDACL{Entries: []windowsACE{allow(user, full), allow("S-1-5-11", generic)}}, []string{"S-1-5-11", "Authenticated Users"}},
		{"another user by SID", windowsDACL{Entries: []windowsACE{allow(user, full), allow(other, full)}}, []string{other}},
		{"the user through a group", windowsDACL{Entries: []windowsACE{allow(domain, full)}}, []string{domain}},
		{"a denied entry grants nothing", windowsDACL{Entries: []windowsACE{allow(user, full), {SID: "S-1-1-0", Mask: full}}}, nil},
		{"a denied entry does not excuse an allow entry", windowsDACL{Entries: []windowsACE{{SID: "S-1-5-32-545", Mask: write}, allow("S-1-5-32-545", full)}}, []string{"Users"}},
		{"an inherit-only entry does not apply to the file", windowsDACL{Entries: []windowsACE{allow(user, full), {SID: "S-1-5-32-545", Allow: true, Flags: inherit, Mask: full}}}, nil},
		{"CREATOR OWNER is a placeholder", windowsDACL{Entries: []windowsACE{allow(user, full), allow(sidCreatorOwner, full)}}, nil},
		{"an allow entry without data access", windowsDACL{Entries: []windowsACE{allow(user, full), allow("S-1-1-0", readAttr|syncOnly)}}, nil},
		{"a NULL access list", windowsDACL{Null: true}, []string{"no access list", "every user"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listenSocketRefusal(user, tt.dacl)
			if tt.want == nil {
				if got != "" {
					t.Fatalf("refused a private socket: %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("accepted a socket that other users can connect to")
			}
			for _, part := range tt.want {
				if !strings.Contains(got, part) {
					t.Fatalf("the refusal %q does not name %q", got, part)
				}
			}
		})
	}
}
