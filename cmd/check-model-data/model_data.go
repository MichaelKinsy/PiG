package main

// Ports packages/ai/scripts/model-data.ts.

import (
	"crypto/sha256"
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
	"syscall"
)

const ModelDataSchemaVersion = 3
const ModelDataManifestFile = ".manifest.json"

type ModelDataStructure map[string]map[string]string

type ModelDataManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	GeneratedAt   string            `json:"generatedAt"`
	StructureHash string            `json:"structureHash"`
	Files         map[string]string `json:"files"`
}

var modelDataImportPattern = regexp.MustCompile(`(?m)^import \{ [A-Z][A-Z0-9_]*_MODELS \} from "\./providers/([^"/]+)\.models\.ts";$`)

func modelDataSHA256(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func AssertExactModelIds(label string, expected, actual []string) error {
	expected, actual = uniqueModelStrings(expected), uniqueModelStrings(actual)
	if slices.Equal(expected, actual) {
		return nil
	}
	return fmt.Errorf("%s model IDs do not match (%s)", label, describeSetDifference(expected, actual))
}

func uniqueModelStrings(values []string) []string {
	values = slices.Clone(values)
	sortModelStrings(values)
	return slices.Compact(values)
}

func describeSetDifference(expected, actual []string) string {
	var missing, extra, descriptions []string
	for _, key := range expected {
		if !slices.Contains(actual, key) {
			missing = append(missing, key)
		}
	}
	for _, key := range actual {
		if !slices.Contains(expected, key) {
			extra = append(extra, key)
		}
	}
	if len(missing) > 0 {
		descriptions = append(descriptions, "missing: "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		descriptions = append(descriptions, "extra: "+strings.Join(extra, ", "))
	}
	return strings.Join(descriptions, "; ")
}

func ReadModelDataProviderIds(packageRoot string) ([]string, error) {
	path := filepath.Join(packageRoot, "src", "models.generated.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, modelDataFileError(err)
	}
	matches := modelDataImportPattern.FindAllStringSubmatch(string(data), -1)
	providers := make([]string, 0, len(matches))
	for _, match := range matches {
		providers = append(providers, match[1])
	}
	sortModelStrings(providers)
	if len(providers) == 0 {
		return nil, fmt.Errorf("No generated provider imports found in %s", path)
	}
	if len(uniqueModelStrings(providers)) != len(providers) {
		return nil, fmt.Errorf("Generated model aggregator contains duplicate provider imports: %s", path)
	}
	return providers, nil
}

func ReadModelDataStructure(packageRoot string) (ModelDataStructure, error) {
	providers, err := ReadModelDataProviderIds(packageRoot)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(packageRoot, "src", "providers")
	expected := make([]string, 0, len(providers))
	for _, provider := range providers {
		expected = append(expected, provider+".models.ts")
	}
	sortModelStrings(expected)
	actual, err := modelDataFiles(dir, ".models.ts", "")
	if err != nil {
		return nil, err
	}
	if !slices.Equal(expected, actual) {
		return nil, fmt.Errorf("Generated model aggregator and provider shards do not match (%s)", describeSetDifference(expected, actual))
	}
	structure := ModelDataStructure{}
	for _, provider := range providers {
		path := filepath.Join(dir, "data", provider+".json")
		var errs []string
		groups := readModelDataObject(path, provider+".json", &errs)
		if groups == nil {
			return nil, errors.New(strings.Join(errs, "\n"))
		}
		models := map[string]string{}
		for _, api := range groups.keys {
			group := parseModelDataObject(groups.values[api])
			if group == nil {
				return nil, fmt.Errorf("%s API group %s must be an object", path, modelDataQuote(api))
			}
			for _, id := range group.keys {
				if _, exists := models[id]; exists {
					return nil, fmt.Errorf("%s contains model %s in more than one API group", path, id)
				}
				models[id] = api
			}
		}
		if len(models) == 0 {
			return nil, fmt.Errorf("%s contains no generated model data", path)
		}
		structure[provider] = models
	}
	return structure, nil
}

func ModelDataStructureHash(structure ModelDataStructure) string {
	var out strings.Builder
	out.WriteByte('{')
	for i, provider := range sortedModelObjectKeys(structure) {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(modelDataQuote(provider))
		out.WriteString(":{")
		for j, id := range sortedModelObjectKeys(structure[provider]) {
			if j > 0 {
				out.WriteByte(',')
			}
			out.WriteString(modelDataQuote(id))
			out.WriteByte(':')
			out.WriteString(modelDataQuote(structure[provider][id]))
		}
		out.WriteByte('}')
	}
	out.WriteByte('}')
	return modelDataSHA256(out.String())
}

func CreateModelDataManifest(structure ModelDataStructure, fileContents map[string]string, generatedAt string) ModelDataManifest {
	files := make(map[string]string, len(fileContents))
	for file, content := range fileContents {
		files[file] = modelDataSHA256(content)
	}
	return ModelDataManifest{ModelDataSchemaVersion, generatedAt, ModelDataStructureHash(structure), files}
}

func modelDataFiles(dir, suffix, exclude string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), suffix) && entry.Name() != exclude {
			out = append(out, entry.Name())
		}
	}
	sortModelStrings(out)
	return out, nil
}

func ValidateGeneratedModelData(packageRoot string) error {
	structure, err := ReadModelDataStructure(packageRoot)
	if err != nil {
		return err
	}
	return ValidateModelDataDirectory(structure, filepath.Join(packageRoot, "src", "providers", "data"))
}

func ValidateModelDataDirectory(structure ModelDataStructure, dataDir string) error {
	info, err := os.Stat(dataDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("Generated model data directory does not exist: %s", dataDir)
	}
	var errs []string
	expected := make([]string, 0, len(structure))
	for provider := range structure {
		expected = append(expected, provider+".json")
	}
	sortModelStrings(expected)
	actual, err := modelDataFiles(dataDir, ".json", ModelDataManifestFile)
	if err != nil {
		return err
	}
	if !slices.Equal(expected, actual) {
		errs = append(errs, "provider data files do not match the generated catalog ("+describeSetDifference(expected, actual)+")")
	}
	manifest := readModelDataObject(filepath.Join(dataDir, ModelDataManifestFile), "model data manifest", &errs)
	if !modelDataNumberEquals(manifest.get("schemaVersion"), ModelDataSchemaVersion) {
		errs = append(errs, fmt.Sprintf("model data schema is %s, expected %d", modelDataValue(manifest.get("schemaVersion")), ModelDataSchemaVersion))
	}
	if valid, err := modelDataTimestampValid(manifest.get("generatedAt")); err != nil {
		return err
	} else if !valid {
		errs = append(errs, "model data manifest has an invalid generation timestamp")
	}
	if manifest.string("structureHash") != ModelDataStructureHash(structure) {
		errs = append(errs, "model data generation stamp does not match the generated catalog")
	}
	files := parseModelDataObject(manifest.get("files"))
	if files == nil {
		errs = append(errs, "model data manifest has no file hashes")
	} else {
		names := slices.Clone(files.keys)
		sortModelStrings(names)
		if !slices.Equal(expected, names) {
			errs = append(errs, "manifest file hashes do not match provider data files ("+describeSetDifference(expected, names)+")")
		}
	}
	for _, provider := range sortedModelObjectKeys(structure) {
		filename := provider + ".json"
		path := filepath.Join(dataDir, filename)
		content, readErr := os.ReadFile(path)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		if files != nil && files.string(filename) != modelDataSHA256(string(content)) {
			errs = append(errs, filename+" does not match its manifest hash")
		}
		groups := readModelDataObject(path, filename, &errs)
		if groups == nil {
			continue
		}
		actualModels := map[string]string{}
		for _, api := range groups.keys {
			group := parseModelDataObject(groups.get(api))
			if group == nil {
				errs = append(errs, filename+" API group "+modelDataQuote(api)+" must be an object")
				continue
			}
			for _, id := range group.keys {
				if _, exists := actualModels[id]; exists {
					errs = append(errs, provider+"/"+id+" appears in more than one API group")
					continue
				}
				actualModels[id] = api
				validateModelValue(group.get(id), provider, id, api, &errs)
			}
		}
		wantIDs, actualIDs := slices.Collect(maps.Keys(structure[provider])), slices.Collect(maps.Keys(actualModels))
		sortModelStrings(wantIDs)
		sortModelStrings(actualIDs)
		if AssertExactModelIds(filename, wantIDs, actualIDs) != nil {
			errs = append(errs, filename+" model IDs do not match the generated catalog ("+describeSetDifference(wantIDs, actualIDs)+")")
		}
		for _, id := range sortedModelObjectKeys(structure[provider]) {
			if api, exists := actualModels[id]; exists && api != structure[provider][id] {
				errs = append(errs, fmt.Sprintf("%s/%s is grouped under API %s, expected %s", provider, id, modelDataQuote(api), modelDataQuote(structure[provider][id])))
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	visible := errs[:min(30, len(errs))]
	message := "Invalid generated model data:\n  - " + strings.Join(visible, "\n  - ")
	if len(errs) > len(visible) {
		message += fmt.Sprintf("\n  ... and %d more", len(errs)-len(visible))
	}
	return errors.New(message)
}

func modelDataFileError(err error) error {
	var pathError *os.PathError
	if !errors.As(err, &pathError) {
		return err
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("ENOENT: no such file or directory, open '%s'", pathError.Path)
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("EACCES: permission denied, open '%s'", pathError.Path)
	case errors.Is(err, syscall.EISDIR):
		return errors.New("EISDIR: illegal operation on a directory, read")
	default:
		return err
	}
}

func readModelDataObject(path, description string, errs *[]string) *modelDataObject {
	data, err := os.ReadFile(path)
	if err == nil && !json.Valid(data) {
		message, nodeErr := runModelDataNode(`const fs = require("node:fs"); try { JSON.parse(fs.readFileSync(0, "utf8")); } catch (error) { process.stdout.write(error.message); }`, data)
		if nodeErr != nil {
			err = nodeErr
		} else {
			err = errors.New(message)
		}
	}
	if err != nil {
		*errs = append(*errs, description+" is not valid JSON: "+modelDataFileError(err).Error())
		return nil
	}
	object := parseModelDataObject(data)
	if object == nil {
		*errs = append(*errs, description+" must contain a JSON object")
	}
	return object
}
