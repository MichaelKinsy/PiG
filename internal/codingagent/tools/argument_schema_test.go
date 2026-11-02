package tools

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// A built-in validates its arguments against the parameters its Schema declares to the provider: ArgumentSchema is that schema's JSON text, in the declared member order, with no metadata beyond it.
func TestBuiltinArgumentSchemaIsTheDeclaredParameters(t *testing.T) {
	for _, tool := range []agent.AgentTool{&ReadTool{}, &BashTool{}, &EditTool{}, &WriteTool{}, &GrepTool{}, &FindTool{}, &LsTool{}, &PowerShellTool{}} {
		t.Run(tool.Name(), func(t *testing.T) {
			declared, err := tool.Schema().ParametersJSON()
			if err != nil {
				t.Fatal(err)
			}
			argument := tool.(interface{ ArgumentSchema() json.RawMessage }).ArgumentSchema()
			if !bytes.Equal(argument, declared) {
				t.Fatalf("ArgumentSchema = %s\ndeclared       = %s", argument, declared)
			}
			if strings.Contains(string(argument), "~kind") {
				t.Fatalf("argument schema contains TypeBox metadata: %s", argument)
			}
		})
	}
}
