package experimental

// Ports packages/coding-agent/src/experimental/plugins/package.ts (server-owned builder).
// Ports packages/chord/src/node/bundle.ts.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/evanw/esbuild/pkg/cli"
	"github.com/google/uuid"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	textutil "github.com/MichaelKinsy/PiG/internal/text"
)

// ConfiguredServerPluginPackage is one serialized server-owned builder. Build waits for the complete compiler/filesystem operation, as upstream's context-free Promise does. It never evaluates plugin JavaScript.
type ConfiguredServerPluginPackage struct {
	ManifestPath string
	Build        func(context.Context) ([]FacetBundleArtifact, error)
}

type facetBuildTail struct {
	mu   sync.Mutex
	done chan struct{}
}

func (tail *facetBuildTail) run(build func() ([]FacetBundleArtifact, error)) ([]FacetBundleArtifact, error) {
	tail.mu.Lock()
	previous := tail.done
	done := make(chan struct{})
	tail.done = done
	tail.mu.Unlock()
	defer close(done)
	if previous != nil {
		<-previous
	}
	return build()
}

var pluginDirectoryLabel = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func nodeFacetUTF8(value string) string { return string(utf16.Decode(textutil.UTF16Units(value))) }

// nodeBasename is Node's path.basename of an absolute, cleaned path: its last element, or "" for a filesystem root, where filepath.Base returns the separator.
func nodeBasename(path string) string {
	if filepath.Dir(path) == path {
		return ""
	}
	return filepath.Base(path)
}

// CreateServerPluginPackage derives the server-owned cache path without building or loading the package.
func CreateServerPluginPackage(directory, serverId, packagePath string) (*ConfiguredServerPluginPackage, error) {
	normalized, err := filepath.Abs(packagePath)
	if err != nil {
		return nil, err
	}
	packageDirectory := normalized
	if filepath.Base(normalized) == "package.json" {
		packageDirectory = filepath.Dir(normalized)
	}
	label := pluginDirectoryLabel.ReplaceAllString(nodeFacetUTF8(nodeBasename(packageDirectory)), "-")
	if label == "" {
		label = "plugin"
	}
	if !strings.HasSuffix(label, "-plugin") {
		label += "-plugin"
	}
	digest := sha256.Sum256([]byte(nodeFacetUTF8(normalized)))
	outdir := filepath.Join(directory, "plugin-builds", serverId, label+"-"+hex.EncodeToString(digest[:])[:12])
	manifestPath := filepath.Join(outdir, FacetBundleManifestFile)
	tail := &facetBuildTail{}
	return &ConfiguredServerPluginPackage{ManifestPath: manifestPath, Build: func(ctx context.Context) ([]FacetBundleArtifact, error) {
		return tail.run(func() ([]FacetBundleArtifact, error) {
			result, err := BundleFacetPackage(ctx, BundleFacetPackageOptions{PackagePath: normalized, Outdir: outdir, DefaultFacets: []FacetEntrySource{{Name: "session", Source: "src/session.ts"}, {Name: "tui", Source: "src/tui.ts"}}})
			if err != nil {
				return nil, err
			}
			manifest := result.Manifest
			entry, ok := manifest.Entries["tui"]
			if !ok {
				return []FacetBundleArtifact{}, nil
			}
			contents, err := os.ReadFile(filepath.Join(outdir, entry.File))
			if err != nil {
				return nil, err
			}
			actual := sha256.Sum256(contents)
			if entry.Integrity != "sha256-"+base64.StdEncoding.EncodeToString(actual[:]) {
				return nil, fmt.Errorf("Facet bundle integrity check failed for %s", entry.File)
			}
			artifact := FacetBundleArtifact{Format: FacetBundleArtifactFormat, FormatVersion: FacetBundleArtifactFormatVersion, Plugin: manifest.Plugin, EntryName: "tui", Entry: entry, Source: string(contents)}
			if entry.SourceMap != nil {
				data, err := os.ReadFile(filepath.Join(outdir, *entry.SourceMap))
				if err != nil {
					return nil, err
				}
				artifact.SourceMapContents = new(string(data))
			}
			return []FacetBundleArtifact{artifact}, nil
		})
	}}, nil
}

// BundleFacets builds opaque entries with the same compiler and transactional output replacement used by server packages. It waits for completion even if ctx is cancelled because upstream has no cancellation signal.
func BundleFacets(_ context.Context, options BundleFacetsOptions) (result BundleFacetsResult, err error) {
	entries, err := normalizeFacetSources(options.Entries)
	if err != nil {
		return result, err
	}
	if err := validateFacetBuild(options, entries); err != nil {
		return result, err
	}
	workingDirectory := ""
	if options.WorkingDirectory != nil {
		workingDirectory = *options.WorkingDirectory
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		return result, err
	}
	output := options.Outdir
	if !filepath.IsAbs(output) {
		output = filepath.Join(workingDirectory, output)
	}
	output = filepath.Clean(output)
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return result, err
	}
	temporary := filepath.Join(parent, "."+filepath.Base(output)+".tmp-"+uuid.NewString())
	if err := os.Mkdir(temporary, 0o755); err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			if cleanup := os.RemoveAll(temporary); cleanup != nil {
				err = cleanup
			}
		}
	}()
	external := []string{"@earendil-works/chord", "@earendil-works/chord/*"}
	for _, specifier := range options.External {
		if !slices.Contains(external, specifier) {
			external = append(external, specifier)
		}
	}
	plugin := options.Plugin
	if plugin.Version != nil {
		plugin.Version = new(*plugin.Version)
	}
	manifest := FacetBundleManifest{Format: FacetBundleFormat, FormatVersion: FacetBundleFormatVersion, Plugin: plugin, Entries: map[string]FacetBundleEntry{}}
	collator := collate.New(language.Und)
	slices.SortStableFunc(entries, func(left, right FacetEntrySource) int { return collator.CompareString(left.Name, right.Name) })
	names := make([]string, 0, len(entries))
	for _, input := range entries {
		source := input.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(workingDirectory, source)
		}
		entry, err := bundleFacetEntry(workingDirectory, options, input.Name, source, temporary, external)
		if err != nil {
			return result, err
		}
		// Upstream assigns entry objects into {}, so __proto__ changes the prototype but is not an own manifest entry.
		if input.Name != "__proto__" {
			manifest.Entries[input.Name] = entry
			names = append(names, input.Name)
		}
	}
	data, err := facetManifestJSON(manifest, names)
	if err != nil {
		return result, err
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, data, "", "  "); err != nil {
		return result, err
	}
	formatted.WriteByte('\n')
	if err := os.WriteFile(filepath.Join(temporary, FacetBundleManifestFile), formatted.Bytes(), 0o644); err != nil {
		return result, err
	}
	if err := replaceFacetBuildDirectory(temporary, output); err != nil {
		return result, err
	}
	return BundleFacetsResult{Manifest: manifest, ManifestPath: filepath.Join(output, FacetBundleManifestFile)}, nil
}

func facetManifestJSON(manifest FacetBundleManifest, names []string) (json.RawMessage, error) {
	plugin := protocol.Object{{Key: "id", Value: manifest.Plugin.Id}}
	if manifest.Plugin.Version != nil {
		plugin = append(plugin, protocol.Property{Key: "version", Value: *manifest.Plugin.Version})
	}
	entries := protocol.Object{}
	for _, name := range names {
		entry := manifest.Entries[name]
		external := make([]any, len(entry.ExternalImports))
		for i, value := range entry.ExternalImports {
			external[i] = value
		}
		value := protocol.Object{{Key: "file", Value: entry.File}, {Key: "integrity", Value: entry.Integrity}, {Key: "externalImports", Value: external}}
		if entry.SourceMap != nil {
			value = append(value, protocol.Property{Key: "sourceMap", Value: *entry.SourceMap})
		}
		entries = append(entries, protocol.Property{Key: name, Value: value})
	}
	return protocol.ToJSON(protocol.Object{{Key: "format", Value: manifest.Format}, {Key: "formatVersion", Value: manifest.FormatVersion}, {Key: "plugin", Value: plugin}, {Key: "entries", Value: entries}})
}

func bundleFacetEntry(workingDirectory string, options BundleFacetsOptions, name, source, temporary string, external []string) (FacetBundleEntry, error) {
	withSourceMap := options.SourceMap
	digest := sha256.Sum256([]byte(nodeFacetUTF8(name)))
	sourceMap := api.SourceMapNone
	if withSourceMap {
		sourceMap = api.SourceMapExternal
	}
	platform, target, engines, err := facetBuildTarget(options)
	if err != nil {
		if errors.Is(err, errFacetBuildPlatform) {
			return FacetBundleEntry{}, fmt.Errorf("Could not bundle facet entry %s", name)
		}
		return FacetBundleEntry{}, fmt.Errorf("Could not bundle facet entry %s\n%w", name, err)
	}
	// Pi pins esbuild 0.28.2. Its JavaScript API uses this same Go compiler; no plugin JavaScript is evaluated here.
	result := api.Build(api.BuildOptions{AbsWorkingDir: workingDirectory, Banner: map[string]string{"js": "\"use strict\";"}, Bundle: true, Define: options.Define, EntryNames: "facet-" + hex.EncodeToString(digest[:])[:12] + "-[hash]", EntryPoints: []string{source}, External: external, Format: api.FormatCommonJS, LegalComments: api.LegalCommentsNone, LogLevel: api.LogLevelSilent, Metafile: true, MinifyIdentifiers: options.Minify, MinifySyntax: options.Minify, MinifyWhitespace: options.Minify, Outdir: temporary, OutExtension: map[string]string{".js": ".cjs"}, Platform: platform, Sourcemap: sourceMap, Supported: map[string]bool{"dynamic-import": false}, Target: target, Engines: engines, Write: true})
	if len(result.Errors) > 0 {
		messages := make([]string, len(result.Errors))
		for i, message := range result.Errors {
			messages[i] = message.Text
			if message.Location != nil {
				location := message.Location
				messages[i] = fmt.Sprintf("%s:%d:%d: %s", location.File, location.Line, location.Column+1, message.Text)
			}
		}
		return FacetBundleEntry{}, fmt.Errorf("Could not bundle facet entry %s\n%s", name, strings.Join(messages, "\n"))
	}
	if result.Metafile == "" {
		return FacetBundleEntry{}, fmt.Errorf("Facet entry %s produced no build metadata", name)
	}
	var info struct {
		Outputs map[string]struct {
			EntryPoint string `json:"entryPoint"`
			Imports    []struct {
				Path     string `json:"path"`
				External bool   `json:"external"`
			} `json:"imports"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(result.Metafile), &info); err != nil {
		return FacetBundleEntry{}, err
	}
	var outputPath string
	count := 0
	externalImports := []string{}
	for path, output := range info.Outputs {
		if output.EntryPoint != "" && filepath.Ext(path) == ".cjs" {
			count++
			outputPath = path
			for _, entry := range output.Imports {
				if entry.External && !slices.Contains(externalImports, entry.Path) {
					externalImports = append(externalImports, entry.Path)
				}
			}
		}
	}
	if count != 1 {
		return FacetBundleEntry{}, fmt.Errorf("Facet entry %s did not produce exactly one JavaScript file", name)
	}
	// esbuild's metafile uses forward slashes on every OS.
	absolute := filepath.FromSlash(outputPath)
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(workingDirectory, absolute)
	}
	file, err := filepath.Rel(temporary, absolute)
	if err != nil {
		return FacetBundleEntry{}, err
	}
	if file == "." || strings.HasPrefix(file, ".."+string(filepath.Separator)) || filepath.Base(file) != file {
		return FacetBundleEntry{}, fmt.Errorf("Facet entry %s produced an invalid output path", name)
	}
	var mapName *string
	if withSourceMap {
		mapName = new(file + ".map")
	}
	for path := range info.Outputs {
		path = filepath.FromSlash(path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(workingDirectory, path)
		}
		if path != absolute && (mapName == nil || path != filepath.Join(temporary, *mapName)) {
			return FacetBundleEntry{}, fmt.Errorf("Facet entry %s produced files other than its JavaScript bundle and source map", name)
		}
	}
	contents, err := os.ReadFile(absolute)
	if err != nil {
		return FacetBundleEntry{}, err
	}
	if mapName != nil {
		if _, err := os.Stat(filepath.Join(temporary, *mapName)); err != nil {
			return FacetBundleEntry{}, err
		}
	}
	sum := sha256.Sum256(contents)
	sortJSStrings(externalImports)
	return FacetBundleEntry{File: file, Integrity: "sha256-" + base64.StdEncoding.EncodeToString(sum[:]), ExternalImports: externalImports, SourceMap: mapName}, nil
}

// errFacetBuildPlatform marks an unknown platform. esbuild's JavaScript API rejects it with a plain Error that carries no build messages, so bundle.ts reports the entry name alone.
var errFacetBuildPlatform = errors.New("invalid facet bundle platform")

// facetBuildTarget resolves bundle.ts's platform and target defaults and applies the option checks of esbuild's JavaScript API (lib/main.js validateAndJoinStringArray and the define loop): a target or define key containing its separator is a build message. The target list takes esbuild's own target grammar (es2022, node22.19, ...), parsed by the esbuild command-line parser the JavaScript API shares.
func facetBuildTarget(options BundleFacetsOptions) (api.Platform, api.Target, []api.Engine, error) {
	for _, target := range options.Target {
		if strings.Contains(target, ",") {
			return 0, 0, nil, fmt.Errorf("Invalid target: %s", target)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(options.Define)) {
		if strings.Contains(key, "=") {
			return 0, 0, nil, fmt.Errorf("Invalid define: %s", key)
		}
	}
	platform := api.PlatformNode
	switch options.Platform {
	case "", FacetBundlePlatformNode:
	case FacetBundlePlatformBrowser:
		platform = api.PlatformBrowser
	case FacetBundlePlatformNeutral:
		platform = api.PlatformNeutral
	default:
		return 0, 0, nil, errFacetBuildPlatform
	}
	targets := options.Target
	if targets == nil {
		if platform == api.PlatformNode {
			targets = []string{"node22.19"}
		} else {
			targets = []string{"es2022"}
		}
	}
	parsed, err := cli.ParseBuildOptions([]string{"--target=" + strings.Join(targets, ",")})
	if err != nil {
		return 0, 0, nil, err
	}
	return platform, parsed.Target, parsed.Engines, nil
}

func replaceFacetBuildDirectory(temporary, output string) error {
	backup := output + ".old-" + uuid.NewString()
	moved := false
	if err := os.Rename(output, backup); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		moved = true
	}
	if err := os.Rename(temporary, output); err != nil {
		if moved {
			if restore := os.Rename(backup, output); restore != nil {
				return restore
			}
		}
		return err
	}
	if moved {
		return os.RemoveAll(backup)
	}
	return nil
}
