package source

import (
	"bytes"
	"encoding/json"
	"strings"
)

// SourceInfo mirrors core/source-info.ts SourceInfo: where a resource, tool or command came from.
type SourceInfo struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Scope   string `json:"scope"`
	Origin  string `json:"origin"`
	BaseDir string `json:"baseDir,omitempty"`
	// ChangelogPath is the changelog file of the package that supplied the resource, when it has one.
	ChangelogPath string `json:"changelogPath,omitempty"`
}

// GetSourcePackageKey is core/source-info.ts getSourcePackageKey: the key under which per-package state, such as the last seen
// extension changelog version, is stored. It is the JSON text of {source, scope}.
func GetSourcePackageKey(source, scope string) string {
	// JSON.stringify leaves <, > and & as they are, so the encoder must not escape them.
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(struct {
		Source string `json:"source"`
		Scope  string `json:"scope"`
	}{source, scope}); err != nil {
		// Two strings always encode.
		panic(err)
	}
	return strings.TrimSuffix(encoded.String(), "\n")
}
