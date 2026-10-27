//go:build !pig_strip_codemode

package builtin

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/compiledin"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// registerCodemodeReference makes the codemode extension the one an extension names with createCodemodeExtension(options) in a loader it constructs (internal/compiledin).
func registerCodemodeReference() { compiledin.Register("codemode", createCodemodeReference) }

// createCodemodeReference is createCodemodeExtension (index.ts CodemodeExtensionOptions: mode, inlineBudget and models, default true).
func createCodemodeReference(raw json.RawMessage) (extension.InlineExtension, error) {
	var options struct {
		Mode         string   `json:"mode"`
		InlineBudget *float64 `json:"inlineBudget"`
		Models       *bool    `json:"models"`
	}
	if compiledin.HasOptions(raw) {
		if err := json.Unmarshal(raw, &options); err != nil {
			return nil, compiledin.OptionsError("codemode", err)
		}
	}
	if options.Mode != "" && options.Mode != codemode.ModeOn && options.Mode != codemode.ModeOnly {
		return nil, fmt.Errorf(`createCodemodeExtension option mode must be "on" or "only", got %q`, options.Mode)
	}
	built := codemode.Options{Mode: options.Mode, DisableModels: options.Models != nil && !*options.Models}
	if options.InlineBudget != nil {
		budget := int(*options.InlineBudget)
		built.InlineBudget = &budget
	}
	return codemode.CreateCodemodeExtension(built), nil
}

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
