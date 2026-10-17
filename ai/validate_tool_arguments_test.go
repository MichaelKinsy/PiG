package ai

import (
	"strings"
	"testing"
)

// Pi validation.ts validateToolArguments: arguments are cloned, optional nulls dropped, then coerced against the schema without touching the call.
func TestValidateToolArgumentsDoesNotMutateTheCallLikeUpstream(t *testing.T) {
	tool := ToolSchema{Name: "count", Parameters: map[string]any{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "number"}}, "required": []any{"count"}}}
	call := ToolCall{Name: "count", Arguments: map[string]any{"count": "7"}}
	got, err := ValidateToolArguments(tool, call)
	if err != nil || got["count"] != float64(7) {
		t.Fatalf("got %v err %v", got, err)
	}
	if call.Arguments["count"] != "7" {
		t.Fatalf("call arguments mutated: %v", call.Arguments)
	}
}

// Pi validation.ts validateToolCall: the first tool carrying the call's name validates its arguments; an unknown name throws `Tool "<name>" not found`.
func TestValidateToolCallPicksTheNamedToolOrFailsLikeUpstream(t *testing.T) {
	schema := func(kind string) map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{"v": map[string]any{"type": kind}}, "required": []any{"v"}}
	}
	// A second "num" with a string schema: tools.find takes the first, so "7" still coerces to a number.
	tools := []ToolSchema{{Name: "num", Parameters: schema("number")}, {Name: "str", Parameters: schema("string")}, {Name: "num", Parameters: schema("string")}}
	tests := []struct {
		name string
		call ToolCall
		want any
		err  string
	}{
		{"first tool coerces to number", ToolCall{Name: "num", Arguments: map[string]any{"v": "7"}}, float64(7), ""},
		{"second tool keeps the string", ToolCall{Name: "str", Arguments: map[string]any{"v": "7"}}, "7", ""},
		{"unknown tool", ToolCall{Name: "missing", Arguments: map[string]any{}}, nil, `Tool "missing" not found`},
		{"no tools", ToolCall{Name: "num"}, nil, `Tool "num" not found`},
		// validation.ts:305 interpolates the name into a template literal; nothing is escaped.
		{"unknown name is not escaped", ToolCall{Name: "say \"hi\"\n\\é"}, nil, "Tool \"say \"hi\"\n\\é\" not found"},
		{"invalid arguments reach the validator", ToolCall{Name: "num", Arguments: map[string]any{"v": "x"}}, nil, "Validation failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list := tools
			if tc.name == "no tools" {
				list = nil
			}
			got, err := ValidateToolCall(list, tc.call)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) || (strings.HasPrefix(tc.err, "Tool ") && err.Error() != tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || got["v"] != tc.want {
				t.Fatalf("got %v err %v, want v=%v", got, err, tc.want)
			}
		})
	}
}
