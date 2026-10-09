package evals

// The judges the eval suites use, ported from vitest-evals 0.15.0 (dist/judges/structuredOutputJudge.mjs and
// toolCallJudge.mjs, @vitest-evals/core toolCalls). Pi's suites configure StructuredOutputJudge with match "strict" and
// allowExtras false, and ToolCallJudge with its defaults (unordered, every tool required, extras allowed, strict
// arguments); only those configurations exist here.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// JudgeScore is one judge's verdict.
type JudgeScore struct {
	Name     string         `json:"name"`
	Score    float64        `json:"score"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ToolCall is a transcript tool call joined with its result (@vitest-evals/core toolCalls).
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Status    string         `json:"status"`
	Result    any            `json:"result,omitempty"`
	Error     map[string]any `json:"error,omitempty"`
}

// Judge scores a run's output and tool calls.
type Judge interface {
	Name() string
	Assess(output any, calls []ToolCall) (JudgeScore, error)
}

// ToolCalls joins each tool_call event with the first tool_result of the same call ID.
func ToolCalls(events []TranscriptEvent) []ToolCall {
	results := map[string]TranscriptEvent{}
	for _, event := range events {
		if event["type"] != "tool_result" {
			continue
		}
		id, _ := event["toolCallId"].(string)
		if _, seen := results[id]; !seen {
			results[id] = event
		}
	}
	calls := []ToolCall{}
	for _, event := range events {
		if event["type"] != "tool_call" {
			continue
		}
		name, _ := event["name"].(string)
		call := ToolCall{Name: name}
		if arguments, ok := event["arguments"].(map[string]any); ok && arguments != nil {
			call.Arguments = arguments
		}
		id, _ := event["id"].(string)
		result, found := results[id]
		switch {
		case !found:
			call.Status = "pending"
		case result["error"] != nil:
			call.Status = "error"
			call.Error, _ = result["error"].(map[string]any)
		default:
			call.Status = "ok"
			call.Result = result["content"]
		}
		calls = append(calls, call)
	}
	return calls
}

// normalizeJSON is the JSON round trip every judge applies to outputs and arguments.
func normalizeJSON(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	return decoded, json.Unmarshal(encoded, &decoded)
}

// strictEquals is vitest-evals' strictEquals over decoded JSON: same type, same array elements, same object keys.
func strictEquals(expected, actual any) bool {
	if expected == nil || actual == nil {
		return expected == nil && actual == nil
	}
	switch expected := expected.(type) {
	case []any:
		actual, ok := actual.([]any)
		if !ok || len(expected) != len(actual) {
			return false
		}
		for index := range expected {
			if !strictEquals(expected[index], actual[index]) {
				return false
			}
		}
		return true
	case map[string]any:
		actual, ok := actual.(map[string]any)
		if !ok || len(expected) != len(actual) {
			return false
		}
		for key, value := range expected {
			other, present := actual[key]
			if !present || !strictEquals(value, other) {
				return false
			}
		}
		return true
	}
	return reflect.TypeOf(expected) == reflect.TypeOf(actual) && expected == actual
}

type structuredOutputJudge struct {
	expected map[string]any
}

// StructuredOutputJudge scores a JSON object output against expected: strict field equality, an error field fails
// the run, and extra fields fail it (allowExtras false). The score is the fraction of matching fields.
func StructuredOutputJudge(expected any) (Judge, error) {
	normalized, err := normalizeJSON(expected)
	if err != nil {
		return nil, err
	}
	record, _ := normalized.(map[string]any)
	return structuredOutputJudge{expected: record}, nil
}

func (structuredOutputJudge) Name() string { return "StructuredOutputJudge" }

func formatValue(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case string:
		return `"` + value + `"`
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func (judge structuredOutputJudge) Assess(output any, _ []ToolCall) (JudgeScore, error) {
	var text string
	if output == nil {
		text = ""
	} else if str, ok := output.(string); ok {
		text = str
	} else {
		encoded, err := json.Marshal(output)
		if err != nil {
			return JudgeScore{}, err
		}
		text = string(encoded)
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return JudgeScore{Score: 0, Metadata: map[string]any{"rationale": "Failed to parse output as JSON: " + err.Error(), "output": text}}, nil
	}
	if len(judge.expected) == 0 {
		return JudgeScore{Score: 1, Metadata: map[string]any{"rationale": "Valid JSON output (no expected fields specified)"}}, nil
	}
	record, _ := parsed.(map[string]any)
	if value := record["error"]; value != nil && value != "" && value != false && value != 0.0 {
		return JudgeScore{Score: 0, Metadata: map[string]any{"rationale": fmt.Sprintf("Output contains error: %v", value), "output": text}}, nil
	}
	expectedKeys := sortedKeys(judge.expected)
	var mismatches, extras []string
	var details []string
	for _, key := range expectedKeys {
		if actual, present := record[key]; present && strictEquals(judge.expected[key], actual) {
			continue
		}
		mismatches = append(mismatches, key)
		details = append(details, fmt.Sprintf("%s: expected %s, got %s", key, formatValue(judge.expected[key]), formatValue(record[key])))
	}
	for _, key := range sortedKeysInInsertionOrder(text, record) {
		if _, expected := judge.expected[key]; !expected {
			extras = append(extras, key)
		}
	}
	if len(mismatches) > 0 {
		return JudgeScore{Score: 0, Metadata: map[string]any{"rationale": "Missing required fields: " + strings.Join(mismatches, ", ") + " - " + strings.Join(details, "; ")}}, nil
	}
	if len(extras) > 0 {
		return JudgeScore{Score: 0, Metadata: map[string]any{"rationale": "Unexpected extra fields: " + strings.Join(extras, ", ")}}, nil
	}
	return JudgeScore{Score: 1, Metadata: map[string]any{"rationale": "All expected fields match"}}, nil
}

func sortedKeys(record map[string]any) []string {
	keys := make([]string, 0, len(record))
	for key := range record {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, compareUTF16)
	return keys
}

// sortedKeysInInsertionOrder lists record's keys in the order they appear in text, which is how Object.keys orders
// them for non-integer keys.
func sortedKeysInInsertionOrder(text string, record map[string]any) []string {
	decoder := json.NewDecoder(strings.NewReader(text))
	var keys []string
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return sortedKeys(record)
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		if key, ok := token.(string); ok && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
		var skipped json.RawMessage
		if decoder.Decode(&skipped) != nil {
			break
		}
	}
	return keys
}

// ExpectedTool is a tool a ToolCallJudge requires. A nil Arguments matches any arguments.
type ExpectedTool struct {
	Name      string
	Arguments map[string]any
}

type toolCallJudge struct{ expected []ExpectedTool }

// ToolCallJudge requires every expected tool to be called, in any order, with strictly equal arguments when given;
// extra calls are allowed.
func ToolCallJudge(expected ...ExpectedTool) Judge { return toolCallJudge{expected: expected} }

func (toolCallJudge) Name() string { return "ToolCallJudge" }

func (judge toolCallJudge) Assess(_ any, calls []ToolCall) (JudgeScore, error) {
	if len(judge.expected) == 0 {
		return JudgeScore{Score: 1, Metadata: map[string]any{"rationale": "No tool calls expected"}}, nil
	}
	if len(calls) == 0 {
		return JudgeScore{Score: 0, Metadata: map[string]any{"rationale": fmt.Sprintf("Expected %d tool(s) but none were called", len(judge.expected))}}, nil
	}
	remainingExpected := slices.Clone(judge.expected)
	remainingActual := slices.Clone(calls)
	for index, want := range slices.Backward(remainingExpected) {
		match := slices.IndexFunc(remainingActual, func(call ToolCall) bool {
			if call.Name != want.Name {
				return false
			}
			if want.Arguments == nil {
				return true
			}
			expectedArguments, errExpected := normalizeJSON(want.Arguments)
			actualArguments, errActual := normalizeJSON(orEmpty(call.Arguments))
			return errExpected == nil && errActual == nil && strictEquals(expectedArguments, actualArguments)
		})
		if match != -1 {
			remainingExpected = slices.Delete(remainingExpected, index, index+1)
			remainingActual = slices.Delete(remainingActual, match, match+1)
		}
	}
	var issues []string
	for _, missing := range remainingExpected {
		if missing.Arguments != nil && slices.ContainsFunc(calls, func(call ToolCall) bool { return call.Name == missing.Name }) {
			issues = append(issues, fmt.Sprintf("Tool '%s' called but with incorrect arguments", missing.Name))
		} else {
			issues = append(issues, "Missing required tool: "+missing.Name)
		}
	}
	if len(issues) > 0 {
		return JudgeScore{Score: 0, Metadata: map[string]any{"rationale": strings.Join(issues, "; ")}}, nil
	}
	rationale := "All expected tools were called"
	if len(remainingActual) > 0 {
		names := make([]string, len(remainingActual))
		for i, call := range remainingActual {
			names[i] = call.Name
		}
		rationale += " (plus extra: " + strings.Join(names, ", ") + ")"
	}
	return JudgeScore{Score: 1, Metadata: map[string]any{"rationale": rationale}}, nil
}

func orEmpty(arguments map[string]any) map[string]any {
	if arguments == nil {
		return map[string]any{}
	}
	return arguments
}
