package cellpack

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// nodeFixture is a Node cell extracted once into dest. The tests below change
// what that extraction left and start again.
type nodeFixture struct {
	fsys     fstest.MapFS
	m        Manifest
	warnings bytes.Buffer // what starts wrote as warnings
	data     []byte
	digest   string
	dest     string
	entryDir string // <dest>/node/<digest>
	root     string // <entryDir>/sources
}

// nodeFixtureEntries is a member the way the native builder archives one:
// folders before their files, an executable, and node_modules with the links a
// package manager leaves (links only when the file system takes them).
func nodeFixtureEntries(links bool) []archiveEntry {
	entries := []archiveEntry{
		{name: "greeter/", dir: true},
		{name: "greeter/index.ts", body: "export default function () {}\n"},
		{name: "greeter/lib/", dir: true},
		{name: "greeter/lib/echo.ts", body: "export const echo = (text: string) => text;\n"},
		{name: "greeter/bin/", dir: true},
		{name: "greeter/bin/run", body: "#!/bin/sh\n", mode: 0o755},
		{name: "greeter/node_modules/", dir: true},
		{name: "greeter/node_modules/dep/", dir: true},
		{name: "greeter/node_modules/dep/index.js", body: "module.exports = 1;\n"},
	}
	if links {
		entries = append(entries,
			archiveEntry{name: "greeter/node_modules/.bin/", dir: true},
			archiveEntry{name: "greeter/node_modules/.bin/dep", link: "../dep/index.js"},
			archiveEntry{name: "greeter/node_modules/greeter", link: ".."},
		)
	}
	return entries
}

func newNodeFixture(t *testing.T, links bool) *nodeFixture {
	t.Helper()
	data, digest := nodeArchive(t, nodeFixtureEntries(links)...)
	f := &nodeFixture{
		fsys:   fstest.MapFS{"cells/node/abc.tar": {Data: data}},
		m:      nodeManifest(digest),
		data:   data,
		digest: digest,
		dest:   t.TempDir(),
	}
	f.entryDir = filepath.Join(f.dest, "node", digest)
	f.root = filepath.Join(f.entryDir, "sources")
	if err := f.start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	f.requireArchive(t)
	return f
}

// start is one Binary start as far as the Node cell goes.
func (f *nodeFixture) start() error { return extract(f.fsys, f.m, f.dest, &f.warnings) }

func (f *nodeFixture) path(rel string) string { return filepath.Join(f.root, filepath.FromSlash(rel)) }

// treeEntry is what the archive says one path of the extracted tree is.
type treeEntry struct {
	kind byte // 'd' folder, 'f' file, 'l' link
	body string
	link string
	exec bool
}

// archiveTree reads the archive on its own, so the oracle does not share code with the extraction or its check.
func archiveTree(t *testing.T, data []byte) map[string]treeEntry {
	t.Helper()
	want := map[string]treeEntry{}
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return want
		}
		if err != nil {
			t.Fatal(err)
		}
		name := path.Clean(header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			want[name] = treeEntry{kind: 'd'}
		case tar.TypeReg:
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			want[name] = treeEntry{kind: 'f', body: string(body), exec: header.Mode&0o111 != 0}
		case tar.TypeSymlink:
			want[name] = treeEntry{kind: 'l', link: header.Linkname}
		}
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			if _, ok := want[dir]; !ok {
				want[dir] = treeEntry{kind: 'd'}
			}
		}
	}
}

// requireArchive fails unless the extracted sources are exactly what the archive unpacks to: every entry present with its bytes, link target and executable bit, no entry more, no folder or file a link.
func (f *nodeFixture) requireArchive(t *testing.T) {
	t.Helper()
	want := archiveTree(t, f.data)
	if info, err := os.Lstat(f.entryDir); err != nil || !info.IsDir() {
		t.Fatalf("%s is not a folder: %v, %v", f.entryDir, info, err)
	}
	if info, err := os.Lstat(f.root); err != nil || !info.IsDir() {
		t.Fatalf("%s is not a folder: %v, %v", f.root, info, err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&^0o755 != 0 {
		t.Errorf("sources: folder mode %v is wider than 0755", info.Mode())
	}
	got := map[string]bool{}
	err := filepath.WalkDir(f.root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(f.root, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		got[rel] = true
		entryWant, ok := want[rel]
		if !ok {
			t.Errorf("%s: not in the archive", rel)
			return nil
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		switch entryWant.kind {
		case 'd':
			if !info.IsDir() {
				t.Errorf("%s: not a folder (%v)", rel, info.Mode())
			}
			if runtime.GOOS != "windows" && info.Mode().Perm()&^0o755 != 0 {
				t.Errorf("%s: folder mode %v is wider than 0755", rel, info.Mode())
			}
		case 'f':
			if !info.Mode().IsRegular() {
				t.Errorf("%s: not a regular file (%v)", rel, info.Mode())
				return nil
			}
			body, err := os.ReadFile(current)
			if err != nil || string(body) != entryWant.body {
				t.Errorf("%s: bytes %q, %v; want %q", rel, body, err, entryWant.body)
			}
			if runtime.GOOS != "windows" {
				if (info.Mode()&0o111 != 0) != entryWant.exec {
					t.Errorf("%s: mode %v, executable should be %v", rel, info.Mode(), entryWant.exec)
				}
				if info.Mode()&(fs.ModePerm|fs.ModeSetuid|fs.ModeSetgid)&^0o755 != 0 || (!entryWant.exec && info.Mode().Perm()&^0o644 != 0) {
					t.Errorf("%s: mode %v is wider than extraction leaves", rel, info.Mode())
				}
			}
		case 'l':
			target, err := os.Readlink(current)
			if err != nil || filepath.ToSlash(target) != entryWant.link {
				t.Errorf("%s: link %q, %v; want %q", rel, target, err, entryWant.link)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel := range want {
		if !got[rel] {
			t.Errorf("%s: in the archive, not extracted", rel)
		}
	}
}

// leftovers lists the build scratch and replaced entries a start left beside the digest folder.
func (f *nodeFixture) leftovers(t *testing.T) []string {
	t.Helper()
	listing, err := os.ReadDir(filepath.Dir(f.entryDir))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range listing {
		if strings.HasPrefix(entry.Name(), ".build-") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func writeAll(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Every way of leaving the extracted directory different from its archive: the next start must put the archive's tree back. Before, a published entry was reused by its ready.json alone, and the Node runtime loaded whatever the directory held.
func TestNodeCellStartExtractsTheArchiveAgainWhenTheTreeDiffers(t *testing.T) {
	for name, tamper := range map[string]func(t *testing.T, f *nodeFixture){
		"a file changed, same size and time": func(t *testing.T, f *nodeFixture) {
			name := f.path("greeter/index.ts")
			info, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			body := []byte("export default function () { EVIL }")
			body = body[:info.Size()]
			if err := os.WriteFile(name, body, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(name, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
		},
		"a file made longer": func(t *testing.T, f *nodeFixture) {
			writeAll(t, f.path("greeter/lib/echo.ts"), "export const echo = () => 'evil';\n")
		},
		"a file deleted": func(t *testing.T, f *nodeFixture) {
			if err := os.Remove(f.path("greeter/lib/echo.ts")); err != nil {
				t.Fatal(err)
			}
		},
		"a folder deleted": func(t *testing.T, f *nodeFixture) {
			if err := os.RemoveAll(f.path("greeter/lib")); err != nil {
				t.Fatal(err)
			}
		},
		"a module added to the member": func(t *testing.T, f *nodeFixture) {
			writeAll(t, f.path("greeter/helper.ts"), "export const x = 1;\n")
		},
		"a node_modules package shadowing a dependency": func(t *testing.T, f *nodeFixture) {
			writeAll(t, f.path("greeter/node_modules/shadow/index.js"), "module.exports = 'evil';\n")
		},
		"a file added next to the members": func(t *testing.T, f *nodeFixture) {
			writeAll(t, f.path("evil.ts"), "export default function () {}\n")
		},
		"an empty folder added": func(t *testing.T, f *nodeFixture) {
			if err := os.MkdirAll(f.path("greeter/extra"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"ready.json forged with a changed file": func(t *testing.T, f *nodeFixture) {
			writeAll(t, f.path("greeter/index.ts"), "export default function () { EVIL }\n")
			ready := filepath.Join(f.entryDir, "ready.json")
			body, err := os.ReadFile(ready)
			if err != nil {
				t.Fatal(err)
			}
			forged := strings.Replace(string(body), `"created":`, `"created":1`, 1)
			if forged == string(body) {
				t.Fatalf("ready.json has no created field: %s", body)
			}
			if err := os.WriteFile(ready, []byte(forged), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a node_modules folder beside the sources": func(t *testing.T, f *nodeFixture) {
			writeAll(t, filepath.Join(f.entryDir, "node_modules", "dep", "index.js"), "module.exports = 'evil';\n")
		},
		"a package.json beside the sources": func(t *testing.T, f *nodeFixture) {
			writeAll(t, filepath.Join(f.entryDir, "package.json"), `{"type":"commonjs"}`)
		},
		"a tsconfig.json beside the sources": func(t *testing.T, f *nodeFixture) {
			writeAll(t, filepath.Join(f.entryDir, "tsconfig.json"), `{"compilerOptions":{"paths":{"*":["./evil/*"]}}}`)
		},
		"a jsconfig.json beside the sources": func(t *testing.T, f *nodeFixture) {
			writeAll(t, filepath.Join(f.entryDir, "jsconfig.json"), `{}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newNodeFixture(t, false)
			tamper(t, f)
			if err := f.start(); err != nil {
				t.Fatalf("start after the change: %v", err)
			}
			f.requireArchive(t)
			for _, forbidden := range []string{"node_modules", "package.json", "tsconfig.json", "jsconfig.json"} {
				if _, err := os.Lstat(filepath.Join(f.entryDir, forbidden)); err == nil {
					t.Errorf("%s is still in %s", forbidden, f.entryDir)
				}
			}
			if left := f.leftovers(t); len(left) != 0 {
				t.Errorf("the start left %v beside the entry", left)
			}
			// The repair is complete: another start finds nothing to do.
			if err := f.start(); err != nil {
				t.Fatalf("start after the repair: %v", err)
			}
			f.requireArchive(t)
		})
	}
}

// The folders above a Node cell's entry belong to PiG, and Node resolves modules through every folder above a module, so a module-lookup name there is never legitimate and cannot be repaired by extracting again: the start fails and names the path.
func TestNodeCellStartRefusesModuleLookupNamesAboveTheEntry(t *testing.T) {
	for _, tc := range []struct{ folder, name string }{
		{"node", "node_modules"}, {"node", "package.json"}, {"node", "tsconfig.json"}, {"node", "jsconfig.json"},
		{".", "node_modules"}, {".", "package.json"}, {".", "tsconfig.json"}, {".", "jsconfig.json"},
	} {
		t.Run(tc.folder+"/"+tc.name, func(t *testing.T) {
			f := newNodeFixture(t, false)
			forbidden := filepath.Join(f.dest, tc.folder, tc.name)
			writeAll(t, forbidden, "{}")
			err := f.start()
			if err == nil || !strings.Contains(err.Error(), forbidden) || !strings.Contains(err.Error(), "remove it") {
				t.Fatalf("start = %v; want an error naming %s and saying to remove it", err, forbidden)
			}
			// A first start is refused as well, before anything is extracted.
			fresh := t.TempDir()
			writeAll(t, filepath.Join(fresh, tc.folder, tc.name), "{}")
			if err := extract(f.fsys, f.m, fresh, io.Discard); err == nil || !strings.Contains(err.Error(), filepath.Join(fresh, tc.folder, tc.name)) {
				t.Fatalf("first start = %v; want an error naming the path", err)
			}
			if _, err := os.Lstat(filepath.Join(fresh, "node", f.digest)); err == nil {
				t.Error("the refused start extracted the cell")
			}
		})
	}
}

// A clean tree is not written: nothing is replaced, nothing is left beside it.
func TestNodeCellStartLeavesACleanTreeAlone(t *testing.T) {
	f := newNodeFixture(t, false)
	before := snapshotTree(t, f.entryDir)
	for range 3 {
		if err := f.start(); err != nil {
			t.Fatal(err)
		}
	}
	if after := snapshotTree(t, f.entryDir); !slices.Equal(before, after) {
		t.Fatalf("a start changed a clean tree:\nbefore %v\nafter  %v", before, after)
	}
	if left := f.leftovers(t); len(left) != 0 {
		t.Fatalf("leftovers %v", left)
	}
}

// snapshotTree lists every path under dir with what a rewrite would change. usage.json is the use record a start refreshes; it is not content.
func snapshotTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "usage.json" {
			return nil
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, current)
		out = append(out, strings.Join([]string{rel, info.Mode().String(), info.ModTime().String(), sizeAndIdentity(info)}, " "))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A repair is announced once, on the warning writer, by the start that made it: one line in the form of the neighbouring startup warnings, naming the entry folder and the first difference (what, and the path in the entry). A clean start, and the start after a repair, say nothing.
func TestNodeCellStartWarnsOnceNamingTheFirstDifference(t *testing.T) {
	for name, test := range map[string]struct {
		tamper func(t *testing.T, f *nodeFixture)
		want   string
	}{
		"changed": {func(t *testing.T, f *nodeFixture) {
			writeAll(t, f.path("greeter/lib/echo.ts"), "export const echo = () => 'evil';\n")
		}, "changed: sources/greeter/lib/echo.ts"},
		"missing": {func(t *testing.T, f *nodeFixture) {
			if err := os.Remove(f.path("greeter/bin/run")); err != nil {
				t.Fatal(err)
			}
		}, "missing: sources/greeter/bin/run"},
		"added": {func(t *testing.T, f *nodeFixture) {
			writeAll(t, f.path("greeter/node_modules/shadow/index.js"), "evil")
		}, "added: sources/greeter/node_modules/shadow"},
		"added beside the sources": {func(t *testing.T, f *nodeFixture) {
			writeAll(t, filepath.Join(f.entryDir, "package.json"), "{}")
		}, "added: package.json"},
		"the first of several, in archive order": {func(t *testing.T, f *nodeFixture) {
			writeAll(t, f.path("greeter/extra.ts"), "x")
			writeAll(t, f.path("greeter/lib/echo.ts"), "export const echo = () => 'evil';\n")
			writeAll(t, f.path("greeter/index.ts"), "evil")
		}, "changed: sources/greeter/index.ts"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newNodeFixture(t, false)
			if f.warnings.Len() != 0 {
				t.Fatalf("the first start warned: %q", f.warnings.String())
			}
			if err := f.start(); err != nil || f.warnings.Len() != 0 {
				t.Fatalf("a clean start = %v, warned %q", err, f.warnings.String())
			}
			test.tamper(t, f)
			if err := f.start(); err != nil {
				t.Fatal(err)
			}
			want := "pig: warning: the extracted Node sources in " + f.entryDir + " differ from the Binary's archive (" + test.want + "); extracted them again\n"
			if f.warnings.String() != want {
				t.Fatalf("warning = %q\nwant      %q", f.warnings.String(), want)
			}
			f.warnings.Reset()
			if err := f.start(); err != nil || f.warnings.Len() != 0 {
				t.Fatalf("the start after the repair = %v, warned %q", err, f.warnings.String())
			}
		})
	}
}

// Register is the startup entry: it extracts, repairs and warns the same way, and the warning goes to the writer registerFrom is given (Register gives stderr), never anywhere a mode prints its result.
func TestRegisterRepairsAndWarnsOnTheGivenWriter(t *testing.T) {
	data, digest := nodeArchive(t, nodeFixtureEntries(false)...)
	manifest, err := json.Marshal(nodeManifest(digest))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{"cells/manifest.json": {Data: manifest}, "cells/node/abc.tar": {Data: data}}
	t.Cleanup(func() {
		runtimecell.SetPrebuiltResolver(nil)
		loadedCells = nil
	})
	t.Setenv("PIG_HOME", filepath.Join(t.TempDir(), "home"))
	var warnings bytes.Buffer
	if err := registerFrom(fsys, &warnings); err != nil || warnings.Len() != 0 {
		t.Fatalf("first registerFrom = %v, warned %q", err, warnings.String())
	}
	loaded := LoadedCells()
	if len(loaded) != 1 {
		t.Fatalf("loaded cells = %#v", loaded)
	}
	writeAll(t, filepath.Join(loaded[0].BinaryPath, "greeter", "index.ts"), "evil")
	if err := registerFrom(fsys, &warnings); err != nil {
		t.Fatal(err)
	}
	want := "(changed: sources/greeter/index.ts); extracted them again\n"
	if !strings.HasSuffix(warnings.String(), want) || strings.Count(warnings.String(), "\n") != 1 {
		t.Fatalf("warning = %q, want one line ending %q", warnings.String(), want)
	}
	if body, err := os.ReadFile(filepath.Join(LoadedCells()[0].BinaryPath, "greeter", "index.ts")); err != nil || string(body) != "export default function () {}\n" {
		t.Fatalf("index.ts = %q, %v after the repair", body, err)
	}
}

// The PiG configuration root, which holds installed packages and settings, and everything above it are out of scope: Node searches them for every extension anywhere, so a module-lookup name there does not stop a Binary. Only the two folders PiG creates for the cells (piglet-binary-cells and piglet-binary-cells/node) are checked.
func TestRegisterIgnoresModuleLookupNamesInTheConfigRoot(t *testing.T) {
	data, digest := nodeArchive(t, nodeFixtureEntries(false)...)
	manifest, err := json.Marshal(nodeManifest(digest))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{"cells/manifest.json": {Data: manifest}, "cells/node/abc.tar": {Data: data}}
	t.Cleanup(func() {
		runtimecell.SetPrebuiltResolver(nil)
		loadedCells = nil
	})
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("PIG_HOME", home)
	for _, name := range moduleLookupNames {
		writeAll(t, filepath.Join(home, name, "keep"), "{}")
		writeAll(t, filepath.Join(filepath.Dir(home), name, "keep"), "{}")
	}
	var warnings bytes.Buffer
	for range 2 {
		if err := registerFrom(fsys, &warnings); err != nil {
			t.Fatalf("registerFrom with module-lookup names in the config root: %v", err)
		}
	}
	if warnings.Len() != 0 {
		t.Fatalf("warned %q", warnings.String())
	}
	loaded := LoadedCells()
	if len(loaded) != 1 {
		t.Fatalf("loaded cells = %#v", loaded)
	}
	if _, err := os.Lstat(filepath.Join(loaded[0].BinaryPath, "greeter", "index.ts")); err != nil {
		t.Fatal(err)
	}
}
