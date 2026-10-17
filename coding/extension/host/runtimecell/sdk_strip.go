package runtimecell

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// StrippedSDKError reports the error for a language ("go", "rust" or
// "python") whose embedded extension SDK this process runs without, or nil.
// pig additive (D92): a Piglet strips an extension SDK, so a build that needs
// the SDK this binary would stage reports the strip instead of a missing SDK.
// Stock PiG strips none.
func StrippedSDKError(lang string) error {
	var id, what string
	switch lang {
	case "go":
		id, what = pigstrip.ExtensionSDKGo, "The Go extension SDK"
	case "rust":
		id, what = pigstrip.ExtensionSDKRust, "The Rust extension SDK"
	case "python":
		id, what = pigstrip.ExtensionSDKPython, "The Python extension SDK"
	default:
		return nil
	}
	if !pigstrip.Has(pigstrip.ListFeatures, id) {
		return nil
	}
	return pigstrip.Error(what, pigstrip.ListFeatures, id)
}
