package tools

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// The differential matrix in agent uses Pi's schemas. Check that each real
// built-in supplies those same validation types and kinds, without leaking
// non-enumerable metadata into the schema sent to the provider.
func TestBuiltinArgumentSchemasMatchPi(t *testing.T) {
	data, err := os.ReadFile("../../../agent/testdata/tool-validation.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schemas []json.RawMessage `json:"schemas"`
		Cases   []struct {
			Name   string `json:"name"`
			Tool   string `json:"tool"`
			Schema int    `json:"schema"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []agent.AgentTool{&ReadTool{}, &BashTool{}, &EditTool{}, &WriteTool{}, &GrepTool{}, &FindTool{}, &LsTool{}, &PowerShellTool{}} {
		t.Run(tool.Name(), func(t *testing.T) {
			raw := tool.(interface{ ArgumentSchema() json.RawMessage }).ArgumentSchema()
			var actual map[string]any
			if err := json.Unmarshal(raw, &actual); err != nil {
				t.Fatal(err)
			}
			for _, tc := range fixture.Cases {
				if tc.Name != tool.Name()+"/ordinary" {
					continue
				}
				var expected map[string]any
				if err := json.Unmarshal(fixture.Schemas[tc.Schema], &expected); err != nil {
					t.Fatal(err)
				}
				assertArgumentSchemaTypes(t, actual, expected)
				encoded, err := json.Marshal(tool.Schema())
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(encoded), "~kind") {
					t.Fatalf("provider schema contains TypeBox metadata: %s", encoded)
				}
				return
			}
			t.Fatal("Pi schema is absent from oracle")
		})
	}
}

func assertArgumentSchemaTypes(t *testing.T, actual, expected map[string]any) {
	t.Helper()
	for _, key := range []string{"type", "~kind", "required"} {
		if !reflect.DeepEqual(actual[key], expected[key]) {
			t.Fatalf("schema %s = %v, want %v", key, actual[key], expected[key])
		}
	}
	if properties, ok := expected["properties"].(map[string]any); ok {
		for key, child := range properties {
			got := actual["properties"].(map[string]any)[key]
			if got == nil {
				t.Fatalf("property %s absent", key)
			}
			assertArgumentSchemaTypes(t, got.(map[string]any), child.(map[string]any))
		}
	}
	if items, ok := expected["items"].(map[string]any); ok {
		assertArgumentSchemaTypes(t, actual["items"].(map[string]any), items)
	}
}
