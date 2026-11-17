package cli

import "fmt"

// windowsACE is one entry of a Windows access list, reduced to what the --listen socket check reads. pig additive (D232).
type windowsACE struct {
	SID   string // the entry's trustee in S-1-... form
	Allow bool   // an access-allowed entry; false is an access-denied one
	Flags uint8  // the entry's AceFlags
	Mask  uint32 // the entry's access mask
}

// windowsDACL is the access list of a file. Null is a file with no list or a NULL one, which grants every user full access. pig additive (D232).
type windowsDACL struct {
	Null    bool
	Entries []windowsACE
}

const (
	// aceInheritOnly marks an entry that applies to the objects created below a directory, not to the object itself.
	aceInheritOnly = 0x08

	// The rights a user needs to connect to the socket file or to take it over: its data, and its access list or owner.
	aclFileReadData   = 0x00000001
	aclFileWriteData  = 0x00000002
	aclFileAppendData = 0x00000004
	aclWriteDAC       = 0x00040000
	aclWriteOwner     = 0x00080000
	aclGenericAll     = 0x10000000
	aclGenericWrite   = 0x40000000
	aclGenericRead    = 0x80000000
	aclDataAccess     = aclFileReadData | aclFileWriteData | aclFileAppendData | aclWriteDAC | aclWriteOwner | aclGenericAll | aclGenericWrite | aclGenericRead

	sidSystem         = "S-1-5-18"
	sidAdministrators = "S-1-5-32-544"
	sidCreatorOwner   = "S-1-3-0"
	sidCreatorGroup   = "S-1-3-1"
)

// wellKnownSIDNames names the principals that most often hold an entry in a shared directory's access list.
var wellKnownSIDNames = map[string]string{
	"S-1-1-0":         "Everyone",
	"S-1-2-1":         "Console Logon",
	sidCreatorOwner:   "CREATOR OWNER",
	sidCreatorGroup:   "CREATOR GROUP",
	"S-1-5-4":         "Interactive",
	"S-1-5-7":         "Anonymous Logon",
	"S-1-5-11":        "Authenticated Users",
	sidSystem:         "SYSTEM",
	sidAdministrators: "Administrators",
	"S-1-5-32-545":    "Users",
	"S-1-5-32-546":    "Guests",
}

// describeSID names a principal for the user: its well-known name and SID, or the SID alone.
func describeSID(sid string) string {
	if name, ok := wellKnownSIDNames[sid]; ok {
		return name + " (" + sid + ")"
	}
	return sid
}

// listenSocketRefusal reports why the --listen socket whose access list is dacl is not private to the user, or "" when it is. Only the user's own SID (user), SYSTEM and Administrators may hold data access: an entry for a group the user belongs to, such as Domain Users, admits every member of the group. Entries that grant nothing to anyone else are ignored: an access-denied entry (it only takes access away; the check does not evaluate entry order, so it never excuses an allow entry), an inherit-only entry (it does not apply to the file itself), the CREATOR OWNER and CREATOR GROUP placeholders (no access token holds them), and an allow entry whose rights include no data access. A NULL access list is refused. pig additive (D232).
func listenSocketRefusal(user string, dacl windowsDACL) string {
	if dacl.Null {
		return "the socket has no access list, which lets every user connect"
	}
	for _, entry := range dacl.Entries {
		if !entry.Allow || entry.Flags&aceInheritOnly != 0 || entry.Mask&aclDataAccess == 0 {
			continue
		}
		switch entry.SID {
		case user, sidSystem, sidAdministrators, sidCreatorOwner, sidCreatorGroup:
			continue
		}
		return fmt.Sprintf("the access list of the socket lets %s connect", describeSID(entry.SID))
	}
	return ""
}
