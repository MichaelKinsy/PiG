package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// shellParams is the shell tool input (upstream bashSchema): a command and an
// optional timeout in seconds. Timeout is a pointer because upstream rejects
// an explicit zero or negative timeout while an omitted one means none.
type shellParams struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout"`
}

// shellToolConfig mirrors upstream ShellToolConfig plus the options
// createShellToolDefinition receives.
type shellToolConfig struct {
	name           string
	shellName      string
	tempFilePrefix string
	// operations executes the command (upstream options.operations, default
	// createLocalShellOperations).
	operations BashOperations
	// commandPrefix is prepended with "\n" to every command.
	commandPrefix string
	// exposeSessionEnvironment mirrors upstream exposeSessionEnvironment.
	exposeSessionEnvironment bool
	// binDir is prepended to PATH (upstream getShellEnv's getBinDir).
	binDir string
}

// shellToolSchema mirrors createShellToolDefinition's name, description,
// parameters, guidelines, and constrained sampling request.
func shellToolSchema(name, shellName string, exposeSessionEnvironment bool) ai.ToolSchema {
	var guidelines []string
	if exposeSessionEnvironment {
		guidelines = []string{sessionGuideline}
	}
	return ai.ToolSchema{
		Name:             name,
		PromptGuidelines: guidelines,
		Description: "Execute a " + shellName + " command in the current working directory. Returns stdout and stderr. " +
			"Output is truncated to last " + strconv.Itoa(DefaultMaxLinesUpstream) + " lines or " +
			strconv.Itoa(DefaultMaxBytesUpstream/1024) + "KB (whichever is hit first). " +
			"If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute",
				},
				"timeout": map[string]any{
					"type":        "number",
					"description": "Timeout in seconds (optional, no default timeout)",
				},
			},
			"required": []string{"command"},
		},
		ConstrainedSampling: strictToolSampling(),
	}
}

// strictToolSampling mirrors the constrainedSampling upstream's read, bash,
// powershell, edit and write definitions request: { type: "json_schema",
// strict: "prefer" }.
func strictToolSampling() *ai.ConstrainedSamplingConfig {
	return &ai.ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}
}

// FormatJSNumber mirrors JavaScript String(number) for a finite number.
func FormatJSNumber(v float64) string { return jsNumber(v) }

// jsNumber mirrors JavaScript String(number) for the finite values a JSON
// timeout carries.
func jsNumber(v float64) string {
	if abs := math.Abs(v); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		mantissa, exp, _ := strings.Cut(strconv.FormatFloat(v, 'e', -1, 64), "e")
		return mantissa + "e" + exp[:1] + strings.TrimLeft(exp[1:], "0")
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// shellToolError is a failed shell tool call. Upstream throws, and the agent
// loop turns the message into an error result without details.
func shellToolError(message string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: message, IsError: true}
}

// appendShellStatus mirrors upstream appendStatus.
func appendShellStatus(text, status string) string {
	if text == "" {
		return status
	}
	return text + "\n\n" + status
}

// executeShellTool mirrors upstream createShellToolDefinition.execute: an
// initial empty update, then one operations.exec call whose raw output feeds
// an OutputAccumulator, with throttled streaming updates and upstream's
// abort, timeout, and exit-code statuses.
func executeShellTool(ctx context.Context, cwd string, cfg shellToolConfig, rawParams json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p shellParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("%s: invalid params: %w", cfg.name, err)
	}
	command := p.Command
	if cfg.commandPrefix != "" {
		command = cfg.commandPrefix + "\n" + command
	}
	env := sessionEnvironment(ctx, cfg.exposeSessionEnvironment, cfg.binDir)
	output := NewOutputAccumulator(cfg.tempFilePrefix)
	var updates *shellUpdateScheduler
	if onUpdate != nil {
		updates = newShellUpdateScheduler(output, onUpdate)
		// The initial empty update creates the streaming card before any
		// output arrives.
		onUpdate("", nil)
	}
	finishOutput := func() OutputSnapshot {
		output.Finish()
		if updates != nil {
			updates.finish()
		}
		snapshot := output.Snapshot(true)
		_ = output.CloseTempFile()
		return snapshot
	}

	result, err := cfg.operations.Exec(ctx, command, cwd, BashOperationsExecOptions{
		OnData: func(data []byte) {
			output.Append(data)
			if updates != nil {
				updates.schedule()
			}
		},
		Timeout: p.Timeout,
		Env:     env,
	})
	if err != nil {
		snapshot := finishOutput()
		text, _ := formatShellOutput(snapshot, output.LastLineBytes(), "")
		switch msg := err.Error(); {
		case msg == "aborted":
			return shellToolError(appendShellStatus(text, "Command aborted")), nil
		case strings.HasPrefix(msg, "timeout:"):
			return shellToolError(appendShellStatus(text, "Command timed out after "+strings.TrimPrefix(msg, "timeout:")+" seconds")), nil
		default:
			return shellToolError(msg), nil
		}
	}
	snapshot := finishOutput()
	text, details := formatShellOutput(snapshot, output.LastLineBytes(), "(no output)")
	if result.ExitCode == nil {
		return shellToolError(appendShellStatus(text, "Command terminated without an exit code")), nil
	}
	if *result.ExitCode != 0 {
		return shellToolError(appendShellStatus(text, fmt.Sprintf("Command exited with code %d", *result.ExitCode))), nil
	}
	var resultDetails any
	if details != nil {
		resultDetails = details
	}
	return agent.AgentToolResult{Content: text, Details: resultDetails}, nil
}

// formatShellOutput mirrors upstream formatOutput: the output (or emptyText),
// plus the truncation notice naming the full-output file when truncated.
func formatShellOutput(snapshot OutputSnapshot, lastLineBytes int, emptyText string) (string, *BashDetails) {
	text := snapshot.Content
	if text == "" {
		text = emptyText
	}
	tr := snapshot.Truncation
	if !tr.Truncated {
		return text, nil
	}
	details := &BashDetails{Truncation: &tr, FullOutputPath: snapshot.FullOutputPath}
	startLine := tr.TotalLines - tr.OutputLines + 1
	endLine := tr.TotalLines
	switch {
	case tr.LastLinePartial:
		text += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
			FormatSize(tr.OutputBytes), endLine, FormatSize(lastLineBytes), snapshot.FullOutputPath)
	case tr.TruncatedBy == "lines":
		text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]",
			startLine, endLine, tr.TotalLines, snapshot.FullOutputPath)
	default:
		text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
			startLine, endLine, tr.TotalLines, FormatSize(DefaultMaxBytesUpstream), snapshot.FullOutputPath)
	}
	return text, details
}
