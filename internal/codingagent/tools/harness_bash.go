package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

// BashExecution is the mutable shell request passed to Prepare.
type BashExecution struct {
	Command    string
	Cwd        string
	Env        map[string]string
	InheritEnv bool
}

// BashToolOptions configures a harness bash tool.
type BashToolOptions struct {
	CommandPrefix string
	Prepare       func(context.Context, *BashExecution, any) error
}

// HarnessBashToolDetails describes truncated shell output.
type HarnessBashToolDetails struct {
	Truncation     *harness.ShellOutputTruncation `json:"truncation,omitempty"`
	FullOutputPath string                         `json:"fullOutputPath,omitempty"`
}

// Ports packages/agent/src/harness/tools/bash.ts.
// CreateBashTool delegates execution and bounded output capture to the turn's
// environment. Distinct progress snapshots request checkpoints at most once
// every two seconds; callbacks after execution settles are ignored.
func CreateBashTool(options *BashToolOptions) *harness.AgentHarnessTool {
	schema := shellToolSchema("bash", "bash", false)
	schema.Description = fmt.Sprintf("Execute a bash command in the current working directory. Returns combined stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.", DefaultMaxLines, DefaultMaxBytes/1024)
	schema.Parameters["properties"].(map[string]any)["command"].(map[string]any)["description"] = "Bash command to execute"
	schema.ConstrainedSampling = nil
	return &harness.AgentHarnessTool{ToolSchema: schema, Label: "bash", Execute: func(ctx context.Context, _ string, params map[string]any, onUpdate harness.AgentHarnessToolUpdateCallback, toolContext any, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
		var p shellParams
		if err := harnessParams(params, &p); err != nil {
			return harness.AgentToolResult{}, err
		}
		if p.Timeout != nil {
			if math.IsNaN(*p.Timeout) || math.IsInf(*p.Timeout, 0) || *p.Timeout <= 0 {
				return harness.AgentToolResult{}, errors.New("Invalid timeout: must be a finite number of seconds")
			}
			// upstream: packages/agent/src/harness/tools/bash.ts:MAX_TIMEOUT_SECONDS
			if *p.Timeout > 2_147_483_647.0/1000 {
				return harness.AgentToolResult{}, errors.New("Invalid timeout: maximum is 2147483.647 seconds")
			}
		}
		env, err := harnessToolEnv(toolContext)
		if err != nil {
			return harness.AgentToolResult{}, err
		}
		execution := BashExecution{Command: p.Command, Cwd: env.Cwd(), Env: map[string]string{}, InheritEnv: true}
		if options != nil {
			if options.CommandPrefix != "" {
				execution.Command = options.CommandPrefix + "\n" + execution.Command
			}
			if options.Prepare != nil {
				if err := options.Prepare(ctx, &execution, toolContext); err != nil {
					return harness.AgentToolResult{}, err
				}
			}
		}
		return executeHarnessBash(ctx, env, execution, p.Timeout, onUpdate)
	}}
}
func executeHarnessBash(ctx context.Context, env harness.ExecutionEnv, execution BashExecution, timeout *float64, onUpdate harness.AgentHarnessToolUpdateCallback) (harness.AgentToolResult, error) {
	var mu sync.Mutex
	var view *harness.ShellOutputView
	accepting := true
	lastCheckpointAt := time.Now()
	lastCheckpoint := ""
	if onUpdate != nil {
		onUpdate(harness.AgentToolResult{Content: []ai.ToolResultMessageContent{}}, harness.AgentHarnessToolUpdateOptions{})
	}
	result, err := env.Exec(ctx, execution.Command, &harness.ShellExecOptions{Cwd: execution.Cwd, Env: execution.Env, InheritEnv: new(execution.InheritEnv), Timeout: timeout, Capture: &harness.ShellOutputCaptureOptions{Limits: harness.ShellOutputLimits{MaxBytes: DefaultMaxBytes, MaxLines: DefaultMaxLines, Retain: harness.ShellOutputRetainTail}, Spill: true}, OnUpdate: func(_ context.Context, update harness.ShellOutputUpdate) error {
		mu.Lock()
		defer mu.Unlock()
		if !accepting {
			return nil
		}
		next := ApplyShellOutputUpdate(view, update)
		view = &next
		snapshot := harnessText(view.Text)
		details := &HarnessBashToolDetails{FullOutputPath: view.SpillPath}
		if view.Truncation.Truncated {
			tr := view.Truncation
			details.Truncation = &tr
		}
		snapshot.Details = details
		now := time.Now()
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		// upstream: packages/agent/src/harness/tools/bash.ts:BASH_CHECKPOINT_INTERVAL_MS
		checkpoint := now.Sub(lastCheckpointAt) >= 2_000*time.Millisecond && string(encoded) != lastCheckpoint
		if onUpdate != nil {
			onUpdate(snapshot, harness.AgentHarnessToolUpdateOptions{Checkpoint: checkpoint})
		}
		if checkpoint {
			lastCheckpointAt = now
			lastCheckpoint = string(encoded)
		}
		return nil
	}})
	mu.Lock()
	accepting = false
	output := ""
	var capture *harness.ShellOutputMetadata
	if view != nil {
		output = view.Text
		metadata := view.ShellOutputMetadata
		capture = &metadata
	}
	if err == nil {
		capture = &result.ShellOutputMetadata
	}
	mu.Unlock()
	var details *HarnessBashToolDetails
	if capture != nil && capture.Truncation.Truncated {
		tr := capture.Truncation
		details = &HarnessBashToolDetails{Truncation: &tr, FullOutputPath: capture.SpillPath}
		end := tr.TotalLines
		start := end - tr.OutputLines + 1
		switch {
		case tr.LastLinePartial:
			lastLineBytes := tr.OutputBytes
			if capture.LastLineBytes != nil {
				lastLineBytes = *capture.LastLineBytes
			}
			output += fmt.Sprintf("\n\n[Showing last %s of line %d (line is %s). Full output: %s]", FormatSize(tr.OutputBytes), end, FormatSize(lastLineBytes), capture.SpillPath)
		case tr.TruncatedBy != nil && *tr.TruncatedBy == "lines":
			output += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Full output: %s]", start, end, tr.TotalLines, capture.SpillPath)
		default:
			output += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]", start, end, tr.TotalLines, FormatSize(DefaultMaxBytes), capture.SpillPath)
		}
	}
	if err != nil {
		status := err.Error()
		if executionError, ok := errors.AsType[*harness.ExecutionError](err); ok {
			switch executionError.Code {
			case harness.ExecutionErrorTimeout:
				seconds := "undefined"
				if timeout != nil {
					seconds = jsNumber(*timeout)
				}
				status = "Command timed out after " + seconds + " seconds"
			case harness.ExecutionErrorAborted:
				status = "Command aborted"
			}
		}
		return harness.AgentToolResult{}, &harnessToolFailure{message: appendShellStatus(output, status), cause: err}
	}
	if result.ExitCode != 0 {
		return harness.AgentToolResult{}, fmt.Errorf("%s", appendShellStatus(output, fmt.Sprintf("Command exited with code %d", result.ExitCode)))
	}
	if output == "" {
		output = "(no output)"
	}
	out := harnessText(output)
	if details != nil {
		out.Details = details
	}
	return out, nil
}

type harnessToolFailure struct {
	message string
	cause   error
}

func (err *harnessToolFailure) Error() string { return err.message }
func (err *harnessToolFailure) Unwrap() error { return err.cause }
