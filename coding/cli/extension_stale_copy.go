package cli

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig additive (D109): two copies of one extension install, loaded from two directories, register the same tools. Pi reports the conflict only; PiG names the stale copy.

const (
	// copyScanFileLimit and copyScanFileBytes bound the comparison of two extension directories.
	// pig additive (D109): Pi has no copy comparison, so these caps have no Pi counterpart.
	copyScanFileLimit = 2000
	// pig additive (D109): see copyScanFileLimit.
	copyScanFileBytes = 1 << 20
	// copySimilarity is the share of files two directories must have in common, by path and content, for one to be a copy of the other.
	copySimilarity = 0.5
)

// extensionTree is the comparable content of an extension directory.
type extensionTree struct {
	files  map[string][sha256.Size]byte
	newest time.Time
}

// readExtensionTree reads the source files of dir, skipping version control, dependency and build directories.
func readExtensionTree(dir string) extensionTree {
	tree := extensionTree{files: map[string][sha256.Size]byte{}}
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != dir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "target" || name == "vendor" || name == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(tree.files) >= copyScanFileLimit || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > copyScanFileBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		relative, _ := filepath.Rel(dir, path)
		tree.files[filepath.ToSlash(relative)] = sha256.Sum256(data)
		if info.ModTime().After(tree.newest) {
			tree.newest = info.ModTime()
		}
		return nil
	})
	return tree
}

// isCopyOf reports whether most of the smaller tree's files appear in the other with the same path and content.
func (t extensionTree) isCopyOf(other extensionTree) bool {
	smaller, larger := t, other
	if len(larger.files) < len(smaller.files) {
		smaller, larger = larger, smaller
	}
	if len(smaller.files) == 0 {
		return false
	}
	shared := 0
	for path, sum := range smaller.files {
		if larger.files[path] == sum {
			shared++
		}
	}
	return float64(shared)/float64(len(smaller.files)) >= copySimilarity
}

// extensionSourceDir returns the directory a loaded extension came from.
func extensionSourceDir(path string, configs []subprocess.ExtConfig) string {
	canonical := canonicalPath(path)
	for _, config := range configs {
		for _, candidate := range []string{config.Source, config.Path} {
			if candidate == "" {
				continue
			}
			if root := canonicalPath(candidate); canonical == root || strings.HasPrefix(canonical, root+string(filepath.Separator)) {
				if config.PackageRoot != "" {
					return config.PackageRoot
				}
				if config.Source != "" {
					return config.Source
				}
				return filepath.Dir(candidate)
			}
		}
	}
	return ""
}

// staleCopyDiagnostics explains a tool conflict between two copies of one extension: it names both directories and the stale one to remove, the one whose newest source file is older. A conflict between different extensions is left as Pi reports it.
func staleCopyDiagnostics(conflicts []codingagent.ToolConflict, configs []subprocess.ExtConfig) []codingagent.AgentSessionRuntimeDiagnostic {
	var diagnostics []codingagent.AgentSessionRuntimeDiagnostic
	seen := map[string]bool{}
	for _, conflict := range conflicts {
		first, second := extensionSourceDir(conflict.Owner, configs), extensionSourceDir(conflict.Path, configs)
		if first == "" || second == "" || canonicalPath(first) == canonicalPath(second) {
			continue
		}
		key := canonicalPath(first) + "\x00" + canonicalPath(second)
		if seen[key] {
			continue
		}
		seen[key] = true
		firstTree, secondTree := readExtensionTree(first), readExtensionTree(second)
		if !firstTree.isCopyOf(secondTree) {
			continue
		}
		stale, current := second, first
		if secondTree.newest.After(firstTree.newest) {
			stale, current = first, second
		}
		diagnostics = append(diagnostics, codingagent.AgentSessionRuntimeDiagnostic{
			Type: "warning",
			Message: fmt.Sprintf("%s and %s are copies of one extension and both register the tool %q. Remove the stale copy, %s, and keep %s.",
				first, second, conflict.Tool, stale, current),
		})
	}
	return diagnostics
}
