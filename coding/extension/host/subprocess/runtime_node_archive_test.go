//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"io/fs"
	"testing"
)

// This source embed exists only in the test binary. New, removed and changed files must all invalidate the archive and its generated digest.
//
//go:embed runtime-node/*.mjs runtime-node/harness all:runtime-node/shims
var nodeRuntimeSource embed.FS

func TestNodeRuntimeArchiveMatchesSources(t *testing.T) {
	digest := sha256.New()
	files := map[string]bool{}
	err := fs.WalkDir(nodeRuntimeSource, "runtime-node", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		want, err := nodeRuntimeSource.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := nodeRuntimeFS.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			t.Errorf("archive differs at %s; run go generate ./coding/extension/host/subprocess", path)
		}
		files[path] = true
		digest.Write([]byte(path))
		digest.Write(want)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(digest.Sum(nil), nodeRuntimeDigest()) {
		t.Error("runtime digest is stale; run go generate ./coding/extension/host/subprocess")
	}
	archive, err := nodeRuntimeZip()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		if !files[file.Name] {
			t.Errorf("archive contains removed source %s", file.Name)
		}
	}
}

func TestNodeRuntimeDigestDoesNotOpenArchive(t *testing.T) {
	saved := nodeRuntimeZip
	t.Cleanup(func() { nodeRuntimeZip = saved })
	nodeRuntimeZip = nil
	if len(nodeRuntimeDigest()) != sha256.Size {
		t.Fatal("generated digest is not SHA-256")
	}
}
