package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/nodefs"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

// ─── PowerShell Tool ──────────────────────────────────────────────────────────

// PowerShellArgs mirrors upstream POWERSHELL_ARGS (utils/shell.ts): no
// profile, non-interactive, a process-local execution policy bypass, and the
// command as the final argument.
var PowerShellArgs = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"}

// powerShellUTF8OutputPrefix mirrors upstream UTF8_OUTPUT_PREFIX: every
// command first switches the console output encoding to UTF-8.
const powerShellUTF8OutputPrefix = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\n"

// powerShellWindowsExitSuffix mirrors upstream WINDOWS_POWERSHELL_EXIT_SUFFIX. Windows PowerShell 5.1 turns redirected native stderr into error
// records and sets $? to false, so `git push 2>&1 | Select-Object -Last 3` exits 1 although git exited 0 (#8842). PowerShell 7 does not. Exit 0
// when the last failure is only such a record and the native command exited 0; otherwise keep the -Command rule: 0 when the last statement
// succeeded, 1 when it failed. The lone `;` keeps an unfinished last statement, such as `Get-ChildItem |`, a parse error instead of continuing
// into the suffix and running.
const powerShellWindowsExitSuffix = "\n;\nif ($?) { exit 0 }\nif (($LASTEXITCODE -eq 0) -and (\"$($Error[0].FullyQualifiedErrorId)\" -like \"NativeCommandError*\")) { exit 0 }\nexit 1"

// powerShellCommand is the text createLocalPowerShellOperations runs for command in shell: the UTF-8 output switch, the command, and for
// powershell.exe the exit-code suffix. A final backtick would continue the user's last line into the suffix, so such a command is left unchanged.
func powerShellCommand(command string, shell ShellConfig) string {
	suffix := ""
	if strings.ToLower(nodepath.Win32Basename(shell.Path)) == "powershell.exe" && !strings.HasSuffix(command, "`") {
		suffix = powerShellWindowsExitSuffix
	}
	return powerShellUTF8OutputPrefix + command + suffix
}

// PowerShellPromptSnippet mirrors upstream
// powershellToolSystemPromptContribution.snippet.
const PowerShellPromptSnippet = "Execute PowerShell commands"

// GetPowerShellConfig resolves PowerShell on Windows, preferring PowerShell 7
// (pwsh.exe) over Windows PowerShell (powershell.exe). Mirrors upstream
// getPowerShellConfig: each executable is looked up on PATH, then PowerShell 7 in
// its ProgramFiles installation directory and Windows PowerShell in its SystemRoot
// one; on any other platform it reports that the tool is Windows-only.
func GetPowerShellConfig() (ShellConfig, error) {
	return powerShellConfigFor(runtime.GOOS, findExecutableOnPath, os.Getenv, nodefs.Access)
}

// findExecutableOnPath resolves a command name as Pi's findExecutableOnPath does (shellconfig), in the shape of exec.LookPath.
func findExecutableOnPath(name string) (string, error) {
	if path := shellconfig.FindExecutableOnPath(name); path != "" {
		return path, nil
	}
	return "", exec.ErrNotFound
}

// powerShellConfigFor is [GetPowerShellConfig] for a platform, a PATH lookup, an environment and an existence check. The
// installation directories may be on any drive and need not be on PATH, so they are joined with Windows path rules
// whatever the host.
func powerShellConfigFor(goos string, lookPath func(string) (string, error), getenv func(string) string, exists func(string) bool) (ShellConfig, error) {
	if goos != "windows" {
		return ShellConfig{}, errors.New("The powershell tool is only available on Windows.")
	}
	shell, err := lookPath("pwsh.exe")
	if err != nil {
		shell = ""
		if programFiles := getenv("ProgramFiles"); programFiles != "" {
			if pwsh7 := nodepath.Win32Join(programFiles, "PowerShell", "7", "pwsh.exe"); exists(pwsh7) {
				shell = pwsh7
			}
		}
	}
	if shell == "" {
		if shell, err = lookPath("powershell.exe"); err != nil {
			shell = ""
			if systemRoot := getenv("SystemRoot"); systemRoot != "" {
				if windowsPowerShell := nodepath.Win32Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"); exists(windowsPowerShell) {
					shell = windowsPowerShell
				}
			}
		}
	}
	if shell == "" {
		return ShellConfig{}, errors.New("No PowerShell executable found. Install PowerShell or add powershell.exe/pwsh.exe to PATH.")
	}
	return ShellConfig{Path: shell, Args: append([]string(nil), PowerShellArgs...)}, nil
}

// PowerShellTool executes PowerShell commands. It mirrors upstream
// tools/powershell.ts: the shared shell tool definition with the PowerShell
// config (name/label "powershell", shell name "PowerShell", prompt "PS>"),
// local operations that resolve PowerShell through GetPowerShellConfig and
// prefix each command with the UTF-8 output switch (and, for Windows PowerShell 5.1, append the exit-code suffix), and no command prefix or
// shell path setting (PowerShellToolOptions picks only operations,
// exposeSessionEnvironment, and spawnHook from BashToolOptions).
// PowerShellOperations is upstream PowerShellOperations: the same extension point as [BashOperations].
type PowerShellOperations = BashOperations

// NewLocalPowerShellOperations is upstream createLocalPowerShellOperations: local execution through PowerShell, each
// command prefixed with the UTF-8 output switch.
func NewLocalPowerShellOperations() *LocalShellOperations {
	return &LocalShellOperations{
		ShellName:    "PowerShell",
		ResolveShell: GetPowerShellConfig,
		WrapCommand:  powerShellCommand,
	}
}

type PowerShellTool struct {
	CWD string
	// Operations delegates command execution; nil selects [NewLocalPowerShellOperations]. Upstream
	// PowerShellToolOptions.operations.
	Operations PowerShellOperations
	// BinDir (<agentDir>/bin) is prepended to the command's PATH.
	BinDir string
	// HideSessionEnvironment turns off upstream's exposeSessionEnvironment.
	HideSessionEnvironment bool
	// SpawnHook adjusts the command, working directory or environment before execution (upstream spawnHook).
	SpawnHook PowerShellSpawnHook
}

func (t *PowerShellTool) Name() string  { return "powershell" }
func (t *PowerShellTool) Label() string { return "" }

// OutputSchema mirrors upstream bashOutputSchema: the structuredContent of a
// completed command.
func (t *PowerShellTool) OutputSchema() json.RawMessage { return json.RawMessage(shellOutputSchema) }

func (t *PowerShellTool) Schema() ai.ToolSchema {
	return shellToolSchema("powershell", "PowerShell", !t.HideSessionEnvironment)
}

// ExecutionMode is parallel: upstream's shell tool definition sets no
// executionMode.
func (t *PowerShellTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

func (t *PowerShellTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return executeShellTool(ctx, t.CWD, t.shellConfig(), rawParams, onUpdate)
}

func (t *PowerShellTool) operations() PowerShellOperations {
	if t.Operations != nil {
		return t.Operations
	}
	return NewLocalPowerShellOperations()
}

func (t *PowerShellTool) shellConfig() shellToolConfig {
	return shellToolConfig{
		name:                     "powershell",
		shellName:                "PowerShell",
		tempFilePrefix:           "pi-powershell",
		operations:               t.operations(),
		exposeSessionEnvironment: !t.HideSessionEnvironment,
		binDir:                   t.BinDir,
		spawnHook:                t.SpawnHook,
	}
}
