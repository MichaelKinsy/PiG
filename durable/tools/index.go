// Package tools is the coding tools of pi-durable: read, write, edit and bash
// over an execution environment. Nothing installs them automatically.
//
// Ports packages/durable/src/tools/index.ts.
package tools

import "github.com/MichaelKinsy/PiG/durable"

// CreateCodingTools is the coding-tools extension: read, write, edit, and bash; nothing installs it automatically.
// CreatePowerShellTool adds powershell. options.Images prepares the images read returns (see ReadToolOptions).
func CreateCodingTools(options *ReadToolOptions) *durable.Extension {
	return &durable.Extension{
		Name:  "coding-tools",
		Tools: []*durable.ToolRegistration{CreateReadTool(options), CreateWriteTool(), CreateEditTool(), CreateBashTool(nil)},
	}
}

// CodingTools is CreateCodingTools without an image processor.
var CodingTools = CreateCodingTools(nil)
