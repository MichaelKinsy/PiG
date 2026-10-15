//go:build pig_strip_extension_sdk_rust

package pigsdk

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): this Piglet Binary compiled out the embedded Rust SDK, so it stages none and a Rust build that needs it reports the strip.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.ExtensionSDKRust) }

func rustBundle() (sdkBundle, bool) { return sdkBundle{}, false }
