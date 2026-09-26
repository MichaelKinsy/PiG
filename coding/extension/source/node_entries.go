package source

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/ignorerules"
)

// NodeManifestEntries returns the extension entry files a Node package's
// package.json "pi.extensions" names, in declaration order, as upstream Pi's
// package manager collects them (collectFilesFromManifestEntries in
// core/package-manager.ts): a file entry is itself, a directory entry expands
// with NodeDirectoryEntries, and an entry that does not exist is reported in
// missing. declared is false when package.json has no non-empty
// "pi.extensions".
func NodeManifestEntries(root string) (entries, missing []string, declared bool, err error) {
	declaredEntries, declared, err := nodeManifestDeclared(root)
	if err != nil || !declared {
		return nil, nil, declared, err
	}
	for _, relative := range declaredEntries {
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, statErr := os.Stat(path)
		if statErr != nil {
			missing = append(missing, relative)
			continue
		}
		if info.IsDir() {
			entries = append(entries, NodeDirectoryEntries(path)...)
			continue
		}
		entries = append(entries, path)
	}
	return entries, missing, true, nil
}

// NodeDirectoryEntries returns the extension entry files in dir as upstream
// Pi's collectAutoExtensionEntries (core/package-manager.ts) does: the
// entries dir's own package.json "pi.extensions" declares when any exist,
// else dir/index.ts, else dir/index.js; otherwise every .ts and .js file in
// dir and the entry of every subdirectory that has one, skipping hidden
// entries, node_modules, and paths excluded by dir's .gitignore, .ignore, or
// .fdignore. A directory with none of these has no entries.
func NodeDirectoryEntries(dir string) []string {
	if entries := nodeRootEntries(dir); entries != nil {
		return entries
	}
	listing, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	rules := ignorerules.Append(nil, dir, dir)
	var entries []string
	for _, entry := range listing {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		full := filepath.Join(dir, name)
		// Upstream follows symbolic links to classify the target.
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		if ignorerules.Ignored(full, info.IsDir(), dir, rules) {
			continue
		}
		switch {
		case info.Mode().IsRegular() && (strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".js")):
			entries = append(entries, full)
		case info.IsDir():
			entries = append(entries, nodeRootEntries(full)...)
		}
	}
	return entries
}

// NodeRootEntries is upstream's resolveExtensionEntries
// (core/package-manager.ts): the existing entries dir's package.json
// "pi.extensions" declares, else index.ts, else index.js, else nil. Declared
// entries are returned as declared, without expanding a directory among them,
// as upstream does.
func NodeRootEntries(dir string) []string {
	return nodeRootEntries(dir)
}

// NodeDeclaresExtensions reports whether dir's package.json has a "pi"
// manifest with a non-empty "pi.extensions" list. Upstream loads only what
// such a manifest names.
func NodeDeclaresExtensions(dir string) bool {
	_, ok, _ := nodeManifestDeclared(dir)
	return ok
}

func nodeRootEntries(dir string) []string {
	if declared, ok, _ := nodeManifestDeclared(dir); ok {
		var entries []string
		for _, relative := range declared {
			path := filepath.Join(dir, filepath.FromSlash(relative))
			if exists(path) {
				entries = append(entries, path)
			}
		}
		if len(entries) > 0 {
			return entries
		}
	}
	for _, name := range []string{"index.ts", "index.js"} {
		if path := filepath.Join(dir, name); exists(path) {
			return []string{path}
		}
	}
	return nil
}

// nodeManifestDeclared reads package.json "pi.extensions" as upstream
// readPiManifest (core/pi-manifest.ts) does: a leading byte-order mark is
// ignored, "pi" must be an object, and the list counts only when every entry
// is a string. ok is false when there is no such non-empty list.
func nodeManifestDeclared(dir string) (entries []string, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var manifest struct {
		PI map[string]json.RawMessage `json:"pi"`
	}
	if json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &manifest) != nil || manifest.PI == nil {
		return nil, false, nil
	}
	var values []any
	if json.Unmarshal(manifest.PI["extensions"], &values) != nil || len(values) == 0 {
		return nil, false, nil
	}
	for _, value := range values {
		entry, isString := value.(string)
		if !isString {
			return nil, false, nil
		}
		entries = append(entries, entry)
	}
	return entries, true, nil
}
