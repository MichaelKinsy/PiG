//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// D92: with node-extensions stripped at runtime, a TypeScript extension build,
// a packed Node cell and the Node preflight all report the strip the way a
// pig_strip_node_extensions Binary does; unstripped they proceed as before.
func TestStripNodeExtensionsRuntimeReportsStrip(t *testing.T) {
	want := "The TypeScript and JavaScript extension runtime is stripped from this Piglet (strip.features: node-extensions)"
	src := filepath.Join(t.TempDir(), "hello.ts")
	if err := os.WriteFile(src, []byte("export default function (pi: any) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheRoot := t.TempDir()
	exts := []nodeExtension{{Name: "hello", Entry: src, Hash: "h"}}

	// Unstripped: the embedded runtime materializes, so the packed cell builds.
	if _, err := buildNodePackedCell(t.Context(), filepath.Join(cacheRoot, "stock"), "node:stock", exts); err != nil {
		t.Fatalf("stock packed Node cell: %v", err)
	}

	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.NodeExtensions))
	builder := NewBuilderWithConfigRoot(filepath.Join(cacheRoot, "ext"), t.TempDir())
	if _, err := builder.BuildContext(t.Context(), "hello", src); err == nil || err.Error() != want {
		t.Fatalf("stripped TypeScript build error = %v, want %q", err, want)
	}
	if _, err := buildNodePackedCell(t.Context(), filepath.Join(cacheRoot, "stripped"), "node:stripped", exts); err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("stripped packed Node cell error = %v, want suffix %q", err, want)
	}
	if _, err := ensureNodeRuntime(t.Context()); err == nil || err.Error() != want {
		t.Fatalf("stripped Node preflight error = %v, want %q", err, want)
	}
	if entries, _ := os.ReadDir(filepath.Join(cacheRoot, "ext")); len(entries) != 0 {
		t.Fatalf("stripped TypeScript build wrote cache entries: %v", entries)
	}
}
