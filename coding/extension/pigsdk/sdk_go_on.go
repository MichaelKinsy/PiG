//go:build !pig_strip_extension_sdk_go

package pigsdk

import (
	"github.com/MichaelKinsy/PiG/internal/gosdkbundle"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): only a build that embeds the Go SDK registers its staged directory.
func init() { registerBundle("go", goBundle) }

// goBundle is the embedded Go SDK, absent when the active Piglet strips it.
// pig additive (D92): a Piglet Binary built with pig_strip_extension_sdk_go links sdk_go_off.go instead and embeds no Go SDK.
func goBundle() (sdkBundle, bool) {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.ExtensionSDKGo) {
		return sdkBundle{}, false
	}
	return sdkBundle{lang: "go", stagedHash: gosdkbundle.StagedHash(), unpack: func() ([]byte, error) { return decodeBundle(gosdkbundle.Source) }}, true
}
