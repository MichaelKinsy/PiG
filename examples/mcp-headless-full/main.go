// Command mcp-headless-full is the size baseline of examples/mcp-headless: it
// imports the full coding/mcpext package, which links the terminal UI through
// coding/extension. It only builds the extension and prints how many tools it
// registered (none).
package main

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

type host struct{}

func (host) RegisterTool(extension.ToolDefinition)          {}
func (host) RegisterTools([]extension.ToolDefinition)       {}
func (host) GetAllTools() []extension.ToolInfo              { return nil }
func (host) GetActiveTools() []string                       { return nil }
func (host) SetActiveTools([]string)                        {}
func (host) GetMcpServers() []extension.RegisteredMcpServer { return nil }

func main() {
	e := mcpext.NewHeadless(host{}, mcpext.Options{})
	fmt.Println(e != nil)
}
