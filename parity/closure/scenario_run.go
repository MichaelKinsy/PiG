package closure

import (
	"encoding/json"
	"fmt"
	"strings"
)

// scenarioRunPrefix is the content-addressed ID namespace for a recorded parity
// scenario run outcome.
const scenarioRunPrefix = "scenario-run:"

// ScenarioOutcomePass and ScenarioOutcomeFail are the only recognized outcomes of
// a canonical parity scenario run.
const (
	ScenarioOutcomePass = "pass"
	ScenarioOutcomeFail = "fail"
)

// ScenarioRun is a canonical, content-addressed record of one parity scenario
// execution. It binds the scenario denominator fact to an attested pass/fail
// outcome under an exact snapshot, comparator, command, and captured artifacts,
// so family and scenario reports can derive coverage from proof rather than
// asserting it. A pass credits the scenario; a fail contradicts it. The record
// carries no unverified narrative and fails Build if its ID is not the hash of
// its own content.
type ScenarioRun struct {
	Kind           Kind               `json:"kind"`
	ID             string             `json:"id"`
	SnapshotID     string             `json:"snapshotId"`
	ScenarioFactID string             `json:"scenarioFactId"`
	Outcome        string             `json:"outcome"`
	Runs           int                `json:"runs"`
	Comparator     string             `json:"comparator"`
	CommandHash    string             `json:"commandHash"`
	Artifacts      []EvidenceArtifact `json:"artifacts"`
}

func (r *ScenarioRun) RecordKind() Kind { return r.Kind }
func (r *ScenarioRun) RecordID() string { return r.ID }

func scenarioRunContentID(run *ScenarioRun) (string, error) {
	clone := *run
	clone.ID = ""
	data, err := json.Marshal(&clone)
	if err != nil {
		return "", err
	}
	return scenarioRunPrefix + strings.TrimPrefix(HashBytes(data), "sha256:"), nil
}

// scenarioOutcome is the derived verdict for one scenario from its recorded runs.
type scenarioOutcome struct {
	state    VerdictState
	lastRun  string
	passRuns int
	failRuns int
}

// deriveScenarioOutcomes reduces the recorded scenario runs to one verdict per
// scenario path. A recorded pass proves the scenario; a recorded fail with no
// pass contradicts it; absent any run it stays open. Outcomes are keyed by the
// scenario denominator fact's subject path so the reports can join them directly.
func deriveScenarioOutcomes(graph *Graph) (map[string]scenarioOutcome, error) {
	pathByFactID := make(map[string]string)
	for _, factID := range graph.recordIDs(KindFact) {
		fact := graph.Records[factID].(*Fact)
		if fact.FactType == "denominator:scenario" {
			pathByFactID[fact.ID] = fact.SubjectID
		}
	}

	outcomes := make(map[string]scenarioOutcome)
	for _, runID := range graph.recordIDs(KindScenarioRun) {
		run := graph.Records[runID].(*ScenarioRun)
		path := pathByFactID[run.ScenarioFactID]
		current := outcomes[path]
		if run.Outcome == ScenarioOutcomePass {
			current.passRuns += run.Runs
		} else {
			current.failRuns += run.Runs
		}
		outcomes[path] = current
	}
	for path, outcome := range outcomes {
		switch {
		case outcome.passRuns > 0:
			outcome.state = VerdictProven
		case outcome.failRuns > 0:
			outcome.state = VerdictContradicted
		default:
			outcome.state = VerdictOpen
		}
		outcome.lastRun = scenarioLastRun(outcome.passRuns, outcome.failRuns)
		outcomes[path] = outcome
	}
	return outcomes, nil
}

func scenarioLastRun(passRuns, failRuns int) string {
	switch {
	case passRuns > 0 && failRuns > 0:
		return fmt.Sprintf("%d pass / %d fail", passRuns, failRuns)
	case passRuns > 0:
		return fmt.Sprintf("%d pass", passRuns)
	case failRuns > 0:
		return fmt.Sprintf("%d fail", failRuns)
	default:
		return "not recorded"
	}
}
