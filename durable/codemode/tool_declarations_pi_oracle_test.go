package codemode

// pi: packages/durable/src/codemode/{codemode,models}.ts, packages/durable/src/tool-search/tool-search.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/toolsearch"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestToolDeclarationsMatchPi runs Pi's code mode tool, tool_search tool and model tools (built with pi-schema's builders
// since Pi 6536f801) under Node and compares what each declares to the model: the description, the parameter schema as
// text (so the member order is compared too, because the declaration is part of the provider request) and the structured
// output schema a program that calls the tool receives, as a value (a Go map has no member order).
func TestToolDeclarationsMatchPi(t *testing.T) {
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/tool_declarations_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected map[string]struct {
		Description            string         `json:"description"`
		Parameters             string         `json:"parameters"`
		StructuredOutputSchema map[string]any `json:"structuredOutputSchema"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	tools := append([]*durable.ToolRegistration{CreateTool(ToolOptions{}), toolsearch.CreateTool()}, createModelTools()...)
	if len(expected) != len(tools) {
		t.Fatalf("Pi declares %d tools, the test compares %d", len(expected), len(tools))
	}
	for _, tool := range tools {
		t.Run(tool.Name, func(t *testing.T) {
			want, found := expected[tool.Name]
			if !found {
				t.Fatalf("Pi declares no tool %q", tool.Name)
			}
			if tool.Description != want.Description {
				t.Errorf("description = %q\nPi: %q", tool.Description, want.Description)
			}
			whole, err := json.Marshal(tool.ToolSchema)
			if err != nil {
				t.Fatal(err)
			}
			var parts struct {
				Parameters json.RawMessage `json:"parameters"`
			}
			if err := json.Unmarshal(whole, &parts); err != nil {
				t.Fatal(err)
			}
			if string(parts.Parameters) != want.Parameters {
				t.Errorf("parameters = %s\nPi:         %s", parts.Parameters, want.Parameters)
			}
			if !reflect.DeepEqual(tool.StructuredOutputSchema, want.StructuredOutputSchema) {
				got, _ := json.Marshal(tool.StructuredOutputSchema)
				wantText, _ := json.Marshal(want.StructuredOutputSchema)
				t.Errorf("structuredOutputSchema = %s\nPi: %s", got, wantText)
			}
		})
	}
}
