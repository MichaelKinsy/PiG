package tools

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

func declaredParameters(t *testing.T, schema string) map[string]any {
	t.Helper()
	var parameters map[string]any
	if err := json.Unmarshal([]byte(schema), &parameters); err != nil {
		t.Fatal(err)
	}
	return parameters
}

// declaredParametersText is the parameter schema as the core records and compares a declaration: the core compares declarations as JSON text
// (driver.parametersJSON), so the member order of TypeBox's schema is part of what Pi declares, and a map comparison cannot see it.
func declaredParametersText(t *testing.T, tool *durable.ToolRegistration, want string) {
	t.Helper()
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
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(want)); err != nil {
		t.Fatal(err)
	}
	if string(parts.Parameters) != compact.String() {
		t.Fatalf("declared parameters = %s\nwant %s", parts.Parameters, compact.String())
	}
}

// upstream: packages/durable/src/tools/write.ts:9-12 (writeSchema) and :17-21 (createWriteTool), edit.ts:19-35 (replaceEditSchema, editSchema) and :90-96 (createEditTool),
// bash.ts:10-17 (bashSchema, powershellSchema) and :124-133, :148-155 (createBashTool, createPowerShellTool): each tool is declared to the model with Pi's name, description
// and parameter schema (descriptions included; DEFAULT_MAX_LINES 2000 and DEFAULT_MAX_BYTES / 1024 = 50 fill the shell tools' template), and the shell tools retain the tail of
// their output (`outputLimits: { retain: "tail" }`).
// mutation-checked: a changed word in every description, a moved schema member (TypeBox order: type, required, properties, then the properties in declaration order), a dropped `required` member, a changed property type or description, a renamed tool and the retain mode each fail it.
func TestWriteEditAndShellToolDeclarationsMatchUpstream(t *testing.T) {
	const shellTail = "Returns combined stdout and stderr. Output is truncated to last 2000 lines or 50KB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds."
	for _, test := range []struct {
		tool        *durable.ToolRegistration
		name        string
		description string
		parameters  string
		retainTail  bool
	}{
		{CreateWriteTool(), "write", "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
			`{"type":"object","required":["path","content"],"properties":{"path":{"type":"string","description":"Path to the file to write (relative or absolute)"},"content":{"type":"string","description":"Content to write to the file"}}}`, false},
		{CreateEditTool(), "edit", "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
			`{"type":"object","required":["path","edits"],"properties":{"path":{"type":"string","description":"Path to the file to edit (relative or absolute)"},"edits":{"type":"array","items":{"type":"object","required":["oldText","newText"],"properties":{"oldText":{"type":"string","description":"Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},"newText":{"type":"string","description":"Replacement text for this targeted edit."}}},"description":"One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."}}}`, false},
		{CreateBashTool(nil), "bash", "Execute a bash command in the current working directory. " + shellTail,
			`{"type":"object","required":["command"],"properties":{"command":{"type":"string","description":"Bash command to execute"},"timeout":{"type":"number","description":"Timeout in seconds (optional, no default timeout)"}}}`, true},
		{CreatePowerShellTool(nil), "powershell", "Execute a PowerShell command in the current working directory. " + shellTail,
			`{"type":"object","required":["command"],"properties":{"command":{"type":"string","description":"PowerShell command to execute"},"timeout":{"type":"number","description":"Timeout in seconds (optional, no default timeout)"}}}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.tool.Name != test.name {
				t.Fatalf("name = %q, want %q", test.tool.Name, test.name)
			}
			if test.tool.Description != test.description {
				t.Fatalf("description = %q\nwant %q", test.tool.Description, test.description)
			}
			if want := declaredParameters(t, test.parameters); !reflect.DeepEqual(test.tool.Parameters, want) {
				got, _ := json.Marshal(test.tool.Parameters)
				t.Fatalf("parameters = %s\nwant %v", got, want)
			}
			declaredParametersText(t, test.tool, test.parameters)
			if retainsTail := test.tool.OutputLimits != nil && test.tool.OutputLimits.Retain == durable.RetainTail; retainsTail != test.retainTail {
				t.Fatalf("retains the tail = %t, want %t (outputLimits %+v)", retainsTail, test.retainTail, test.tool.OutputLimits)
			}
		})
	}
}
