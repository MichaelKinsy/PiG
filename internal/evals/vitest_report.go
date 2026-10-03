package evals

// Reads the subset of a Vitest JSON report that report.ts consumes through @vitest-evals/core 0.15.0
// (src/report/vitest-json.ts, src/report/metadata.ts, src/harness/index.ts, src/report/workspace.ts): the report and
// file schemas pass unknown keys through, the eval metadata schemas are strict, and an assertion whose metadata does
// not parse contributes no report case.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
)

// ReportCaseStatus is a Vitest assertion status.
type ReportCaseStatus string

const (
	ReportCaseStatusPassed   ReportCaseStatus = "passed"
	ReportCaseStatusFailed   ReportCaseStatus = "failed"
	ReportCaseStatusSkipped  ReportCaseStatus = "skipped"
	ReportCaseStatusPending  ReportCaseStatus = "pending"
	ReportCaseStatusTodo     ReportCaseStatus = "todo"
	ReportCaseStatusDisabled ReportCaseStatus = "disabled"
)

// schema validates one JSON member; present is false for an absent member (undefined).
type schema func(raw json.RawMessage, present bool) bool

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func optional(inner schema) schema {
	return func(raw json.RawMessage, present bool) bool { return !present || inner(raw, true) }
}

// withDefault is zod's .default(): an absent member takes the default, which the inner schema accepts.
func withDefault(inner schema) schema { return optional(inner) }

func nullable(inner schema) schema {
	return func(raw json.RawMessage, present bool) bool { return present && (isNull(raw) || inner(raw, true)) }
}

func decodes[T any](raw json.RawMessage, present bool) bool {
	if !present || isNull(raw) {
		return false
	}
	var value T
	return json.Unmarshal(raw, &value) == nil
}

func stringSchema(raw json.RawMessage, present bool) bool  { return decodes[string](raw, present) }
func booleanSchema(raw json.RawMessage, present bool) bool { return decodes[bool](raw, present) }

// finiteNumber accepts any JSON number: JSON cannot spell a non-finite one.
func finiteNumber(raw json.RawMessage, present bool) bool { return decodes[float64](raw, present) }

// optionalFiniteNumber treats null as absent (OptionalFiniteNumberSchema).
func optionalFiniteNumber(raw json.RawMessage, present bool) bool {
	return !present || isNull(raw) || finiteNumber(raw, true)
}

// nullableFiniteNumber is NullableFiniteNumberSchema: null, absent, or a number.
func nullableFiniteNumber(raw json.RawMessage, present bool) bool {
	return optionalFiniteNumber(raw, present)
}

func jsonValue(raw json.RawMessage, present bool) bool { return present }

func jsonObject(raw json.RawMessage, present bool) bool {
	return decodes[map[string]json.RawMessage](raw, present)
}

func literal(values ...string) schema {
	return func(raw json.RawMessage, present bool) bool {
		var value string
		return present && !isNull(raw) && json.Unmarshal(raw, &value) == nil && slices.Contains(values, value)
	}
}

func array(element schema) schema {
	return func(raw json.RawMessage, present bool) bool {
		var items []json.RawMessage
		if !present || isNull(raw) || json.Unmarshal(raw, &items) != nil {
			return false
		}
		for _, item := range items {
			if !element(item, true) {
				return false
			}
		}
		return true
	}
}

type fields map[string]schema

func objectSchema(members fields, strict bool) schema {
	return func(raw json.RawMessage, present bool) bool {
		var object map[string]json.RawMessage
		if !present || isNull(raw) || json.Unmarshal(raw, &object) != nil {
			return false
		}
		for name, member := range members {
			value, ok := object[name]
			if !member(value, ok) {
				return false
			}
		}
		if strict {
			for name := range object {
				if _, known := members[name]; !known {
					return false
				}
			}
		}
		return true
	}
}

func strictObject(members fields) schema { return objectSchema(members, true) }

func passthroughObject(members fields) schema { return objectSchema(members, false) }

func discriminatedUnion(key string, variants map[string]schema) schema {
	return func(raw json.RawMessage, present bool) bool {
		var object map[string]json.RawMessage
		if !present || isNull(raw) || json.Unmarshal(raw, &object) != nil {
			return false
		}
		var tag string
		if json.Unmarshal(object[key], &tag) != nil {
			return false
		}
		variant, ok := variants[tag]
		return ok && variant(raw, true)
	}
}

func extend(base fields, extra fields) fields {
	merged := fields{}
	maps.Copy(merged, base)
	maps.Copy(merged, extra)
	return merged
}

var (
	normalizedError = objectSchema(fields{"message": stringSchema, "type": optional(stringSchema)}, false)

	transcriptEvent = discriminatedUnion("type", map[string]schema{
		"message": strictObject(fields{
			"type": literal("message"), "role": literal("system", "user", "assistant"),
			"content": optional(jsonValue), "metadata": optional(jsonObject),
		}),
		"tool_call": strictObject(fields{
			"type": literal("tool_call"), "id": stringSchema, "name": stringSchema, "arguments": optional(jsonObject),
			"startedAt": optional(stringSchema), "finishedAt": optional(stringSchema), "durationMs": optional(finiteNumber),
			"metadata": optional(jsonObject),
		}),
		"tool_result": strictObject(fields{
			"type": literal("tool_result"), "toolCallId": stringSchema, "name": optional(stringSchema),
			"content": optional(jsonValue), "error": optional(normalizedError), "startedAt": optional(stringSchema),
			"finishedAt": optional(stringSchema), "durationMs": optional(finiteNumber), "metadata": optional(jsonObject),
		}),
	})

	toolCallBase = fields{"name": stringSchema, "arguments": optional(jsonObject)}
	toolCall     = discriminatedUnion("status", map[string]schema{
		"pending": strictObject(extend(toolCallBase, fields{"status": literal("pending")})),
		"ok":      strictObject(extend(toolCallBase, fields{"status": literal("ok"), "result": optional(jsonValue)})),
		"error":   strictObject(extend(toolCallBase, fields{"status": literal("error"), "error": normalizedError})),
	})

	normalizedSpan = strictObject(fields{
		"id": optional(stringSchema), "traceId": optional(stringSchema), "parentId": optional(stringSchema),
		"name": stringSchema, "kind": optional(literal("run", "agent", "model", "tool", "guardrail", "handoff", "custom")),
		"startedAt": optional(stringSchema), "finishedAt": optional(stringSchema), "durationMs": optional(finiteNumber),
		"status": optional(literal("ok", "error")), "error": optional(normalizedError), "attributes": optional(jsonObject),
		"events": optional(array(strictObject(fields{
			"name": stringSchema, "timestamp": optional(stringSchema), "attributes": optional(jsonObject),
		}))),
	})

	harnessRun = strictObject(fields{
		"output": optional(jsonValue),
		"session": strictObject(fields{
			"events": array(transcriptEvent), "provider": optional(stringSchema), "model": optional(stringSchema),
			"metadata": optional(jsonObject),
		}),
		"usage": strictObject(fields{
			"provider": optional(stringSchema), "model": optional(stringSchema), "inputTokens": optional(finiteNumber),
			"outputTokens": optional(finiteNumber), "reasoningTokens": optional(finiteNumber),
			"totalTokens": optional(finiteNumber), "toolCalls": optional(finiteNumber), "retries": optional(finiteNumber),
			"metadata": optional(jsonObject),
		}),
		"timings":   optional(strictObject(fields{"totalMs": optional(finiteNumber), "metadata": optional(jsonObject)})),
		"artifacts": optional(jsonObject),
		"traces": optional(array(strictObject(fields{
			"id": optional(stringSchema), "name": optional(stringSchema), "startedAt": optional(stringSchema),
			"finishedAt": optional(stringSchema), "durationMs": optional(finiteNumber), "metadata": optional(jsonObject),
			"spans": array(normalizedSpan),
		}))),
		"errors": array(jsonObject),
	})

	evalTaskMeta = strictObject(fields{
		"eval": optional(strictObject(fields{
			"scores": optional(array(strictObject(fields{
				"name": optional(stringSchema), "score": nullableFiniteNumber, "metadata": optional(jsonObject),
			}))),
			"avgScore":        nullableFiniteNumber,
			"output":          optional(jsonValue),
			"thresholdFailed": optional(booleanSchema),
			"toolCalls":       optional(array(toolCall)),
		})),
		"harness": optional(strictObject(fields{"name": optional(stringSchema), "run": optional(harnessRun)})),
	})

	vitestStatus = literal("passed", "failed", "skipped", "pending", "todo", "disabled")

	vitestJSONReport = passthroughObject(fields{
		"numFailedTests": finiteNumber, "numPassedTests": finiteNumber, "numPendingTests": finiteNumber,
		"numTodoTests": finiteNumber, "numTotalTests": finiteNumber, "startTime": finiteNumber, "success": booleanSchema,
		"testResults": withDefault(array(passthroughObject(fields{
			"message": stringSchema, "name": stringSchema, "status": literal("failed", "passed"),
			"startTime": optionalFiniteNumber, "endTime": optionalFiniteNumber,
			"assertionResults": withDefault(array(passthroughObject(fields{
				"ancestorTitles": withDefault(array(stringSchema)), "fullName": stringSchema, "status": vitestStatus,
				"title": stringSchema, "meta": optional(jsonValue), "duration": optional(nullable(finiteNumber)),
				"failureMessages": optional(nullable(array(stringSchema))),
				"location":        optional(nullable(passthroughObject(fields{"line": finiteNumber, "column": finiteNumber}))),
				"tags":            optional(array(stringSchema)),
			}))),
		}))),
	})
)

// vitestAssertion is the part of a Vitest assertion result report.ts reads; Meta keeps the raw metadata.
type vitestAssertion struct {
	FullName string
	Status   ReportCaseStatus
	Meta     json.RawMessage
}

type vitestFile struct {
	AssertionResults []vitestAssertion
}

type vitestJSONReportFile struct {
	TestResults []vitestFile
}

// exactMembers decodes the named members of a JSON object. The report, file and assertion schemas pass unknown keys
// through, and JavaScript reads members by exact name, so a key that differs only in case (which encoding/json would
// match to a struct field) must not replace a member.
func exactMembers(data []byte, members map[string]any) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	for name, target := range members {
		if raw, ok := object[name]; ok {
			if err := json.Unmarshal(raw, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func (report *vitestJSONReportFile) UnmarshalJSON(data []byte) error {
	return exactMembers(data, map[string]any{"testResults": &report.TestResults})
}

func (file *vitestFile) UnmarshalJSON(data []byte) error {
	return exactMembers(data, map[string]any{"assertionResults": &file.AssertionResults})
}

func (assertion *vitestAssertion) UnmarshalJSON(data []byte) error {
	return exactMembers(data, map[string]any{"fullName": &assertion.FullName, "status": &assertion.Status, "meta": &assertion.Meta})
}

// readVitestJSONReportFile is readVitestJsonReportFile: the file must hold a valid Vitest JSON report.
func readVitestJSONReportFile(path string) (vitestJSONReportFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return vitestJSONReportFile{}, fmt.Errorf("Failed to read eval result file %s: %w", path, err)
	}
	if !json.Valid(data) || !vitestJSONReport(data, true) {
		return vitestJSONReportFile{}, fmt.Errorf("Failed to read eval result file %s: Invalid Vitest JSON report", path)
	}
	var report vitestJSONReportFile
	if err := json.Unmarshal(data, &report); err != nil {
		return vitestJSONReportFile{}, fmt.Errorf("Failed to read eval result file %s: %w", path, err)
	}
	return report, nil
}

// harnessUsage is UsageSummary; Metadata keeps raw JSON values so a non-number metric stays distinct.
type harnessUsage struct {
	Provider     *string                    `json:"provider"`
	Model        *string                    `json:"model"`
	InputTokens  *float64                   `json:"inputTokens"`
	OutputTokens *float64                   `json:"outputTokens"`
	TotalTokens  *float64                   `json:"totalTokens"`
	ToolCalls    *float64                   `json:"toolCalls"`
	Metadata     map[string]json.RawMessage `json:"metadata"`
}

type harnessRunResult struct {
	Usage   harnessUsage `json:"usage"`
	Timings *struct {
		TotalMs *float64 `json:"totalMs"`
	} `json:"timings"`
	Artifacts map[string]json.RawMessage `json:"artifacts"`
	Errors    []json.RawMessage          `json:"errors"`
}

type evalMeta struct {
	AvgScore *float64 `json:"avgScore"`
}

// reportCase is the part of a collected ReportCase report.ts reads.
type reportCase struct {
	FullName string
	Status   ReportCaseStatus
	Eval     *evalMeta
	Run      *harnessRunResult
}

// collectReportCases is collectReportWorkspace's case list for one report: assertions without valid eval or harness
// metadata are not eval cases, and a harness-only case scores 1 when it passed, 0 when it failed and null otherwise.
func collectReportCases(report vitestJSONReportFile) []reportCase {
	var cases []reportCase
	for _, file := range report.TestResults {
		for _, assertion := range file.AssertionResults {
			testCase, ok := readEvalTaskMeta(assertion)
			if ok {
				cases = append(cases, testCase)
			}
		}
	}
	return cases
}

func readEvalTaskMeta(assertion vitestAssertion) (reportCase, bool) {
	var meta map[string]json.RawMessage
	if len(assertion.Meta) == 0 || json.Unmarshal(assertion.Meta, &meta) != nil || meta == nil {
		return reportCase{}, false
	}
	selected := map[string]json.RawMessage{}
	for _, name := range []string{"eval", "harness"} {
		if value, ok := meta[name]; ok {
			selected[name] = value
		}
	}
	if len(selected) == 0 {
		return reportCase{}, false
	}
	encoded, err := json.Marshal(selected)
	if err != nil || !evalTaskMeta(encoded, true) {
		return reportCase{}, false
	}
	var parsed struct {
		Eval    *evalMeta `json:"eval"`
		Harness *struct {
			Run *harnessRunResult `json:"run"`
		} `json:"harness"`
	}
	if json.Unmarshal(encoded, &parsed) != nil {
		return reportCase{}, false
	}
	testCase := reportCase{FullName: assertion.FullName, Status: assertion.Status, Eval: parsed.Eval}
	if parsed.Harness != nil {
		testCase.Run = parsed.Harness.Run
		if testCase.Eval == nil {
			testCase.Eval = &evalMeta{}
			switch assertion.Status {
			case ReportCaseStatusPassed:
				testCase.Eval.AvgScore = new(1.0)
			case ReportCaseStatusFailed:
				testCase.Eval.AvgScore = new(0.0)
			}
		}
	}
	return testCase, true
}
