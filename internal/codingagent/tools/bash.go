package tools

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ─── Bash Tool ────────────────────────────────────────────────────────────────

// BashTool executes shell commands.
//
// Contract (mirrors upstream tools/bash.ts):
//   - One-shot spawn per call. cwd does NOT persist between calls.
//   - Resolved command = CommandPrefix + "\n" + command (when prefix set).
//   - Spawn via getShellConfig: settings shellPath, else /bin/bash, bash on
//     PATH, then sh (always with -c).
//   - Stream chunks: ANSI strip, binary sanitize, drop \r.
//   - Tempfile overflow at DEFAULT_MAX_BYTES (50 KB); rolling buffer 2x.
//   - On exit ≠ 0: append "Command exited with code N", return as IsError.
//     A signal-killed shell reports 128 + the signal number.
//   - On context cancel: append "Command aborted", return as IsError.
//   - Details: BashDetails{Truncation, FullOutputPath} on a truncated
//     success; error results carry none (upstream throws).
type BashTool struct {
	CWD string
	// Settings is consulted via GetShellConfig to resolve shellPath. nil
	// falls through to the platform default.
	Settings SettingsView
	// CommandPrefix prepended (with \n) to every command. Empty = no prefix.
	CommandPrefix string
	// BinDir (<agentDir>/bin) is prepended to the command's PATH, mirroring
	// upstream getShellEnv. Empty leaves PATH unchanged.
	BinDir string
	// HideSessionEnvironment turns off upstream's exposeSessionEnvironment:
	// commands then see no PI_* session variables, and the prompt omits the
	// guideline that mentions them.
	HideSessionEnvironment bool
}

func (t *BashTool) Name() string  { return "bash" }
func (t *BashTool) Label() string { return "" }

func (t *BashTool) Schema() ai.ToolSchema {
	return shellToolSchema("bash", "bash", !t.HideSessionEnvironment)
}

// ExecutionMode is parallel: upstream's shell tool definition sets no
// executionMode.
func (t *BashTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// Execute runs the command through the shared shell tool execution (upstream
// createShellToolDefinition with the bash config: getShellConfig-resolved
// bash, the settings command prefix, and pig-bash temp files).
func (t *BashTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return executeShellTool(ctx, t.CWD, shellToolConfig{
		name:                     "bash",
		shellName:                "bash",
		tempFilePrefix:           "pi-bash",
		operations:               &LocalShellOperations{ShellName: "bash", ResolveShell: func() (ShellConfig, error) { return GetShellConfig(t.Settings) }},
		commandPrefix:            t.CommandPrefix,
		exposeSessionEnvironment: !t.HideSessionEnvironment,
		binDir:                   t.BinDir,
	}, rawParams, onUpdate)
}
