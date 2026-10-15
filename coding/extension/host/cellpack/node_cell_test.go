package cellpack

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

type archiveEntry struct {
	name, body, link string
	dir              bool
	mode             int64
}

func nodeArchive(t *testing.T, entries ...archiveEntry) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(entry.body))}
		switch {
		case entry.dir:
			header.Typeflag, header.Size, header.Mode = tar.TypeDir, 0, 0o755
		case entry.link != "":
			header.Typeflag, header.Size, header.Linkname = tar.TypeSymlink, 0, entry.link
		case entry.mode != 0:
			header.Mode = entry.mode
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buffer.Bytes())
	return buffer.Bytes(), hex.EncodeToString(sum[:])
}

func nodeManifest(digest string) Manifest {
	return Manifest{PigCoreVersion: "test", Cells: []CellEntry{{
		Language: "node", Key: "packed-node:k", Strategy: "packed-node", OS: runtime.GOOS, Arch: runtime.GOARCH,
		Binary: "node/abc.tar", Digest: digest, Extensions: []ExtEntry{{Name: "greeter", Hash: "h", Source: "greeter"}},
	}}}
}

// A Node cell's archive extracts once into a directory named by its digest,
// and the loaded cell points the host at that directory with each member's
// source path.
func TestExtractNodeCellPublishesMemberSources(t *testing.T) {
	data, digest := nodeArchive(t,
		archiveEntry{name: "greeter/", dir: true},
		archiveEntry{name: "greeter/index.ts", body: "export default function () {}\n"},
		archiveEntry{name: "greeter/bin/run", body: "#!/bin/sh\n", mode: 0o755},
		archiveEntry{name: "greeter/node_modules/dep/index.js", body: "module.exports = 1;\n"},
		archiveEntry{name: "greeter/node_modules/.bin/dep", link: "../dep/index.js"},
		archiveEntry{name: "greeter/node_modules/greeter", link: ".."},
	)
	m := nodeManifest(digest)
	fsys := fstest.MapFS{"cells/node/abc.tar": {Data: data}}
	dest := t.TempDir()
	for range 2 {
		if err := extract(fsys, m, dest); err != nil {
			t.Fatalf("extract: %v", err)
		}
	}
	loaded := loadedCellsFromManifest(m, dest)
	want := filepath.Join(dest, "node", digest, "sources")
	if len(loaded) != 1 || loaded[0].BinaryPath != want || loaded[0].Extensions[0].Source != "greeter" {
		t.Fatalf("loaded = %#v, want BinaryPath %s", loaded, want)
	}
	body, err := os.ReadFile(filepath.Join(want, "greeter", "index.ts"))
	if err != nil || string(body) != "export default function () {}\n" {
		t.Fatalf("extracted index.ts = %q, %v", body, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(want, "greeter", "bin", "run"))
		if err != nil || info.Mode()&0o100 == 0 {
			t.Fatalf("executable source lost its mode: %v %v", info, err)
		}
		linked, err := os.ReadFile(filepath.Join(want, "greeter", "node_modules", ".bin", "dep"))
		if err != nil || string(linked) != "module.exports = 1;\n" {
			t.Fatalf("relative link = %q, %v", linked, err)
		}
		self, err := os.ReadFile(filepath.Join(want, "greeter", "node_modules", "greeter", "index.ts"))
		if err != nil || string(self) != "export default function () {}\n" {
			t.Fatalf("link to the member itself = %q, %v", self, err)
		}
	}
	if resolver := NewResolver(m, dest); len(resolver.index) != 0 {
		t.Fatalf("resolver indexes a Node cell: %#v", resolver.index)
	}
}

// Startup refuses a Node archive whose bytes differ from the manifest digest
// and an archive that would write or link outside its own tree.
func TestExtractNodeCellRefusesMismatchedOrEscapingArchives(t *testing.T) {
	good, digest := nodeArchive(t, archiveEntry{name: "greeter/index.ts", body: "x"})
	tampered := bytes.Clone(good)
	tampered[len(tampered)-1025] ^= 1
	escape, escapeDigest := nodeArchive(t, archiveEntry{name: "../outside.ts", body: "x"})
	link, linkDigest := nodeArchive(t, archiveEntry{name: "greeter/leak", link: "../../outside"})
	for name, test := range map[string]struct {
		data   []byte
		digest string
		want   string
	}{
		"tampered": {tampered, digest, "does not match its manifest digest"},
		"escape":   {escape, escapeDigest, "is not inside the archive"},
		"link":     {link, linkDigest, "points outside its sources"},
	} {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{"cells/node/abc.tar": {Data: test.data}}
			err := extract(fsys, nodeManifest(test.digest), t.TempDir())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("extract error = %v, want %q", err, test.want)
			}
		})
	}
}

// A Binary publishes its Node cell under the PiG home, as every other embedded
// cell (3a50b7960): ConfigRoot()/piglet-binary-cells/node/<digest>, absolute
// for a relative PIG_HOME, and under $XDG_CONFIG_HOME/pig without PIG_HOME.
// Nothing is written anywhere else, in particular not under ~/.pig.
func TestRegisterPublishesTheNodeCellOnlyUnderThePigHome(t *testing.T) {
	data, digest := nodeArchive(t, archiveEntry{name: "greeter/index.ts", body: "export default function () {}\n"})
	manifest, err := json.Marshal(nodeManifest(digest))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{
		"cells/manifest.json": {Data: manifest},
		"cells/node/abc.tar":  {Data: data},
	}
	t.Cleanup(func() {
		runtimecell.SetPrebuiltResolver(nil)
		loadedCells = nil
	})
	for name, pigHome := range map[string]func(t *testing.T, sandbox string) string{
		"relative PIG_HOME": func(t *testing.T, sandbox string) string {
			work := filepath.Join(sandbox, "work")
			if err := os.Mkdir(work, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(work)
			t.Setenv("PIG_HOME", "relative-home")
			return filepath.Join(work, "relative-home")
		},
		"XDG_CONFIG_HOME": func(t *testing.T, sandbox string) string {
			t.Setenv("PIG_HOME", "")
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(sandbox, "xdg"))
			return filepath.Join(sandbox, "xdg", "pig")
		},
	} {
		t.Run(name, func(t *testing.T) {
			sandbox, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", filepath.Join(sandbox, "home"))
			t.Setenv("USERPROFILE", filepath.Join(sandbox, "home"))
			root := pigHome(t, sandbox)
			if err := registerFrom(fsys); err != nil {
				t.Fatalf("registerFrom: %v", err)
			}
			cellsDir := filepath.Join(root, "piglet-binary-cells", "node")
			want := filepath.Join(cellsDir, digest)
			if loaded := LoadedCells(); len(loaded) != 1 || loaded[0].BinaryPath != filepath.Join(want, "sources") {
				t.Fatalf("loaded cells = %#v, want BinaryPath %s", loaded, filepath.Join(want, "sources"))
			}
			if body, err := os.ReadFile(filepath.Join(want, "sources", "greeter", "index.ts")); err != nil || string(body) != "export default function () {}\n" {
				t.Fatalf("published index.ts = %q, %v", body, err)
			}
			// The publish lock beside the entry is the only file outside it.
			lock := filepath.Join(cellsDir, ".locks", digest+".lock")
			err = filepath.WalkDir(sandbox, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() || path == lock {
					return err
				}
				if !strings.HasPrefix(path, want+string(filepath.Separator)) {
					t.Errorf("Node cell wrote %s outside %s", path, want)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
