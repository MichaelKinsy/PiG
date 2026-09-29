package subprocess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// materializeNodeRuntime publishes one immutable tree per runtime content and version. Each launcher keeps a self-contained hard-linked tree, so cache pruning and Piglet artifact copies never depend on a shared path surviving. Filesystems without hard links receive ordinary copies.
func materializeNodeRuntime(ctx context.Context, cacheDir, destination string) (err error) {
	digest := sha256.Sum256(append([]byte(nodeRuntimeVersion), nodeRuntimeDigest()...))
	hash := hex.EncodeToString(digest[:])
	root := filepath.Join(cacheDir, "node-runtime-"+hash)
	// pig additive (D20): content-addressed runtime materialization shares the governed cell cache.
	entry, err := runtimecell.PublishArtifact(ctx, root, "content.sha256", hash, "node", func(scratch string) (string, error) {
		runtimeDir := filepath.Join(scratch, "runtime")
		if err := os.Mkdir(runtimeDir, 0o755); err != nil {
			return "", err
		}
		if err := copyEmbeddedTree(nodeRuntimeFS, "runtime-node", runtimeDir); err != nil {
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
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
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
		if err := os.Link(path, target); err == nil {
			return nil
		}
		return copyNodeRuntimeFile(path, target)
	})
}

func copyNodeRuntimeFile(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	_, err = io.Copy(output, input)
	return err
}
