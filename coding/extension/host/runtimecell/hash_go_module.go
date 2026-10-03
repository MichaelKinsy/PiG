package runtimecell

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// goSourceExtensions are the file types `go build` compiles into a package besides .go: C, C++, Objective-C, headers, assembly, SWIG, Fortran and system objects (go/build's package file categories).
var goSourceExtensions = map[string]bool{
	".c": true, ".cc": true, ".cpp": true, ".cxx": true, ".m": true,
	".h": true, ".hh": true, ".hpp": true, ".hxx": true,
	".s": true, ".S": true, ".sx": true,
	".swig": true, ".swigcxx": true, ".syso": true,
	".f": true, ".F": true, ".for": true, ".f90": true,
}

// hashGoModule identifies the source set `go build` reads from the module at root: go.mod, go.sum, the non-test sources of every package directory, and the files those packages embed. Nested modules, testdata, vendor, and `.`/`_` directories are outside the module's source set (cmd/go: "Directory and file names that begin with "." or "_" are ignored by the go tool, as are directories named "testdata""; a directory with a go.mod is another module). A tree marked with CACHEDIR.TAG, such as a Cargo target directory, is treated as build output and skipped too, although cmd/go would compile a package placed inside one. Files outside that set, such as build output or documentation created while a build runs, cannot change the identity or fail it. The physical root participates because generated manifests embed it.
func hashGoModule(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "error:" + err.Error()
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "error:" + err.Error()
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "error:" + err.Error()
	}
	if !info.IsDir() {
		return hashTree(abs)
	}
	return hashGoModuleFS(os.DirFS(abs), abs)
}

// GoModuleSourceDigest is hashGoModule's identity as a digest for build locks: an error identity is returned as an error.
func GoModuleSourceDigest(root string) (string, error) {
	digest := hashGoModule(root)
	if after, ok := strings.CutPrefix(digest, "error:"); ok {
		return "", errors.New(after)
	}
	return digest, nil
}

// hashGoModuleFS hashes the module rooted at fsys; label identifies where it lives.
func hashGoModuleFS(fsys fs.FS, label string) string {
	h := sha256.New()
	h.Write([]byte(label))
	h.Write([]byte{0})
	add := func(name string) error {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
		return nil
	}
	if err := hashGoModuleDir(fsys, ".", add); err != nil {
		return "error:" + err.Error()
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashGoModuleDir(fsys fs.FS, dir string, add func(string) error) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		// A directory deleted after its parent listed it held no source of this build.
		if dir != "." && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var files, subdirs []string
	for _, entry := range entries {
		switch {
		case entry.IsDir():
			subdirs = append(subdirs, entry.Name())
		case entry.Type().IsRegular() || entry.Type()&fs.ModeSymlink != 0:
			files = append(files, entry.Name())
		}
	}
	if dir != "." && slices.Contains(files, "go.mod") {
		return nil
	}
	if dir != "." && slices.Contains(files, cacheDirTag) {
		tagged, err := hasCacheDirSignature(fsys, path.Join(dir, cacheDirTag))
		if err != nil || tagged {
			return err
		}
	}
	var sources []string
	var embeds []string
	hasGo := false
	for _, name := range files {
		switch {
		case dir == "." && (name == "go.mod" || name == "go.sum"):
			sources = append(sources, name)
		case ignoredByGo(name):
		case strings.HasSuffix(name, "_test.go"):
		case strings.HasSuffix(name, ".go"):
			hasGo = true
			sources = append(sources, name)
			data, err := fs.ReadFile(fsys, path.Join(dir, name))
			if err != nil {
				return err
			}
			embeds = append(embeds, embedPatterns(data)...)
		case goSourceExtensions[path.Ext(name)]:
			sources = append(sources, name)
		}
	}
	if hasGo {
		for _, name := range sources {
			if err := add(path.Join(dir, name)); err != nil {
				return err
			}
		}
		for _, name := range embeddedFiles(fsys, dir, embeds) {
			if err := add(name); err != nil {
				return err
			}
		}
	} else if dir == "." {
		for _, name := range sources {
			if err := add(name); err != nil {
				return err
			}
		}
	}
	for _, name := range subdirs {
		if ignoredByGo(name) || name == "testdata" || name == "vendor" {
			continue
		}
		if err := hashGoModuleDir(fsys, path.Join(dir, name), add); err != nil {
			return err
		}
	}
	return nil
}

// cacheDirTag names the file that marks a directory tree as a regenerable cache (https://bford.info/cachedir/). Cargo writes it at the root of every target directory, which a build running beside the hash fills and empties.
const cacheDirTag = "CACHEDIR.TAG"

const cacheDirSignature = "Signature: 8a477f597d28d172789f06886806bc55"

// hasCacheDirSignature reports whether the tag file at name begins with the CACHEDIR.TAG signature. A tree so marked holds build output, not source of the module, so the walk skips it without opening any directory below it: Windows refuses to open a directory another process is deleting. A tag file that vanished while the walk read it marks nothing.
func hasCacheDirSignature(fsys fs.FS, name string) (bool, error) {
	file, err := fsys.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	// Only the leading signature identifies a tag, so the read is bounded by its length whatever the file holds.
	head := make([]byte, len(cacheDirSignature))
	n, err := io.ReadFull(file, head)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return string(head[:n]) == cacheDirSignature, nil
}

func ignoredByGo(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// embedPatterns returns the patterns of every //go:embed comment in a Go source file, parsed as go/build's readGoInfo does: each line comment token that is a go:embed directive, with bare, double-quoted and back-quoted arguments. go/build reads them only from files that import "embed"; reading them from every file can only widen the identity.
func embedPatterns(source []byte) []string {
	if !bytes.Contains(source, []byte("//go:embed")) {
		return nil
	}
	fset := token.NewFileSet()
	file := fset.AddFile("", -1, len(source))
	var sc scanner.Scanner
	sc.Init(file, source, nil, scanner.ScanComments)
	var patterns []string
	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			return patterns
		}
		if tok != token.COMMENT || !strings.HasPrefix(lit, "//go:embed") {
			continue
		}
		directive, ok := ast.ParseDirective(pos, lit)
		if !ok || directive.Tool != "go" || directive.Name != "embed" {
			continue
		}
		args, err := directive.ParseArgs()
		if err != nil {
			continue
		}
		for _, arg := range args {
			patterns = append(patterns, arg.Arg)
		}
	}
}

// embeddedFiles resolves patterns relative to dir. A matched directory contributes its tree without `.` and `_` files, or with them under the `all:` prefix, and without nested modules.
func embeddedFiles(fsys fs.FS, dir string, patterns []string) []string {
	var found []string
	for _, pattern := range patterns {
		all := false
		if rest, ok := strings.CutPrefix(pattern, "all:"); ok {
			all, pattern = true, rest
		}
		matches, err := fs.Glob(fsys, path.Join(dir, pattern))
		if err != nil {
			continue
		}
		for _, match := range matches {
			found = append(found, embeddedTree(fsys, match, all)...)
		}
	}
	slices.Sort(found)
	return slices.Compact(found)
}

func embeddedTree(fsys fs.FS, name string, all bool) []string {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		return []string{name}
	}
	var found []string
	_ = fs.WalkDir(fsys, name, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if current == name {
			return nil
		}
		if !all && ignoredByGo(entry.Name()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if _, err := fs.Stat(fsys, path.Join(current, "go.mod")); err == nil {
				return fs.SkipDir
			}
			return nil
		}
		found = append(found, current)
		return nil
	})
	return found
}
