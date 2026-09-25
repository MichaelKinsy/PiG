package subprocess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

func TestCurrentCacheEntriesProtectsStartupArtifactAndReusesSecondBuild(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	source := filepath.Join(t.TempDir(), "standalone")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module example.com/cache-current\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(home, "cache")
	builder := NewBuilder(filepath.Join(cacheRoot, "ext"))
	first, err := builder.Build("current", source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := builder.Build("current", source)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached || !second.Cached || first.BinaryPath != second.BinaryPath {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	config := ExtConfig{Name: "current", Source: source, RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "standalone", Isolation: "isolated", Enabled: true}
	current, errs := CurrentCacheEntries([]ExtConfig{config}, cacheRoot)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	entry := filepath.Dir(first.BinaryPath)
	if _, ok := current[entry]; !ok {
		t.Fatalf("current roots = %v, want %s", current, entry)
	}
	if _, err := runtimecell.PruneCaches(runtimecell.CacheLifecycleOptions{CacheRoot: cacheRoot, Current: current}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.BinaryPath); err != nil {
		t.Fatalf("current startup artifact removed by prune: %v", err)
	}
}
