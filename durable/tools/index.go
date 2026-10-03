// Package tools is the coding tools of pi-durable: read, write, edit and bash
// over an execution environment. Nothing installs them automatically.
//
// Ports packages/durable/src/tools/index.ts.
package tools

import "github.com/MichaelKinsy/PiG/durable"

// CodingTools is the extension of read, write, edit, and bash.
var CodingTools = &durable.Extension{
	Name:  "coding-tools",
	Tools: []*durable.ToolRegistration{CreateReadTool(), CreateWriteTool(), CreateEditTool(), CreateBashTool(nil)},
}
