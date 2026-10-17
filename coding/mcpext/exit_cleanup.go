package mcpext

import (
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Pi's stdio transport kills the process group of every running server from a process.once("exit") hook; Go's os.Exit runs no
// hooks, so the exits that skip the orderly session shutdown run this through tools.KillTrackedDetachedChildren.
func init() { tools.RegisterExitCleanup(mcp.KillLiveProcessGroups) }
