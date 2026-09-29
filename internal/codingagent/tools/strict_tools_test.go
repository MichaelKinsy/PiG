package tools

import (
	"encoding/json"
	"slices"
	"testing"
)

// Ported from upstream test/builtin-tool-strict-mode.test.ts: read, bash,
// powershell, edit and write prefer strict sampling; grep, find and ls do not,
// and strictness does not change the execution schema.
func TestBuiltinToolsPreferStrictSampling(t *testing.T) {
	strict := []string{"read", "bash", "powershell", "edit", "write"}
	for _, tool := range CreateAllTools(t.TempDir(), nil, "") {
		s := tool.Schema()
		cs := s.ConstrainedSampling
		if slices.Contains(strict, tool.Name()) {
			if cs == nil || cs.Type != "json_schema" || cs.Strict != "prefer" {
				t.Errorf("%s constrained sampling = %+v, want json_schema/prefer", tool.Name(), cs)
			}
		} else if cs != nil {
			t.Errorf("%s constrained sampling = %+v, want none", tool.Name(), cs)
		}
		raw, err := json.Marshal(s.Parameters["required"])
		if err != nil {
			t.Fatal(err)
		}
		var required []string
		if err := json.Unmarshal(raw, &required); err != nil {
			t.Fatal(err)
		}
		switch tool.Name() {
		case "read":
			if !slices.Equal(required, []string{"path"}) {
				t.Errorf("read required = %v", required)
			}
		case "bash":
			if !slices.Equal(required, []string{"command"}) {
				t.Errorf("bash required = %v", required)
			}
		}
	}
}
