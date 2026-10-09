package experimental

// Ports packages/chord/src/node/bundle.ts (direct build contracts).
// Ports packages/chord/src/node/package.ts (package build contracts).

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// FacetEntrySource retains the order of an upstream string-valued record. Repeated names overwrite their value without moving the first property; integer names enumerate before other names, as in Object.entries.
type FacetEntrySource struct {
	Name   string
	Source string
}

// FacetEntrySources is upstream's Record<string, string> of entry names to sources (bundle.ts BundleFacetsOptions.entries,
// package.ts defaultFacets): the insertion-ordered slice of FacetEntrySource. MarshalJSON writes the JSON object with the key order
// Object.entries enumerates; UnmarshalJSON reads one with that order and with repeated names collapsed.
type FacetEntrySources []FacetEntrySource

// MarshalJSON writes the entries as one JSON object, integer names first and a repeated name once at its first position (the order
// protocol.ToJSON gives an object).
func (s FacetEntrySources) MarshalJSON() ([]byte, error) {
	object := make(protocol.Object, len(s))
	for index, entry := range s {
		object[index] = protocol.Property{Key: entry.Name, Value: entry.Source}
	}
	return protocol.ToJSON(object)
}

// UnmarshalJSON reads a JSON object of string values into the entries in enumeration order.
func (s *FacetEntrySources) UnmarshalJSON(data []byte) error {
	value, err := protocol.FromJSON(data)
	if err != nil {
		return err
	}
	properties, ok := value.(protocol.Object)
	if !ok {
		return fmt.Errorf("facet entries must be a JSON object, got %T", value)
	}
	entries := make(FacetEntrySources, len(properties))
	for index, property := range properties {
		key, keyOK := property.Key.(string)
		source, sourceOK := property.Value.(string)
		if !keyOK || !sourceOK {
			return fmt.Errorf("facet entry %v must have a string source", property.Key)
		}
		entries[index] = FacetEntrySource{Name: key, Source: source}
	}
	*s = entries
	return nil
}

// FacetBundlePlatform selects the esbuild platform of a facet bundle (bundle.ts FacetBundlePlatform).
type FacetBundlePlatform string

// The facet bundle platforms. The empty value selects FacetBundlePlatformNode.
const (
	FacetBundlePlatformNode    FacetBundlePlatform = "node"
	FacetBundlePlatformBrowser FacetBundlePlatform = "browser"
	FacetBundlePlatformNeutral FacetBundlePlatform = "neutral"
)

// BundleFacetsOptions selects independent entries and their output directory. A nil plugin version stays omitted; SourceMap and Minify default to false. WorkingDirectory defaults to the current directory. A nil Define adds no replacements, an empty Platform selects node, and a nil Target selects node22.19 for the node platform and es2022 for the others; several targets are esbuild's comma-separated target list.
type BundleFacetsOptions struct {
	Plugin           FacetBundlePlugin
	Entries          FacetEntrySources
	Outdir           string
	WorkingDirectory *string
	External         []string
	SourceMap        bool
	Minify           bool
	Define           map[string]string
	Platform         FacetBundlePlatform
	Target           []string
}

// BundleFacetsResult owns the manifest data and the absolute path of its installed manifest file.
type BundleFacetsResult struct {
	Manifest     FacetBundleManifest
	ManifestPath string
}

// BundleFacetPackageOptions selects a directory or package.json and application-provided conventional entries. Nil defaults select no conventional entries.
type BundleFacetPackageOptions struct {
	PackagePath   string
	Outdir        string
	DefaultFacets FacetEntrySources
}

// BundleFacetPackageResult includes the canonical package paths used to read metadata and resolve entries.
type BundleFacetPackageResult struct {
	BundleFacetsResult
	PackageDirectory string
	PackageJsonPath  string
}

// BundleFacetPackage applies metadata and caller conventions before invoking the shared compiler. Package source maps default to true unless chord.sourceMap disables them. The call waits for all compiler/filesystem work; upstream supplies no cancellation signal.
func BundleFacetPackage(ctx context.Context, options BundleFacetPackageOptions) (BundleFacetPackageResult, error) {
	metadata, err := readFacetPackageMetadata(options.PackagePath)
	if err != nil {
		return BundleFacetPackageResult{}, err
	}
	entries, err := resolveFacetEntries(metadata, options.DefaultFacets)
	if err != nil {
		return BundleFacetPackageResult{}, err
	}
	external := []string{}
	for _, specifiers := range [][]string{metadata.peers, metadata.external} {
		for _, specifier := range specifiers {
			external = append(external, specifier, specifier+"/*")
		}
	}
	result, err := BundleFacets(ctx, BundleFacetsOptions{
		Plugin:  FacetBundlePlugin{Id: metadata.name, Version: new(metadata.version)},
		Entries: entries, Outdir: options.Outdir, WorkingDirectory: &metadata.directory,
		External: external, SourceMap: metadata.sourceMap,
	})
	if err != nil {
		return BundleFacetPackageResult{}, err
	}
	return BundleFacetPackageResult{BundleFacetsResult: result, PackageDirectory: metadata.directory, PackageJsonPath: metadata.jsonPath}, nil
}

func normalizeFacetSources(entries []FacetEntrySource) ([]FacetEntrySource, error) {
	object := make(protocol.Object, len(entries))
	for index, entry := range entries {
		object[index] = protocol.Property{Key: entry.Name, Value: entry.Source}
	}
	encoded, err := protocol.ToJSON(object)
	if err != nil {
		return nil, err
	}
	value, err := protocol.FromJSON(encoded)
	if err != nil {
		return nil, err
	}
	properties := value.(protocol.Object)
	result := make([]FacetEntrySource, len(properties))
	for index, property := range properties {
		result[index] = FacetEntrySource{Name: property.Key.(string), Source: property.Value.(string)}
	}
	return result, nil
}

func validateFacetBuild(options BundleFacetsOptions, entries []FacetEntrySource) error {
	if options.Plugin.Id == "" {
		return errors.New("Facet bundle plugin ID must not be empty")
	}
	if options.Plugin.Version != nil && *options.Plugin.Version == "" {
		return errors.New("Facet bundle plugin version must not be empty")
	}
	if len(entries) == 0 {
		return errors.New("Facet bundle must contain at least one entry")
	}
	for _, entry := range entries {
		if entry.Name == "" {
			return errors.New("Facet bundle entry name must not be empty")
		}
		if entry.Source == "" {
			return fmt.Errorf("Facet bundle entry %s must have a source path", entry.Name)
		}
	}
	if slices.Contains(options.External, "") {
		return errors.New("Facet bundle external import must not be empty")
	}
	return nil
}
