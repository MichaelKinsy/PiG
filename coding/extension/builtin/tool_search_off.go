//go:build pig_strip_tool_search

package builtin

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): a build without tool-search records it as stripped, so `builtin:tool-search` is filtered out silently.
// The toolsearch package stays linked while codemode is built in, since codemode reuses its search helpers.
func init() { pigstrip.Strip(pigstrip.ListExtensions, "tool-search") }

func toolSearchEntries() []Extension { return nil }
