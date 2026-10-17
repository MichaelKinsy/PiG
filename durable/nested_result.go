package durable

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// nestedResultWire is NestedToolExecutionResult's JSON: taskId, structuredOutput, isError, details, diagnostics, usage,
// durationMs, each optional member absent when unset (tool.ts nestedResult, strict JSON without undefined members).
type nestedResultWire struct {
	TaskId           TaskId           `json:"taskId"`
	StructuredOutput *JsonValue       `json:"structuredOutput,omitempty"`
	IsError          bool             `json:"isError"`
	Details          *JsonValue       `json:"details,omitempty"`
	Diagnostics      []ToolDiagnostic `json:"diagnostics"`
	Usage            *ai.Usage        `json:"usage,omitempty"`
	DurationMs       *int64           `json:"durationMs,omitempty"`
}

// MarshalJSON writes the members in declaration order, leaving out the absent ones.
func (result NestedToolExecutionResult) MarshalJSON() ([]byte, error) {
	wire := nestedResultWire{TaskId: result.TaskId, IsError: result.IsError, Diagnostics: result.Diagnostics, Usage: result.Usage, DurationMs: result.DurationMs}
	if wire.Diagnostics == nil {
		wire.Diagnostics = []ToolDiagnostic{}
	}
	if result.HasStructuredOutput {
		wire.StructuredOutput = &result.StructuredOutput
	}
	if result.HasDetails {
		wire.Details = &result.Details
	}
	return json.Marshal(wire) //perf:ok once per nested tool call result encoded; the members a result lacks are left out, so the Marshaler encodes its wire struct
}

// UnmarshalJSON reads a result written by MarshalJSON; a present null member is a value.
func (result *NestedToolExecutionResult) UnmarshalJSON(data []byte) error {
	var wire struct {
		TaskId           TaskId           `json:"taskId"`
		StructuredOutput json.RawMessage  `json:"structuredOutput"`
		IsError          bool             `json:"isError"`
		Details          json.RawMessage  `json:"details"`
		Diagnostics      []ToolDiagnostic `json:"diagnostics"`
		Usage            *ai.Usage        `json:"usage"`
		DurationMs       *int64           `json:"durationMs"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*result = NestedToolExecutionResult{TaskId: wire.TaskId, IsError: wire.IsError, Diagnostics: wire.Diagnostics, Usage: wire.Usage, DurationMs: wire.DurationMs}
	if wire.StructuredOutput != nil {
		value, err := decodeJsonValue(wire.StructuredOutput)
		if err != nil {
			return err
		}
		result.StructuredOutput, result.HasStructuredOutput = value, true
	}
	if wire.Details != nil {
		value, err := decodeJsonValue(wire.Details)
		if err != nil {
			return err
		}
		result.Details, result.HasDetails = value, true
	}
	return nil
}

func decodeJsonValue(raw json.RawMessage) (JsonValue, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return ToJsonValue(value)
}
