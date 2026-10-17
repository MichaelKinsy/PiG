//go:build !pig_strip_extension_sdk_go && !pig_strip_extension_sdk_python && !pig_strip_extension_sdk_rust

package pigsdk

import (
	"slices"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pysdk "github.com/MichaelKinsy/PiG/extensions/sdk-py"
	rssdk "github.com/MichaelKinsy/PiG/extensions/sdk-rs"
)

var bundleFilesSink []sdkBundle

// Each language's bundle lists what its SDK package lists, from one walk of the embedded files per process.
func TestBundlesShareOneFileListPerLanguage(t *testing.T) {
	for _, c := range []struct {
		lang   string
		listed func() []string
		walk   func() []string
	}{
		{"go", goBundleFiles, sdk.BundledFiles},
		{"python", pythonBundleFiles, pysdk.BundledFiles},
		{"rust", rustBundleFiles, rssdk.BundledFiles},
	} {
		got, want := c.listed(), c.walk()
		if len(want) == 0 || !slices.Equal(got, want) {
			t.Fatalf("%s bundle lists %d files, the SDK %d, or they differ", c.lang, len(got), len(want))
		}
		if cap(got) != len(got) {
			t.Fatalf("%s bundle list has capacity %d for %d files; an append would write into the shared list", c.lang, cap(got), len(got))
		}
		bundle, ok := bundleFor(c.lang)
		if !ok || &bundle.files[0] != &got[0] {
			t.Fatalf("the %s bundle does not use the shared list", c.lang)
		}
	}
	walk := testing.AllocsPerRun(5, func() { _ = sdk.BundledFiles() })
	if allocs := testing.AllocsPerRun(5, func() { bundleFilesSink = bundles() }); allocs*10 > walk {
		t.Fatalf("bundles() = %v allocations, one walk of the Go SDK %v", allocs, walk)
	}
}
