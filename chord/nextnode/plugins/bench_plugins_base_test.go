package plugins

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// BenchmarkPluginsWatcherStart is the watcher's start over a base directory of source files, the cost of an app's start with
// the base marks (recordOwn hashes each file once; before them the directory was only watched).
func BenchmarkPluginsWatcherStart(b *testing.B) {
	base := b.TempDir()
	for i := range 300 {
		file := filepath.Join(base, "src", "d"+strconv.Itoa(i%10), "f"+strconv.Itoa(i)+".ts")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("export const value = "+strconv.Itoa(i)+";\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	root := b.TempDir()
	b.ReportAllocs()
	for b.Loop() {
		w := newPluginWatcher(watcherOptions{Roots: []string{root}, Base: []string{base}, Kinds: kinds, Failed: func(error) {}},
			state.NewBatch(func(state.BatchError) {}, nil))
		w.close()
		w.join()
	}
}
