//go:build !pig_strip_extension_sdk_lisp

package pigsdk

import (
	"github.com/MichaelKinsy/PiG/internal/pigstrip"

	lispsdk "github.com/MichaelKinsy/PiG/extensions/sdk-lisp"
)

// pig additive (D92): only a build that embeds the Lisp SDK registers its staged directory.
func init() { registerBundle("lisp", lispBundle) }

// lispBundle is the embedded Lisp SDK, absent when the active Piglet strips it.
// pig additive (D92): a Piglet Binary built with pig_strip_extension_sdk_lisp links sdk_lisp_off.go instead and embeds no Lisp SDK.
func lispBundle() (sdkBundle, bool) {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.ExtensionSDKLisp) {
		return sdkBundle{}, false
	}
	return sdkBundle{lang: "lisp", stagedHash: lispsdk.StagedHash(), unpack: func() ([]byte, error) { return decodeBundle(lispsdk.Source) }}, true
}
