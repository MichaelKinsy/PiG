//go:build nocodemode

package builtin

import "github.com/MichaelKinsy/PiG/coding/extension"

// CodemodeOptions is empty in a build without codemode: the sandbox, its wasm module and wazero are not linked
// (docs/specs/builtin-codemode-tool-search.md, "Strip").
type CodemodeOptions struct{}

func codemodeEntries(Options) []Extension { return nil }

// ConfigureCodemode does nothing: this build has no codemode.
func (o *Options) ConfigureCodemode(string, func() extension.Settings) {}
