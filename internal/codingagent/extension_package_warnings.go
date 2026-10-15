package codingagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ExtensionWarning is a {path, warning} entry of types.ts LoadExtensionsResult.warnings.
type ExtensionWarning struct{ Path, Warning string }

// hostProvidedExtensionPackages are the packages an extension package must declare in peerDependencies.
// Ports .upstream/current/packages/coding-agent/src/core/resource-loader.ts:53-64.
var hostProvidedExtensionPackages = []string{
	"@earendil-works/pi-agent-core", "@earendil-works/pi-ai", "@earendil-works/pi-coding-agent", "@earendil-works/pi-tui",
	"@mariozechner/pi-agent-core", "@mariozechner/pi-ai", "@mariozechner/pi-coding-agent", "@mariozechner/pi-tui",
	"@sinclair/typebox", "typebox",
}

// ExtensionPackageWarnings warns about an extension package whose package.json lists host-provided packages under `dependencies`.
// It reads each package root once, in first-seen order, and fails when a manifest cannot be parsed. packageRoots holds the
// PathMetadata.packageRoot of each extension path; a top-level extension and a local file source have none.
// Ports .upstream/current/packages/coding-agent/src/core/resource-loader.ts:66-93 (collectExtensionPackageWarnings).
func ExtensionPackageWarnings(packageRoots []string) ([]ExtensionWarning, error) {
	var roots []string
	for _, root := range packageRoots {
		if root != "" && !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}
	var warnings []ExtensionWarning
	for _, root := range roots {
		manifestPath := filepath.Join(root, "package.json")
		data, err := os.ReadFile(manifestPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var manifest any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(string(data), "\ufeff")), &manifest); err != nil {
			return nil, err
		}
		if manifest == nil {
			return nil, errors.New("Cannot read properties of null (reading 'dependencies')")
		}
		object, _ := manifest.(map[string]any)
		dependencies, _ := object["dependencies"].(map[string]any)
		var host []string
		for name := range dependencies {
			if slices.Contains(hostProvidedExtensionPackages, name) {
				host = append(host, name)
			}
		}
		if len(host) == 0 {
			continue
		}
		slices.Sort(host)
		warnings = append(warnings, ExtensionWarning{
			Path:    manifestPath,
			Warning: fmt.Sprintf(`Host-provided extension packages must be declared in peerDependencies with a "*" range, not dependencies: %s. Installed copies can bypass the extension loader and create duplicate runtime modules.`, strings.Join(host, ", ")),
		})
	}
	return warnings, nil
}

// MergeExtensionWarnings keeps one warning per path: the last one, at the position of the first.
// Ports .upstream/current/packages/coding-agent/src/core/resource-loader.ts:95-102 (mergeExtensionWarnings).
func MergeExtensionWarnings(existing, added []ExtensionWarning) []ExtensionWarning {
	merged := make([]ExtensionWarning, 0, len(existing)+len(added))
	for _, warning := range slices.Concat(existing, added) {
		if index := slices.IndexFunc(merged, func(seen ExtensionWarning) bool { return seen.Path == warning.Path }); index >= 0 {
			merged[index] = warning
			continue
		}
		merged = append(merged, warning)
	}
	return merged
}
