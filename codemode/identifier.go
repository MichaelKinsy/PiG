package codemode

import "strings"

// ToCodemodeIdentifier returns the identifier a script uses for a tool: characters that are not valid in a
// JavaScript identifier become `_`.
//
// Ports packages/codemode/src/identifier.ts.
func ToCodemodeIdentifier(name string) string {
	var identifier strings.Builder
	for _, char := range name {
		valid := char == '_' || char == '$' || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')
		if identifier.Len() > 0 {
			valid = valid || (char >= '0' && char <= '9')
		}
		if valid {
			identifier.WriteRune(char)
		} else {
			identifier.WriteByte('_')
		}
	}
	if identifier.Len() == 0 {
		return "_"
	}
	return identifier.String()
}
