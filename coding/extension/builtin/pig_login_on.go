//go:build !pig_strip_pig_login

package builtin

import (
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pigLoginEntries is the pig-login registry row (the `/sprite` command), absent while pig-login is stripped at runtime as in a
// build without it.
// pig additive (D92): a runtime stripped pig-login registers nothing, like the pig_strip_pig_login build.
func pigLoginEntries() []Extension {
	if pigstrip.Has(pigstrip.ListExtensions, piglogin.Name) {
		return nil
	}
	return []Extension{{Name: piglogin.Name, Replaceable: true, Factory: piglogin.Extension}}
}
