package runtimecell

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// hashBuildInput records host-side inputs that affect generated packed-cell
// artefacts but are not part of individual extension content hashes: the SDK
// source bridge, generated runner templates, and runtime/compiler versions.
func hashBuildInput(h hash.Hash, label, value string) {
	h.Write([]byte(label))
	h.Write([]byte("\x00"))
	h.Write([]byte(value))
	h.Write([]byte("\x00"))
}

// hashTree is the content hash of the tree at root: the physical path of root, then the relative path and the contents of every regular file the
// walk reaches (walkHashedFiles). It always reads the files; sdkTreeHash is the lookup that may answer from a memo.
func hashTree(root string) string {
	abs, err := resolveTreeRoot(root)
	if err != nil {
		return "error:" + err.Error()
	}
	return hashPhysicalTree(abs, nil)
}

// resolveTreeRoot is the physical path of root. WalkDir does not traverse a symlink used as its root. Resolving the root makes the SDK and
// workspace source behind configured links participate in the key. Generated manifests embed the physical root, so its identity participates as
// well: aliases share a key, while relocation produces a new artifact.
func resolveTreeRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// readTreeFile reads one file of a tree hash. Tests replace it to count the files a hash reads.
var readTreeFile = os.ReadFile

// hashPhysicalTree is hashTree of abs, a physical path. When stamps is not nil, each file's stamp is appended to it in walk order, taken before the
// file is read, so a change after the stamp leaves a file that no longer matches it.
func hashPhysicalTree(abs string, stamps *[]treeStamp) string {
	h := sha256.New()
	h.Write([]byte(abs))
	h.Write([]byte{0})
	info, err := os.Stat(abs)
	if err != nil {
		return "error:" + err.Error()
	}
	if !info.IsDir() {
		data, err := readTreeFile(abs)
		if err != nil {
			return "error:" + err.Error()
		}
		h.Write(data)
		return hex.EncodeToString(h.Sum(nil))
	}
	if err := walkHashedFiles(abs, func(path string, info fs.FileInfo) error {
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		data, err := readTreeFile(path)
		if err != nil {
			return err
		}
		if stamps != nil {
			*stamps = append(*stamps, stampOf(path, info))
		}
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write([]byte("\x00"))
		h.Write(data)
		h.Write([]byte("\x00"))
		return nil
	}); err != nil {
		return "error:" + err.Error()
	}
	return hex.EncodeToString(h.Sum(nil))
}

// walkHashedFiles calls visit, in the walk's lexical order, with each regular file of the directory abs that a tree hash covers and the file's
// lstat. It does not enter the directories skipPackedHashDir names, and passes over the files skipPackedHashFile names and every entry that is not a
// regular file, a symbolic link included. The hash and the check of a memoized hash (treeStampsMatch) walk through this one function, so they
// cover the same files.
func walkHashedFiles(abs string, visit func(path string, info fs.FileInfo) error) error {
	return filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != abs && skipPackedHashDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || skipPackedHashFile(d.Name()) {
			return nil
		}
		return visit(path, info)
	})
}

func skipPackedHashDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "target", "vendor", "dist", "build", "__pycache__", ".venv":
		return true
	default:
		return false
	}
}

func skipPackedHashFile(name string) bool {
	return strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, "~") || strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".pyo")
}
