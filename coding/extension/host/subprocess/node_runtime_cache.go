package subprocess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/fspublish"
	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

// materializeNodeRuntime publishes one immutable tree per runtime content and version. Each launcher keeps a self-contained hard-linked tree, so cache pruning and Piglet artifact copies never depend on a shared path surviving. Filesystems without hard links receive ordinary copies.
func materializeNodeRuntime(ctx context.Context, cacheDir, destination string) (err error) {
	// pig additive (D92): a Piglet that strips node-extensions has no runtime to publish.
	if err := nodeRuntimeUnavailable(); err != nil {
		return err
	}
	digest := sha256.Sum256(append([]byte(nodeRuntimeVersion), nodeRuntimeDigest()...))
	hash := hex.EncodeToString(digest[:])
	root := filepath.Join(cacheDir, "node-runtime-"+hash)
	// pig additive (D20): content-addressed runtime materialization shares the governed cell cache.
	entry, err := runtimecell.PublishArtifact(ctx, root, "content.sha256", hash, "node", func(scratch string) (string, error) {
		runtimeDir := filepath.Join(scratch, "runtime")
		if err := os.Mkdir(runtimeDir, 0o755); err != nil {
			return "", err
		}
		if err := extractNodeRuntime(runtimeDir); err != nil {
			return "", err
		}
		marker := filepath.Join(scratch, "content.sha256")
		if err := os.WriteFile(marker, []byte(hash), 0o644); err != nil {
			return "", err
		}
		return marker, nil
	}, "runtime")
	if err != nil {
		return err
	}
	// Protect the published source while linking; the launcher then owns its hard links independently.
	lease, err := runtimecell.AcquireArtifactUsageLease(entry.ArtifactPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lease.Release()) }()
	source := filepath.Join(entry.Dir, "runtime")
	// The walk creates every directory in order; the links are independent, so they run on GOMAXPROCS workers. The error reported is
	// the walk-order first one.
	type link struct{ source, target string }
	var links []link
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		links = append(links, link{path, target})
		return nil
	}); err != nil {
		return err
	}
	return linkInWalkOrder(ctx, len(links), func(i int) error { return fspublish.LinkOrCopy(links[i].source, links[i].target) })
}

// linkInWalkOrder runs link(0..n-1) on GOMAXPROCS workers and returns the error of the lowest failing index, the one a sequential loop returns. After a failure at index f a worker skips only indices above f, so every index below the lowest failure runs.
func linkInWalkOrder(ctx context.Context, n int, link func(int) error) error {
	errs := make([]error, n)
	var next atomic.Int64
	var firstFailed atomic.Int64
	firstFailed.Store(int64(n))
	if n > 1 {
		// pig additive (D174): a Node extension's launch waits on these links; they run at the runtime default width.
		defer procpolicy.Widen()()
	}
	var workers sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), n) {
		workers.Go(func() {
			for {
				i := next.Add(1) - 1
				if i >= int64(n) || i > firstFailed.Load() {
					return
				}
				if err := ctx.Err(); err != nil {
					errs[i] = err
				} else {
					errs[i] = link(int(i))
				}
				if errs[i] == nil {
					continue
				}
				for {
					failed := firstFailed.Load()
					if i >= failed || firstFailed.CompareAndSwap(failed, i) {
						break
					}
				}
			}
		})
	}
	workers.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
