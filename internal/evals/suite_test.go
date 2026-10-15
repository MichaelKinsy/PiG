package evals

// Tests for the suite runner and judges. vitest-evals has no Go form, so the cases pin the semantics read from
// vitest-evals 0.15.0 (structuredOutputJudge.mjs, toolCallJudge.mjs, index.js applyAutomaticJudges): scores, the
// threshold, the report shape report.go reads, discovery and filtering.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

func fixtureHarness(output any, events ...TranscriptEvent) func(context.Context, PiCodingAgentInput) (*HarnessRun, error) {
	return func(context.Context, PiCodingAgentInput) (*HarnessRun, error) {
		return &HarnessRun{Name: "fixture", Output: output, Events: events, Usage: UsageSummary{Provider: "p", Model: "m"},
			Artifacts: map[string]any{}}, nil
	}
}

func TestStructuredOutputJudgeScoresStrictly(t *testing.T) {
	judge, err := StructuredOutputJudge(map[string]any{"result": map[string]any{"ok": true, "n": 2}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		output    any
		score     float64
		rationale string
	}{
		{"match", map[string]any{"result": map[string]any{"ok": true, "n": 2}}, 1, "All expected fields match"},
		{"string output is parsed", `{"result":{"ok":true,"n":2}}`, 1, "All expected fields match"},
		{"nested mismatch", map[string]any{"result": map[string]any{"ok": true, "n": 3}}, 0, `Missing required fields: result - result: expected {"n":2,"ok":true}, got {"n":3,"ok":true}`},
		{"extra field", map[string]any{"result": map[string]any{"ok": true, "n": 2}, "more": 1}, 0, "Unexpected extra fields: more"},
		{"extra nested key is a mismatch", map[string]any{"result": map[string]any{"ok": true, "n": 2, "x": 1}}, 0, ""},
		{"error field", map[string]any{"error": "boom"}, 0, "Output contains error: boom"},
		{"not json", "plain", 0, "Failed to parse output as JSON: invalid character 'p' looking for beginning of value"},
		{"number type", map[string]any{"result": map[string]any{"ok": true, "n": "2"}}, 0, ""},
	}
	for _, c := range cases {
		score, err := judge.Assess(c.output, nil)
		if err != nil || score.Score != c.score || (c.rationale != "" && score.Metadata["rationale"] != c.rationale) {
			t.Errorf("%s: score = %+v, %v; want %v %q", c.name, score, err, c.score, c.rationale)
		}
	}
}

func TestToolCallJudgeMatchesUnorderedWithStrictArguments(t *testing.T) {
	calls := []ToolCall{{Name: "read", Arguments: map[string]any{"path": "a"}, Status: "ok"}, {Name: "hello", Arguments: map[string]any{"name": "Bob"}, Status: "ok"}}
	cases := []struct {
		name      string
		expected  []ExpectedTool
		calls     []ToolCall
		score     float64
		rationale string
	}{
		{"matched with extras", []ExpectedTool{{Name: "hello", Arguments: map[string]any{"name": "Bob"}}}, calls, 1, "All expected tools were called (plus extra: read)"},
		{"no expectation", nil, calls, 1, "No tool calls expected"},
		{"no calls", []ExpectedTool{{Name: "hello"}}, nil, 0, "Expected 1 tool(s) but none were called"},
		{"missing", []ExpectedTool{{Name: "write"}}, calls, 0, "Missing required tool: write"},
		{"wrong arguments", []ExpectedTool{{Name: "hello", Arguments: map[string]any{"name": "Al"}}}, calls, 0, "Tool 'hello' called but with incorrect arguments"},
		{"any arguments", []ExpectedTool{{Name: "hello"}}, calls, 1, "All expected tools were called (plus extra: read)"},
	}
	for _, c := range cases {
		score, err := ToolCallJudge(c.expected...).Assess(nil, c.calls)
		if err != nil || score.Score != c.score || score.Metadata["rationale"] != c.rationale {
			t.Errorf("%s: score = %+v, %v; want %v %q", c.name, score, err, c.score, c.rationale)
		}
	}
}

func TestToolCallsJoinResultsByID(t *testing.T) {
	events := []TranscriptEvent{
		{"type": "tool_call", "id": "1", "name": "a", "arguments": map[string]any{"x": 1}},
		{"type": "tool_call", "id": "2", "name": "b", "arguments": map[string]any{}},
		{"type": "tool_call", "id": "3", "name": "c", "arguments": map[string]any{}},
		{"type": "tool_result", "toolCallId": "1", "content": "done"},
		{"type": "tool_result", "toolCallId": "1", "content": "ignored duplicate"},
		{"type": "tool_result", "toolCallId": "2", "error": map[string]any{"message": "no"}},
	}
	got := ToolCalls(events)
	want := []ToolCall{
		{Name: "a", Arguments: map[string]any{"x": 1}, Status: "ok", Result: "done"},
		{Name: "b", Arguments: map[string]any{}, Status: "error", Error: map[string]any{"message": "no"}},
		{Name: "c", Arguments: map[string]any{}, Status: "pending"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ToolCalls = %+v, want %+v", got, want)
	}
}

func TestRunnerListsRunsAndReportsLikeVitest(t *testing.T) {
	judge, _ := StructuredOutputJudge(map[string]any{"ok": true})
	var cleaned []string
	suites := []Suite{
		{Name: "Docs set", File: "evals/a.docs.eval.go", Harness: fixtureHarness(map[string]any{"ok": true}), Judges: []Judge{judge}, NoJudgeThreshold: true,
			Setup: func(context.Context) (func(context.Context) error, error) {
				return func(context.Context) error { cleaned = append(cleaned, "a"); return nil }, nil
			},
			Cases: []Case{
				{Name: "passes", Run: func(ctx context.Context, run RunFunc) error { _, err := run(ctx, PromptInput("x")); return err }},
				{Name: "other", Run: func(context.Context, RunFunc) error { return errors.New("assertion failed") }},
			}},
		{Name: "Strict set", File: "evals/b.docs.eval.go", Harness: fixtureHarness(map[string]any{"ok": false}), Judges: []Judge{judge},
			Cases: []Case{{Name: "below threshold", Run: func(ctx context.Context, run RunFunc) error { _, err := run(ctx, PromptInput("x")); return err }}}},
		{Name: "Host set", File: "evals/smoke.eval.go", Harness: fixtureHarness("Paris"), Cases: []Case{{Name: "host", Run: func(ctx context.Context, run RunFunc) error { _, err := run(ctx, PromptInput("x")); return err }}}},
	}
	runner := Runner{Suites: suites, PackageRoot: "/repo/packages/evals"}

	docs := Selection{Project: EvalProjectDocs}
	if want := []DiscoveredTest{
		{Name: "Docs set > passes", File: "/repo/packages/evals/evals/a.docs.eval.go"}, {Name: "Docs set > other", File: "/repo/packages/evals/evals/a.docs.eval.go"},
		{Name: "Strict set > below threshold", File: "/repo/packages/evals/evals/b.docs.eval.go"},
	}; !reflect.DeepEqual(runner.List(docs), want) {
		t.Errorf("docs list = %v", runner.List(docs))
	}
	if got := runner.List(Selection{Project: EvalProjectHost}); len(got) != 1 || got[0].Name != "Host set > host" {
		t.Errorf("host list = %v", got)
	}
	if got := runner.List(Selection{Project: EvalProjectDocs, Files: []string{"evals/b.docs.eval.go"}, NamePattern: regexp.MustCompile(`^Strict set below threshold$`)}); len(got) != 1 {
		t.Errorf("filtered list = %v", got)
	}
	if got := runner.List(Selection{Project: EvalProjectDocs, NamePattern: regexp.MustCompile(`^nothing$`)}); len(got) != 0 {
		t.Errorf("unmatched pattern listed %v", got)
	}

	report := runner.Run(t.Context(), docs)
	if report.Success || report.NumTotalTests != 3 || report.NumPassedTests != 1 || report.NumFailedTests != 2 {
		t.Fatalf("report = %+v", report)
	}
	if !reflect.DeepEqual(cleaned, []string{"a"}) {
		t.Errorf("suite cleanup = %v", cleaned)
	}
	first := report.TestResults[0].AssertionResults
	if first[0].FullName != "Docs set passes" || first[0].Status != "passed" || first[1].Status != "failed" || first[1].FailureMessages[0] != "assertion failed" {
		t.Errorf("first file = %+v", first)
	}
	strict := report.TestResults[1].AssertionResults[0]
	if strict.Status != "failed" || strict.FailureMessages[0] != "Score: 0.00 below threshold: 1.00" {
		t.Errorf("a case below the default threshold must fail: %+v", strict)
	}
	// A recorded run reads back through the same reader the comparison uses.
	path := filepath.Join(t.TempDir(), "vitest.json")
	if err := WriteVitestReport(path, report); err != nil {
		t.Fatal(err)
	}
	read, err := readVitestJSONReportFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := collectReportCases(read)
	// The failing assertion that never ran a harness has no eval metadata, so the report reader skips it.
	if len(cases) != 2 || cases[0].Status != ReportCaseStatusPassed || cases[0].Eval == nil || *cases[0].Eval.AvgScore != 1 || cases[0].Run == nil || *cases[0].Run.Usage.Provider != "p" {
		t.Errorf("report cases = %+v", cases)
	}
	if cases[1].Eval == nil || *cases[1].Eval.AvgScore != 0 || cases[1].Status != ReportCaseStatusFailed {
		t.Errorf("the below-threshold case scores 0: %+v", cases[1])
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// TestRunnerAppliesTheJudgeThresholdToTheAverage pins the threshold boundary of applyAutomaticJudges: a case fails only
// when the average score is below the threshold, and the default threshold is 1.
func TestRunnerAppliesTheJudgeThresholdToTheAverage(t *testing.T) {
	pass, _ := StructuredOutputJudge(map[string]any{"ok": true})
	fail, _ := StructuredOutputJudge(map[string]any{"ok": false})
	half := 0.5
	run := func(ctx context.Context, run RunFunc) error { _, err := run(ctx, PromptInput("x")); return err }
	suites := []Suite{
		{Name: "At threshold", File: "evals/a.docs.eval.go", Harness: fixtureHarness(map[string]any{"ok": true}), Judges: []Judge{pass, fail}, JudgeThreshold: &half,
			Cases: []Case{{Name: "half", Run: run}}},
		{Name: "Default threshold", File: "evals/b.docs.eval.go", Harness: fixtureHarness(map[string]any{"ok": true}), Judges: []Judge{pass, fail},
			Cases: []Case{{Name: "half", Run: run}}},
	}
	report := Runner{Suites: suites, PackageRoot: "/repo/packages/evals"}.Run(t.Context(), Selection{Project: EvalProjectDocs})
	if len(report.TestResults) != 2 {
		t.Fatalf("report = %+v", report)
	}
	if at := report.TestResults[0].AssertionResults[0]; at.Status != "passed" {
		t.Errorf("an average equal to the threshold must pass: %+v", at)
	}
	below := report.TestResults[1].AssertionResults[0]
	if below.Status != "failed" || below.FailureMessages[0] != "Score: 0.50 below threshold: 1.00" {
		t.Errorf("an average below the default threshold must fail: %+v", below)
	}
}

// TestRunnerCleansUpAfterAFailedSetup pins Vitest's hook order: a failing beforeAll fails every case of the suite and
// its afterAll still runs.
func TestRunnerCleansUpAfterAFailedSetup(t *testing.T) {
	cleaned := false
	suite := Suite{Name: "Setup fails", File: "evals/a.docs.eval.go", Harness: fixtureHarness("x"),
		Setup: func(context.Context) (func(context.Context) error, error) {
			return func(context.Context) error { cleaned = true; return nil }, errors.New("server did not start")
		},
		Cases: []Case{{Name: "one", Run: func(context.Context, RunFunc) error { return nil }}}}
	report := Runner{Suites: []Suite{suite}, PackageRoot: "/repo/packages/evals"}.Run(t.Context(), Selection{Project: EvalProjectDocs})
	if report.Success || report.NumFailedTests != 1 || report.TestResults[0].AssertionResults[0].FailureMessages[0] != "server did not start" {
		t.Errorf("report = %+v", report)
	}
	if !cleaned {
		t.Error("the cleanup of a failed setup did not run")
	}
}
