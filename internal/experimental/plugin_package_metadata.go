package experimental

// Ports packages/chord/src/node/package.ts.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	textutil "github.com/MichaelKinsy/PiG/internal/text"
)

type facetPackageMetadata struct {
	directory, jsonPath, name, version string
	peers, external                    []string
	facets                             protocol.Object
	sourceMap                          bool
}
type facetBuildError struct {
	message string
	cause   error
}

func (err *facetBuildError) Error() string { return err.message }
func (err *facetBuildError) Unwrap() error { return err.cause }
func facetError(message string, cause error) error {
	return &facetBuildError{message: message, cause: cause}
}

type facetJSONMember struct {
	key   string
	value json.RawMessage
}

func facetJSONObject(raw json.RawMessage) ([]facetJSONMember, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	members := []facetJSONMember{}
	positions := map[string]int{}
	for decoder.More() {
		start := decoder.InputOffset()
		if _, err := decoder.Token(); err != nil {
			return nil, false
		}
		keyRaw := bytes.TrimLeft(raw[start:decoder.InputOffset()], ", \t\r\n")
		key, ok := facetJSONString(keyRaw)
		if !ok {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		if index, exists := positions[key]; exists {
			members[index].value = value
		} else {
			positions[key] = len(members)
			members = append(members, facetJSONMember{key, value})
		}
	}
	index := func(key string) uint64 {
		value, err := strconv.ParseUint(key, 10, 32)
		if err == nil && value < math.MaxUint32 && strconv.FormatUint(value, 10) == key {
			return value
		}
		return math.MaxUint32
	}
	slices.SortStableFunc(members, func(a, b facetJSONMember) int {
		left, right := index(a.key), index(b.key)
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	})
	return members, true
}
func facetJSONField(members []facetJSONMember, name string) (json.RawMessage, bool) {
	for _, member := range members {
		if member.key == name {
			return member.value, true
		}
	}
	return nil, false
}
func facetJSONString(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	value, err := protocol.FromJSON(raw)
	if err != nil {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}
func sortJSStrings(values []string) {
	slices.SortFunc(values, func(left, right string) int {
		return slices.Compare(textutil.UTF16Units(left), textutil.UTF16Units(right))
	})
}

func readFacetPackageMetadata(packagePath string) (facetPackageMetadata, error) {
	metadata := facetPackageMetadata{sourceMap: true}
	if packagePath == "" {
		return metadata, errors.New("Facet package path must not be empty")
	}
	candidate, err := filepath.Abs(packagePath)
	if err != nil {
		return metadata, err
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return metadata, facetError("Could not access facet package "+candidate, err)
	}
	switch {
	case info.IsDir():
		metadata.directory, err = filepath.EvalSymlinks(candidate)
		metadata.jsonPath = filepath.Join(metadata.directory, "package.json")
	case info.Mode().IsRegular() && filepath.Base(candidate) == "package.json":
		metadata.jsonPath, err = filepath.EvalSymlinks(candidate)
		metadata.directory = filepath.Dir(metadata.jsonPath)
	default:
		return metadata, fmt.Errorf("Facet package path must name a directory or package.json: %s", candidate)
	}
	if err != nil {
		return metadata, err
	}
	data, err := os.ReadFile(metadata.jsonPath)
	if err != nil {
		return metadata, facetError("Could not read facet package metadata "+metadata.jsonPath, err)
	}
	if !json.Valid(data) {
		return metadata, facetError("Could not read facet package metadata "+metadata.jsonPath, errors.New("Invalid JSON"))
	}
	members, ok := facetJSONObject(data)
	if !ok {
		return metadata, fmt.Errorf("Facet package metadata must be an object: %s", metadata.jsonPath)
	}
	name, _ := facetJSONField(members, "name")
	metadata.name, ok = facetJSONString(name)
	if !ok || metadata.name == "" {
		return metadata, fmt.Errorf("Facet package must have a non-empty name: %s", metadata.jsonPath)
	}
	version, _ := facetJSONField(members, "version")
	metadata.version, ok = facetJSONString(version)
	if !ok || metadata.version == "" {
		return metadata, fmt.Errorf("Facet package must have a non-empty version: %s", metadata.jsonPath)
	}
	if peers, present := facetJSONField(members, "peerDependencies"); present {
		entries, ok := facetJSONObject(peers)
		if !ok {
			return metadata, fmt.Errorf("Facet package has invalid peerDependencies: %s", metadata.jsonPath)
		}
		for _, entry := range entries {
			if _, ok := facetJSONString(entry.value); entry.key == "" || !ok {
				return metadata, fmt.Errorf("Facet package has invalid peerDependencies: %s", metadata.jsonPath)
			}
			metadata.peers = append(metadata.peers, entry.key)
		}
		sortJSStrings(metadata.peers)
	}
	if chord, present := facetJSONField(members, "chord"); present {
		configuration, ok := facetJSONObject(chord)
		if !ok {
			return metadata, fmt.Errorf("Facet package chord configuration must be an object: %s", metadata.jsonPath)
		}
		for _, entry := range configuration {
			if entry.key != "facets" && entry.key != "external" && entry.key != "sourceMap" {
				return metadata, fmt.Errorf("Facet package chord configuration has an unknown field: %s", metadata.jsonPath)
			}
		}
		if facets, present := facetJSONField(configuration, "facets"); present {
			entries, ok := facetJSONObject(facets)
			if !ok {
				return metadata, fmt.Errorf("Facet package chord.facets must be an object: %s", metadata.jsonPath)
			}
			for _, entry := range entries {
				source, stringOK := facetJSONString(entry.value)
				disabled := bytes.Equal(bytes.TrimSpace(entry.value), []byte("false"))
				if entry.key == "" || (!stringOK && !disabled) || (stringOK && source == "") {
					return metadata, fmt.Errorf("Facet package has an invalid chord.facets entry: %s", metadata.jsonPath)
				}
				// Upstream assigns string/false values into an ordinary object, whose __proto__ setter ignores these values.
				if entry.key != "__proto__" {
					var value any = source
					if disabled {
						value = false
					}
					metadata.facets = append(metadata.facets, protocol.Property{Key: entry.key, Value: value})
				}
			}
		}
		if external, present := facetJSONField(configuration, "external"); present {
			var values []json.RawMessage
			if err := json.Unmarshal(external, &values); err != nil || values == nil {
				return metadata, fmt.Errorf("Facet package chord.external must contain non-empty strings: %s", metadata.jsonPath)
			}
			for _, value := range values {
				specifier, ok := facetJSONString(value)
				if !ok || specifier == "" {
					return metadata, fmt.Errorf("Facet package chord.external must contain non-empty strings: %s", metadata.jsonPath)
				}
				if !slices.Contains(metadata.external, specifier) {
					metadata.external = append(metadata.external, specifier)
				}
			}
			sortJSStrings(metadata.external)
		}
		if sourceMap, present := facetJSONField(configuration, "sourceMap"); present {
			trimmed := string(bytes.TrimSpace(sourceMap))
			if trimmed != "true" && trimmed != "false" {
				return metadata, fmt.Errorf("Facet package chord.sourceMap must be a boolean: %s", metadata.jsonPath)
			}
			metadata.sourceMap = trimmed == "true"
		}
	}
	return metadata, nil
}

func resolveFacetPackageEntry(directory, source, name string) (string, error) {
	if filepath.IsAbs(source) || (runtime.GOOS == "windows" && strings.HasPrefix(strings.ReplaceAll(source, `\`, "/"), "/")) {
		return "", fmt.Errorf("Facet package entry %s must be relative to the package directory", name)
	}
	if volume := filepath.VolumeName(source); volume != "" {
		if !strings.EqualFold(volume, filepath.VolumeName(directory)) {
			return "", fmt.Errorf("Facet package entry %s escapes the package directory", name)
		}
		source = strings.TrimPrefix(source, volume)
	}
	path := filepath.Join(directory, source)
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return "", err
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("Facet package entry %s escapes the package directory", name)
	}
	return path, nil
}
func resolveFacetEntries(metadata facetPackageMetadata, defaults []FacetEntrySource) ([]FacetEntrySource, error) {
	entries := []FacetEntrySource{}
	resolve := func(name, source string, conventional bool) error {
		kind := "configured"
		if conventional {
			kind = "default"
		}
		if name == "" {
			return fmt.Errorf("Facet package %s entry name must not be empty", kind)
		}
		if source == "" {
			return fmt.Errorf("Facet package %s entry %s must have a source path", kind, name)
		}
		path, err := resolveFacetPackageEntry(metadata.directory, source, name)
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			if conventional && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if conventional {
				return err
			}
			return facetError(fmt.Sprintf("Could not access configured facet entry %s: %s", name, path), err)
		}
		kind = "Configured"
		if conventional {
			kind = "Default"
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s facet entry %s is not a file: %s", kind, name, path)
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			if conventional && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		relative, err := filepath.Rel(metadata.directory, canonical)
		if err != nil {
			return err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("Facet package entry %s resolves outside the package directory", name)
		}
		if name != "__proto__" {
			entries = append(entries, FacetEntrySource{Name: name, Source: canonical})
		}
		return nil
	}
	defaults, err := normalizeFacetSources(defaults)
	if err != nil {
		return nil, err
	}
	for _, entry := range defaults {
		if err := resolve(entry.Name, entry.Source, true); err != nil {
			return nil, err
		}
	}
	for _, entry := range metadata.facets {
		name := entry.Key.(string)
		if entry.Value == false {
			entries = slices.DeleteFunc(entries, func(entry FacetEntrySource) bool { return entry.Name == name })
			continue
		}
		if err := resolve(name, entry.Value.(string), false); err != nil {
			return nil, err
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("Facet package %s has no configured or conventional facet entries", metadata.name)
	}
	return normalizeFacetSources(entries)
}
