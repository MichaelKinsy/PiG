package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"runtime"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ─── PowerShell Tool ──────────────────────────────────────────────────────────

// PowerShellArgs mirrors upstream POWERSHELL_ARGS (utils/shell.ts): no
// profile, non-interactive, a process-local execution policy bypass, and the
// command as the final argument.
var PowerShellArgs = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"}

// powerShellUTF8OutputPrefix mirrors upstream UTF8_OUTPUT_PREFIX: every
// command first switches the console output encoding to UTF-8.
const powerShellUTF8OutputPrefix = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\n"

// PowerShellPromptSnippet mirrors upstream
// powershellToolSystemPromptContribution.snippet.
const PowerShellPromptSnippet = "Execute PowerShell commands"

// GetPowerShellConfig resolves PowerShell on Windows, preferring PowerShell 7
// (pwsh.exe) over Windows PowerShell (powershell.exe). Mirrors upstream
// getPowerShellConfig: on any other platform it reports that the tool is
// Windows-only.
func GetPowerShellConfig() (ShellConfig, error) {
	if runtime.GOOS != "windows" {
		return ShellConfig{}, errors.New("The powershell tool is only available on Windows.")
	}
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return ShellConfig{Path: path, Args: append([]string(nil), PowerShellArgs...)}, nil
		}
	}
	return ShellConfig{}, errors.New("No PowerShell executable found. Install PowerShell or add powershell.exe/pwsh.exe to PATH.")
}

// PowerShellTool executes PowerShell commands. It mirrors upstream
// tools/powershell.ts: the shared shell tool definition with the PowerShell
// config (name/label "powershell", shell name "PowerShell", prompt "PS>"),
// local operations that resolve PowerShell through GetPowerShellConfig and
// prefix each command with the UTF-8 output switch, and no command prefix or
// shell path setting (PowerShellToolOptions picks only operations,
// exposeSessionEnvironment, and spawnHook from BashToolOptions).
type PowerShellTool struct {
	CWD string
	// BinDir (<agentDir>/bin) is prepended to the command's PATH.
	BinDir string
	// HideSessionEnvironment turns off upstream's exposeSessionEnvironment.
	HideSessionEnvironment bool
}

func (t *PowerShellTool) Name() string  { return "powershell" }
func (t *PowerShellTool) Label() string { return "" }

func (t *PowerShellTool) Schema() ai.ToolSchema {
	return shellToolSchema("powershell", "PowerShell", !t.HideSessionEnvironment)
}

// ExecutionMode is parallel: upstream's shell tool definition sets no
// executionMode.
func (t *PowerShellTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

func (t *PowerShellTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return executeShellTool(ctx, t.CWD, shellToolConfig{
		name:           "powershell",
		shellName:      "PowerShell",
		tempFilePrefix: "pi-powershell",
		operations: &LocalShellOperations{
			ShellName:    "PowerShell",
			ResolveShell: GetPowerShellConfig,
			WrapCommand:  func(command string) string { return powerShellUTF8OutputPrefix + command },
		},
		exposeSessionEnvironment: !t.HideSessionEnvironment,
		binDir:                   t.BinDir,
	}, rawParams, onUpdate)
}
