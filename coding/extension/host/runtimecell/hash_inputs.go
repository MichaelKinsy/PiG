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

func hashTree(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "error:" + err.Error()
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "error:" + err.Error()
	}
	// WalkDir does not traverse a symlink used as its root. Resolve the root so
	// SDK and workspace source behind configured links participates in the key.
	// Generated manifests embed the physical root, so its identity participates
	// as well: aliases share a key, while relocation produces a new artifact.
	h := sha256.New()
	h.Write([]byte(abs))
	h.Write([]byte{0})
	info, err := os.Stat(abs)
	if err != nil {
		return "error:" + err.Error()
	}
	if !info.IsDir() {
		data, err := os.ReadFile(abs)
		if err != nil {
			return "error:" + err.Error()
		}
		h.Write(data)
		return hex.EncodeToString(h.Sum(nil))
	}
	if err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
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
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
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
