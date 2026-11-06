//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepeers"
)

// The runtime source tree is data only: runtime-node/go.mod keeps it out of the root module zip, so no go:embed may name it. The test reads it from the checkout, and the go test cache hashes every file and directory the test opens, so new, removed and changed files still invalidate the archive and its generated digest.
var nodeRuntimeSource = os.DirFS(".")

// nodeRuntimeSourceSelected reports whether the archive packs path, as the go:embed patterns runtime-node/*.mjs, runtime-node/harness and all:runtime-node/shims did: the .mjs files of the root, the harness without names that begin with "." or "_", and all of shims.
func nodeRuntimeSourceSelected(path string, entry fs.DirEntry) (bool, error) {
	relative := strings.TrimPrefix(path, "runtime-node")
	relative = strings.TrimPrefix(relative, "/")
	pack := func(keep bool) (bool, error) {
		if !keep && entry.IsDir() {
			return false, fs.SkipDir
		}
		return keep && !entry.IsDir(), nil
	}
	switch top, _, nested := strings.Cut(relative, "/"); {
	case relative == "":
		return false, nil
	case top == "shims":
		return pack(true)
	case top == "harness":
		return pack(!strings.HasPrefix(entry.Name(), ".") && !strings.HasPrefix(entry.Name(), "_"))
	case !nested && entry.IsDir():
		return false, fs.SkipDir
	default:
		return !nested && strings.HasSuffix(entry.Name(), ".mjs"), nil
	}
}

func TestNodeRuntimeArchiveMatchesSources(t *testing.T) {
	digest := sha256.New()
	files := map[string]bool{}
	err := fs.WalkDir(nodeRuntimeSource, "runtime-node", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		selected, err := nodeRuntimeSourceSelected(path, entry)
		if err != nil || !selected {
			return err
		}
		if noderuntimepack.UnreadAtRuntime(strings.TrimPrefix(path, "runtime-node/")) {
			return nil
		}
		want, err := fs.ReadFile(nodeRuntimeSource, path)
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
		data, err := fs.ReadFile(nodeRuntimeSource, name)
		if err != nil {
			t.Fatal(err)
		}
		sources[strings.TrimPrefix(name, "runtime-node/")] = data
	}
	var harness struct {
		Version string `json:"version"`
	}
	manifest, err := fs.ReadFile(nodeRuntimeSource, "runtime-node/harness/package.json")
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
