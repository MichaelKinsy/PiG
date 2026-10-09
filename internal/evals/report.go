package evals

// Ports packages/evals/src/report.ts.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/term"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// PiSessionSnapshotArtifact is the harness artifact that carries the Session JSONL snapshot.
const PiSessionSnapshotArtifact = "piSessionJsonl"

// EvalRunIdentity identifies one run of a case.
type EvalRunIdentity struct {
	EvalSet   string               `json:"evalSet"`
	CaseID    string               `json:"caseId"`
	Variant   DocumentationVariant `json:"variant"`
	Model     string               `json:"model"`
	RunNumber int                  `json:"runNumber"`
}

// ExpectedEvalRun is a run the plan expects.
type ExpectedEvalRun = EvalRunIdentity

// EvalMetrics are a run's measured costs. A nil metric was not measured, which is distinct from zero.
type EvalMetrics struct {
	InputTokens      *float64 `json:"inputTokens,omitempty"`
	OutputTokens     *float64 `json:"outputTokens,omitempty"`
	CacheReadTokens  *float64 `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens *float64 `json:"cacheWriteTokens,omitempty"`
	TotalTokens      *float64 `json:"totalTokens,omitempty"`
	ToolCalls        *float64 `json:"toolCalls,omitempty"`
	TotalMs          *float64 `json:"totalMs,omitempty"`
	EstimatedCostUsd *float64 `json:"estimatedCostUsd,omitempty"`
}

// EvalOutcome classifies a run.
type EvalOutcome string

const (
	EvalOutcomeScored   EvalOutcome = "scored"
	EvalOutcomeUnscored EvalOutcome = "unscored"
	EvalOutcomeSkipped  EvalOutcome = "skipped"
	EvalOutcomePending  EvalOutcome = "pending"
	EvalOutcomeErrored  EvalOutcome = "errored"
)

// EvalObservation is one observed run. Score is set exactly when Outcome is EvalOutcomeScored.
type EvalObservation struct {
	EvalRunIdentity
	EvalMetrics
	Outcome EvalOutcome `json:"outcome"`
	Score   *float64    `json:"score,omitempty"`
}

// PairedMetricSummary compares a metric over the pairs that measured it in both variants.
type PairedMetricSummary struct {
	EligiblePairs int      `json:"eligiblePairs"`
	ControlMean   *float64 `json:"controlMean"`
	TreatmentMean *float64 `json:"treatmentMean"`
	MeanDelta     *float64 `json:"meanDelta"`
}

// EvalComparisonFlag marks a notable comparison result.
type EvalComparisonFlag string

const (
	EvalComparisonFlagNoLift             EvalComparisonFlag = "no-lift"
	EvalComparisonFlagNegativeDelta      EvalComparisonFlag = "negative-delta"
	EvalComparisonFlagControlSaturated   EvalComparisonFlag = "control-saturated"
	EvalComparisonFlagTreatmentSaturated EvalComparisonFlag = "treatment-saturated"
	EvalComparisonFlagFlaky              EvalComparisonFlag = "flaky"
)

// EvalSetComparison compares the variants over one eval set. Pass rates and lift are nil unless every pair is
// eligible.
type EvalSetComparison struct {
	EvalSet           string               `json:"evalSet"`
	TotalPairs        int                  `json:"totalPairs"`
	EligiblePairs     int                  `json:"eligiblePairs"`
	BlockedPairs      int                  `json:"blockedPairs"`
	ControlPassRate   *float64             `json:"controlPassRate"`
	TreatmentPassRate *float64             `json:"treatmentPassRate"`
	Lift              *float64             `json:"lift"`
	Flags             []EvalComparisonFlag `json:"flags"`
	TotalTokens       PairedMetricSummary  `json:"totalTokens"`
	ToolCalls         PairedMetricSummary  `json:"toolCalls"`
	TotalMs           PairedMetricSummary  `json:"totalMs"`
	EstimatedCostUsd  PairedMetricSummary  `json:"estimatedCostUsd"`
}

// BlockedPair is a pair excluded from comparison, with the reasons.
type BlockedPair struct {
	EvalSet   string   `json:"evalSet"`
	CaseID    string   `json:"caseId"`
	Model     string   `json:"model"`
	RunNumber int      `json:"runNumber"`
	Reasons   []string `json:"reasons"`
}

// OperationalMetricTotal sums a metric over the runs that measured it. Total is nil when no run did.
type OperationalMetricTotal struct {
	AvailableRuns int      `json:"availableRuns"`
	Total         *float64 `json:"total"`
}

// VariantTotals are one variant's operational totals over every observed run.
type VariantTotals struct {
	Variant          DocumentationVariant   `json:"variant"`
	Runs             int                    `json:"runs"`
	InputTokens      OperationalMetricTotal `json:"inputTokens"`
	OutputTokens     OperationalMetricTotal `json:"outputTokens"`
	CacheReadTokens  OperationalMetricTotal `json:"cacheReadTokens"`
	CacheWriteTokens OperationalMetricTotal `json:"cacheWriteTokens"`
	TotalTokens      OperationalMetricTotal `json:"totalTokens"`
	ToolCalls        OperationalMetricTotal `json:"toolCalls"`
	TotalMs          OperationalMetricTotal `json:"totalMs"`
	EstimatedCostUsd OperationalMetricTotal `json:"estimatedCostUsd"`
}

// EvalComparisonReport is the paired comparison of the documentation variants.
type EvalComparisonReport struct {
	SchemaVersion     int                  `json:"schemaVersion"`
	ProtocolDigest    string               `json:"protocolDigest"`
	Control           DocumentationVariant `json:"control"`
	Treatment         DocumentationVariant `json:"treatment"`
	Comparisons       []EvalSetComparison  `json:"comparisons"`
	BlockedPairs      []BlockedPair        `json:"blockedPairs"`
	OperationalTotals []VariantTotals      `json:"operationalTotals"`
}

// evalReportSchemaVersion is the EvalComparisonReport schema version.
const evalReportSchemaVersion = 3

func optionalMetric(value json.RawMessage, present bool, name string) (*float64, error) {
	if !present {
		return nil, nil
	}
	var number float64
	if isNull(value) || json.Unmarshal(value, &number) != nil || number < 0 {
		return nil, fmt.Errorf("%s must be a finite non-negative number.", name)
	}
	return &number, nil
}

func numberMetric(value *float64, name string) (*float64, error) {
	if value == nil {
		return nil, nil
	}
	if *value < 0 {
		return nil, fmt.Errorf("%s must be a finite non-negative number.", name)
	}
	return new(*value), nil
}

func validateScore(value *float64) (*float64, error) {
	if value == nil {
		return nil, nil
	}
	if *value < 0 || *value > 1 {
		return nil, fmt.Errorf("Eval score must be between 0 and 1.")
	}
	return new(*value), nil
}

// ClassifyCaseStatus maps a failed case to errored, a skipped, to-do or disabled case to skipped, and a pending case to
// pending. It returns "" for any other status.
func ClassifyCaseStatus(status ReportCaseStatus) EvalOutcome {
	switch status {
	case ReportCaseStatusFailed:
		return EvalOutcomeErrored
	case ReportCaseStatusSkipped, ReportCaseStatusTodo, ReportCaseStatusDisabled:
		return EvalOutcomeSkipped
	case ReportCaseStatusPending:
		return EvalOutcomePending
	}
	return ""
}

func taskIdentity(task EvalTask) EvalRunIdentity {
	return EvalRunIdentity{EvalSet: task.EvalSet, CaseID: task.CaseID, Variant: task.Variant, Model: task.Model, RunNumber: task.RunNumber}
}

// ErroredObservation is the observation of a task that produced no report.
func ErroredObservation(task EvalTask) EvalObservation {
	return EvalObservation{EvalRunIdentity: taskIdentity(task), Outcome: EvalOutcomeErrored}
}

// taskIdentityDigest is the SHA-256 of JSON.stringify([evalSet, caseId, variant, model, runNumber]), which names a
// task's artifact directories.
func taskIdentityDigest(task EvalTask) ([sha256.Size]byte, error) {
	identity, err := json.Marshal([]any{task.EvalSet, task.CaseID, task.Variant, task.Model, task.RunNumber})
	if err == nil {
		identity, err = jsonstringify.Canonicalize(identity)
	}
	return sha256.Sum256(identity), err
}

func persistSession(run *harnessRunResult, task EvalTask, artifactDirectory string) error {
	var session string
	if raw, ok := run.Artifacts[PiSessionSnapshotArtifact]; !ok || isNull(raw) || json.Unmarshal(raw, &session) != nil {
		return nil
	}
	digest, err := taskIdentityDigest(task)
	if err != nil {
		return err
	}
	directory := filepath.Join(artifactDirectory, string(task.Variant), "sessions", hex.EncodeToString(digest[:]))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "session.jsonl"), []byte(session), 0o600)
}

// ReadTaskObservation reads the single-case Vitest report of task, persists its Session snapshot under
// artifactDirectory, and classifies the run. A report that cannot be read, holds another case, or reports another
// model is an errored observation; only a failure to persist the snapshot is returned as an error.
func ReadTaskObservation(task EvalTask, reportPath, artifactDirectory string) (EvalObservation, error) {
	identity := taskIdentity(task)
	errored := EvalObservation{EvalRunIdentity: identity, Outcome: EvalOutcomeErrored}
	report, err := readVitestJSONReportFile(reportPath)
	if err != nil {
		return errored, nil
	}
	workspace := collectReportCases(report)
	reportedFullName := task.EvalSet + " " + task.CaseID
	var assertions []vitestAssertion
	for _, file := range report.TestResults {
		assertions = append(assertions, file.AssertionResults...)
	}
	if len(assertions) != 1 {
		return errored, nil
	}
	assertion := assertions[0]
	if assertion.FullName != reportedFullName {
		return errored, nil
	}
	statusOutcome := ClassifyCaseStatus(assertion.Status)
	if statusOutcome == EvalOutcomeSkipped || statusOutcome == EvalOutcomePending {
		return EvalObservation{EvalRunIdentity: identity, Outcome: statusOutcome}, nil
	}
	if len(workspace) != 1 {
		return errored, nil
	}
	caseResult := workspace[0]
	if caseResult.FullName != reportedFullName || caseResult.Status != assertion.Status {
		return errored, nil
	}
	run := caseResult.Run
	if run == nil {
		return errored, nil
	}
	if err := persistSession(run, task, artifactDirectory); err != nil {
		return EvalObservation{}, err
	}
	if run.Usage.Provider == nil || *run.Usage.Provider == "" || run.Usage.Model == nil || *run.Usage.Model == "" ||
		*run.Usage.Provider+"/"+*run.Usage.Model != task.Model {
		return errored, nil
	}
	metrics, err := runMetrics(run)
	if err != nil {
		return errored, nil
	}
	if statusOutcome == EvalOutcomeErrored || len(run.Errors) > 0 {
		return EvalObservation{EvalRunIdentity: identity, EvalMetrics: metrics, Outcome: EvalOutcomeErrored}, nil
	}
	var avgScore *float64
	if caseResult.Eval != nil {
		avgScore = caseResult.Eval.AvgScore
	}
	score, err := validateScore(avgScore)
	if err != nil {
		return EvalObservation{EvalRunIdentity: identity, EvalMetrics: metrics, Outcome: EvalOutcomeErrored}, nil
	}
	if score == nil {
		return EvalObservation{EvalRunIdentity: identity, EvalMetrics: metrics, Outcome: EvalOutcomeUnscored}, nil
	}
	return EvalObservation{EvalRunIdentity: identity, EvalMetrics: metrics, Outcome: EvalOutcomeScored, Score: score}, nil
}

func runMetrics(run *harnessRunResult) (metrics EvalMetrics, err error) {
	metadata := func(name string) (*float64, error) {
		value, ok := run.Usage.Metadata[name]
		return optionalMetric(value, ok, name)
	}
	var totalMs *float64
	if run.Timings != nil {
		totalMs = run.Timings.TotalMs
	}
	for _, metric := range []struct {
		target **float64
		read   func() (*float64, error)
	}{
		{&metrics.InputTokens, func() (*float64, error) { return numberMetric(run.Usage.InputTokens, "inputTokens") }},
		{&metrics.OutputTokens, func() (*float64, error) { return numberMetric(run.Usage.OutputTokens, "outputTokens") }},
		{&metrics.CacheReadTokens, func() (*float64, error) { return metadata("cacheReadTokens") }},
		{&metrics.CacheWriteTokens, func() (*float64, error) { return metadata("cacheWriteTokens") }},
		{&metrics.TotalTokens, func() (*float64, error) { return numberMetric(run.Usage.TotalTokens, "totalTokens") }},
		{&metrics.ToolCalls, func() (*float64, error) { return numberMetric(run.Usage.ToolCalls, "toolCalls") }},
		{&metrics.TotalMs, func() (*float64, error) { return numberMetric(totalMs, "totalMs") }},
		{&metrics.EstimatedCostUsd, func() (*float64, error) { return metadata("estimatedCostUsd") }},
	} {
		if *metric.target, err = metric.read(); err != nil {
			return EvalMetrics{}, err
		}
	}
	return metrics, nil
}

const (
	control   = DocumentationVariantWithoutDocs
	treatment = DocumentationVariantWithDocs
)

type pair struct{ control, treatment EvalObservation }

type pairGroup struct {
	evalSet, caseID, model string
	runNumber              int
	expected               map[DocumentationVariant]int
	observations           map[DocumentationVariant][]EvalObservation
}

func mean(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return new(sum / float64(len(values)))
}

// difference is Number((treatment - control).toPrecision(15)).
func difference(treatment, control float64) float64 {
	return jsnumber.Parse(jsstring.ToPrecision(treatment-control, 15))
}

func groupPairs(expectedRuns []ExpectedEvalRun, observations []EvalObservation) []*pairGroup {
	type key struct {
		evalSet, caseID, model string
		runNumber              int
	}
	groups := map[key]*pairGroup{}
	var order []*pairGroup
	getGroup := func(identity EvalRunIdentity) *pairGroup {
		k := key{identity.EvalSet, identity.CaseID, identity.Model, identity.RunNumber}
		if existing := groups[k]; existing != nil {
			return existing
		}
		group := &pairGroup{
			evalSet: identity.EvalSet, caseID: identity.CaseID, model: identity.Model, runNumber: identity.RunNumber,
			expected: map[DocumentationVariant]int{}, observations: map[DocumentationVariant][]EvalObservation{},
		}
		groups[k] = group
		order = append(order, group)
		return group
	}
	for _, expected := range expectedRuns {
		getGroup(expected).expected[expected.Variant]++
	}
	for _, observation := range observations {
		group := getGroup(observation.EvalRunIdentity)
		group.observations[observation.Variant] = append(group.observations[observation.Variant], observation)
	}
	collator := collate.New(language.Und)
	slices.SortStableFunc(order, func(left, right *pairGroup) int {
		if c := collator.CompareString(left.evalSet, right.evalSet); c != 0 {
			return c
		}
		if c := collator.CompareString(left.caseID, right.caseID); c != 0 {
			return c
		}
		if c := collator.CompareString(left.model, right.model); c != 0 {
			return c
		}
		return left.runNumber - right.runNumber
	})
	return order
}

func resolvePair(group *pairGroup) (*pair, *BlockedPair) {
	var reasons []string
	for _, variant := range DocumentationVariants {
		expected := group.expected[variant]
		observed := group.observations[variant]
		if expected != 1 {
			reasons = append(reasons, fmt.Sprintf("%s: design expected 1 run, found %d", variant, expected))
		}
		if len(observed) != expected {
			plural := "s"
			if expected == 1 {
				plural = ""
			}
			reasons = append(reasons, fmt.Sprintf("%s: expected %d observation%s, found %d", variant, expected, plural, len(observed)))
		}
		if expected == 1 && len(observed) == 1 && observed[0].Outcome != EvalOutcomeScored {
			reasons = append(reasons, fmt.Sprintf("%s: %s", variant, observed[0].Outcome))
		}
	}
	if len(reasons) > 0 {
		return nil, &BlockedPair{EvalSet: group.evalSet, CaseID: group.caseID, Model: group.model, RunNumber: group.runNumber, Reasons: reasons}
	}
	return &pair{control: group.observations[control][0], treatment: group.observations[treatment][0]}, nil
}

func summarizeMetric(pairs []pair, selectMetric func(EvalObservation) *float64) PairedMetricSummary {
	var controlValues, treatmentValues []float64
	for _, p := range pairs {
		controlValue, treatmentValue := selectMetric(p.control), selectMetric(p.treatment)
		if controlValue == nil || treatmentValue == nil {
			continue
		}
		controlValues = append(controlValues, *controlValue)
		treatmentValues = append(treatmentValues, *treatmentValue)
	}
	summary := PairedMetricSummary{EligiblePairs: len(controlValues), ControlMean: mean(controlValues), TreatmentMean: mean(treatmentValues)}
	if summary.ControlMean != nil && summary.TreatmentMean != nil {
		summary.MeanDelta = new(difference(*summary.TreatmentMean, *summary.ControlMean))
	}
	return summary
}

func operationalTotal(runs []EvalObservation, selectMetric func(EvalObservation) *float64) OperationalMetricTotal {
	total := OperationalMetricTotal{}
	sum := 0.0
	for _, run := range runs {
		if value := selectMetric(run); value != nil {
			total.AvailableRuns++
			sum += *value
		}
	}
	if total.AvailableRuns > 0 {
		total.Total = &sum
	}
	return total
}

func variantTotals(observations []EvalObservation, variant DocumentationVariant) VariantTotals {
	var runs []EvalObservation
	for _, observation := range observations {
		if observation.Variant == variant {
			runs = append(runs, observation)
		}
	}
	return VariantTotals{
		Variant:          variant,
		Runs:             len(runs),
		InputTokens:      operationalTotal(runs, func(o EvalObservation) *float64 { return o.InputTokens }),
		OutputTokens:     operationalTotal(runs, func(o EvalObservation) *float64 { return o.OutputTokens }),
		CacheReadTokens:  operationalTotal(runs, func(o EvalObservation) *float64 { return o.CacheReadTokens }),
		CacheWriteTokens: operationalTotal(runs, func(o EvalObservation) *float64 { return o.CacheWriteTokens }),
		TotalTokens:      operationalTotal(runs, func(o EvalObservation) *float64 { return o.TotalTokens }),
		ToolCalls:        operationalTotal(runs, func(o EvalObservation) *float64 { return o.ToolCalls }),
		TotalMs:          operationalTotal(runs, func(o EvalObservation) *float64 { return o.TotalMs }),
		EstimatedCostUsd: operationalTotal(runs, func(o EvalObservation) *float64 { return o.EstimatedCostUsd }),
	}
}

// passed is score >= 1; a missing score compares as undefined does, so it has not passed.
func passed(observation EvalObservation) bool {
	return observation.Score != nil && *observation.Score >= 1
}

func comparisonFlags(pairs []pair, controlPassRate, treatmentPassRate *float64) []EvalComparisonFlag {
	flags := []EvalComparisonFlag{}
	if controlPassRate != nil && treatmentPassRate != nil {
		if *controlPassRate == *treatmentPassRate {
			flags = append(flags, EvalComparisonFlagNoLift)
		}
		if *treatmentPassRate < *controlPassRate {
			flags = append(flags, EvalComparisonFlagNegativeDelta)
		}
		if *controlPassRate == 1 {
			flags = append(flags, EvalComparisonFlagControlSaturated)
		}
		if *treatmentPassRate == 1 {
			flags = append(flags, EvalComparisonFlagTreatmentSaturated)
		}
	}
	type key struct {
		caseID  string
		variant DocumentationVariant
	}
	outcomes := map[key]map[bool]bool{}
	record := func(observation EvalObservation) {
		k := key{observation.CaseID, observation.Variant}
		if outcomes[k] == nil {
			outcomes[k] = map[bool]bool{}
		}
		outcomes[k][passed(observation)] = true
	}
	for _, p := range pairs {
		record(p.control)
		record(p.treatment)
	}
	for _, values := range outcomes {
		if len(values) > 1 {
			flags = append(flags, EvalComparisonFlagFlaky)
			break
		}
	}
	return flags
}

func passRate(pairs []pair, observation func(pair) EvalObservation) float64 {
	passing := 0
	for _, p := range pairs {
		if passed(observation(p)) {
			passing++
		}
	}
	return float64(passing) / float64(len(pairs))
}

// SummarizeEvalObservations pairs each case run's control and treatment observations and compares the variants per
// eval set. A pair is blocked unless the design expects one run of each variant and each produced exactly one scored
// observation; an eval set with a blocked pair withholds its pass rates.
func SummarizeEvalObservations(protocolDigest string, expectedRuns []ExpectedEvalRun, observations []EvalObservation) EvalComparisonReport {
	blockedPairs := []BlockedPair{}
	pairsByEvalSet := map[string][]pair{}
	totalsByEvalSet := map[string]int{}
	var evalSets []string
	for _, group := range groupPairs(expectedRuns, observations) {
		if _, seen := totalsByEvalSet[group.evalSet]; !seen {
			evalSets = append(evalSets, group.evalSet)
		}
		totalsByEvalSet[group.evalSet]++
		p, blocked := resolvePair(group)
		if blocked != nil {
			blockedPairs = append(blockedPairs, *blocked)
		}
		if p != nil {
			pairsByEvalSet[group.evalSet] = append(pairsByEvalSet[group.evalSet], *p)
		}
	}
	collator := collate.New(language.Und)
	slices.SortStableFunc(evalSets, collator.CompareString)
	comparisons := []EvalSetComparison{}
	for _, evalSet := range evalSets {
		pairs := pairsByEvalSet[evalSet]
		totalPairs := totalsByEvalSet[evalSet]
		blockedPairCount := totalPairs - len(pairs)
		var controlPassRate, treatmentPassRate, lift *float64
		if blockedPairCount == 0 && len(pairs) > 0 {
			controlPassRate = new(passRate(pairs, func(p pair) EvalObservation { return p.control }))
			treatmentPassRate = new(passRate(pairs, func(p pair) EvalObservation { return p.treatment }))
			lift = new(difference(*treatmentPassRate, *controlPassRate))
		}
		comparisons = append(comparisons, EvalSetComparison{
			EvalSet:           evalSet,
			TotalPairs:        totalPairs,
			EligiblePairs:     len(pairs),
			BlockedPairs:      blockedPairCount,
			ControlPassRate:   controlPassRate,
			TreatmentPassRate: treatmentPassRate,
			Lift:              lift,
			Flags:             comparisonFlags(pairs, controlPassRate, treatmentPassRate),
			TotalTokens:       summarizeMetric(pairs, func(o EvalObservation) *float64 { return o.TotalTokens }),
			ToolCalls:         summarizeMetric(pairs, func(o EvalObservation) *float64 { return o.ToolCalls }),
			TotalMs:           summarizeMetric(pairs, func(o EvalObservation) *float64 { return o.TotalMs }),
			EstimatedCostUsd:  summarizeMetric(pairs, func(o EvalObservation) *float64 { return o.EstimatedCostUsd }),
		})
	}
	return EvalComparisonReport{
		SchemaVersion:     evalReportSchemaVersion,
		ProtocolDigest:    protocolDigest,
		Control:           control,
		Treatment:         treatment,
		Comparisons:       comparisons,
		BlockedPairs:      blockedPairs,
		OperationalTotals: []VariantTotals{variantTotals(observations, control), variantTotals(observations, treatment)},
	}
}

func percentage(value *float64) string {
	if value == nil {
		return "unavailable"
	}
	return jsstring.ToFixed(*value*100, 1) + "%"
}

func signed(value float64, digits int) string {
	if value >= 0 {
		return "+" + jsstring.ToFixed(value, digits)
	}
	return jsstring.ToFixed(value, digits)
}

func padStart(text string, width int) string {
	if pad := width - len(text); pad > 0 {
		return strings.Repeat(" ", pad) + text
	}
	return text
}

func pairedMetric(label string, metric PairedMetricSummary, unit string) string {
	if metric.MeanDelta == nil || metric.ControlMean == nil || metric.TreatmentMean == nil {
		return "    " + padStart(label, 10) + "  unavailable"
	}
	return fmt.Sprintf("    %s  %s%s (with %s%s, without %s%s, %d pairs)", padStart(label, 10), signed(*metric.MeanDelta, 1), unit,
		jsstring.ToFixed(*metric.TreatmentMean, 1), unit, jsstring.ToFixed(*metric.ControlMean, 1), unit, metric.EligiblePairs)
}

func operationalMetric(metric OperationalMetricTotal, runs int, format func(float64) string) string {
	if metric.Total == nil {
		return fmt.Sprintf("unavailable (0/%d measured)", runs)
	}
	coverage := ""
	if metric.AvailableRuns != runs {
		coverage = fmt.Sprintf(" (%d/%d measured)", metric.AvailableRuns, runs)
	}
	return format(*metric.Total) + coverage
}

// nodeTermColorPatterns are TERM_ENVS_REG_EXP of Node 24's lib/internal/tty.js.
var nodeTermColorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ansi`), regexp.MustCompile(`color`), regexp.MustCompile(`linux`), regexp.MustCompile(`direct`),
	regexp.MustCompile(`^con[0-9]*x[0-9]`), regexp.MustCompile(`^rxvt`), regexp.MustCompile(`^screen`),
	regexp.MustCompile(`^xterm`), regexp.MustCompile(`^vt100`), regexp.MustCompile(`^vt220`),
}

// nodeTermEnvs are the keys of TERM_ENVS in Node 24's lib/internal/tty.js; every value is at least 16 colors.
var nodeTermEnvs = map[string]bool{
	"eterm": true, "cons25": true, "console": true, "cygwin": true, "dtterm": true, "gnome": true, "hurd": true,
	"jfbterm": true, "konsole": true, "kterm": true, "mlterm": true, "mosh": true, "putty": true, "st": true,
	"rxvt-unicode-24bit": true, "terminator": true, "xterm-kitty": true,
}

// nodeObjectPrototypeLowercaseKeys are the lowercase Object.prototype properties: TERM_ENVS is a plain object, so
// such a TERM returns a non-number, which is not above two colors.
var nodeObjectPrototypeLowercaseKeys = map[string]bool{"constructor": true, "__proto__": true}

var teamCityColorVersion = regexp.MustCompile(`^(9\.(0*[1-9]\d*)\.|\d{2,}\.)`)

// nodeColorDepthAboveTwo is getColorDepth(env) > 2 from Node 24's lib/internal/tty.js.
func nodeColorDepthAboveTwo(lookup func(string) (string, bool), goos string) bool {
	get := func(name string) string {
		value, _ := lookup(name)
		return value
	}
	has := func(name string) bool {
		_, ok := lookup(name)
		return ok
	}
	if force, ok := lookup("FORCE_COLOR"); ok {
		switch force {
		case "", "1", "true", "2", "3":
			return true
		}
		return false
	}
	if get("NODE_DISABLE_COLORS") != "" || get("NO_COLOR") != "" || get("TERM") == "dumb" {
		return false
	}
	if goos == "windows" {
		// Every Windows release reports 16 colors or more.
		return true
	}
	if get("TMUX") != "" || has("TF_BUILD") && has("AGENT_NAME") {
		return true
	}
	if has("CI") {
		if slices.ContainsFunc([]string{"APPVEYOR", "BUILDKITE", "CIRCLECI", "DRONE", "GITEA_ACTIONS", "GITHUB_ACTIONS", "GITLAB_CI", "TRAVIS"}, has) {
			return true
		}
		return get("CI_NAME") == "codeship"
	}
	if version, ok := lookup("TEAMCITY_VERSION"); ok {
		return teamCityColorVersion.MatchString(version)
	}
	switch get("TERM_PROGRAM") {
	case "iTerm.app", "HyperTerm", "MacTerm", "Apple_Terminal":
		return true
	}
	colorTerm := get("COLORTERM")
	if colorTerm == "truecolor" || colorTerm == "24bit" {
		return true
	}
	if term := get("TERM"); term != "" {
		if strings.Contains(term, "truecolor") || strings.HasPrefix(term, "xterm-256") {
			return true
		}
		termEnv := strings.ToLower(term)
		if nodeTermEnvs[termEnv] {
			return true
		}
		if nodeObjectPrototypeLowercaseKeys[termEnv] {
			return false
		}
		for _, pattern := range nodeTermColorPatterns {
			if pattern.MatchString(termEnv) {
				return true
			}
		}
	}
	return colorTerm != ""
}

// stdoutShouldColorize is Node 24's shouldColorize(process.stdout), which util.styleText consults by default: FORCE_COLOR
// decides without a terminal, and otherwise stdout must be a terminal whose color depth is above two.
func stdoutShouldColorize() bool {
	if _, ok := os.LookupEnv("FORCE_COLOR"); !ok && !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}
	return nodeColorDepthAboveTwo(os.LookupEnv, runtime.GOOS)
}

func bold(text string) string {
	if !stdoutShouldColorize() {
		return text
	}
	return "\x1b[1m" + text + "\x1b[22m"
}

// FormatEvalComparisonReport renders report for the terminal. It returns "" for a report without comparisons.
func FormatEvalComparisonReport(report EvalComparisonReport) string {
	if len(report.Comparisons) == 0 {
		return ""
	}
	lines := []string{bold("Documentation Eval Comparisons")}
	for _, comparison := range report.Comparisons {
		lines = append(lines, "  "+comparison.EvalSet)
		lines = append(lines, fmt.Sprintf("         Pairs  %d/%d eligible", comparison.EligiblePairs, comparison.TotalPairs))
		switch {
		case comparison.Lift != nil:
			lines = append(lines, fmt.Sprintf("     Pass rate  %s pp (with %s, without %s)", signed(*comparison.Lift*100, 1),
				percentage(comparison.TreatmentPassRate), percentage(comparison.ControlPassRate)))
		case comparison.BlockedPairs > 0:
			lines = append(lines, "     Pass rate  withheld because pairs are blocked")
		default:
			lines = append(lines, "     Pass rate  unavailable")
		}
		if len(comparison.Flags) > 0 {
			flags := make([]string, len(comparison.Flags))
			for i, flag := range comparison.Flags {
				flags[i] = string(flag)
			}
			lines = append(lines, "         Flags  "+strings.Join(flags, ", "))
		}
		lines = append(lines, pairedMetric("Tokens", comparison.TotalTokens, ""))
		lines = append(lines, pairedMetric("Tools", comparison.ToolCalls, ""))
		lines = append(lines, pairedMetric("Latency", comparison.TotalMs, "ms"))
		cost := comparison.EstimatedCostUsd
		if cost.MeanDelta == nil || cost.ControlMean == nil || cost.TreatmentMean == nil {
			lines = append(lines, "     Est. cost  unavailable")
		} else {
			sign := "+"
			if *cost.MeanDelta < 0 {
				sign = "-"
			}
			lines = append(lines, fmt.Sprintf("     Est. cost  %s$%s (with $%s, without $%s, %d pairs)", sign, jsstring.ToFixed(math.Abs(*cost.MeanDelta), 4),
				jsstring.ToFixed(*cost.TreatmentMean, 4), jsstring.ToFixed(*cost.ControlMean, 4), cost.EligiblePairs))
		}
	}
	lines = append(lines, "  Operational totals")
	for _, totals := range report.OperationalTotals {
		tokens := operationalMetric(totals.TotalTokens, totals.Runs, func(total float64) string { return jsnumber.String(total) + " tokens" })
		tools := operationalMetric(totals.ToolCalls, totals.Runs, func(total float64) string { return jsnumber.String(total) + " tools" })
		latency := operationalMetric(totals.TotalMs, totals.Runs, func(total float64) string { return jsstring.ToFixed(total/1000, 2) + "s" })
		cost := operationalMetric(totals.EstimatedCostUsd, totals.Runs, func(total float64) string { return "$" + jsstring.ToFixed(total, 4) + " cost" })
		lines = append(lines, fmt.Sprintf("    %s: %d runs, %s, %s, %s, %s", totals.Variant, totals.Runs, tokens, tools, latency, cost))
	}
	if len(report.BlockedPairs) > 0 {
		lines = append(lines, "  Blocked pairs")
		for _, blocked := range report.BlockedPairs {
			lines = append(lines, fmt.Sprintf("    %s/%s/%s/run-%d: %s", blocked.EvalSet, blocked.CaseID, blocked.Model, blocked.RunNumber, strings.Join(blocked.Reasons, "; ")))
		}
	}
	return strings.Join(lines, "\n")
}
