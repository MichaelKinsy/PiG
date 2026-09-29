package experimental

// Ports packages/chord/src/node/manifest.ts.
// Ports packages/coding-agent/src/experimental/plugins/bundled.ts (presentation data).

import (
	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// These format discriminators belong to the pinned upstream Chord schema.
const (
	FacetBundleFormat                = "chord.facet-bundle"
	FacetBundleFormatVersion         = 2
	FacetBundleManifestFile          = "chord-facets.json"
	FacetBundleArtifactFormat        = "chord.facet-bundle-artifact"
	FacetBundleArtifactFormatVersion = 2
)

// FacetBundlePlugin identifies the selected plugin content.
type FacetBundlePlugin struct {
	Id      string  `json:"id"`
	Version *string `json:"version,omitempty"`
}

// MarshalJSON preserves the JavaScript identity strings, including lone UTF-16 units, until the receiving wire codec validates them.
func (plugin FacetBundlePlugin) MarshalJSON() ([]byte, error) {
	value := protocol.Object{{Key: "id", Value: plugin.Id}}
	if plugin.Version != nil {
		value = append(value, protocol.Property{Key: "version", Value: *plugin.Version})
	}
	return protocol.ToJSON(value)
}

// FacetBundleEntry names one content-addressed CommonJS entry and its declared external imports.
type FacetBundleEntry struct {
	File            string   `json:"file"`
	Integrity       string   `json:"integrity"`
	ExternalImports []string `json:"externalImports"`
	SourceMap       *string  `json:"sourceMap,omitempty"`
}

// FacetBundleManifest describes the server-owned bundle entries. The builder owns entry ordering in the manifest file.
type FacetBundleManifest struct {
	Format        string                      `json:"format"`
	FormatVersion int                         `json:"formatVersion"`
	Plugin        FacetBundlePlugin           `json:"plugin"`
	Entries       map[string]FacetBundleEntry `json:"entries"`
}

// FacetBundleArtifact is one self-contained entry transported to a Node host. Optional string pointers preserve omission separately from an explicit empty string.
type FacetBundleArtifact struct {
	Format            string            `json:"format"`
	FormatVersion     int               `json:"formatVersion"`
	Plugin            FacetBundlePlugin `json:"plugin"`
	EntryName         string            `json:"entryName"`
	Entry             FacetBundleEntry  `json:"entry"`
	Source            string            `json:"source"`
	SourceMapContents *string           `json:"sourceMapContents,omitempty"`
}

// CreatePresentationFacetData preserves artifact order in the server-selected presentation payload. It does not build, load, or activate plugin code.
func CreatePresentationFacetData(artifacts []FacetBundleArtifact) pico3.JsonValue {
	selected := make([]FacetBundleArtifact, len(artifacts))
	copy(selected, artifacts)
	return map[string]any{"presentationFacetBundles": selected}
}
