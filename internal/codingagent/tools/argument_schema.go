package tools

import (
	"encoding/json"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
)

// ArgumentSchema is the tool's parameters schema as the JSON text the argument validator reads (ai.ValidateToolArgumentsJSON), in the authored member order, which orders the validation errors
// as Pi's validator lists them. Schema remains the declaration for provider requests. Each tool's parameters are source constants (the bash and PowerShell variants differ only in
// guidelines), so the bytes are encoded once per tool.
func (t *ReadTool) ArgumentSchema() json.RawMessage       { return readArgumentSchema() }
func (t *BashTool) ArgumentSchema() json.RawMessage       { return bashArgumentSchema() }
func (t *PowerShellTool) ArgumentSchema() json.RawMessage { return powerShellArgumentSchema() }
func (t *EditTool) ArgumentSchema() json.RawMessage       { return editArgumentSchema() }
func (t *WriteTool) ArgumentSchema() json.RawMessage      { return writeArgumentSchema() }
func (t *GrepTool) ArgumentSchema() json.RawMessage       { return grepArgumentSchema() }
func (t *FindTool) ArgumentSchema() json.RawMessage       { return findArgumentSchema() }
func (t *LsTool) ArgumentSchema() json.RawMessage         { return lsArgumentSchema() }

var (
	readArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&ReadTool{}).Schema() })
	bashArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&BashTool{}).Schema() })
	powerShellArgumentSchema = argumentSchemaOnce(func() ai.ToolSchema { return (&PowerShellTool{}).Schema() })
	editArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&EditTool{}).Schema() })
	writeArgumentSchema      = argumentSchemaOnce(func() ai.ToolSchema { return (&WriteTool{}).Schema() })
	grepArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&GrepTool{}).Schema() })
	findArgumentSchema       = argumentSchemaOnce(func() ai.ToolSchema { return (&FindTool{}).Schema() })
	lsArgumentSchema         = argumentSchemaOnce(func() ai.ToolSchema { return (&LsTool{}).Schema() })
)

// argumentSchemaOnce encodes a tool's parameters once; each call returns its own copy of those bytes, so a caller may
// keep or edit them, at the cost of one copy instead of a schema clone and encode.
func argumentSchemaOnce(schema func() ai.ToolSchema) func() json.RawMessage {
	encoded := sync.OnceValue(func() json.RawMessage {
		parameters, err := schema().ParametersJSON()
		if err != nil {
			panic(err) // Parameters are source literals, not user input.
		}
		return parameters
	})
	return func() json.RawMessage { return slices.Clone(encoded()) }
}
