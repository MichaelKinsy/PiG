package runtimecell

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// D92: a packed Python, Rust or Go cell whose SDK the active Piglet strips
// reports the strip instead of "cannot locate ... SDK", and does not fall back
// to a staged copy or a source checkout. Unstripped, the checkout this test
// runs in serves each SDK.
func TestStripExtensionSDKRuntimeReportsStrip(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_SDK_PY_ROOT", "")
	t.Setenv("PIG_SDK_RS_ROOT", "")
	t.Setenv("PIG_SDK_GO_ROOT", "")
	t.Setenv("PIG_SOURCE_ROOT", "")
	for _, tc := range []struct {
		id, want string
		find     func() (string, error)
	}{
		{pigstrip.ExtensionSDKPython, "The Python extension SDK is stripped from this Piglet (strip.features: extension-sdk-python)", findPythonSDKRoot},
		{pigstrip.ExtensionSDKRust, "The Rust extension SDK is stripped from this Piglet (strip.features: extension-sdk-rust)", func() (string, error) { return findRustSDKRoot(nil) }},
		{pigstrip.ExtensionSDKGo, "The Go extension SDK is stripped from this Piglet (strip.features: extension-sdk-go)", func() (string, error) { return findSDKRoot(nil) }},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if root, err := tc.find(); err != nil || root == "" {
				t.Fatalf("stock SDK root = %q, %v; want the checkout's SDK", root, err)
			}
			undo := pigstrip.Strip(pigstrip.ListFeatures, tc.id)
			defer undo()
			if root, err := tc.find(); err == nil || err.Error() != tc.want {
				t.Fatalf("stripped SDK root = %q, %v; want %q", root, err, tc.want)
			}
		})
	}
}
