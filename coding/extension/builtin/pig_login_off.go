//go:build pig_strip_pig_login

package builtin

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): a build without pig-login records it as stripped, so `builtin:pig-login` is filtered out silently. The
// piglogin package stays linked because internal/codingagent draws its sprite; only the `/sprite` extension leaves.
func init() { pigstrip.Strip(pigstrip.ListExtensions, "pig-login") }

func pigLoginEntries() []Extension { return nil }
