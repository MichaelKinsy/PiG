//go:build pig_strip_mcp

package cli

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// runMcpCommand reports that a build without built-in MCP has no `pig mcp` command, and returns -1 when args are not an `mcp`
// command.
// pig additive (D92): `pig mcp` in a Piglet Binary without MCP fails with the strip message instead of becoming a prompt.
func runMcpCommand(args []string) int {
	if len(args) == 0 || args[0] != "mcp" {
		return -1
	}
	fmt.Fprintln(os.Stderr, pigstrip.Error("pig mcp", pigstrip.ListExtensions, "mcp"))
	return 1
}
