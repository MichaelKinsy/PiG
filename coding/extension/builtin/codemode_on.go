//go:build !pig_strip_codemode

package builtin

import (
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// CodemodeOptions configures the codemode extension.
type CodemodeOptions = codemode.Options

// codemodeEntries is the codemode registry row, absent while codemode is stripped at runtime as in a build without it.
// pig additive (D92): a runtime stripped codemode registers nothing, like the pig_strip_codemode build.
func codemodeEntries(options Options) []Extension {
	if pigstrip.Has(pigstrip.ListExtensions, "codemode") {
		return nil
	}
	return []Extension{{Name: "codemode", Replaceable: true, Factory: codemode.CreateCodemodeExtension(options.Codemode)}}
}

// ConfigureCodemode sets the sandbox's compilation cache directory. The tool reads the settings through the extension API.
func (o *Options) ConfigureCodemode(cacheDir string) {
	o.Codemode.CacheDir = cacheDir
}
