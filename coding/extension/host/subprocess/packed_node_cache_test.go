package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestBuildNodePackedCellConcurrentColdBuildsDoNotCorrupt is a deterministic
// regression for CNCACHE-001: buildNodePackedCell used to check-then-build
// with no lock and fixed "manifest.json.tmp"/"runner.tmp" names, so
// concurrent cold builds of the same cell (two Pig instances sharing a cache
// root, or two goroutines racing a reload) corrupted each other -
// os.Rename(tmp, path) from one builder could remove the file another
// builder's copyEmbeddedTree was still writing, or one winner's rename could
// race a second winner's rename onto the same fixed name, leaving ENOENT
// failures behind. buildNodePackedCell now shares runtimecell.PublishArtifact
// (the same per-digest-locked, atomic-rename primitive every packed
// Go/Rust/Python cell and every source-mode extension build already uses),
// so this asserts the same guarantee here: every concurrent builder for one
// cell key succeeds, agrees on one BinaryPath, and the published cache entry
// is valid.
func TestBuildNodePackedCellConcurrentColdBuildsDoNotCorrupt(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "concurrent.mjs")
	src := "export default function (pi) { pi.registerTool({name:'concurrent', label:'concurrent', description:'x', parameters:{type:'object',properties:{}}, async execute(){return {content:[{type:'text',text:'ok'}]};}}); }\n"
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exts := []nodeExtension{{Name: "concurrent", Entry: entry, Hash: entry}}
	cacheRoot := t.TempDir()

	const builders = 32
	var (
		start sync.WaitGroup
		done  sync.WaitGroup
		mu    sync.Mutex
	)
	start.Add(1)
	done.Add(builders)
	results := make([]*nodePackedCell, builders)
	errs := make([]error, builders)
	for i := range builders {
		go func(i int) {
			defer done.Done()
			start.Wait()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cell, err := buildNodePackedCell(ctx, cacheRoot, "concurrent-cell", exts)
			mu.Lock()
			results[i] = cell
			errs[i] = err
			mu.Unlock()
		}(i)
	}
	start.Done() // release the barrier: every goroutine starts as close together as possible
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("builder %d: %v", i, err)
		}
	}
	want := results[0].BinaryPath
	for i, cell := range results {
		if cell.BinaryPath != want {
			t.Fatalf("builder %d BinaryPath = %q, want %q (every concurrent builder must agree on one published entry)", i, cell.BinaryPath, want)
		}
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("published launcher missing: %v", err)
	}
	manifestPath := filepath.Join(filepath.Dir(want), "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("published manifest missing: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("published manifest is empty")
	}
	runtimeDir := filepath.Join(filepath.Dir(want), "runtime")
	if info, err := os.Stat(filepath.Join(runtimeDir, "cell.mjs")); err != nil || info.IsDir() {
		t.Fatalf("published runtime tree missing cell.mjs: %v", err)
	}

	// A cache hit after the concurrent cold builds (as a later reload would
	// see) must also succeed and reuse the same entry.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reused, err := buildNodePackedCell(ctx, cacheRoot, "concurrent-cell", exts)
	if err != nil {
		t.Fatalf("reuse after concurrent builds: %v", err)
	}
	if reused.BinaryPath != want || !reused.Cached {
		t.Fatalf("reuse = %+v, want Cached=true BinaryPath=%q", reused, want)
	}
}
