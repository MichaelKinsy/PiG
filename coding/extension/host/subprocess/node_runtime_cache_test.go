//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepack"
	"github.com/MichaelKinsy/PiG/internal/fspublish"
)

func TestNodeRuntimeMaterializesOncePerContentHash(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(t.TempDir(), "first")
	if err := materializeNodeRuntime(t.Context(), root, first); err != nil {
		t.Fatal(err)
	}
	savedArchive, savedDigest := nodeRuntimeArchive, nodeRuntimeDigest
	t.Cleanup(func() { nodeRuntimeArchive = savedArchive; nodeRuntimeDigest = savedDigest })
	// A corrupt archive makes any decode fail: a cache hit must not decode.
	const corrupt = "\xff"
	nodeRuntimeArchive = corrupt
	second := filepath.Join(t.TempDir(), "second")
	if err := materializeNodeRuntime(t.Context(), root, second); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(first, "runtime.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(second, "runtime.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("pruning shared materialization damaged the launcher's owned tree")
	}
	nodeRuntimeArchive = savedArchive
	if err := materializeNodeRuntime(t.Context(), root, filepath.Join(t.TempDir(), "recreated")); err != nil {
		t.Fatal(err)
	}
	nodeRuntimeArchive = corrupt
	nodeRuntimeDigest = func() []byte { return []byte("changed runtime content") }
	if err := materializeNodeRuntime(t.Context(), root, filepath.Join(t.TempDir(), "changed")); !errors.Is(err, noderuntimepack.ErrTruncated) {
		t.Fatalf("new runtime reused stale contents: %v", err)
	}
}

func TestNodeRuntimeCopyFallbackDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(source, []byte("runtime bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fspublish.LinkOrCopy(source, target); err != nil {
		t.Fatal(err)
	}
	if err := fspublish.LinkOrCopy(source, target); !errors.Is(err, os.ErrExist) {
		t.Fatalf("fallback overwrote existing destination: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "runtime bytes" {
		t.Fatalf("copied bytes = %q", got)
	}
}

// The launcher tree is linked on several workers, but a failure is reported as the sequential walk reported it: the first conflicting
// file in walk order, whichever worker reached a later conflict first.
func TestNodeRuntimeLinkReportsTheWalkOrderFirstFailure(t *testing.T) {
	cache := t.TempDir()
	warm := filepath.Join(t.TempDir(), "warm")
	if err := os.MkdirAll(warm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := materializeNodeRuntime(t.Context(), cache, warm); err != nil {
		t.Fatal(err)
	}
	var files []string
	if err := filepath.WalkDir(warm, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("runtime has %d files, too few to exercise the workers", len(files))
	}
	for range 5 {
		destination := filepath.Join(t.TempDir(), "launcher")
		// Adjacent conflicts are in flight together, so a worker can fail on the later one first.
		first, later := files[len(files)/3], files[len(files)/3+1]
		for _, conflict := range []string{later, first} {
			relative, _ := filepath.Rel(warm, conflict)
			target := filepath.Join(destination, relative)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("occupied"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		err := materializeNodeRuntime(t.Context(), cache, destination)
		relative, _ := filepath.Rel(warm, first)
		if err == nil || !strings.Contains(err.Error(), filepath.Join(destination, relative)) {
			t.Fatalf("error = %v, want the walk-order first conflict %s", err, relative)
		}
	}
}

// Every index below the lowest failure runs and its error is the one reported, however fast later indices fail: a worker that claimed a lower index before a later one failed still runs it, as a sequential loop would. The skip-after-any-failure rule fails this test in 2 of 3 -race runs.
func TestLinkInWalkOrderRunsEveryIndexBelowTheFirstFailure(t *testing.T) {
	const n, failing = 64, 3
	for range 20000 {
		var ran [n]atomic.Bool
		err := linkInWalkOrder(t.Context(), n, func(i int) error {
			ran[i].Store(true)
			if i >= failing {
				return fmt.Errorf("link %d", i)
			}
			return nil
		})
		if err == nil || err.Error() != fmt.Sprintf("link %d", failing) {
			t.Fatalf("error = %v, want link %d", err, failing)
		}
		for i := range failing {
			if !ran[i].Load() {
				t.Fatalf("index %d below the first failure did not run", i)
			}
		}
	}
}

// A cancelled context stops the links: each remaining index reports the context error and nothing is linked.
func TestLinkInWalkOrderStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var linked atomic.Int64
	err := linkInWalkOrder(ctx, 32, func(int) error { linked.Add(1); return nil })
	if !errors.Is(err, context.Canceled) || linked.Load() != 0 {
		t.Fatalf("error = %v, linked = %d; want context.Canceled and 0", err, linked.Load())
	}
}
