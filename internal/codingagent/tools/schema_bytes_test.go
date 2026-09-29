package tools

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Compare actual Pi definitions, not a sorted JSON projection. Pi tools/index.ts:89-99 owns the built-in denominator; each tools/<name>.ts owns its declaration order.
func TestToolParameterBytesMatchPi(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `import {createAllTools} from './extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/tools/index.js'; for (const tool of Object.values(createAllTools(process.cwd()))) console.log(JSON.stringify({name:tool.name,parameters:tool.parameters}));`)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pi definitions: %v: %s", err, output)
	}
	expected := map[string]json.RawMessage{}
	for line := range bytes.Lines(output) {
		var tool struct {
			Name       string
			Parameters json.RawMessage
		}
		if err := json.Unmarshal(line, &tool); err != nil {
			t.Fatal(err)
		}
		expected[tool.Name] = tool.Parameters
	}
	for _, tool := range CreateAllTools(t.TempDir(), nil, "") {
		t.Run(tool.Name(), func(t *testing.T) {
			schema := tool.Schema()
			for _, declaration := range []ai.ToolSchema{schema, ai.ToToolDeclaration(schema)} {
				data, err := json.Marshal(declaration)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct{ Parameters json.RawMessage }
				if err := json.Unmarshal(data, &wire); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(wire.Parameters, expected[tool.Name()]) {
					t.Fatalf("parameters:\n got %s\nwant %s", wire.Parameters, expected[tool.Name()])
				}
			}
		})
		delete(expected, tool.Name())
	}
	if len(expected) != 0 {
		t.Fatalf("missing Pi tools: %v", expected)
	}
}
