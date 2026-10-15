package evals

// pi: packages/evals/src/report.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type reportProbe struct {
	Digest       string            `json:"digest"`
	Expected     []ExpectedEvalRun `json:"expected"`
	Observations []EvalObservation `json:"observations"`
}

type reportOutcome struct {
	Report any    `json:"report"`
	Text   string `json:"text"`
	Err    string `json:"err"`
}

// reportProbes draw eval-set, case and model names that collate differently in ICU and in simple code-point order (case, accents, digits,
// punctuation, spaces, CJK), designs with missing, repeated and unexpected runs, every outcome, and metrics that are absent, zero, fractional and
// sums whose floating-point result depends on the order of addition.
// observationCount is nearly always one observation per expected run, sometimes none and sometimes two.
func observationCount(random *rand.Rand) int {
	switch random.Intn(110) {
	case 0:
		return 0
	case 1:
		return 2
	}
	return 1
}

func reportProbes() []reportProbe {
	random := rand.New(rand.NewSource(20260930))
	names := []string{"Add model", "add model", "Äpfel", "apple", "Zeta", "10", "2", "a-b", "a_b", "a b", "é", "e", "E", "日本", "ß", "ss", "Ab", "aB", "a.b", "A1", "a10", "a2"}
	pick := func() string { return names[random.Intn(len(names))] }
	metric := func() *float64 {
		if random.Intn(3) == 0 {
			return nil
		}
		return new([]float64{0, 1, 2, 3, 10, 0.1, 0.2, 0.3, 1234.5, 1e-7, 123456789, 0.7, 4.35}[random.Intn(13)])
	}
	var probes []reportProbe
	for range 700 {
		probe := reportProbe{Digest: pick(), Expected: []ExpectedEvalRun{}, Observations: []EvalObservation{}}
		sets, cases := 1+random.Intn(3), 1+random.Intn(3)
		models := []string{pick(), pick()}[:1+random.Intn(2)]
		var evalSets, caseIDs []string
		for range sets {
			evalSets = append(evalSets, pick())
		}
		for range cases {
			caseIDs = append(caseIDs, pick())
		}
		for _, evalSet := range evalSets {
			for _, caseID := range caseIDs {
				for _, model := range models {
					for run := 1; run <= 1+random.Intn(3); run++ {
						for _, variant := range DocumentationVariants {
							identity := EvalRunIdentity{EvalSet: evalSet, CaseID: caseID, Variant: variant, Model: model, RunNumber: run}
							if random.Intn(150) != 0 {
								probe.Expected = append(probe.Expected, identity)
							}
							if random.Intn(150) == 0 {
								probe.Expected = append(probe.Expected, identity)
							}
							for range observationCount(random) {
								observation := EvalObservation{EvalRunIdentity: identity, Outcome: []EvalOutcome{EvalOutcomeScored, EvalOutcomeScored, EvalOutcomeScored, EvalOutcomeScored, EvalOutcomeUnscored, EvalOutcomeSkipped, EvalOutcomePending, EvalOutcomeErrored}[random.Intn(8)]}
								if observation.Outcome == EvalOutcomeScored {
									observation.Score = new([]float64{0, 1, 1, 0.5, 0.99, 0.3, 1}[random.Intn(7)])
								}
								observation.EvalMetrics = EvalMetrics{InputTokens: metric(), OutputTokens: metric(), CacheReadTokens: metric(), CacheWriteTokens: metric(), TotalTokens: metric(), ToolCalls: metric(), TotalMs: metric(), EstimatedCostUsd: metric()}
								probe.Observations = append(probe.Observations, observation)
							}
						}
					}
				}
			}
		}
		random.Shuffle(len(probe.Observations), func(i, j int) {
			probe.Observations[i], probe.Observations[j] = probe.Observations[j], probe.Observations[i]
		})
		random.Shuffle(len(probe.Expected), func(i, j int) { probe.Expected[i], probe.Expected[j] = probe.Expected[j], probe.Expected[i] })
		probes = append(probes, probe)
	}
	return probes
}

// report.ts summarizeEvalObservations and formatEvalComparisonReport run in Node from the pinned source against the same probes: the report (pair
// resolution, blocked reasons, pass rates, lift, flags, paired means and deltas, operational totals, and the ICU localeCompare ordering of eval sets,
// cases and models) and the formatted text must match Pi's.
func TestEvalReportMatchesPiOnSeededDesigns(t *testing.T) {
	probes := reportProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "evals", "src", "report.ts")
	cmd := exec.CommandContext(t.Context(), "node", "testdata/report.mjs", source)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []reportOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	blocked, flagged, differing := 0, 0, 0
	for i, probe := range probes {
		report := SummarizeEvalObservations(probe.Digest, probe.Expected, probe.Observations)
		raw, _ := json.Marshal(report)
		var got reportOutcome
		_ = json.Unmarshal(raw, &got.Report)
		got.Text = FormatEvalComparisonReport(report)
		if len(report.BlockedPairs) > 0 {
			blocked++
		}
		for _, comparison := range report.Comparisons {
			if len(comparison.Flags) > 0 {
				flagged++
			}
		}
		if expected[i].Err != "" || !reflect.DeepEqual(got, expected[i]) {
			if differing++; differing <= 3 {
				t.Errorf("probe %d differs from Pi (Pi error %q):\n  Pig %s\n  Pi  %s\n  Pig report %v\n  Pi  report %v", i, expected[i].Err, got.Text, expected[i].Text, got.Report, expected[i].Report)
			}
		}
	}
	if differing > 3 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if blocked < len(probes)/4 || flagged < len(probes)/4 {
		t.Errorf("probes miss the blocked or flagged paths: %d blocked, %d flagged of %d", blocked, flagged, len(probes))
	}
}
