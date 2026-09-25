package extension

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEvents_JSONTagsAreCamelCase is a smoke check that the most-watched
// event types marshal with the expected upstream camelCase field names.
// The reflection-based parity gates in tests/upstream-parity/ provide the
// authoritative coverage; this test exists so a failed JSON tag fails fast
// inside the package itself.
func TestEvents_JSONTagsAreCamelCase(t *testing.T) {
	t.Run("ToolExecutionEndEvent", func(t *testing.T) {
		b, err := marshalNoEscape(ToolExecutionEndEvent{
			Type:       "tool_execution_end",
			ToolCallID: "abc",
			ToolName:   "bash",
			Result:     "ok",
			IsError:    false,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"type"`, `"toolCallId"`, `"toolName"`, `"result"`, `"isError"`} {
			if !strings.Contains(b, want) {
				t.Errorf("missing %s in %s", want, b)
			}
		}
	})

	t.Run("UserBashEvent", func(t *testing.T) {
		b, err := marshalNoEscape(UserBashEvent{
			Type:               "user_bash",
			Command:            "ls && pwd",
			ExcludeFromContext: true,
			Cwd:                "/tmp",
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"command"`, `"excludeFromContext"`, `"cwd"`} {
			if !strings.Contains(b, want) {
				t.Errorf("missing %s in %s", want, b)
			}
		}
		// Faithfulness: && must NOT be HTML-escaped to \u0026\u0026
		// (per AGENTS.md JSON rule). marshalNoEscape uses
		// json.Encoder.SetEscapeHTML(false).
		if strings.Contains(b, `\u0026`) {
			t.Errorf("command was HTML-escaped: %s", b)
		}
	})

	t.Run("BashToolCallEvent_PromotedFields", func(t *testing.T) {
		// Embedded ToolCallEventBase must promote Type and ToolCallID into
		// the JSON output.
		b, err := marshalNoEscape(BashToolCallEvent{
			ToolCallEventBase: ToolCallEventBase{Type: "tool_call", ToolCallID: "x"},
			ToolName:          "bash",
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"type"`, `"toolCallId"`, `"toolName"`} {
			if !strings.Contains(b, want) {
				t.Errorf("missing %s in %s", want, b)
			}
		}
	})

	t.Run("SessionTreeEvent_NullLeafIds", func(t *testing.T) {
		// Faithfulness: upstream NewLeafID/OldLeafID are `string | null`,
		// not optional. Both must serialize as JSON null when nil, NOT be
		// omitted, so a session JSONL written by pig round-trips through
		// upstream pi without losing the field.
		b, err := marshalNoEscape(SessionTreeEvent{Type: "session_tree"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b, `"newLeafId":null`) {
			t.Errorf("expected newLeafId:null, got %s", b)
		}
		if !strings.Contains(b, `"oldLeafId":null`) {
			t.Errorf("expected oldLeafId:null, got %s", b)
		}
	})
}

// marshalNoEscape encodes v as JSON without HTML escaping, matching the
// AGENTS.md rule for any string that may appear in a TUI surface.
func marshalNoEscape(v any) (string, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
