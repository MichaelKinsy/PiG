//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepeers"
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
	// The host peer packages under harness/node_modules are generated from the sources, after them, as noderuntimegen writes them.
	sources := map[string][]byte{}
	for name := range files {
		data, err := nodeRuntimeSource.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sources[strings.TrimPrefix(name, "runtime-node/")] = data
	}
	var harness struct {
		Version string `json:"version"`
	}
	manifest, err := nodeRuntimeSource.ReadFile("runtime-node/harness/package.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifest, &harness); err != nil {
		t.Fatal(err)
	}
	peers, err := noderuntimepeers.HostPeerFiles(harness.Version, sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range peers {
		got, err := nodeRuntimeFS.ReadFile(peer.Name)
		if err != nil {
			t.Errorf("archive lacks generated host peer file %s; run go generate ./coding/extension/host/subprocess", peer.Name)
		} else if !bytes.Equal(got, peer.Data) {
			t.Errorf("archive differs at generated host peer file %s; run go generate ./coding/extension/host/subprocess", peer.Name)
		}
		files[peer.Name] = true
		digest.Write([]byte(peer.Name))
		digest.Write(peer.Data)
	}
	if !bytes.Equal(digest.Sum(nil), nodeRuntimeDigest()) {
		t.Error("runtime digest is stale; run go generate ./coding/extension/host/subprocess")
	}
	var mu sync.Mutex
	if err := noderuntimepack.Extract(nodeRuntimeArchive, func(name string, _ []byte) error {
		mu.Lock()
		defer mu.Unlock()
		if !files[name] {
			t.Errorf("archive contains removed source %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNodeRuntimeDigestDoesNotOpenArchive(t *testing.T) {
	saved := nodeRuntimeArchive
	t.Cleanup(func() { nodeRuntimeArchive = saved })
	nodeRuntimeArchive = "\xff"
	if len(nodeRuntimeDigest()) != sha256.Size {
		t.Fatal("generated digest is not SHA-256")
	}
}
