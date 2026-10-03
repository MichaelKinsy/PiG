//go:build !nocodemode

package builtin

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
)

// CodemodeOptions configures the codemode extension.
type CodemodeOptions = codemode.Options

func codemodeEntries(options Options) []Extension {
	return []Extension{{Name: "codemode", Replaceable: true, Factory: func() (extension.Extension, error) { return codemode.Extension(options.Codemode) }}}
}

// ConfigureCodemode sets the sandbox's compilation cache directory and the settings source the tool reads on every use.
func (o *Options) ConfigureCodemode(cacheDir string, getSettings func() extension.Settings) {
	o.Codemode.CacheDir, o.Codemode.GetSettings = cacheDir, getSettings
}
