//go:build !pig_strip_extension_sdk_python

package pigsdk

import (
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"

	pysdk "github.com/MichaelKinsy/PiG/extensions/sdk-py"
)

// pig additive (D92): only a build that embeds the Python SDK registers its staged directory.
func init() { registerBundle("python", pythonBundle) }

// pythonBundle is the embedded Python SDK, absent when the active Piglet strips it.
// pig additive (D92): a Piglet Binary built with pig_strip_extension_sdk_python links sdk_python_off.go instead and embeds no Python SDK.
func pythonBundle() (sdkBundle, bool) {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.ExtensionSDKPython) {
		return sdkBundle{}, false
	}
	return sdkBundle{lang: "python", files: pythonBundleFiles(), fsys: pysdk.Source}, true
}

// pythonBundleFiles lists the embedded Python SDK once per process: the embedded files never change, and staging and the staged directory's hash both read the list, which they never write; its capacity is clipped, so an append copies it.
var pythonBundleFiles = sync.OnceValue(func() []string { return slices.Clip(pysdk.BundledFiles()) })
