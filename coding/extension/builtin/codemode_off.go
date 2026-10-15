//go:build pig_strip_codemode

package builtin

import (
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): a build without codemode records it as stripped, so `builtin:codemode` is filtered out silently.
func init() { pigstrip.Strip(pigstrip.ListExtensions, "codemode") }

// CodemodeOptions is empty in a build without codemode: the sandbox, its wasm module and wazero are not linked
// (docs/specs/builtin-codemode-tool-search.md, "Strip").
type CodemodeOptions struct{}

func codemodeEntries(Options) []Extension { return nil }

// ConfigureCodemode does nothing: this build has no codemode.
func (o *Options) ConfigureCodemode(string) {}
