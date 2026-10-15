package ai

import "testing"

// upstream: packages/ai/src/api/google-shared.ts:410-436 mapToolChoice and resolveGoogleFunctionCallingMode.
func TestResolveGoogleFunctionCallingModeMatchesPi(t *testing.T) {
	plain := ToolSchema{Name: "plain", Parameters: JsonObject{"type": "object", "properties": JsonObject{}}}
	strict := ToolSchema{Name: "strict", Parameters: JsonObject{"type": "object", "properties": JsonObject{}}, ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"}}
	for _, tc := range []struct {
		name         string
		tools        []ToolSchema
		choice       string
		supportsMode bool
		want         string
	}{
		{"no choice and no strict tool leaves the mode absent", []ToolSchema{plain}, "", true, ""},
		{"no tools and no choice", nil, "", true, ""},
		{"auto", []ToolSchema{plain}, "auto", true, "AUTO"},
		{"none", []ToolSchema{plain}, "none", true, "NONE"},
		{"any", []ToolSchema{plain}, "any", true, "ANY"},
		{"an unknown choice is AUTO", []ToolSchema{plain}, "required", true, "AUTO"},
		{"a strict tool selects VALIDATED", []ToolSchema{plain, strict}, "", true, "VALIDATED"},
		{"a strict tool overrides auto", []ToolSchema{strict}, "auto", true, "VALIDATED"},
		{"none beats strict mode", []ToolSchema{strict}, "none", true, "NONE"},
		{"any beats strict mode", []ToolSchema{strict}, "any", true, "ANY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveGoogleFunctionCallingMode(tc.tools, tc.choice, tc.supportsMode)
			if err != nil || got != tc.want {
				t.Fatalf("mode = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	// A required strict tool that the model cannot honour is an error, as resolveJsonSchemaStrictSampling throws upstream.
	if _, err := resolveGoogleFunctionCallingMode([]ToolSchema{strict}, "auto", false); err == nil {
		t.Fatal("strict=require without model support must fail")
	}
	for in, want := range map[string]string{"auto": "AUTO", "none": "NONE", "any": "ANY", "": "AUTO", "x": "AUTO"} {
		if got := mapToolChoice(in); got != want {
			t.Errorf("mapToolChoice(%q) = %q, want %q", in, got, want)
		}
	}
}
