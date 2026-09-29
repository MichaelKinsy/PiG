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

// BundleFacetsOptions selects independent entries and their output directory. A nil plugin version stays omitted; SourceMap defaults to false. WorkingDirectory defaults to the current directory.
type BundleFacetsOptions struct {
	Plugin           FacetBundlePlugin
	Entries          []FacetEntrySource
	Outdir           string
	WorkingDirectory *string
	External         []string
	SourceMap        bool
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
	DefaultFacets []FacetEntrySource
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
