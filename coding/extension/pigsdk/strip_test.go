//go:build !pig_strip_extension_sdk_go && !pig_strip_extension_sdk_rust && !pig_strip_extension_sdk_python

package pigsdk

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// D92: a stripped extension SDK is neither staged nor reported as an unknown
// language: `pig reload --sdk-path <lang>`, `pig extension init --lang <lang>`
// (EnsureSyncedLang, SDKDirFor) and `--sdk-version` name the strip. The other
// languages stage as before.
func TestStripExtensionSDKRuntimeReportsStrip(t *testing.T) {
	for _, tc := range []struct{ lang, id, what, dir string }{
		{"go", pigstrip.ExtensionSDKGo, "The Go extension SDK", "sdk"},
		{"python", pigstrip.ExtensionSDKPython, "The Python extension SDK", "sdk-py"},
		{"rust", pigstrip.ExtensionSDKRust, "The Rust extension SDK", "sdk-rs"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PIG_HOME", root)
			if _, err := SDKDirFor(root, tc.lang); err != nil {
				t.Fatalf("stock SDKDirFor(%s): %v", tc.lang, err)
			}

			undo := pigstrip.Strip(pigstrip.ListFeatures, tc.id)
			defer undo()
			want := pigstrip.Error(tc.what, pigstrip.ListFeatures, tc.id).Error()
			if _, err := SDKDirFor(root, tc.lang); err == nil || err.Error() != want {
				t.Fatalf("stripped SDKDirFor = %v, want %q", err, want)
			}
			if err := EnsureSyncedLang(root, tc.lang); err == nil || err.Error() != want {
				t.Fatalf("stripped EnsureSyncedLang = %v, want %q", err, want)
			}
			for _, flag := range []string{"--sdk-path", "--sdk-version"} {
				var stdout, stderr bytes.Buffer
				if code := RunCommand([]string{"reload", flag, tc.lang}, &stdout, &stderr); code != 1 || stderr.String() != "pig reload: "+want+"\n" {
					t.Fatalf("pig reload %s %s = %d, stderr %q, want %q", flag, tc.lang, code, stderr.String(), "pig reload: "+want)
				}
			}
			if err := EnsureSynced(root); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "state", "pigsdk", tc.dir)); !os.IsNotExist(err) {
				t.Fatalf("stripped %s SDK was staged: %v", tc.lang, err)
			}
			statuses, err := Status(root)
			if err != nil {
				t.Fatal(err)
			}
			var langs []string
			for _, st := range statuses {
				langs = append(langs, st.Lang)
				if !st.Current {
					t.Errorf("%s SDK not staged beside the stripped %s", st.Lang, tc.lang)
				}
			}
			if len(langs) != 2 || slices.Contains(langs, tc.lang) {
				t.Fatalf("Status languages = %v, want the two unstripped ones", langs)
			}
		})
	}
}
