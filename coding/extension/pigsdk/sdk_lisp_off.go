//go:build pig_strip_extension_sdk_lisp

package pigsdk

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): this Piglet Binary compiled out the embedded Lisp SDK, so it stages none and a Lisp build that needs it reports the strip.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.ExtensionSDKLisp) }

func lispBundle() (sdkBundle, bool) { return sdkBundle{}, false }
