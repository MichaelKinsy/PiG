package packagecontent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// PiManifest mirrors upstream pi-manifest.ts PiManifest: the resource lists a
// package.json "pi" object declares. A nil field was absent or malformed.
type PiManifest struct {
	Extensions []string
	Skills     []string
	Prompts    []string
	Themes     []string
	// ChangelogPath is the package-relative changelog file the "pi" object names. Nil when the field is absent or not a string; a
	// present empty string means the package has no changelog (resolvePackageChangelogPath).
	ChangelogPath *string
}

// utf8BOM is the byte-order mark upstream stripBom removes before JSON.parse.
var utf8BOM = []byte("\xef\xbb\xbf")

// ReadPiManifest mirrors upstream readPiManifest. It returns nil when the file
// cannot be read or parsed, or when the package or its "pi" value is not a JSON
// object. A resource field is kept only when it is an array of strings.
func ReadPiManifest(packageJSONPath string) *PiManifest {
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil
	}
	pkg := decodeJSONObject(bytes.TrimPrefix(data, utf8BOM))
	if pkg == nil {
		return nil
	}
	pi := decodeJSONObject(pkg["pi"])
	if pi == nil {
		return nil
	}
	manifest := &PiManifest{}
	for field, target := range map[string]*[]string{
		"extensions": &manifest.Extensions,
		"skills":     &manifest.Skills,
		"prompts":    &manifest.Prompts,
		"themes":     &manifest.Themes,
	} {
		if entries, ok := decodeJSONStringArray(pi[field]); ok {
			*target = entries
		}
	}
	var changelogPath string
	if json.Unmarshal(pi["changelogPath"], &changelogPath) == nil && len(pi["changelogPath"]) > 0 && pi["changelogPath"][0] == '"' {
		manifest.ChangelogPath = &changelogPath
	}
	return manifest
}

// decodeJSONObject returns the members of a JSON object, or nil for any other
// value, including null and arrays.
func decodeJSONObject(raw []byte) map[string]json.RawMessage {
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil {
		return nil
	}
	return object
}

// decodeJSONStringArray reports whether raw is a JSON array whose every entry
// is a string, matching upstream Array.isArray(entries) && entries.every(string).
func decodeJSONStringArray(raw []byte) ([]string, bool) {
	var values []any
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, false
	}
	entries := make([]string, 0, len(values))
	for _, value := range values {
		entry, ok := value.(string)
		if !ok {
			return nil, false
		}
		entries = append(entries, entry)
	}
	return entries, true
}

// ResolvePackageChangelogPath is package-manager.ts resolvePackageChangelogPath (b4b72a35): the changelog file of the package at
// packageRoot, or "" when it has none. The file is the manifest's `pi.changelogPath`, else CHANGELOG.md; an empty manifest value means
// no changelog, without the fallback. A path that does not exist, is not a regular file, or leaves the package root (once symbolic
// links are resolved) is ignored.
func ResolvePackageChangelogPath(packageRoot string) string {
	changelogPath := "CHANGELOG.md"
	if manifest := ReadPiManifest(filepath.Join(packageRoot, "package.json")); manifest != nil && manifest.ChangelogPath != nil {
		changelogPath = *manifest.ChangelogPath
	}
	if changelogPath == "" {
		return ""
	}
	resolved := filepath.Join(packageRoot, changelogPath)
	if filepath.IsAbs(changelogPath) {
		resolved = filepath.Clean(changelogPath)
	}
	if _, err := os.Stat(resolved); err != nil {
		return ""
	}
	if !isPathInside(canonicalizePath(packageRoot), canonicalizePath(resolved)) {
		return ""
	}
	if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return resolved
}

// canonicalizePath is utils/paths.ts canonicalizePath: the real path, or the path itself when it cannot be resolved.
func canonicalizePath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// isPathInside is package-manager.ts isPathInside: child is strictly below parent.
func isPathInside(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
