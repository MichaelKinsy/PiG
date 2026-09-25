package closure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/coding"
)

type DenominatorImportReport struct {
	Counts            map[string]int
	ProvisionalClaims int
	SourceFiles       []string
}

const maxDenominatorSourceBytes = 512 << 20

func ImportCurrentDenominators(root string, snapshot *Snapshot) ([]Record, DenominatorImportReport, error) {
	if snapshot == nil {
		return nil, DenominatorImportReport{}, fmt.Errorf("import denominator inputs: snapshot is nil")
	}
	if snapshot.RecordKind() != KindSnapshot || snapshot.RecordID() == "" {
		return nil, DenominatorImportReport{}, fmt.Errorf("import denominator inputs: invalid snapshot")
	}
	importer := denominatorImporter{
		root: root, snapshot: snapshot,
		records:   []Record{snapshot},
		report:    DenominatorImportReport{Counts: make(map[string]int)},
		seenFacts: make(map[string]struct{}),
	}
	jsonSources, tomlSources := currentDenominatorSources()
	for _, source := range jsonSources {
		if err := importer.importJSON(source); err != nil {
			return nil, DenominatorImportReport{}, err
		}
	}
	for _, source := range tomlSources {
		if err := importer.importTOML(source); err != nil {
			return nil, DenominatorImportReport{}, err
		}
	}
	if err := importer.importFamilies("parity/families.toml"); err != nil {
		return nil, DenominatorImportReport{}, err
	}
	if err := importer.importScenarios("parity/scenarios"); err != nil {
		return nil, DenominatorImportReport{}, err
	}
	if err := importer.importMarkdownTable("port-map", "PORT_MAP.md", 3, true); err != nil {
		return nil, DenominatorImportReport{}, err
	}
	if err := importer.importMarkdownTable("coverage-row", "parity/coverage.md", 5, false); err != nil {
		return nil, DenominatorImportReport{}, err
	}
	if err := importer.importDivergences("DIVERGENCES.md"); err != nil {
		return nil, DenominatorImportReport{}, err
	}
	if err := validateDenominatorJoins(importer.records); err != nil {
		return nil, DenominatorImportReport{}, err
	}
	slices.Sort(importer.report.SourceFiles)
	return importer.records, importer.report, nil
}

type denominatorImporter struct {
	root      string
	snapshot  *Snapshot
	records   []Record
	report    DenominatorImportReport
	seenFacts map[string]struct{}
}

// currentDenominatorSources lists the generated and reviewed ledgers for the
// current pin that ImportCurrentDenominators accounts for.
func currentDenominatorSources() ([]jsonDenominatorSource, []tomlDenominatorSource) {
	version := coding.UpstreamVersion
	jsonSources := []jsonDenominatorSource{
		{dataset: "semantic-interface", path: "parity/interfaces/upstream-v" + version + ".json", lists: []jsonList{{key: "interfaces", subjectField: "id"}}, metadata: []string{"upstreamVersion", "typescriptVersion", "origin", "packages"}, resolution: "resolved"},
		{dataset: "semantic-mapping", path: "parity/interfaces/mapping-v" + version + ".json", lists: []jsonList{{key: "mappings", subjectField: "id", statusField: "disposition"}}, metadata: []string{"upstreamVersion"}, resolution: "observed"},
		{dataset: "semantic-delta", path: "parity/interfaces/delta-v" + coding.UpstreamReviewedVersion + "-v" + version + ".json", lists: []jsonList{{key: "changes", subjectField: "id", statusField: "disposition"}}, metadata: []string{"from", "to"}, resolution: "resolved"},
		{dataset: "go-interface", path: "parity/interfaces/pig-go.json", lists: []jsonList{{key: "interfaces", subjectField: "id"}}, metadata: []string{"goVersion", "packages"}, resolution: "resolved"},
		{dataset: "interface-recommendation", path: "parity/interfaces/recommendations-v" + version + ".json", lists: []jsonList{{key: "recommendations", subjectField: "id", hypothesis: true}}, metadata: []string{"upstreamVersion", "generatedBy"}, resolution: "observed"},
		{dataset: "cli-interface", path: "parity/interfaces/cli-v" + version + ".json", lists: []jsonList{{key: "interfaces", subjectField: "id"}}, metadata: []string{"upstreamVersion", "kind"}, resolution: "resolved"},
		{dataset: "behavior-input", path: "parity/interfaces/behavior-inputs-v" + version + ".json", lists: []jsonList{{key: "keybindings", subjectField: "id"}, {key: "handlers", subjectField: "id"}, {key: "renderers", subjectField: "id"}}, metadata: []string{"upstreamVersion"}, resolution: "resolved"},
		{dataset: "behavior-input-mapping", path: "parity/interfaces/behavior-input-mapping-v" + version + ".json", lists: []jsonList{{key: "mappings", subjectField: "id", statusField: "disposition"}, {key: "renderMappings", subjectField: "id", statusField: "disposition"}}, metadata: []string{"upstreamVersion"}, resolution: "observed"},
	}
	tomlSources := []tomlDenominatorSource{
		{dataset: "async-contract", path: "parity/async-contracts.toml", listKey: "files", subjectField: "path", statusField: "disposition", metadata: []string{"version"}, resolution: "observed"},
		{dataset: "upstream-sync", path: "parity/upstream-sync/v" + version + ".toml", listKey: "files", subjectField: "path", statusField: "disposition", metadata: []string{"from", "to"}, resolution: "observed"},
		{dataset: "behavior-contract", path: "parity/behavior-contracts.toml", listKey: "contract", subjectField: "id", statusField: "status", metadata: []string{"upstream_version"}, resolution: "observed"},
		{dataset: "format-version", path: "parity/format-versions.toml", listKey: "fields", subjectField: "id", resolution: "resolved"},
	}
	return jsonSources, tomlSources
}

type jsonDenominatorSource struct {
	dataset    string
	path       string
	lists      []jsonList
	metadata   []string
	resolution string
}

type jsonList struct {
	key          string
	subjectField string
	statusField  string
	hypothesis   bool
}

type tomlDenominatorSource struct {
	dataset      string
	path         string
	listKey      string
	subjectField string
	statusField  string
	metadata     []string
	resolution   string
}

func (i *denominatorImporter) importJSON(source jsonDenominatorSource) error {
	data, pinID, err := i.readSource(source.dataset, source.path)
	if err != nil {
		return err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("import %s: decode %s: %w", source.dataset, source.path, err)
	}
	allowed := make(map[string]struct{}, len(source.metadata)+len(source.lists))
	metadata := make(map[string]json.RawMessage, len(source.metadata))
	for _, key := range source.metadata {
		allowed[key] = struct{}{}
		value, exists := top[key]
		if !exists {
			return fmt.Errorf("import %s: %s missing metadata %s", source.dataset, source.path, key)
		}
		metadata[key] = value
	}
	for _, list := range source.lists {
		allowed[list.key] = struct{}{}
		var rows []json.RawMessage
		if err := json.Unmarshal(top[list.key], &rows); err != nil {
			return fmt.Errorf("import %s: decode %s.%s: %w", source.dataset, source.path, list.key, err)
		}
		subjects := make([]string, 0, len(rows))
		for _, row := range rows {
			var identity map[string]json.RawMessage
			if err := json.Unmarshal(row, &identity); err != nil {
				return fmt.Errorf("import %s: decode row: %w", source.dataset, err)
			}
			subject, err := rawString(identity[list.subjectField])
			if err != nil || subject == "" {
				return fmt.Errorf("import %s: row has invalid %s", source.dataset, list.subjectField)
			}
			subjects = append(subjects, subject)
			if list.hypothesis {
				i.addHypothesis(source.dataset, subject, pinID, row)
			} else {
				if err := i.addFact(source.dataset, subject, pinID, source.resolution, row); err != nil {
					return err
				}
			}
			if list.statusField != "" {
				status, _ := rawString(identity[list.statusField])
				i.addProvisional(source.dataset, subject, pinID, status)
			}
		}
		if source.dataset == SemanticMappingDataset ||
			source.dataset == SemanticDeltaDataset ||
			source.dataset == InputRenderMappingDataset {
			if err := i.addRowOrderFact(source.dataset, list.key, pinID, subjects); err != nil {
				return err
			}
		}
	}
	for key := range top {
		if _, exists := allowed[key]; !exists {
			return fmt.Errorf("import %s: %s has unknown top-level field %s", source.dataset, source.path, key)
		}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("import %s metadata: %w", source.dataset, err)
	}
	return i.addMetadataFact(source.dataset, pinID, metadataJSON)
}

func (i *denominatorImporter) importTOML(source tomlDenominatorSource) error {
	data, pinID, err := i.readSource(source.dataset, source.path)
	if err != nil {
		return err
	}
	var top map[string]any
	if _, err := toml.Decode(string(data), &top); err != nil {
		return fmt.Errorf("import %s: decode %s: %w", source.dataset, source.path, err)
	}
	allowed := make(map[string]struct{}, len(source.metadata)+1)
	metadata := make(map[string]any, len(source.metadata))
	for _, key := range source.metadata {
		allowed[key] = struct{}{}
		value, exists := top[key]
		if !exists {
			return fmt.Errorf("import %s: %s missing metadata %s", source.dataset, source.path, key)
		}
		metadata[key] = value
	}
	allowed[source.listKey] = struct{}{}
	rows, ok := top[source.listKey].([]map[string]any)
	if !ok {
		return fmt.Errorf("import %s: %s.%s is not a table array", source.dataset, source.path, source.listKey)
	}
	subjects := make([]string, 0, len(rows))
	for _, row := range rows {
		subject, _ := row[source.subjectField].(string)
		if subject == "" {
			return fmt.Errorf("import %s: row has invalid %s", source.dataset, source.subjectField)
		}
		subjects = append(subjects, subject)
		value, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("import %s row %s: %w", source.dataset, subject, err)
		}
		if err := i.addFact(source.dataset, subject, pinID, source.resolution, value); err != nil {
			return err
		}
		status, _ := row[source.statusField].(string)
		i.addProvisional(source.dataset, subject, pinID, status)
	}
	if source.dataset == AsyncContractDataset || source.dataset == ChangedSourceDataset || source.dataset == BehaviorContractDataset || source.dataset == FormatOwnershipDataset {
		if err := i.addRowOrderFact(source.dataset, source.listKey, pinID, subjects); err != nil {
			return err
		}
	}
	for key := range top {
		if _, exists := allowed[key]; !exists {
			return fmt.Errorf("import %s: %s has unknown top-level field %s", source.dataset, source.path, key)
		}
	}
	if len(metadata) > 0 {
		value, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("import %s metadata: %w", source.dataset, err)
		}
		return i.addMetadataFact(source.dataset, pinID, value)
	}
	return nil
}

func (i *denominatorImporter) importFamilies(path string) error {
	data, pinID, err := i.readSource("family", path)
	if err != nil {
		return err
	}
	var document struct {
		Families map[string]map[string]any `toml:"families"`
	}
	metadata, err := toml.Decode(string(data), &document)
	if err != nil {
		return fmt.Errorf("import family: decode %s: %w", path, err)
	}
	if len(metadata.Undecoded()) > 0 {
		return fmt.Errorf("import family: %s has undecoded keys", path)
	}
	names := make([]string, 0, len(document.Families))
	for name := range document.Families {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		value, err := json.Marshal(document.Families[name])
		if err != nil {
			return fmt.Errorf("import family %s: %w", name, err)
		}
		if err := i.addFact("family", name, pinID, "observed", value); err != nil {
			return err
		}
	}
	return nil
}

func (i *denominatorImporter) importScenarios(directory string) error {
	root := filepath.Join(i.root, directory)
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".toml" {
			return nil
		}
		relative, err := filepath.Rel(i.root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return fmt.Errorf("import scenarios: %w", err)
	}
	slices.Sort(paths)
	for _, path := range paths {
		data, pinID, err := i.readSource("scenario", path)
		if err != nil {
			return err
		}
		var document map[string]any
		if _, err := toml.Decode(string(data), &document); err != nil {
			return fmt.Errorf("import scenario %s: %w", path, err)
		}
		value, err := json.Marshal(document)
		if err != nil {
			return fmt.Errorf("import scenario %s: %w", path, err)
		}
		if err := i.addFact("scenario", path, pinID, "observed", value); err != nil {
			return err
		}
	}
	return nil
}

func (i *denominatorImporter) importMarkdownTable(dataset, path string, fieldCount int, provisional bool) error {
	data, pinID, err := i.readSource(dataset, path)
	if err != nil {
		return err
	}
	subjects := make([]string, 0)
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "| `packages/") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < fieldCount+2 {
			return fmt.Errorf("import %s: malformed row %q", dataset, line)
		}
		subject := strings.Trim(strings.TrimSpace(fields[1]), "`")
		subjects = append(subjects, subject)
		value, err := json.Marshal(map[string]any{"fields": trimTableFields(fields[1 : fieldCount+1])})
		if err != nil {
			return fmt.Errorf("import %s row %s: %w", dataset, subject, err)
		}
		if err := i.addFact(dataset, subject, pinID, "observed", value); err != nil {
			return err
		}
		if provisional {
			status := strings.TrimSpace(fields[3])
			i.addProvisional(dataset, subject, pinID, status)
		}
	}
	return i.addRowOrderFact(dataset, "rows", pinID, subjects)
}

func (i *denominatorImporter) importDivergences(path string) error {
	data, pinID, err := i.readSource("divergence", path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for index, line := range lines {
		if !strings.HasPrefix(line, "## D") {
			continue
		}
		identifier, _, _ := strings.Cut(strings.TrimPrefix(line, "## "), " ")
		if len(identifier) < 2 || identifier[0] != 'D' || identifier[1] < '0' || identifier[1] > '9' {
			continue
		}
		end := len(lines)
		for next := index + 1; next < len(lines); next++ {
			if strings.HasPrefix(lines[next], "## D") {
				end = next
				break
			}
		}
		section := strings.Join(lines[index:end], "\n")
		value, err := json.Marshal(map[string]string{"section": section})
		if err != nil {
			return fmt.Errorf("import divergence %s: %w", identifier, err)
		}
		if err := i.addFact("divergence", identifier, pinID, "observed", value); err != nil {
			return err
		}
		status := ""
		if strings.Contains(section, "SCRUTINIZED:approved") {
			status = "approved"
		}
		i.addProvisional("divergence", identifier, pinID, status)
	}
	return nil
}

func (i *denominatorImporter) readSource(dataset, path string) ([]byte, string, error) {
	rootAbsolute, err := filepath.Abs(i.root)
	if err != nil {
		return nil, "", fmt.Errorf("import %s: resolve root: %w", dataset, err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return nil, "", fmt.Errorf("import %s: resolve root: %w", dataset, err)
	}
	fullPath, err := filepath.EvalSymlinks(filepath.Join(rootReal, filepath.FromSlash(path)))
	if err != nil {
		return nil, "", fmt.Errorf("import %s: resolve %s: %w", dataset, path, err)
	}
	relative, err := filepath.Rel(rootReal, fullPath)
	if err != nil {
		return nil, "", fmt.Errorf("import %s: resolve %s: %w", dataset, path, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("import %s: %s symlink escapes repository root", dataset, path)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, "", fmt.Errorf("import %s: stat %s: %w", dataset, path, err)
	}
	if info.Size() > maxDenominatorSourceBytes {
		return nil, "", fmt.Errorf("import %s: %s exceeds %d bytes", dataset, path, maxDenominatorSourceBytes)
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, "", fmt.Errorf("import %s: read %s: %w", dataset, path, err)
	}
	lineCount := bytes.Count(data, []byte{'\n'}) + 1
	pinID := "pin:denominator:" + idDigest(path)
	if _, exists := i.seenFacts[pinID]; !exists {
		i.seenFacts[pinID] = struct{}{}
		i.records = append(i.records, &Pin{
			Kind: KindPin, ID: pinID, SnapshotID: i.snapshot.ID, Repository: "pig", Commit: i.snapshot.TargetCommit,
			Path: path, SemanticID: "denominator:" + dataset, StartLine: 1, EndLine: lineCount, QuoteHash: HashBytes(data),
		})
		i.report.SourceFiles = append(i.report.SourceFiles, path)
	}
	return data, pinID, nil
}

func (i *denominatorImporter) addFact(dataset, subject, pinID, resolution string, value json.RawMessage) error {
	id := "fact:denominator:" + dataset + ":" + idDigest(subject)
	if _, duplicate := i.seenFacts[id]; duplicate {
		return fmt.Errorf("import %s: duplicate subject %s", dataset, subject)
	}
	i.seenFacts[id] = struct{}{}
	compact, err := compactJSON(value)
	if err != nil {
		return fmt.Errorf("import %s subject %s: %w", dataset, subject, err)
	}
	i.records = append(i.records, &Fact{
		Kind: KindFact, ID: id, SnapshotID: i.snapshot.ID, FactType: "denominator:" + dataset,
		SubjectID: subject, Resolution: resolution, PinIDs: []string{pinID}, Value: compact,
	})
	i.report.Counts[dataset]++
	return nil
}

func (i *denominatorImporter) addMetadataFact(dataset, pinID string, value json.RawMessage) error {
	if err := i.addFact(dataset, "<metadata>", pinID, "resolved", value); err != nil {
		return err
	}
	i.report.Counts[dataset]--
	return nil
}

func (i *denominatorImporter) addRowOrderFact(dataset, listKey, pinID string, subjects []string) error {
	value, err := json.Marshal(map[string]any{"subjects": subjects})
	if err != nil {
		return fmt.Errorf("import %s order: %w", dataset, err)
	}
	id := "fact:denominator-order:" + dataset + ":" + listKey
	if _, duplicate := i.seenFacts[id]; duplicate {
		return fmt.Errorf("import %s: duplicate order", dataset)
	}
	i.seenFacts[id] = struct{}{}
	i.records = append(i.records, &Fact{
		Kind: KindFact, ID: id, SnapshotID: i.snapshot.ID, FactType: "denominator-order:" + dataset,
		SubjectID: listKey, Resolution: "resolved", PinIDs: []string{pinID}, Value: value,
	})
	return nil
}

func (i *denominatorImporter) addHypothesis(dataset, subject, pinID string, value json.RawMessage) {
	compact, _ := compactJSON(value)
	i.records = append(i.records, &Hypothesis{
		Kind: KindHypothesis, ID: "hypothesis:denominator:" + dataset + ":" + idDigest(subject), SnapshotID: i.snapshot.ID,
		HypothesisType: dataset, SubjectID: subject, PinIDs: []string{pinID}, Value: compact,
	})
	i.report.Counts[dataset]++
}

func (i *denominatorImporter) addProvisional(dataset, subject, pinID, status string) {
	if status == "" || status == "pending" || status == "⬜" {
		return
	}
	i.records = append(i.records, &ProvisionalClaim{
		Kind: KindProvisionalClaim, ID: "provisional:denominator:" + dataset + ":" + idDigest(subject),
		SnapshotID: i.snapshot.ID, SubjectID: subject, SourcePinID: pinID, Status: status,
	})
	i.report.ProvisionalClaims++
}

func validateDenominatorJoins(records []Record) error {
	sets := make(map[string]map[string]struct{})
	for _, record := range records {
		switch value := record.(type) {
		case *Fact:
			dataset := strings.TrimPrefix(value.FactType, "denominator:")
			if value.SubjectID == "<metadata>" {
				continue
			}
			if sets[dataset] == nil {
				sets[dataset] = make(map[string]struct{})
			}
			sets[dataset][value.SubjectID] = struct{}{}
		case *Hypothesis:
			if sets[value.HypothesisType] == nil {
				sets[value.HypothesisType] = make(map[string]struct{})
			}
			sets[value.HypothesisType][value.SubjectID] = struct{}{}
		}
	}
	semantic := unionSets(sets["semantic-interface"], sets["cli-interface"])
	if err := requireEqualSubjects("semantic inventory", semantic, "semantic mapping", sets["semantic-mapping"]); err != nil {
		return err
	}
	if err := requireEqualSubjects("semantic mapping", sets["semantic-mapping"], "recommendations", sets["interface-recommendation"]); err != nil {
		return err
	}
	behavior := unionSets(sets["behavior-input"])
	deleteBehaviorKeybindings(records, behavior)
	if err := requireEqualSubjects("input/render inventory", behavior, "input/render mapping", sets["behavior-input-mapping"]); err != nil {
		return err
	}
	if err := requireEqualSubjects("PORT_MAP", sets["port-map"], "coverage", sets["coverage-row"]); err != nil {
		return err
	}
	return nil
}

func deleteBehaviorKeybindings(records []Record, subjects map[string]struct{}) {
	for _, record := range records {
		fact, ok := record.(*Fact)
		if !ok || fact.FactType != "denominator:behavior-input" || fact.SubjectID == "<metadata>" {
			continue
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(fact.Value, &value) == nil {
			if _, hasDefaults := value["defaults"]; hasDefaults {
				delete(subjects, fact.SubjectID)
			}
		}
	}
}

func requireEqualSubjects(leftName string, left map[string]struct{}, rightName string, right map[string]struct{}) error {
	for subject := range left {
		if _, exists := right[subject]; !exists {
			return fmt.Errorf("denominator join: %s subject %q missing from %s", leftName, subject, rightName)
		}
	}
	for subject := range right {
		if _, exists := left[subject]; !exists {
			return fmt.Errorf("denominator join: %s subject %q missing from %s", rightName, subject, leftName)
		}
	}
	return nil
}

func unionSets(inputs ...map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{})
	for _, input := range inputs {
		for value := range input {
			result[value] = struct{}{}
		}
	}
	return result
}

func compactJSON(value []byte) ([]byte, error) {
	var output bytes.Buffer
	if err := json.Compact(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func rawString(value json.RawMessage) (string, error) {
	var result string
	if len(value) == 0 {
		return "", fmt.Errorf("missing string")
	}
	if err := json.Unmarshal(value, &result); err != nil {
		return "", err
	}
	return result, nil
}

func idDigest(value string) string {
	return strings.TrimPrefix(HashBytes([]byte(value)), "sha256:")
}

func trimTableFields(fields []string) []string {
	result := make([]string, len(fields))
	for index, field := range fields {
		result[index] = strings.TrimSpace(field)
	}
	return result
}
