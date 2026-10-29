package contracts

// Ports packages/chord/src/next/contracts/names.ts.

// Names that become SQL names or stored identities in the store (decision "Restricted names"): object types, host
// kinds, tables, state definitions and jobs use npm package-name characters, lowercase, so `<host kind>:<object
// type>:<table>` is injective and readable, and a document's or a job record's name is stored as declared.

// CheckName fails unless name is a nonempty lowercase name of `[a-z0-9@/._~-]` (`:` cannot occur, so names joined
// with it stay apart); what names it in the error.
func CheckName(name, what string) error {
	valid := name != ""
	for i := 0; valid && i < len(name); i++ {
		c := name[i]
		valid = c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '@' || c == '/' || c == '.' || c == '_' || c == '~' || c == '-'
	}
	if !valid {
		return fail("%s %s: expected lowercase [a-z0-9@/._~-]", what, quote(name))
	}
	return nil
}
