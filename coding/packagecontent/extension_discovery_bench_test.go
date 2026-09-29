package packagecontent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkNodeConventionalExtensionDiscovery(b *testing.B) {
	for _, count := range []int{0, 1, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			for i := range count {
				name := fmt.Sprintf("extension-%04d.ts", i)
				if i == 0 {
					name = "main.ts"
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte("export default function() {}"), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if got := DiscoverAutomatic(root, Extensions); len(got) != count {
					b.Fatalf("discovered %d of %d extension files", len(got), count)
				}
			}
		})
	}
}
