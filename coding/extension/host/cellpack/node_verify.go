package cellpack

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"

	"github.com/MichaelKinsy/PiG/internal/archivepath"
)

// moduleLookupNames are the names that change what a module loads when they sit in a folder above it, which Node
// searches upward from the module: node_modules for a bare specifier, and the nearest package.json for the module format
// (register-hooks.mjs packageType reads it as Node does), "imports" and "exports". jiti reads a tsconfig.json only with its
// tsconfigPaths option, which the loader leaves at its default of false (jiti-loader.mjs createJiti; shims/jiti/lib/types.d.ts
// documents the walk up from the importing module), but Bun, which the host can run instead of Node, resolves paths through
// the nearest tsconfig.json or jsconfig.json. Node and Bun read no other file by name in the folders above a module.
var moduleLookupNames = [...]string{"node_modules", "package.json", "tsconfig.json", "jsconfig.json"}

// refuseModuleLookupNames fails when one of moduleLookupNames exists in any of dirs. The folders it is given are the
// ones above a Node cell's entry that PiG owns: a name there is never legitimate and extracting the cell again cannot
// remove it, so the start fails and names the path. The folders above them (the PiG configuration root and up) are the
// user's, as they are for every extension Node loads, and are not checked.
func refuseModuleLookupNames(dirs ...string) error {
	for _, dir := range dirs {
		for _, name := range moduleLookupNames {
			found := filepath.Join(dir, name)
			_, err := os.Lstat(found) //perf:ok constant 8 per start (4 names in each of the 2 folders above the entry), part of the D18 re-check
			switch {
			case err == nil:
				return fmt.Errorf("%s exists: Node looks for modules in every folder above an extension's sources, so it would load from it; remove it", found)
			case !errors.Is(err, fs.ErrNotExist):
				return fmt.Errorf("check %s: %w", found, err)
			}
		}
	}
	return nil
}

// nodeDifference is the first way an extracted Node cell entry differs from its archive.
type nodeDifference struct {
	what string // changed, missing, added, link, mode or unreadable
	name string // the path, relative to the entry folder, in slash form
	err  error  // the file system error of an unreadable path
}

func (d *nodeDifference) Error() string {
	if d.err != nil {
		return d.what + ": " + d.name + ": " + d.err.Error()
	}
	return d.what + ": " + d.name
}

func (d *nodeDifference) Unwrap() error { return d.err }

// nodeVerifyChunk is the size of the pieces the extracted files are compared with the archive in.
const nodeVerifyChunk = 64 << 10

// verifyNodeEntry finds how the extracted Node cell entry at entryDir differs from the archive, which it reads where it lies, or returns nil.
// The entry is what untarNodeSources and the publication make: no link where a folder or file should be (the entry
// folder, sources, and every path in it), no module-lookup name beside sources, every archive entry present as the same
// kind with the same size and bytes (a file), the same executable bit and no wider mode than extraction leaves (a file
// or folder), the same target (a link), and exactly the archive's entries in sources and no others.
//
// It reads the entry on every start, as the embedded Go, Rust and Python cell binaries are compared with their embedded
// bytes on every start, so it uses plain paths: each folder on the way to an entry is confirmed a real folder, nearest to
// the root first, before anything is read through it. A link swapped in after that is the window between any check and
// the Node runtime's later imports, which a cell binary has between its comparison and its exec.
func verifyNodeEntry(archive archiveSource, entryDir string) error {
	v := &nodeVerifier{
		entryDir: entryDir,
		root:     filepath.Join(entryDir, nodeSourcesName),
		known:    make(map[string]struct{}),
		children: make(map[string]int),
		chunks:   make([]byte, 2*nodeVerifyChunk),
	}
	if err := v.requireFolder(entryDir, "."); err != nil {
		return err
	}
	for _, name := range moduleLookupNames {
		_, err := os.Lstat(filepath.Join(entryDir, name)) //perf:ok constant 4 per start (the module-lookup names in the entry folder), part of the D18 re-check
		switch {
		case err == nil:
			return &nodeDifference{what: "added", name: name}
		case !errors.Is(err, fs.ErrNotExist):
			return &nodeDifference{what: "unreadable", name: name, err: err}
		}
	}
	if err := v.requireFolder(v.root, nodeSourcesName); err != nil {
		return err
	}
	reader := tar.NewReader(archiveStream(archive))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read the Node cell archive: %w", err)
		}
		name, err := archivepath.Member(header.Name)
		if err != nil {
			return fmt.Errorf("read the Node cell archive: %w", err)
		}
		if err := v.entry(reader, header, name); err != nil {
			return err
		}
	}
	return v.listings()
}

type nodeVerifier struct {
	entryDir string
	root     string
	known    map[string]struct{} // every path of sources the archive names, implicit folders included
	children map[string]int      // the number of archive entries directly inside each folder ("." is sources)
	folders  []string            // the folders of sources in the archive; sources itself is "."
	chunks   []byte
}

// full is the path of name (relative to sources, slash form) on disk.
func (v *nodeVerifier) full(name string) string {
	return filepath.Join(v.root, filepath.FromSlash(name))
}

// shown is name relative to the entry folder, as a difference reports it.
func shown(name string) string { return nodeSourcesName + "/" + name }

// lstat reads the metadata of fullPath; shownName is how a difference names it.
func lstat(fullPath, shownName string) (fs.FileInfo, error) {
	info, err := os.Lstat(fullPath) //perf:ok one per archive folder, file and link at every start: the D18 comparison of the extracted tree with the archive, bounded by the cell's entry count; DirEntry.Info is an lstat per entry too
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, &nodeDifference{what: "missing", name: shownName}
	case err != nil:
		return nil, &nodeDifference{what: "unreadable", name: shownName, err: err}
	}
	return info, nil
}

// requireFolder fails unless fullPath is a real folder, not a link to one, that no other user can write.
func (v *nodeVerifier) requireFolder(fullPath, shownName string) error {
	info, err := lstat(fullPath, shownName)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return &nodeDifference{what: "link", name: shownName}
	case !info.IsDir():
		return &nodeDifference{what: "changed", name: shownName}
	case !folderModeOK(info.Mode()):
		return &nodeDifference{what: "mode", name: shownName}
	}
	return nil
}

// folder confirms the folder name of sources and every folder above it, nearest to the root first, and counts each in its parent.
func (v *nodeVerifier) folder(name string) error {
	if _, ok := v.known[name]; ok {
		return nil
	}
	//portlint:allow pathseparators name is a tar member name from archivepath.Member: a slash path on every OS, not a host path (v.full converts it to one)
	if parent := path.Dir(name); parent != "." {
		if err := v.folder(parent); err != nil {
			return err
		}
	}
	if err := v.requireFolder(v.full(name), shown(name)); err != nil {
		return err
	}
	v.register(name, true)
	return nil
}

func (v *nodeVerifier) register(name string, isFolder bool) {
	v.known[name] = struct{}{}
	//portlint:allow pathseparators name is a tar member name from archivepath.Member: a slash path on every OS, not a host path
	v.children[path.Dir(name)]++
	if isFolder {
		v.folders = append(v.folders, name)
	}
}

// entry verifies one archive entry, whose bytes (a file) are the reader's.
func (v *nodeVerifier) entry(reader io.Reader, header *tar.Header, name string) error {
	//portlint:allow pathseparators name is a tar member name from archivepath.Member: a slash path on every OS, not a host path (v.full converts it to one)
	if parent := path.Dir(name); parent != "." {
		if err := v.folder(parent); err != nil {
			return err
		}
	}
	if _, ok := v.known[name]; ok && header.Typeflag != tar.TypeDir {
		return fmt.Errorf("read the Node cell archive: %s appears twice", name)
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return v.folder(name)
	case tar.TypeReg:
		if err := v.file(reader, header, name); err != nil {
			return err
		}
		v.register(name, false)
	case tar.TypeSymlink:
		info, err := lstat(v.full(name), shown(name))
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			return &nodeDifference{what: "changed", name: shown(name)}
		}
		target, err := os.Readlink(v.full(name))
		if err != nil {
			return &nodeDifference{what: "unreadable", name: shown(name), err: err}
		}
		if filepath.ToSlash(target) != header.Linkname {
			return &nodeDifference{what: "link", name: shown(name)}
		}
		v.register(name, false)
	default:
		return fmt.Errorf("read the Node cell archive: %s has unsupported type %q", name, header.Typeflag)
	}
	return nil
}

// file compares one regular file with the archive's bytes, which are the reader's.
func (v *nodeVerifier) file(reader io.Reader, header *tar.Header, name string) error {
	full := v.full(name)
	info, err := lstat(full, shown(name))
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return &nodeDifference{what: "link", name: shown(name)}
	case !info.Mode().IsRegular() || info.Size() != header.Size:
		return &nodeDifference{what: "changed", name: shown(name)}
	case !fileModeOK(info.Mode(), header.Mode&0o111 != 0):
		return &nodeDifference{what: "mode", name: shown(name)}
	}
	file, err := os.Open(full)
	if err != nil {
		return &nodeDifference{what: "unreadable", name: shown(name), err: err}
	}
	defer func() { _ = file.Close() }()
	want, have := v.chunks[:nodeVerifyChunk], v.chunks[nodeVerifyChunk:]
	for left := header.Size; left > 0; {
		n := int(min(int64(nodeVerifyChunk), left))
		if _, err := io.ReadFull(reader, want[:n]); err != nil {
			return fmt.Errorf("read the Node cell archive: %s: %w", name, err)
		}
		if _, err := io.ReadFull(file, have[:n]); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return &nodeDifference{what: "changed", name: shown(name)}
			}
			return &nodeDifference{what: "unreadable", name: shown(name), err: err}
		}
		if !bytes.Equal(want[:n], have[:n]) {
			return &nodeDifference{what: "changed", name: shown(name)}
		}
		left -= int64(n)
	}
	return nil
}

// listings fails when a folder of sources holds more or fewer entries than the archive puts in it. Every entry the
// archive names was found on disk, so a larger listing holds an entry the archive does not name, and the first one is the difference.
func (v *nodeVerifier) listings() error {
	for _, folder := range append([]string{"."}, v.folders...) {
		dir, err := os.Open(v.full(folder))
		if err != nil {
			return &nodeDifference{what: "unreadable", name: shown(folder), err: err}
		}
		names, err := dir.Readdirnames(-1)
		_ = dir.Close()
		if err != nil {
			return &nodeDifference{what: "unreadable", name: shown(folder), err: err}
		}
		if len(names) == v.children[folder] {
			continue
		}
		for _, name := range names {
			//portlint:allow pathseparators folder is a tar member name (a slash path on every OS, "." for sources) and name one base name from its listing, which is never split: the key is the archive's slash form
			member := path.Join(folder, name)
			if _, ok := v.known[member]; !ok {
				return &nodeDifference{what: "added", name: shown(member)}
			}
		}
		return &nodeDifference{what: "changed", name: shown(folder)}
	}
	return nil
}

// fileModeOK reports whether a file has the executable bit the archive gives it and no more than extraction leaves: 0755
// for an executable and 0644 for the rest, narrowed by the user's umask, with no setuid, setgid or sticky bit. A mode
// bit means nothing on Windows.
func fileModeOK(mode fs.FileMode, executable bool) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	allowed := fs.FileMode(0o644)
	if executable {
		allowed = 0o755
	}
	return (mode&0o111 != 0) == executable && mode&(fs.ModePerm|fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)&^allowed == 0
}

// folderModeOK reports whether a folder is no wider than the 0755 extraction creates, narrowed by the umask.
func folderModeOK(mode fs.FileMode) bool {
	return runtime.GOOS == "windows" || mode.Perm()&^0o755 == 0
}
