package tools

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Ports packages/durable/src/tools/bash.ts.

const maxTimeoutSeconds = 2_147_483_647 / 1000.0

const bashParameters = `{"type":"object","required":["command"],"properties":{"command":{"type":"string","description":"Bash command to execute"},"timeout":{"type":"number","description":"Timeout in seconds (optional, no default timeout)"}}}`

// BashToolInput is the arguments of the bash tool.
type BashToolInput struct {
	Command string `json:"command"`
	// Timeout is in seconds; nil has no timeout.
	Timeout *float64 `json:"timeout,omitempty"`
}

// BashExecution is the command a bash call is about to run; BashPrepare may
// change any of it.
type BashExecution struct {
	Command    string
	Cwd        string
	Env        map[string]string
	InheritEnv bool
}

// BashPrepare adjusts a bash execution before it runs, with the call's api. An
// error fails the call.
type BashPrepare func(ctx context.Context, execution *BashExecution, api durable.ToolExecutionApi) error

// BashToolOptions configure the bash tool.
type BashToolOptions struct {
	// CommandPrefix is prepended, with a newline, to every command.
	CommandPrefix string
	Prepare       BashPrepare
}

func validateTimeout(timeout *float64) error {
	if timeout == nil {
		return nil
	}
	if math.IsNaN(*timeout) || math.IsInf(*timeout, 0) || *timeout <= 0 {
		return errors.New("Invalid timeout: must be a finite number of seconds")
	}
	if *timeout > maxTimeoutSeconds {
		return fmt.Errorf("Invalid timeout: maximum is %s seconds", jsnumber.String(maxTimeoutSeconds))
	}
	return nil
}

// CreateBashTool creates the bash tool. It runs a command through the
// environment's shell. Its output streams to api.Output, where the Harness keeps
// the tail within the default limits; the result content is that retained
// output. Output beyond the limits is spilled to a file whose path is reported as
// a diagnostic. A nonzero exit or timeout returns an error, which makes an error
// result that still carries the output and diagnostics.
func CreateBashTool(options *BashToolOptions) *durable.ToolRegistration {
	if options == nil {
		options = &BashToolOptions{}
	}
	description := fmt.Sprintf("Execute a bash command in the current working directory. Returns combined stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.", durable.DEFAULT_MAX_LINES, durable.DEFAULT_MAX_BYTES/1024)
	return &durable.ToolRegistration{
		ToolSchema:   toolSchema("bash", description, bashParameters),
		OutputLimits: &durable.ToolOutputLimits{Retain: durable.RetainTail},
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return executeBash(ctx, options, args, api)
		},
	}
}

func executeBash(ctx context.Context, options *BashToolOptions, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	input, err := durable.FromJsonValue[BashToolInput](args)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	if err := validateTimeout(input.Timeout); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	executionEnv, err := requireEnv(api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	execution := BashExecution{Command: input.Command, Cwd: executionEnv.Cwd(), Env: map[string]string{}, InheritEnv: true}
	if options.CommandPrefix != "" {
		execution.Command = options.CommandPrefix + "\n" + input.Command
	}
	if options.Prepare != nil {
		if err := options.Prepare(ctx, &execution, api); err != nil {
			return durable.ToolExecutionResult{}, err
		}
	}
	result, execErr := executionEnv.Exec(ctx, execution.Command, &env.ShellExecOptions{
		Cwd:        execution.Cwd,
		Env:        execution.Env,
		InheritEnv: &execution.InheritEnv,
		Timeout:    input.Timeout,
		OnOutput:   func(_ context.Context, text string) { api.Output(text) },
		Spill:      &env.ShellSpillOptions{AfterBytes: durable.DEFAULT_MAX_BYTES, AfterLines: durable.DEFAULT_MAX_LINES},
	})
	executionErr, failedWithExecutionError := errors.AsType[*env.ExecutionError](execErr)
	spillPath := result.SpillPath
	if failedWithExecutionError {
		spillPath = executionErr.SpillPath
	}
	if spillPath != "" {
		api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Code: "full_output", Message: "Full output: " + spillPath})
	}
	if execErr != nil {
		return durable.ToolExecutionResult{}, bashFailure(ctx, execErr, executionErr, input.Timeout)
	}
	if result.ExitCode != 0 {
		return durable.ToolExecutionResult{}, fmt.Errorf("Command exited with code %d", result.ExitCode)
	}
	return durable.ToolExecutionResult{}, nil
}

// bashFailure is the error a failed command reports: the execution error itself
// when the call was aborted, otherwise a message about the timeout or abort.
func bashFailure(ctx context.Context, execErr error, executionErr *env.ExecutionError, timeout *float64) error {
	if executionErr == nil {
		return execErr
	}
	switch {
	case executionErr.Code == env.ExecutionErrorAborted && ctx.Err() != nil:
		return execErr
	case executionErr.Code == env.ExecutionErrorTimeout:
		seconds := "undefined"
		if timeout != nil {
			seconds = jsnumber.String(*timeout)
		}
		return fmt.Errorf("Command timed out after %s seconds", seconds)
	case executionErr.Code == env.ExecutionErrorAborted:
		return errors.New("Command aborted")
	}
	return execErr
}
