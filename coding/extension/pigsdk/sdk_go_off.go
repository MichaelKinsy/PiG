//go:build pig_strip_extension_sdk_go

package pigsdk

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): this Piglet Binary compiled out the embedded Go SDK, so it stages none and a Go build that needs it reports the strip.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.ExtensionSDKGo) }

func goBundle() (sdkBundle, bool) { return sdkBundle{}, false }
