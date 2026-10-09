package pigletbuild

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// readArchive returns each archive entry's name mapped to its body, or to
// "-> target" for a link and "/" for a directory.
func readArchive(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	entries := map[string]string{}
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			entries[header.Name] = "/"
		case tar.TypeSymlink:
			entries[header.Name] = "-> " + header.Linkname
		default:
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			entries[header.Name] = string(body)
		}
	}
}

func writeFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A Node cell embeds its members' sources in one archive: a directory member
// whole (without .git), a single-file member as that file. The cell manifest
// names the archive, its digest, and each member's source path, and the signed
// manifest's embedded files list the archive with the same digest.
func TestBuildNodeCellEmbedsMemberSources(t *testing.T) {
	root := t.TempDir()
	dirMember := filepath.Join(root, "narrow")
	fileMember := filepath.Join(root, "single.ts")
	writeFiles(t, map[string]string{
		filepath.Join(dirMember, "index.ts"):                        "import { x } from \"./lib/x.ts\";\n",
		filepath.Join(dirMember, "lib", "x.ts"):                     "export const x = 1;\n",
		filepath.Join(dirMember, "node_modules", "dep", "index.js"): "module.exports = 1;\n",
		filepath.Join(dirMember, ".git", "HEAD"):                    "ref: refs/heads/main\n",
		fileMember:                                                  "export default function () {}\n",
	})
	if runtime.GOOS != "windows" {
		if err := os.MkdirAll(filepath.Join(dirMember, "node_modules", ".bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		testenv.Symlink(t, "../dep/index.js", filepath.Join(dirMember, "node_modules", ".bin", "dep"))
		// A package's link to itself names the member directory, so it stays.
		testenv.Symlink(t, "..", filepath.Join(dirMember, "node_modules", "narrow"))
	}
	cell := subprocess.CellSpec{
		Key: "packed-node:abc", Strategy: subprocess.CellStrategyPackedNode, Language: "node",
		Extensions: []subprocess.ExtConfig{{Name: "narrow", Source: dirMember, ContentHash: "hn"}, {Name: "single", Source: fileMember}},
	}
	host := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	staged, err := buildNodeCell(cell, t.TempDir(), host)
	if err != nil {
		t.Fatal(err)
	}
	entry := staged.entry
	if entry.Language != "node" || entry.Strategy != "packed-node" || entry.Key != cell.Key || entry.OS != host.OS || entry.Arch != host.Arch {
		t.Fatalf("entry = %#v", entry)
	}
	if entry.Binary != "node/"+shortHash(entry.Digest)+".tar" || len(entry.Digest) != 64 {
		t.Fatalf("entry binary %q digest %q", entry.Binary, entry.Digest)
	}
	if len(entry.Extensions) != 2 || entry.Extensions[0].Name != "narrow" || entry.Extensions[0].Source != "narrow" || entry.Extensions[0].Hash != "hn" ||
		entry.Extensions[1].Name != "single" || entry.Extensions[1].Source != "single/single.ts" {
		t.Fatalf("entry extensions = %#v", entry.Extensions)
	}
	archive := readArchive(t, staged.binaryPath)
	want := map[string]string{
		"narrow/":                          "/",
		"narrow/index.ts":                  "import { x } from \"./lib/x.ts\";\n",
		"narrow/lib/":                      "/",
		"narrow/lib/x.ts":                  "export const x = 1;\n",
		"narrow/node_modules/":             "/",
		"narrow/node_modules/dep/":         "/",
		"narrow/node_modules/dep/index.js": "module.exports = 1;\n",
		"single/":                          "/",
		"single/single.ts":                 "export default function () {}\n",
	}
	if runtime.GOOS != "windows" {
		want["narrow/node_modules/.bin/"] = "/"
		want["narrow/node_modules/.bin/dep"] = "-> ../dep/index.js"
		want["narrow/node_modules/narrow"] = "-> .."
	}
	for name, body := range want {
		if archive[name] != body {
			t.Errorf("archive %s = %q, want %q", name, archive[name], body)
		}
	}
	for name := range archive {
		if _, ok := want[name]; !ok {
			t.Errorf("archive has unexpected entry %s", name)
		}
	}
	files, err := embeddedFiles([]stagedCell{staged})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "cells/"+entry.Binary || files[0].Digest != "sha256:"+entry.Digest {
		t.Fatalf("signed embedded files = %#v, want the archive with digest %s", files, entry.Digest)
	}
	again, err := buildNodeCell(cell, t.TempDir(), host)
	if err != nil || again.entry.Digest != entry.Digest {
		t.Fatalf("rebuilt archive digest = %q, %v; want the reproducible %q", again.entry.Digest, err, entry.Digest)
	}
}

// A symbolic link that leaves the extension directory cannot be embedded: the
// Binary would run without the code it points at.
func TestBuildNodeCellRefusesALinkOutsideTheExtension(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs a privilege Windows test runners do not grant")
	}
	root := t.TempDir()
	member := filepath.Join(root, "narrow")
	writeFiles(t, map[string]string{filepath.Join(member, "index.ts"): "", filepath.Join(root, "shared", "x.ts"): ""})
	testenv.Symlink(t, "../shared", filepath.Join(member, "shared"))
	cell := subprocess.CellSpec{Key: "packed-node:abc", Strategy: subprocess.CellStrategyPackedNode, Language: "node", Extensions: []subprocess.ExtConfig{{Name: "narrow", Source: member}}}
	_, err := buildNodeCell(cell, t.TempDir(), Target{OS: runtime.GOOS, Arch: runtime.GOARCH})
	if err == nil || !strings.Contains(err.Error(), "points outside the extension directory") || !strings.Contains(err.Error(), `"narrow"`) {
		t.Fatalf("build error = %v", err)
	}
}
