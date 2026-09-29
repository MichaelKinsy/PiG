package packagecontent_test

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
)

// Measure the startup validation and collection passes over explicit settings file URLs.
func BenchmarkConfiguredFileURLResolution(b *testing.B) {
	for _, count := range []int{0, 32, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			entries := make([]string, count)
			for i := range count {
				path := filepath.Join(root, fmt.Sprintf("prompt %d.md", i))
				if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
					b.Fatal(err)
				}
				entries[i] = (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}).String()
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := packagecontent.ValidateConfiguredEntries(entries, root); err != nil {
					b.Fatal(err)
				}
				paths, err := packagecontent.ResolveConfiguredWithError(entries, root, packagecontent.Prompts)
				if err != nil || len(paths) != count {
					b.Fatalf("resolved %d of %d paths: %v", len(paths), count, err)
				}
			}
		})
	}
}
