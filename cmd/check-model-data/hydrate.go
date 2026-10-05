package main

// Ports packages/ai/scripts/hydrate-model-catalog.ts and groupProviderModelData of packages/ai/scripts/model-data.ts.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// hydrationGeneratedAt is the manifest stamp of hydrated data. Public catalogs have no generation timestamp, so a fixed
// stamp makes hydration produce identical bytes for identical input, regardless of build time.
const hydrationGeneratedAt = "1970-01-01T00:00:00.000Z"

// ModelCatalogEntry is one typed entry of a published model catalog: its type, id and API, and the entry as
// JSON.stringify writes it.
type ModelCatalogEntry struct {
	Type, ID, API string
	JSON          []byte
}

// GroupProviderModelData groups one provider's typed catalog entries by API and keys them by `type:id`, the layout of
// `src/providers/data/<provider>.json`. It returns the file's JSON (without the trailing newline) and the provider's
// structure.
func GroupProviderModelData(providerID string, models []ModelCatalogEntry) (string, map[string]string, error) {
	var apis []string
	for _, model := range models {
		if !slices.Contains(apis, model.API) {
			apis = append(apis, model.API)
		}
	}
	sortModelStrings(apis)
	structure := map[string]string{}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, api := range apis {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(modelDataQuote(api) + ":{")
		seen := map[string]bool{}
		first := true
		for _, model := range models {
			if model.API != api {
				continue
			}
			identity := model.Type + ":" + model.ID
			if seen[identity] {
				return "", nil, fmt.Errorf("%s/%s has duplicate %s catalog entries", providerID, identity, api)
			}
			seen[identity] = true
			structure[identity] = api
			if !first {
				out.WriteByte(',')
			}
			first = false
			out.WriteString(modelDataQuote(identity) + ":")
			out.Write(model.JSON)
		}
		out.WriteByte('}')
	}
	out.WriteByte('}')
	return out.String(), structure, nil
}

func isJSONKind(raw json.RawMessage, open byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == open
}

// catalogEntry is isCatalogEntry: an object with string type and id and a non-empty string api.
func catalogEntry(raw json.RawMessage) (ModelCatalogEntry, bool) {
	if !isJSONKind(raw, '{') {
		return ModelCatalogEntry{}, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ModelCatalogEntry{}, false
	}
	typ, typeOK := modelDataString(fields["type"])
	id, idOK := modelDataString(fields["id"])
	api, apiOK := modelDataString(fields["api"])
	if !typeOK || !idOK || !apiOK || api == "" {
		return ModelCatalogEntry{}, false
	}
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return ModelCatalogEntry{}, false
	}
	return ModelCatalogEntry{Type: typ, ID: id, API: api, JSON: canonical}, true
}

// HydrateModelCatalog hydrates the package's provider data files from a published typed catalog (`models.all.json`)
// without network access. With validateOnly, it stages and validates the data without replacing the package's current
// data. Any failure leaves the current data in place.
func HydrateModelCatalog(packageRoot, catalogPath string, validateOnly bool) error {
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		return modelDataFileError(err)
	}
	if !json.Valid(data) {
		return fmt.Errorf("Model catalog is not valid JSON: %s", catalogPath)
	}
	if !isJSONKind(data, '{') {
		return errors.New("Model catalog must be an object")
	}
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(data, &catalog); err != nil {
		return err
	}
	providers, err := ReadModelDataProviderIds(packageRoot)
	if err != nil {
		return err
	}
	files := map[string]string{}
	structure := ModelDataStructure{}
	for _, provider := range providers {
		models, ok := catalog[provider]
		if !ok {
			return fmt.Errorf("Model catalog is missing provider: %s", provider)
		}
		var raws []json.RawMessage
		if !isJSONKind(models, '[') || json.Unmarshal(models, &raws) != nil || len(raws) == 0 {
			return fmt.Errorf("Model catalog has no typed model list for provider: %s", provider)
		}
		entries := make([]ModelCatalogEntry, 0, len(raws))
		for _, raw := range raws {
			entry, ok := catalogEntry(raw)
			if !ok {
				return fmt.Errorf("Model catalog has an invalid entry for provider %s", provider)
			}
			entries = append(entries, entry)
		}
		groups, providerStructure, err := GroupProviderModelData(provider, entries)
		if err != nil {
			return err
		}
		structure[provider] = providerStructure
		files[provider+".json"] = groups + "\n"
	}

	manifest, err := json.Marshal(CreateModelDataManifest(structure, files, hydrationGeneratedAt))
	if err != nil {
		return err
	}
	providersDir := filepath.Join(packageRoot, "src", "providers")
	stagingRoot, err := os.MkdirTemp(providersDir, ".model-hydration-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stagingRoot) }()
	stagedData := filepath.Join(stagingRoot, "data")
	if err := os.Mkdir(stagedData, 0o755); err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(stagedData, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(stagedData, ModelDataManifestFile), append(manifest, '\n'), 0o644); err != nil {
		return err
	}
	if err := ValidateModelDataDirectory(structure, stagedData); err != nil {
		return err
	}
	if validateOnly {
		return nil
	}
	dataDir := filepath.Join(providersDir, "data")
	if err := os.RemoveAll(dataDir); err != nil {
		return err
	}
	return os.Rename(stagedData, dataDir)
}
