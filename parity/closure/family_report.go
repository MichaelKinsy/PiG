package closure

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	FamilyCoverageDataset  = "family-coverage"
	ScenarioQualityDataset = "scenario-quality"
)

type scenarioQuality string

const (
	scenarioBehavioral       scenarioQuality = "behavioral"
	scenarioBootOnly         scenarioQuality = "boot-only"
	scenarioRegistrationOnly scenarioQuality = "registration-only"
	scenarioSmokeOnly        scenarioQuality = "smoke-only"
	scenarioDeferred         scenarioQuality = "deferred"
)

type familyCoverageRow struct {
	family       string
	scenarios    int
	behavioral   int
	bootOnly     int
	weak         int
	deferred     int
	proven       int
	open         int
	contradicted int
	passRuns     int
	failRuns     int
	covered      map[string]struct{}
}

type scenarioRecord struct {
	Description string   `json:"description"`
	Covers      []string `json:"covers"`
	Tags        []string `json:"tags"`
}

type scenarioQualityRow struct {
	path    string
	family  string
	quality scenarioQuality
	covers  int
	outcome scenarioOutcome
}

func renderFamilyCoverageReport(graph *Graph) ([]byte, error) {
	outcomes, err := deriveScenarioOutcomes(graph)
	if err != nil {
		return nil, err
	}
	families := make(map[string]*familyCoverageRow)
	seenScenarios := make(map[string]struct{})
	for _, recordID := range graph.recordIDs(KindFact) {
		fact := graph.Records[recordID].(*Fact)
		if fact.FactType != "denominator:scenario" {
			continue
		}
		if _, duplicate := seenScenarios[fact.SubjectID]; duplicate {
			return nil, fmt.Errorf("report %s has duplicate scenario %s", FamilyCoverageDataset, fact.SubjectID)
		}
		seenScenarios[fact.SubjectID] = struct{}{}
		family, scenario, err := decodeScenarioFact(fact, FamilyCoverageDataset)
		if err != nil {
			return nil, err
		}
		row := families[family]
		if row == nil {
			row = &familyCoverageRow{family: family, covered: make(map[string]struct{})}
			families[family] = row
		}
		quality := classifyScenario(scenario.Description, scenario.Tags)
		if quality == scenarioDeferred {
			row.deferred++
			continue
		}
		row.scenarios++
		switch quality {
		case scenarioBehavioral:
			row.behavioral++
		case scenarioBootOnly:
			row.bootOnly++
		default:
			row.weak++
		}
		outcome := outcomes[fact.SubjectID]
		switch outcome.state {
		case VerdictProven:
			row.proven++
		case VerdictContradicted:
			row.contradicted++
		default:
			row.open++
		}
		row.passRuns += outcome.passRuns
		row.failRuns += outcome.failRuns
		for _, cover := range scenario.Covers {
			if cover == "" {
				return nil, fmt.Errorf("report %s scenario %s has an empty cover", FamilyCoverageDataset, fact.SubjectID)
			}
			if quality == scenarioBehavioral || quality == scenarioRegistrationOnly && registrationCoveragePath(cover) {
				row.covered[cover] = struct{}{}
			}
		}
	}
	if len(families) == 0 {
		return nil, fmt.Errorf("report %s has no scenarios", FamilyCoverageDataset)
	}
	familyNames := make([]string, 0, len(families))
	for family := range families {
		familyNames = append(familyNames, family)
	}
	slices.Sort(familyNames)
	var output bytes.Buffer
	output.WriteString("| family | scenarios | behavioral | boot-only | weak | deferred | upstream behavioral covered | last run | proven | waived | provisional | open | contradicted | reason |\n")
	output.WriteString("|---|---:|---:|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|---|\n")
	for _, family := range familyNames {
		if err := validateReportField("family", family); err != nil {
			return nil, err
		}
		row := families[family]
		fmt.Fprintf(&output, "| `%s` | %d | %d | %d | %d | %d | %d | %s | %d | 0 | 0 | %d | %d | %s |\n",
			row.family, row.scenarios, row.behavioral, row.bootOnly, row.weak, row.deferred, len(row.covered),
			scenarioLastRun(row.passRuns, row.failRuns), row.proven, row.open, row.contradicted, familyReason(row))
	}
	return output.Bytes(), nil
}

// familyReason states why a family is not fully proven from its recorded runs.
func familyReason(row *familyCoverageRow) string {
	switch {
	case row.contradicted > 0:
		return "recorded scenario run failed"
	case row.open > 0:
		return "no passing scenario run recorded"
	default:
		return ""
	}
}

func renderScenarioQualityReport(graph *Graph) ([]byte, error) {
	outcomes, err := deriveScenarioOutcomes(graph)
	if err != nil {
		return nil, err
	}
	rows := make([]scenarioQualityRow, 0)
	seen := make(map[string]struct{})
	for _, recordID := range graph.recordIDs(KindFact) {
		fact := graph.Records[recordID].(*Fact)
		if fact.FactType != "denominator:scenario" {
			continue
		}
		if _, duplicate := seen[fact.SubjectID]; duplicate {
			return nil, fmt.Errorf("report %s has duplicate scenario %s", ScenarioQualityDataset, fact.SubjectID)
		}
		seen[fact.SubjectID] = struct{}{}
		family, scenario, err := decodeScenarioFact(fact, ScenarioQualityDataset)
		if err != nil {
			return nil, err
		}
		if slices.Contains(scenario.Covers, "") {
			return nil, fmt.Errorf("report %s scenario %s has an empty cover", ScenarioQualityDataset, fact.SubjectID)
		}
		rows = append(rows, scenarioQualityRow{
			path: fact.SubjectID, family: family,
			quality: classifyScenario(scenario.Description, scenario.Tags), covers: len(scenario.Covers),
			outcome: outcomes[fact.SubjectID],
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("report %s has no scenarios", ScenarioQualityDataset)
	}
	slices.SortFunc(rows, func(left, right scenarioQualityRow) int { return cmp.Compare(left.path, right.path) })
	var output bytes.Buffer
	output.WriteString("| scenario | family | quality | covers | proven | waived | provisional | open | contradicted | reason |\n")
	output.WriteString("|---|---|---|---:|---:|---:|---:|---:|---:|---|\n")
	for _, row := range rows {
		proven, open, contradicted := scenarioStateColumns(row.outcome.state)
		fmt.Fprintf(&output, "| `%s` | `%s` | %s | %d | %d | 0 | 0 | %d | %d | %s |\n",
			row.path, row.family, row.quality, row.covers, proven, open, contradicted, scenarioRowReason(row.outcome.state))
	}
	return output.Bytes(), nil
}

func scenarioStateColumns(state VerdictState) (proven, open, contradicted int) {
	switch state {
	case VerdictProven:
		return 1, 0, 0
	case VerdictContradicted:
		return 0, 0, 1
	default:
		return 0, 1, 0
	}
}

func scenarioRowReason(state VerdictState) string {
	switch state {
	case VerdictProven:
		return ""
	case VerdictContradicted:
		return "recorded scenario run failed"
	default:
		return "no passing scenario run recorded"
	}
}

func decodeScenarioFact(fact *Fact, reportDataset string) (string, scenarioRecord, error) {
	family, err := scenarioFamily(fact.SubjectID)
	if err != nil {
		return "", scenarioRecord{}, err
	}
	var scenario scenarioRecord
	if err := json.Unmarshal(fact.Value, &scenario); err != nil {
		return "", scenarioRecord{}, fmt.Errorf("report %s scenario %s is invalid: %w", reportDataset, fact.SubjectID, err)
	}
	if scenario.Description == "" {
		return "", scenarioRecord{}, fmt.Errorf("report %s scenario %s has no description", reportDataset, fact.SubjectID)
	}
	return family, scenario, nil
}

func scenarioFamily(path string) (string, error) {
	if strings.ContainsAny(path, "|\t\r\n") {
		return "", fmt.Errorf("report %s scenario path contains a report delimiter", FamilyCoverageDataset)
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "parity" || parts[1] != "scenarios" || !strings.HasSuffix(parts[len(parts)-1], ".toml") {
		return "", fmt.Errorf("report %s scenario path %s is invalid", FamilyCoverageDataset, path)
	}
	if len(parts) == 3 {
		return "_top", nil
	}
	if parts[2] == "" {
		return "", fmt.Errorf("report %s scenario path %s has an empty family", FamilyCoverageDataset, path)
	}
	return parts[2], nil
}

func classifyScenario(description string, tags []string) scenarioQuality {
	switch {
	case slices.Contains(tags, "deferred"):
		return scenarioDeferred
	case strings.HasPrefix(description, "boot-only:") || slices.Contains(tags, "boot-only"):
		return scenarioBootOnly
	case slices.Contains(tags, "registration-only"):
		return scenarioRegistrationOnly
	case slices.Contains(tags, "smoke-only"):
		return scenarioSmokeOnly
	default:
		return scenarioBehavioral
	}
}

func registrationCoveragePath(path string) bool {
	return slices.Contains([]string{
		"packages/ai/src/api-registry.ts",
		"packages/ai/src/env-api-keys.ts",
		"packages/ai/src/providers/register-builtins.ts",
		"packages/coding-agent/src/core/auth-storage.ts",
		"packages/coding-agent/src/core/resolve-config-value.ts",
	}, path)
}
