//go:build !pig_strip_extension_sdk_go && !pig_strip_extension_sdk_python && !pig_strip_extension_sdk_rust && !pig_strip_extension_sdk_lisp

package pigsdk

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	lispsdk "github.com/MichaelKinsy/PiG/extensions/sdk-lisp"
	pysdk "github.com/MichaelKinsy/PiG/extensions/sdk-py"
	rssdk "github.com/MichaelKinsy/PiG/extensions/sdk-rs"
	"github.com/MichaelKinsy/PiG/internal/gosdkbundle"
	"github.com/MichaelKinsy/PiG/internal/sdkpack"
)

// bundleSources lists each language's bundle with the directory of the source tree it packs and the file list its package reports.
var bundleSources = []struct {
	lang, dir string
	files     func() []string
	hash      func() string
}{
	{"go", "../../../extensions/sdk", gosdkbundle.BundledFiles, gosdkbundle.StagedHash},
	{"python", "../../../extensions/sdk-py", pysdk.BundledFiles, pysdk.StagedHash},
	{"rust", "../../../extensions/sdk-rs", rssdk.BundledFiles, rssdk.StagedHash},
	{"lisp", "../../../extensions/sdk-lisp", lispsdk.BundledFiles, lispsdk.StagedHash},
}

// Each bundle's frame holds exactly the files its package lists, in that order, with the bytes of the source tree, and the hash its package reports is their staging digest: after an edit to an SDK source, run make sdk-bundles. (The SDK packages carry no decoder, so only here can the frame be read.)
func TestEmbeddedBundlesHoldTheirSources(t *testing.T) {
	for _, c := range bundleSources {
		t.Run(c.lang, func(t *testing.T) {
			b, ok := bundleFor(c.lang)
			if !ok {
				t.Fatalf("no %s bundle", c.lang)
			}
			if b.stagedHash != c.hash() {
				t.Fatalf("pigsdk hash %s, package hash %s", b.stagedHash, c.hash())
			}
			blob, err := b.unpack()
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			err = sdkpack.Walk(blob, func(name string, data []byte) error {
				names = append(names, name)
				want, err := os.ReadFile(filepath.Join(c.dir, filepath.FromSlash(name)))
				if err != nil {
					return err
				}
				if !bytes.Equal(data, want) {
					t.Errorf("%s is %d bytes in the bundle, %d on disk: run make sdk-bundles", name, len(data), len(want))
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(names, c.files()) {
				t.Errorf("the bundle holds %d files, its package lists %d: run make sdk-bundles", len(names), len(c.files()))
			}
			if sdkpack.Hash(blob) != c.hash() {
				t.Errorf("the bundle's files hash to %s, its package reports %s: run make sdk-bundles", sdkpack.Hash(blob), c.hash())
			}
		})
	}
}

// Staging writes every listed file under its own name with the source tree's bytes, in a directory that holds nothing else but the marker.
func TestSyncStagesTheSourceFiles(t *testing.T) {
	root := t.TempDir()
	counts, _, err := Sync(root, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range bundleSources {
		dir, err := SDKDirFor(root, c.lang)
		if err != nil {
			t.Fatal(err)
		}
		if counts[c.lang] != len(c.files()) {
			t.Errorf("%s staged %d files, the package lists %d", c.lang, counts[c.lang], len(c.files()))
		}
		for _, name := range c.files() {
			staged, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(c.dir, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(staged, want) {
				t.Errorf("staged %s/%s (%d bytes) is not the source file (%d bytes)", c.lang, name, len(staged), len(want))
			}
		}
	}
}
