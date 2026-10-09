package evals

// The eval suite runner. Pi's evals are vitest-evals suites run by Vitest (describeEval, vitest.evals.config.ts,
// docker/entrypoint.ts); Go has neither, so a Suite lists its cases and the runner records each case in the Vitest
// JSON report shape that report.go reads (the `vitest list --json` and `--reporter=json` outputs).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// EvalProject selects the docs or host suites (vitest.evals.config.ts projects).
type EvalProject string

const (
	// EvalProjectDocs runs the *.docs.eval.go suites; each case runs in a container per documentation variant.
	EvalProjectDocs EvalProject = "docs"
	// EvalProjectHost runs every other suite on this machine.
	EvalProjectHost EvalProject = "host"
)

// testTimeout is the per-case timeout of vitest.evals.config.ts (testTimeout and hookTimeout 300_000).
const testTimeout = 300 * time.Second

// RunFunc runs input through the suite's harness, records the run for the report and applies the suite's judges
// (the `run` fixture of vitest-evals).
type RunFunc func(ctx context.Context, input PiCodingAgentInput) (*HarnessRun, error)

// Case is one eval case. A returned error fails the case (a Vitest assertion failure).
type Case struct {
	Name string
	Run  func(ctx context.Context, run RunFunc) error
}

// Suite is a describeEval: a harness, judges and cases. File is the path under the evals package, ending in
// DocsEvalSuffix for a documentation suite.
type Suite struct {
	Name    string
	File    string
	Harness func(ctx context.Context, input PiCodingAgentInput) (*HarnessRun, error)
	Judges  []Judge
	// JudgeThreshold is the average score a case needs; nil is Vitest-evals' default of 1. NoJudgeThreshold records
	// scores without failing (judgeThreshold: null).
	JudgeThreshold   *float64
	NoJudgeThreshold bool
	Cases            []Case
	// Setup runs before the suite's cases and returns its cleanup (beforeAll/afterAll). The cleanup runs even when Setup
	// fails.
	Setup func(ctx context.Context) (func(context.Context) error, error)
	// BeforeEach runs before every case (beforeEach).
	BeforeEach func()
}

// Project is the project the suite belongs to.
func (suite Suite) Project() EvalProject {
	if strings.HasSuffix(suite.File, DocsEvalSuffix) {
		return EvalProjectDocs
	}
	return EvalProjectHost
}

// DiscoveredTest is one `vitest list --json` entry.
type DiscoveredTest struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// Selection narrows the suites and cases a runner lists or runs.
type Selection struct {
	Project EvalProject
	// Files are paths relative to the package root; empty selects every file of the project.
	Files []string
	// NamePattern matches "<suite> <case>" (Vitest -t); nil matches all.
	NamePattern *regexp.Regexp
}

// Runner lists and runs suites. PackageRoot is the evals package directory as the container reports it.
type Runner struct {
	Suites      []Suite
	PackageRoot string
}

func (runner Runner) selected(selection Selection) []Suite {
	var suites []Suite
	for _, suite := range runner.Suites {
		if suite.Project() != selection.Project {
			continue
		}
		if len(selection.Files) > 0 && !slices.Contains(selection.Files, suite.File) {
			continue
		}
		suites = append(suites, suite)
	}
	return suites
}

func (suite Suite) cases(selection Selection) []Case {
	var cases []Case
	for _, evalCase := range suite.Cases {
		if selection.NamePattern == nil || selection.NamePattern.MatchString(suite.Name+" "+evalCase.Name) {
			cases = append(cases, evalCase)
		}
	}
	return cases
}

// List is `vitest list --json`: the selected cases named "<suite> > <case>" with their absolute file.
func (runner Runner) List(selection Selection) []DiscoveredTest {
	tests := []DiscoveredTest{}
	for _, suite := range runner.selected(selection) {
		for _, evalCase := range suite.cases(selection) {
			tests = append(tests, DiscoveredTest{Name: suite.Name + " > " + evalCase.Name, File: path.Join(runner.PackageRoot, suite.File)})
		}
	}
	return tests
}

type assertionResult struct {
	AncestorTitles  []string       `json:"ancestorTitles"`
	FullName        string         `json:"fullName"`
	Status          string         `json:"status"`
	Title           string         `json:"title"`
	Duration        float64        `json:"duration"`
	FailureMessages []string       `json:"failureMessages"`
	Meta            map[string]any `json:"meta"`
}

type fileResult struct {
	AssertionResults []assertionResult `json:"assertionResults"`
	StartTime        int64             `json:"startTime"`
	EndTime          int64             `json:"endTime"`
	Status           string            `json:"status"`
	Message          string            `json:"message"`
	Name             string            `json:"name"`
}

// VitestReport is the JSON reporter's output.
type VitestReport struct {
	NumTotalTests   int          `json:"numTotalTests"`
	NumPassedTests  int          `json:"numPassedTests"`
	NumFailedTests  int          `json:"numFailedTests"`
	NumPendingTests int          `json:"numPendingTests"`
	NumTodoTests    int          `json:"numTodoTests"`
	StartTime       int64        `json:"startTime"`
	Success         bool         `json:"success"`
	TestResults     []fileResult `json:"testResults"`
}

func harnessRunMeta(run *HarnessRun) map[string]any {
	errorsJSON := make([]map[string]any, len(run.Errors))
	for i, err := range run.Errors {
		errorsJSON[i] = map[string]any{"message": err.Error(), "type": "Error"}
	}
	events, metadata, usage := run.Events, run.Metadata, run.Usage
	if events == nil {
		events = []TranscriptEvent{}
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	if usage.Metadata == nil {
		usage.Metadata = map[string]any{}
	}
	return map[string]any{
		"output":    run.Output,
		"session":   map[string]any{"events": events, "metadata": metadata},
		"usage":     usage,
		"timings":   map[string]any{"totalMs": run.TotalMs},
		"artifacts": run.Artifacts,
		"errors":    errorsJSON,
	}
}

// Run runs the selected cases in order and returns the JSON report. Suite setup failures fail every case of the
// suite. The report's Success is false when a case failed.
func (runner Runner) Run(ctx context.Context, selection Selection) VitestReport {
	started := time.Now()
	report := VitestReport{StartTime: started.UnixMilli(), Success: true, TestResults: []fileResult{}}
	for _, suite := range runner.selected(selection) {
		cases := suite.cases(selection)
		if len(cases) == 0 {
			continue
		}
		file := fileResult{StartTime: time.Now().UnixMilli(), Status: "passed", Name: path.Join(runner.PackageRoot, suite.File), AssertionResults: []assertionResult{}}
		cleanup := func(context.Context) error { return nil }
		var setupErr error
		if suite.Setup != nil {
			if cleanup, setupErr = suite.Setup(ctx); cleanup == nil {
				cleanup = func(context.Context) error { return nil }
			}
		}
		for _, evalCase := range cases {
			result := assertionResult{AncestorTitles: []string{suite.Name}, FullName: suite.Name + " " + evalCase.Name, Title: evalCase.Name,
				Status: "passed", FailureMessages: []string{}, Meta: map[string]any{}}
			caseStart := time.Now()
			err := setupErr
			if err == nil {
				err = suite.runCase(ctx, evalCase, result.Meta)
			}
			if err != nil {
				result.Status, result.FailureMessages = "failed", []string{err.Error()}
				file.Status, report.Success = "failed", false
				report.NumFailedTests++
			} else {
				report.NumPassedTests++
			}
			report.NumTotalTests++
			result.Duration = float64(time.Since(caseStart)) / float64(time.Millisecond)
			file.AssertionResults = append(file.AssertionResults, result)
		}
		// Vitest runs afterAll hooks even when a beforeAll hook failed.
		if err := cleanup(ctx); err != nil {
			file.Status, file.Message, report.Success = "failed", err.Error(), false
		}
		file.EndTime = time.Now().UnixMilli()
		report.TestResults = append(report.TestResults, file)
	}
	return report
}

// runCase runs one case under the test timeout and records the harness run and judge scores in meta.
func (suite Suite) runCase(ctx context.Context, evalCase Case, meta map[string]any) (failure error) {
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	if suite.BeforeEach != nil {
		suite.BeforeEach()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			failure = fmt.Errorf("panic: %v", recovered)
		}
	}()
	run := func(ctx context.Context, input PiCodingAgentInput) (*HarnessRun, error) {
		harnessRun, err := suite.Harness(ctx, input)
		if partial, ok := errors.AsType[*HarnessRunError](err); ok {
			meta["harness"] = map[string]any{"name": partial.Run.Name, "run": harnessRunMeta(partial.Run)}
		}
		if err != nil {
			return nil, err
		}
		meta["harness"] = map[string]any{"name": harnessRun.Name, "run": harnessRunMeta(harnessRun)}
		return harnessRun, suite.applyJudges(harnessRun, meta)
	}
	return evalCase.Run(ctx, run)
}

// applyJudges scores run with the suite's judges, records meta.eval and fails below the threshold
// (applyAutomaticJudges).
func (suite Suite) applyJudges(run *HarnessRun, meta map[string]any) error {
	if len(suite.Judges) == 0 {
		return nil
	}
	calls := ToolCalls(run.Events)
	scores := make([]JudgeScore, len(suite.Judges))
	total := 0.0
	for i, judge := range suite.Judges {
		score, err := judge.Assess(run.Output, calls)
		if err != nil {
			return err
		}
		score.Name = judge.Name()
		scores[i] = score
		total += score.Score
	}
	average := total / float64(len(scores))
	threshold := 1.0
	if suite.JudgeThreshold != nil {
		threshold = *suite.JudgeThreshold
	}
	thresholdFailed := !suite.NoJudgeThreshold && average < threshold
	callsJSON, _ := json.Marshal(calls)
	var callsDecoded any
	_ = json.Unmarshal(callsJSON, &callsDecoded)
	meta["eval"] = map[string]any{"scores": scores, "avgScore": average, "output": run.Output, "toolCalls": callsDecoded, "thresholdFailed": thresholdFailed}
	if thresholdFailed {
		return fmt.Errorf("Score: %.2f below threshold: %.2f", average, threshold)
	}
	return nil
}

// WriteVitestReport writes report as JSON to outputPath, 0o666 before the umask like Vitest's outputFile.
func WriteVitestReport(outputPath string, report VitestReport) error {
	encoded, err := plainJSON(report)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, encoded, 0o666)
}

// plainJSON is JSON.stringify's compact form: Go's encoder would escape <, > and & as \u00XX.
func plainJSON(value any) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'}), nil
}

// WriteDiscovery writes the discovered tests as JSON to outputPath.
func WriteDiscovery(outputPath string, tests []DiscoveredTest) error {
	encoded, err := plainJSON(tests)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, encoded, 0o666)
}
