package plugins

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

// Not upstream's: the directory an extracted runtime is named by. Builds of different trees on one machine share the cache directory
// (`pig` under the user cache), and the proxy and the bridge are files of the runtime beside the driver: a name that covered only the
// manifest and host.mjs sent a build whose proxy.mjs differs to the same directory, where each repaired the other's files in place
// while a process of the other build was reading them (a keyed member refused by an old proxy.mjs in a run of the new one).
func TestTheExtractedRuntimeIsNamedByEveryFileOfTheRuntimeBesideTheManifest(t *testing.T) {
	runtime := func(change func(files fstest.MapFS)) fstest.MapFS {
		files := fstest.MapFS{}
		entries, err := fs.ReadDir(nodeFiles, "node")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			content, err := fs.ReadFile(nodeFiles, "node/"+entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			files["node/"+entry.Name()] = &fstest.MapFile{Data: content}
		}
		if change != nil {
			change(files)
		}
		return files
	}
	hashOf := func(change func(files fstest.MapFS)) string {
		t.Helper()
		hash, err := nodeTreeHash(runtime(change))
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}
	base := hashOf(nil)
	if again := hashOf(nil); again != base {
		t.Fatalf("the same runtime hashes %s, then %s", base, again)
	}
	for _, name := range []string{"node/proxy.mjs", "node/bridge.mjs", "node/host.mjs", "node/manifest.json"} {
		changed := hashOf(func(files fstest.MapFS) { files[name].Data = append(append([]byte{}, files[name].Data...), ' ') })
		if changed == base {
			t.Errorf("a changed %s leaves the runtime's name as it was", name)
		}
	}
	// A file of the same size with other bytes, or a renamed one, is another runtime too.
	swapped := hashOf(func(files fstest.MapFS) {
		data := append([]byte{}, files["node/proxy.mjs"].Data...)
		data[0] ^= 1
		files["node/proxy.mjs"].Data = data
	})
	if swapped == base {
		t.Error("a proxy.mjs of the same size with other bytes leaves the runtime's name as it was")
	}
}
