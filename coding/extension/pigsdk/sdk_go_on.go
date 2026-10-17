//go:build !pig_strip_extension_sdk_go

package pigsdk

import (
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// pig additive (D92): only a build that embeds the Go SDK registers its staged directory.
func init() { registerBundle("go", goBundle) }

// goBundle is the embedded Go SDK, absent when the active Piglet strips it.
// pig additive (D92): a Piglet Binary built with pig_strip_extension_sdk_go links sdk_go_off.go instead and embeds no Go SDK.
func goBundle() (sdkBundle, bool) {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.ExtensionSDKGo) {
		return sdkBundle{}, false
	}
	return sdkBundle{lang: "go", files: goBundleFiles(), fsys: sdk.Source}, true
}

// goBundleFiles lists the embedded Go SDK once per process: the embedded files never change, and staging and the staged directory's hash both read the list, which they never write; its capacity is clipped, so an append copies it.
var goBundleFiles = sync.OnceValue(func() []string { return slices.Clip(sdk.BundledFiles()) })
