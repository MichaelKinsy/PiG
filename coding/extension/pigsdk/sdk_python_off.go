//go:build pig_strip_extension_sdk_python

package pigsdk

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): this Piglet Binary compiled out the embedded Python SDK, so it stages none and a Python build that needs it reports the strip.
func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.ExtensionSDKPython) }

func pythonBundle() (sdkBundle, bool) { return sdkBundle{}, false }
