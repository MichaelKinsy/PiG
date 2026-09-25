// Custom JSON marshalling for the three sealed-interface union types defined
// in events.go: [InputEventResult], [ToolCallEvent], [ToolResultEvent].
//
// Why custom marshalling. Sealed interfaces in Go don't have a default
// JSON representation \u2014 unmarshalling into an interface is a compile error
// because Go can't know which concrete variant to construct. We solve this
// by probing a discriminator field (`action` for InputEventResult,
// `toolName` for the two tool unions) and dispatching to the right variant
// struct. Marshalling is free: each variant struct already has the right
// field tags; we route everything through noEscapeJSON (below) which
// encodes without HTML-escaping `&`, `<`, `>`: matching upstream JS
// `JSON.stringify` semantics.
//
// Wire format. Each variant marshals to upstream's JSON shape verbatim.
// Upstream-written session JSONL round-trips through these helpers without
// loss (round-trip tests live in marshalling_test.go).
//
// References:
//   - DIVERGENCES.md D2 (the divergence retired by these helpers)
//   - .upstream/current/packages/coding-agent/src/core/extensions/types.ts
//     lines 750 (InputEventResult), 810 (ToolCallEvent), 869 (ToolResultEvent)

package extension

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// noEscapeJSON encodes v as JSON without HTML-escaping `&`, `<`, `>`.
//
// Why this exists. Go's default `json.Marshal` HTML-escapes those three
// characters; upstream pi uses JS `JSON.stringify` which does NOT. A bash
// command containing `ls && pwd` round-tripped through `json.Marshal`
// becomes `"ls \u0026\u0026 pwd"`: wire-format divergence from upstream
// pi sessions. AGENTS.md mandates `SetEscapeHTML(false)` for any wire
// format that round-trips through upstream. All sum-type marshalling in
// this file goes through this helper.
func noEscapeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder.Encode appends a trailing newline; trim.
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return out, nil
}

// ─── InputEventResult ─────────────────────────────────────────────────────

// MarshalInputEventResult serialises a sealed-interface variant to upstream
// wire shape with the `action` discriminator.
//
// Why a free function not a method on the interface: defining a marshal
// method on the interface would force every variant to implement it
// (verbose); using a free function keeps marker structs trivial. Hosts
// dispatching events call this; authors returning a value from OnInput
// have their value passed through this helper by the runtime.
func MarshalInputEventResult(r InputEventResult) ([]byte, error) {
	switch v := r.(type) {
	case InputEventResultContinue:
		return []byte(`{"action":"continue"}`), nil
	case InputEventResultHandled:
		return []byte(`{"action":"handled"}`), nil
	case InputEventResultTransform:
		// Use envelope to inject the discriminator without duplicating
		// the field on the variant struct (variant struct fields stay
		// upstream-faithful).
		envelope := struct {
			Action string         `json:"action"`
			Text   string         `json:"text"`
			Images []ImageContent `json:"images,omitempty"`
		}{
			Action: "transform",
			Text:   v.Text,
			Images: v.Images,
		}
		return noEscapeJSON(envelope)
	case nil:
		return []byte("null"), nil
	default:
		return nil, fmt.Errorf("MarshalInputEventResult: unknown variant %T", r)
	}
}

// UnmarshalInputEventResult parses upstream wire shape into the matching
// sealed-interface variant. Returns an error on unknown action discriminators
// so silent drift cannot enter the system.
func UnmarshalInputEventResult(data []byte) (InputEventResult, error) {
	var probe struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("UnmarshalInputEventResult: %w", err)
	}
	switch probe.Action {
	case "continue":
		return InputEventResultContinue{}, nil
	case "handled":
		return InputEventResultHandled{}, nil
	case "transform":
		var v InputEventResultTransform
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("UnmarshalInputEventResult transform: %w", err)
		}
		return v, nil
	case "":
		return nil, fmt.Errorf("UnmarshalInputEventResult: missing action discriminator")
	default:
		return nil, fmt.Errorf("UnmarshalInputEventResult: unknown action %q", probe.Action)
	}
}

// ─── ToolCallEvent ────────────────────────────────────────────────────────

// MarshalToolCallEvent serialises any of the nine ToolCallEvent variants.
// Each variant struct already declares upstream-faithful JSON tags; this
// helper only narrows the interface to a concrete value before marshalling.
func MarshalToolCallEvent(e ToolCallEvent) ([]byte, error) {
	switch v := e.(type) {
	case BashToolCallEvent:
		return noEscapeJSON(v)
	case PowerShellToolCallEvent:
		return noEscapeJSON(v)
	case ReadToolCallEvent:
		return noEscapeJSON(v)
	case EditToolCallEvent:
		return noEscapeJSON(v)
	case WriteToolCallEvent:
		return noEscapeJSON(v)
	case GrepToolCallEvent:
		return noEscapeJSON(v)
	case FindToolCallEvent:
		return noEscapeJSON(v)
	case LsToolCallEvent:
		return noEscapeJSON(v)
	case CustomToolCallEvent:
		return noEscapeJSON(v)
	case nil:
		return []byte("null"), nil
	default:
		return nil, fmt.Errorf("MarshalToolCallEvent: unknown variant %T", e)
	}
}

// UnmarshalToolCallEvent dispatches by `toolName`. Tool names that don't
// match a known builtin variant fall through to [CustomToolCallEvent] \u2014
// this matches upstream behaviour (custom tools share the wire shape with
// builtins; only `toolName` distinguishes them) and keeps third-party tools
// round-trippable through pig without code changes.
func UnmarshalToolCallEvent(data []byte) (ToolCallEvent, error) {
	var probe struct {
		ToolName string `json:"toolName"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("UnmarshalToolCallEvent: %w", err)
	}
	switch probe.ToolName {
	case "bash":
		var v BashToolCallEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "powershell":
		var v PowerShellToolCallEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "read":
		var v ReadToolCallEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "edit":
		var v EditToolCallEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "write":
		var v WriteToolCallEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "grep":
		var v GrepToolCallEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "find":
		var v FindToolCallEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "ls":
		var v LsToolCallEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "":
		return nil, fmt.Errorf("UnmarshalToolCallEvent: missing toolName discriminator")
	default:
		// Custom or unknown-builtin tool. Upstream treats anything not
		// matching a builtin name as a custom tool; we mirror that.
		var v CustomToolCallEvent
		err := json.Unmarshal(data, &v)
		return v, err
	}
}

// ─── ToolResultEvent ──────────────────────────────────────────────────────

// MarshalToolResultEvent serialises any of the nine ToolResultEvent
// variants. Symmetric with [MarshalToolCallEvent].
func MarshalToolResultEvent(e ToolResultEvent) ([]byte, error) {
	switch v := e.(type) {
	case BashToolResultEvent:
		return noEscapeJSON(v)
	case PowerShellToolResultEvent:
		return noEscapeJSON(v)
	case ReadToolResultEvent:
		return noEscapeJSON(v)
	case EditToolResultEvent:
		return noEscapeJSON(v)
	case WriteToolResultEvent:
		return noEscapeJSON(v)
	case GrepToolResultEvent:
		return noEscapeJSON(v)
	case FindToolResultEvent:
		return noEscapeJSON(v)
	case LsToolResultEvent:
		return noEscapeJSON(v)
	case CustomToolResultEvent:
		return noEscapeJSON(v)
	case nil:
		return []byte("null"), nil
	default:
		return nil, fmt.Errorf("MarshalToolResultEvent: unknown variant %T", e)
	}
}

// UnmarshalToolResultEvent dispatches by `toolName`. See
// [UnmarshalToolCallEvent] for the custom-tool fallback rationale.
func UnmarshalToolResultEvent(data []byte) (ToolResultEvent, error) {
	var probe struct {
		ToolName string `json:"toolName"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("UnmarshalToolResultEvent: %w", err)
	}
	switch probe.ToolName {
	case "bash":
		var v BashToolResultEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "powershell":
		var v PowerShellToolResultEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "read":
		var v ReadToolResultEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "edit":
		var v EditToolResultEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "write":
		var v WriteToolResultEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "grep":
		var v GrepToolResultEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "find":
		var v FindToolResultEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "ls":
		var v LsToolResultEvent
		err := json.Unmarshal(data, &v)
		return v, err
	case "":
		return nil, fmt.Errorf("UnmarshalToolResultEvent: missing toolName discriminator")
	default:
		var v CustomToolResultEvent
		err := json.Unmarshal(data, &v)
		return v, err
	}
}
