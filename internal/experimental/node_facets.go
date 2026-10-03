package experimental

// Ports packages/chord/src/node/bundle-loader.ts (manifest validation and integrity-verified artifact reading).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"

	"golang.org/x/text/encoding/unicode"
)

// FacetBundleExternalResolver resolves one declared external import. A false result leaves resolution to the pinned Node loader; errors propagate before evaluation.
type FacetBundleExternalResolver func(string) (string, bool, error)

// FacetBundleLoaderOptions selects an opaque bundle entry. ResolveExternal maps selected external imports to host module paths or file URLs. Nil VerifyIntegrity verifies the source.
type FacetBundleLoaderOptions struct {
	ManifestPath    string
	Entry           string
	VerifyIntegrity *bool
	ResolveExternal FacetBundleExternalResolver
}

// FacetBundleArtifactLoaderOptions selects a transported entry and its optional materialization parent.
type FacetBundleArtifactLoaderOptions struct {
	Artifact           chord.JsonValue
	TemporaryDirectory string
	ResolveExternal    FacetBundleExternalResolver
}

type nodeFacetLoader struct {
	manifestPath       string
	entry              string
	artifact           json.RawMessage
	temporaryDirectory string
	external           FacetBundleExternalResolver
	pluginAPI          bool
	verifyIntegrity    *bool
	optionalSession    bool
}

// CreateFacetBundleLoader creates a reusable isolated loader. Each Load rereads the manifest and evaluates a fresh CommonJS generation inside the existing Node host.
func CreateFacetBundleLoader(options FacetBundleLoaderOptions) (chord.FacetLoader, error) {
	if options.Entry == "" {
		return nil, errors.New("Facet bundle entry name must not be empty")
	}
	path, err := filepath.Abs(options.ManifestPath)
	if err != nil {
		return nil, err
	}
	return &nodeFacetLoader{manifestPath: path, entry: options.Entry, external: options.ResolveExternal, verifyIntegrity: options.VerifyIntegrity}, nil
}

// CreateFacetBundleArtifactLoader validates transported metadata and source integrity before creating an isolated loader.
func CreateFacetBundleArtifactLoader(options FacetBundleArtifactLoaderOptions) (chord.FacetLoader, error) {
	raw, err := nodeFacetInputJSON(options.Artifact)
	if err != nil {
		return nil, err
	}
	if err := validateNodeFacetArtifact(raw); err != nil {
		return nil, err
	}
	return &nodeFacetLoader{artifact: raw, temporaryDirectory: options.TemporaryDirectory, external: options.ResolveExternal}, nil
}

// CreateSessionPluginFacetLoader preserves manifest order and returns nil for an empty selection. A selected package without a session entry contributes an empty loaded set.
func CreateSessionPluginFacetLoader(manifestPaths []string) (chord.FacetLoader, error) {
	if len(manifestPaths) == 0 {
		return nil, nil
	}
	loaders := make([]chord.FacetLoader, len(manifestPaths))
	for i, path := range manifestPaths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		loaders[i] = &nodeFacetLoader{manifestPath: absolute, entry: "session", optionalSession: true, pluginAPI: true}
	}
	return chord.CombineFacetLoaders(loaders...), nil
}

// CreatePresentationFacetLoaders creates local loaders only for artifacts selected by the connected server. Validation completes before any plugin code is evaluated.
func CreatePresentationFacetLoaders(data chord.JsonValue) ([]chord.FacetLoader, error) {
	raw, err := nodeFacetInputJSON(data)
	if err != nil {
		return nil, err
	}
	members, ok := facetJSONObject(raw)
	if !ok {
		return nil, errors.New("Invalid presentation plugin data")
	}
	artifacts, present := facetJSONField(members, "presentationFacetBundles")
	if !present {
		return []chord.FacetLoader{}, nil
	}
	var selected []json.RawMessage
	if err := json.Unmarshal(artifacts, &selected); err != nil || selected == nil {
		return nil, errors.New("Invalid presentation plugin bundle list")
	}
	loaders := make([]chord.FacetLoader, len(selected))
	for i, artifact := range selected {
		if err := validateNodeFacetArtifact(artifact); err != nil {
			return nil, err
		}
		loaders[i] = &nodeFacetLoader{artifact: artifact, pluginAPI: true}
	}
	return loaders, nil
}

func nodeFacetInputJSON(value any) (json.RawMessage, error) {
	if object, ok := value.(protocol.Object); ok {
		return protocol.ToJSON(object)
	}
	return json.Marshal(value)
}

// facetArtifactManifestJSON encodes the one-entry manifest for a facet bundle artifact. The raw plugin and entries members are embedded as JSON values, so an invalid member is rejected instead of spliced into the document.
func facetArtifactManifestJSON(plugin, entries json.RawMessage) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(struct {
		Format        string          `json:"format"`
		FormatVersion int             `json:"formatVersion"`
		Plugin        json.RawMessage `json:"plugin"`
		Entries       json.RawMessage `json:"entries"`
	}{Format: FacetBundleFormat, FormatVersion: FacetBundleFormatVersion, Plugin: plugin, Entries: entries})
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), err
}

func validateNodeFacetArtifact(raw json.RawMessage) error {
	members, ok := facetJSONObject(raw)
	field := func(name string) json.RawMessage {
		value, ok := facetJSONField(members, name)
		if !ok {
			return json.RawMessage(`null`)
		}
		return value
	}
	format, _ := facetJSONString(field("format"))
	name, nameOK := facetJSONString(field("entryName"))
	source, sourceOK := facetJSONString(field("source"))
	var version float64
	if !ok || format != FacetBundleArtifactFormat || json.Unmarshal(field("formatVersion"), &version) != nil || version != FacetBundleArtifactFormatVersion || !nameOK || name == "" || !sourceOK {
		return errors.New("Invalid facet bundle artifact")
	}
	nameJSON, err := protocol.ToJSON(name)
	if err != nil {
		return err
	}
	manifestJSON, err := facetArtifactManifestJSON(field("plugin"), json.RawMessage("{"+string(nameJSON)+":"+string(field("entry"))+"}"))
	if err != nil {
		return errors.New("Invalid facet bundle artifact")
	}
	_, err = validateFacetBundleManifest(manifestJSON, "facet bundle artifact")
	if err != nil {
		return err
	}
	entry, err := validateFacetBundleEntry(facetJSONMember{key: name, value: field("entry")}, "facet bundle artifact")
	if err != nil {
		return err
	}
	mapRaw, hasMap := facetJSONField(members, "sourceMapContents")
	if entry.SourceMap == nil && hasMap {
		return errors.New("Facet bundle artifact has source map contents without a source map")
	}
	if entry.SourceMap != nil {
		if _, ok := facetJSONString(mapRaw); !ok {
			return errors.New("Facet bundle artifact is missing its source map contents")
		}
	}
	digest := sha256.Sum256([]byte(nodeFacetUTF8(source)))
	if entry.Integrity != "sha256-"+base64.StdEncoding.EncodeToString(digest[:]) {
		return fmt.Errorf("Facet bundle integrity check failed for %s", entry.File)
	}
	return nil
}

func (loader *nodeFacetLoader) Load(ctx context.Context) (chord.LoadedFacets, error) {
	return loader.load(ctx, dispatchNodeFacetHost)
}

func (loader *nodeFacetLoader) load(ctx context.Context, dispatch func(context.Context, *nodeFacetGeneration, string, json.RawMessage) (json.RawMessage, error)) (_ chord.LoadedFacets, err error) {
	if loader.optionalSession {
		manifest, err := ReadFacetBundleManifest(loader.manifestPath)
		if err != nil {
			return chord.LoadedFacets{}, err
		}
		if _, present := manifest.Entries["session"]; !present {
			return chord.LoadedFacets{Facets: []chord.Facet{}, Dispose: func(context.Context) error { return nil }}, nil
		}
	}
	generation, err := openNodeFacetGeneration(ctx, dispatch)
	if err != nil {
		return chord.LoadedFacets{}, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, generation.close(context.Background()))
		}
	}()
	generation.external = loader.external
	var references []nodeFacetReference
	request := struct {
		Op                 string          `json:"op"`
		ManifestPath       string          `json:"manifestPath,omitempty"`
		Entry              string          `json:"entry,omitempty"`
		Artifact           json.RawMessage `json:"artifact,omitempty"`
		TemporaryDirectory string          `json:"temporaryDirectory,omitempty"`
		ResolveExternal    bool            `json:"resolveExternal"`
		PluginAPI          bool            `json:"pluginAPI"`
		VerifyIntegrity    *bool           `json:"verifyIntegrity,omitempty"`
	}{"load", loader.manifestPath, loader.entry, loader.artifact, loader.temporaryDirectory, loader.external != nil, loader.pluginAPI, loader.verifyIntegrity}
	if err := generation.request(ctx, false, request, &references); err != nil {
		return chord.LoadedFacets{}, err
	}
	facets := make([]chord.Facet, len(references))
	for i, reference := range references {
		facets[i] = chord.Facet{Id: reference.Id, Setup: func(env *chord.FacetEnvironment) error {
			generation.mu.Lock()
			if generation.closed {
				generation.mu.Unlock()
				return errors.New("Facet generation is closed")
			}
			generation.references++
			generation.nextId++
			id := fmt.Sprint(generation.nextId)
			generation.environments[id] = env
			generation.mu.Unlock()
			if err := env.Own(func(ctx context.Context) error {
				generation.mu.Lock()
				delete(generation.environments, id)
				generation.mu.Unlock()
				return generation.release(ctx)
			}); err != nil {
				return errors.Join(err, generation.release(context.Background()))
			}
			return generation.request(context.WithoutCancel(ctx), true, map[string]any{"op": "setup", "id": reference.Reference, "environment": id}, nil)
		}}
	}
	var once sync.Once
	var disposeErr error
	return chord.LoadedFacets{Facets: facets, Dispose: func(ctx context.Context) error {
		once.Do(func() {
			disposeErr = errors.Join(generation.request(ctx, false, map[string]any{"op": "releaseLoaded"}, nil), generation.release(ctx))
		})
		return disposeErr
	}}, nil
}

// FacetBundleArtifactReadOptions selects one opaque entry from a manifest on disk.
type FacetBundleArtifactReadOptions struct {
	ManifestPath string
	Entry        string
}

// ReadFacetBundleArtifact reads one transportable entry and verifies its SHA-256 integrity before returning it. Integrity covers the UTF-8 encoding of the decoded source, as in Node; an optional source map is read as text but is not itself hashed.
func ReadFacetBundleArtifact(options FacetBundleArtifactReadOptions) (FacetBundleArtifact, error) {
	if options.Entry == "" {
		return FacetBundleArtifact{}, errors.New("Facet bundle entry name must not be empty")
	}
	manifestPath, err := filepath.Abs(options.ManifestPath)
	if err != nil {
		return FacetBundleArtifact{}, err
	}
	manifest, err := ReadFacetBundleManifest(manifestPath)
	if err != nil {
		return FacetBundleArtifact{}, err
	}
	entry, exists := manifest.Entries[options.Entry]
	if !exists {
		return FacetBundleArtifact{}, fmt.Errorf("Facet bundle %s has no entry named %s", manifest.Plugin.Id, options.Entry)
	}
	source, err := readNodeFacetText(filepath.Join(filepath.Dir(manifestPath), entry.File))
	if err != nil {
		return FacetBundleArtifact{}, err
	}
	digest := sha256.Sum256(source)
	if entry.Integrity != "sha256-"+base64.StdEncoding.EncodeToString(digest[:]) {
		return FacetBundleArtifact{}, fmt.Errorf("Facet bundle integrity check failed for %s", entry.File)
	}
	artifact := FacetBundleArtifact{
		Format: FacetBundleArtifactFormat, FormatVersion: FacetBundleArtifactFormatVersion,
		Plugin: manifest.Plugin, EntryName: options.Entry, Entry: entry, Source: string(source),
	}
	if entry.SourceMap != nil {
		contents, err := readNodeFacetText(filepath.Join(filepath.Dir(manifestPath), *entry.SourceMap))
		if err != nil {
			return FacetBundleArtifact{}, err
		}
		artifact.SourceMapContents = new(string(contents))
	}
	return artifact, nil
}

func readNodeFacetText(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Node's readFile(..., "utf8") consumes a valid prefix of an incomplete sequence as one replacement before parsing or hashing the text.
	return unicode.UTF8.NewDecoder().Bytes(data)
}

// ReadFacetBundleManifest reads and validates the pinned Chord manifest shape without evaluating its entries. Relative paths resolve against the working directory; read and JSON syntax failures retain their cause. Optional null fields are invalid rather than absent.
func ReadFacetBundleManifest(path string) (FacetBundleManifest, error) {
	manifestPath, err := filepath.Abs(path)
	if err != nil {
		return FacetBundleManifest{}, err
	}
	data, err := readNodeFacetText(manifestPath)
	if err != nil {
		return FacetBundleManifest{}, facetError("Could not read facet bundle manifest "+manifestPath, err)
	}
	if !json.Valid(data) {
		return FacetBundleManifest{}, facetError("Could not read facet bundle manifest "+manifestPath, errors.New("Invalid JSON"))
	}
	return validateFacetBundleManifest(data, manifestPath)
}

func validateFacetBundleManifest(data json.RawMessage, path string) (FacetBundleManifest, error) {
	manifest := FacetBundleManifest{}
	members, object := facetJSONObject(data)
	format, _ := facetJSONField(members, "format")
	formatName, _ := facetJSONString(format)
	if !object || formatName != FacetBundleFormat {
		return manifest, fmt.Errorf("Invalid facet bundle manifest format in %s", path)
	}
	version, _ := facetJSONField(members, "formatVersion")
	var number float64
	if err := json.Unmarshal(version, &number); err != nil || number != FacetBundleFormatVersion {
		text, err := facetBundleVersionString(version)
		if err != nil {
			return manifest, err
		}
		return manifest, fmt.Errorf("Unsupported facet bundle manifest version in %s: %s", path, text)
	}
	plugin, _ := facetJSONField(members, "plugin")
	identity, object := facetJSONObject(plugin)
	id, _ := facetJSONField(identity, "id")
	idText, isString := facetJSONString(id)
	if !object || !isString || idText == "" {
		return manifest, fmt.Errorf("Facet bundle manifest has an invalid plugin identity in %s", path)
	}
	manifest.Plugin.Id = idText
	if version, present := facetJSONField(identity, "version"); present {
		text, isString := facetJSONString(version)
		if !isString || text == "" {
			return FacetBundleManifest{}, fmt.Errorf("Facet bundle manifest has an invalid plugin version in %s", path)
		}
		manifest.Plugin.Version = new(text)
	}
	entries, _ := facetJSONField(members, "entries")
	candidates, object := facetJSONObject(entries)
	if !object || len(candidates) == 0 {
		return FacetBundleManifest{}, fmt.Errorf("Facet bundle manifest has no entries in %s", path)
	}
	manifest.Entries = make(map[string]FacetBundleEntry, len(candidates))
	for _, candidate := range candidates {
		entry, err := validateFacetBundleEntry(candidate, path)
		if err != nil {
			return FacetBundleManifest{}, err
		}
		// Upstream assigns into {}, whose __proto__ setter does not create an own entry.
		if candidate.key != "__proto__" {
			manifest.Entries[candidate.key] = entry
		}
	}
	manifest.Format, manifest.FormatVersion = FacetBundleFormat, FacetBundleFormatVersion
	return manifest, nil
}

func validateFacetBundleEntry(candidate facetJSONMember, path string) (FacetBundleEntry, error) {
	entry := FacetBundleEntry{}
	members, object := facetJSONObject(candidate.value)
	if candidate.key == "" || !object {
		return entry, fmt.Errorf("Facet bundle manifest has an invalid entry in %s", path)
	}
	file, _ := facetJSONField(members, "file")
	fileName, isString := facetJSONString(file)
	if !isString {
		return entry, fmt.Errorf("Facet bundle entry %s has no file", candidate.key)
	}
	if err := validateFacetBundleFilename(fileName, "entry "+candidate.key); err != nil {
		return entry, err
	}
	entry.File = fileName
	integrity, _ := facetJSONField(members, "integrity")
	integrityText, isString := facetJSONString(integrity)
	if !isString {
		return entry, fmt.Errorf("Facet bundle entry %s has no integrity", candidate.key)
	}
	if suffix, ok := strings.CutPrefix(integrityText, "sha256-"); !ok || suffix == "" {
		return entry, errors.New("Facet bundle entry has an invalid SHA-256 integrity value")
	}
	entry.Integrity = integrityText
	external, _ := facetJSONField(members, "externalImports")
	var imports []json.RawMessage
	if err := json.Unmarshal(external, &imports); err != nil || imports == nil {
		return entry, fmt.Errorf("Facet bundle entry %s has invalid external imports", candidate.key)
	}
	entry.ExternalImports = make([]string, len(imports))
	for index, specifier := range imports {
		text, isString := facetJSONString(specifier)
		if !isString {
			return entry, fmt.Errorf("Facet bundle entry %s has invalid external imports", candidate.key)
		}
		entry.ExternalImports[index] = text
	}
	seen := make(map[string]bool, len(imports))
	for _, specifier := range entry.ExternalImports {
		if seen[specifier] {
			return entry, fmt.Errorf("Facet bundle entry %s has duplicate external imports", candidate.key)
		}
		seen[specifier] = true
	}
	if sourceMap, present := facetJSONField(members, "sourceMap"); present {
		text, isString := facetJSONString(sourceMap)
		if !isString {
			return entry, fmt.Errorf("Facet bundle entry %s has an invalid source map", candidate.key)
		}
		if err := validateFacetBundleFilename(text, "entry "+candidate.key+" source map"); err != nil {
			return entry, err
		}
		entry.SourceMap = new(text)
	}
	return entry, nil
}

func validateFacetBundleFilename(file, label string) error {
	if file == "" || filepath.IsAbs(file) || filepath.Base(file) != file || file == "." || file == ".." {
		return fmt.Errorf("Facet bundle %s must be a filename relative to its manifest", label)
	}
	return nil
}

// facetBundleVersionString implements String(value) for the invalid version diagnostic, including absent, array, object, and overflowing JSON number values.
func facetBundleVersionString(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined", nil
	}
	if text, isString := facetJSONString(raw); isString {
		return text, nil
	}
	switch raw[0] {
	case '{':
		members, _ := facetJSONObject(raw)
		if _, present := facetJSONField(members, "toString"); present {
			return "", errors.New("Cannot convert object to primitive value")
		}
		return "[object Object]", nil
	case '[':
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil {
			return "", err
		}
		texts := make([]string, len(elements))
		for index, element := range elements {
			if !bytes.Equal(bytes.TrimSpace(element), []byte("null")) {
				text, err := facetBundleVersionString(element)
				if err != nil {
					return "", err
				}
				texts[index] = text
			}
		}
		return strings.Join(texts, ","), nil
	case 't', 'f', 'n':
		return string(raw), nil
	default:
		number, err := strconv.ParseFloat(string(raw), 64)
		if math.IsInf(number, 1) {
			return "Infinity", nil
		}
		if math.IsInf(number, -1) {
			return "-Infinity", nil
		}
		if err != nil {
			return "", err
		}
		if number == 0 {
			return "0", nil
		}
		encoded, err := json.Marshal(number)
		return string(encoded), err
	}
}
