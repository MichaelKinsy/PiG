package cli

// Ports packages/coding-agent/src/cli/agent-dir-arg.ts (Pi dd71a6fc7).

import (
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ApplyAgentDirArg takes `--agent-dir <dir>` out of args and sets the agent directory environment
// variable to its resolved absolute path, so the resume command does not depend on the directory it runs in.
// Arguments after `--` are left untouched as messages.
func ApplyAgentDirArg(args []string) (rest []string, agentDir string, errMsg string) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if arg != "--agent-dir" {
			rest = append(rest, arg)
			continue
		}
		i++
		if i >= len(args) || args[i] == "" {
			return nil, "", "--agent-dir requires a directory"
		}
		agentDir = args[i]
	}
	if agentDir != "" {
		expanded := codingagent.ExpandTildePath(agentDir)
		abs, err := filepath.Abs(expanded)
		if err != nil {
			abs = expanded
		}
		_ = os.Setenv(codingagent.AgentDirEnvName(), abs)
	}
	return rest, agentDir, ""
}
