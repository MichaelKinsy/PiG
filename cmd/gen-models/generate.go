package main

// Ports packages/ai/catalog/generate.ts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
	"github.com/MichaelKinsy/PiG/internal/modelgen"
	"github.com/MichaelKinsy/PiG/internal/modelgen/modeldata"
	"github.com/MichaelKinsy/PiG/internal/modelgen/providers"
)

type generatorOptions struct {
	strict, dataOnly, jsonOnly bool
	// jsonOutputDir is the public catalog directory, "" for none; dataOutputDir is where the provider data files go (Pi's
	// src/providers/data).
	jsonOutputDir, dataOutputDir string
	pretty                       bool
}

func (options generatorOptions) validate() error {
	if options.jsonOnly && options.jsonOutputDir == "" {
		return errors.New("--json-only requires --json-output")
	}
	if options.dataOnly && (options.jsonOnly || options.jsonOutputDir != "") {
		return errors.New("--data-only cannot be combined with JSON catalog output")
	}
	if !options.jsonOnly && options.dataOutputDir == "" {
		return errors.New("--data-output is required unless --json-only")
	}
	if options.jsonOnly && options.dataOutputDir != "" {
		return errors.New("--json-only cannot be combined with --data-output")
	}
	return nil
}

// byID keys models by id, keeping the first entry per id, sorted by id like `localeCompare`; the object enumerates canonical
// array indices first, as the JavaScript object it ports does.
func byID(models []*modelgen.Obj) *modelgen.Obj {
	unique := map[string]*modelgen.Obj{}
	var ids []string
	for _, model := range models {
		if _, listed := unique[model.Str("id")]; !listed {
			unique[model.Str("id")] = model
			ids = append(ids, model.Str("id"))
		}
	}
	slices.SortStableFunc(ids, localecompare.Compare)
	object := modelgen.NewObj()
	for _, id := range ids {
		object.Set(id, unique[id])
	}
	return object
}

func objectValues(object *modelgen.Obj) []*modelgen.Obj {
	var values []*modelgen.Obj
	for _, key := range object.Keys() {
		value, _ := object.Get(key)
		values = append(values, value.(*modelgen.Obj))
	}
	return values
}

func (options generatorOptions) serialize(value any) string {
	indent := ""
	if options.pretty {
		indent = "  "
	}
	return string(modelgen.Stringify(value, indent)) + "\n"
}

func generateModels(options generatorOptions) error {
	if err := options.validate(); err != nil {
		return err
	}
	sources, err := modelgen.LoadCatalogSources(options.strict)
	if err != nil {
		return err
	}
	catalogs, err := modelgen.BuildCatalog(providers.All(), sources, options.strict)
	if err != nil {
		return err
	}
	// Providers without any models are left out, as if they did not exist.
	var built []modelgen.ProviderCatalog
	for _, catalog := range catalogs {
		if len(catalog.Chat)+len(catalog.Image)+len(catalog.Classifier) > 0 {
			built = append(built, catalog)
		}
	}

	var sortedProviderIDs []string
	for _, catalog := range built {
		sortedProviderIDs = append(sortedProviderIDs, catalog.ID)
	}
	slices.Sort(sortedProviderIDs)
	jsonChat := modelgen.NewObj()
	jsonAll := modelgen.NewObj()
	all := map[string][]*modelgen.Obj{}
	for _, id := range sortedProviderIDs {
		catalog := built[slices.IndexFunc(built, func(candidate modelgen.ProviderCatalog) bool { return candidate.ID == id })]
		chat := byID(catalog.Chat)
		jsonChat.Set(id, chat)
		entries := slices.Concat(objectValues(chat), objectValues(byID(catalog.Image)), objectValues(byID(catalog.Classifier)))
		all[id] = entries
		list := make([]any, len(entries))
		for i, entry := range entries {
			list[i] = entry
		}
		jsonAll.Set(id, list)
	}

	generatedDataProviderIDs := sortedProviderIDs
	if options.dataOnly {
		if generatedDataProviderIDs, err = readDataProviderIDs(options.dataOutputDir); err != nil {
			return err
		}
	}
	var missing []string
	for _, id := range generatedDataProviderIDs {
		if _, ok := all[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("Cannot hydrate missing providers: %s", strings.Join(missing, ", "))
	}

	// Only the ignored internal data is grouped by API for type derivation.
	files := map[string]string{}
	structure := modeldata.ModelDataStructure{}
	for _, id := range generatedDataProviderIDs {
		entries := make([]modeldata.ModelCatalogEntry, len(all[id]))
		for i, entry := range all[id] {
			entries[i] = modeldata.ModelCatalogEntry{Type: entry.Str("type"), ID: entry.Str("id"), API: entry.Str("api"), JSON: modelgen.Stringify(entry, "")}
		}
		groups, providerStructure, err := modeldata.GroupProviderModelData(id, entries)
		if err != nil {
			return err
		}
		structure[id] = providerStructure
		content := groups + "\n"
		if options.pretty {
			parsed, err := modelgen.ParseJSON([]byte(groups))
			if err != nil {
				return err
			}
			content = options.serialize(parsed)
		}
		files[id+".json"] = content
	}

	generatedAt := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	if !options.jsonOnly {
		if err := options.writeData(structure, files, generatedAt); err != nil {
			return err
		}
	}

	if options.jsonOutputDir != "" {
		// `models.json` and `providers/{id}.json` retain the legacy keyed chat catalog. The `.all` variants are arrays so the same
		// upstream id can appear once per type.
		providerOutputDir := filepath.Join(options.jsonOutputDir, "providers")
		if err := os.RemoveAll(options.jsonOutputDir); err != nil {
			return err
		}
		if err := os.MkdirAll(providerOutputDir, 0o755); err != nil {
			return err
		}
		providerList := make([]any, len(sortedProviderIDs))
		for i, id := range sortedProviderIDs {
			providerList[i] = id
		}
		write := func(path string, value any) error {
			return os.WriteFile(path, []byte(options.serialize(value)), 0o644)
		}
		if err := write(filepath.Join(options.jsonOutputDir, "models.json"), jsonChat); err != nil {
			return err
		}
		if err := write(filepath.Join(options.jsonOutputDir, "models.all.json"), jsonAll); err != nil {
			return err
		}
		if err := write(filepath.Join(options.jsonOutputDir, "providers.json"), providerList); err != nil {
			return err
		}
		for _, id := range sortedProviderIDs {
			chat, _ := jsonChat.Get(id)
			everything, _ := jsonAll.Get(id)
			if err := write(filepath.Join(providerOutputDir, id+".json"), chat); err != nil {
				return err
			}
			if err := write(filepath.Join(providerOutputDir, id+".all.json"), everything); err != nil {
				return err
			}
		}
		fmt.Printf("Generated JSON model catalog under %s\n", options.jsonOutputDir)
	}

	// Print statistics
	totalModels, reasoningModels := 0, 0
	for _, catalog := range built {
		totalModels += len(catalog.Chat)
		for _, model := range catalog.Chat {
			if model.Bool("reasoning") {
				reasoningModels++
			}
		}
	}
	fmt.Printf("\nModel Statistics:\n")
	fmt.Printf("  Total tool-capable models: %d\n", totalModels)
	fmt.Printf("  Reasoning-capable models: %d\n", reasoningModels)
	for _, catalog := range built {
		fmt.Printf("  %s: %d chat models, %d image models, %d classifier models\n", catalog.ID, len(catalog.Chat), len(catalog.Image), len(catalog.Classifier))
	}
	return nil
}

// readDataProviderIDs lists the providers whose data files are in dir, sorted: the providers `--data-only` regenerates. Pi reads
// them from the generated aggregator of its package tree; the data directory is the Go form of that tree.
func readDataProviderIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if name, isData := strings.CutSuffix(entry.Name(), ".json"); isData && !entry.IsDir() && entry.Name() != modeldata.ModelDataManifestFile {
			ids = append(ids, name)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("No generated provider data found in %s", dir)
	}
	slices.Sort(ids)
	return ids, nil
}

// writeData stages and validates all provider values before replacing the current data directory.
func (options generatorOptions) writeData(structure modeldata.ModelDataStructure, files map[string]string, generatedAt string) (err error) {
	dataDir := filepath.Clean(options.dataOutputDir)
	if err := os.MkdirAll(filepath.Dir(dataDir), 0o755); err != nil {
		return err
	}
	stagingRoot, err := os.MkdirTemp(filepath.Dir(dataDir), ".model-generation-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stagingRoot) }()
	stagedDataDir := filepath.Join(stagingRoot, "data")
	previousDataDir := filepath.Join(stagingRoot, "previous-data")
	if err := os.Mkdir(stagedDataDir, 0o755); err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(stagedDataDir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	manifest := modeldata.CreateModelDataManifest(structure, files, generatedAt)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if options.pretty {
		parsed, err := modelgen.ParseJSON(encoded)
		if err != nil {
			return err
		}
		encoded = []byte(options.serialize(parsed))
		encoded = encoded[:len(encoded)-1]
	}
	if err := os.WriteFile(filepath.Join(stagedDataDir, modeldata.ModelDataManifestFile), append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	if err := modeldata.ValidateModelDataDirectory(structure, stagedDataDir); err != nil {
		return err
	}

	_, statErr := os.Stat(dataDir)
	hadPreviousData := statErr == nil
	if hadPreviousData {
		if err := os.Rename(dataDir, previousDataDir); err != nil {
			return err
		}
	}
	err = os.Rename(stagedDataDir, dataDir)
	if err == nil {
		err = modeldata.ValidateModelDataDirectory(structure, dataDir)
	}
	if err != nil {
		_ = os.RemoveAll(dataDir)
		if hadPreviousData {
			_ = os.Rename(previousDataDir, dataDir)
		}
		return err
	}
	if options.dataOnly {
		fmt.Println("Hydrated JSON model values under", options.dataOutputDir)
	} else {
		fmt.Println("Generated JSON model values under", options.dataOutputDir)
	}
	return nil
}
