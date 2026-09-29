package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkNodeRuntimeMaterialization(b *testing.B) {
	destination := filepath.Join(b.TempDir(), "runtime")
	for b.Loop() {
		if err := os.Mkdir(destination, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := copyEmbeddedTree(nodeRuntimeFS, "runtime-node", destination); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := os.RemoveAll(destination); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}
