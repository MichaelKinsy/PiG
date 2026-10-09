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

// prepareExecution is the execution of one call: the command with its prefix, in the environment's working directory,
// then Prepare.
//
// Ports packages/durable/src/tools/bash.ts (prepareExecution).
func prepareExecution(ctx context.Context, command, commandPrefix string, prepare BashPrepare, api durable.ToolExecutionApi) (BashExecution, error) {
	executionEnv, err := requireEnv(api)
	if err != nil {
		return BashExecution{}, err
	}
	execution := BashExecution{Command: command, Cwd: executionEnv.Cwd(), Env: map[string]string{}, InheritEnv: true}
	if commandPrefix != "" {
		execution.Command = commandPrefix + "\n" + command
	}
	if prepare != nil {
		if err := prepare(ctx, &execution, api); err != nil {
			return BashExecution{}, err
		}
	}
	return execution, nil
}

// CreateBashTool creates the bash tool. It runs a command through the
// environment's shell. Its output streams to api.Output, where the Harness keeps
// the tail within the default limits; the result content is that retained
// output. The retained window goes to the environment, which may omit output
// outside it and report how much it omitted, so dropped counts stay exact.
// Output beyond the limits is spilled to a file whose path is reported as a
// diagnostic. A nonzero exit or timeout returns an error, which makes an error
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
			input, err := durable.FromJsonValue[BashToolInput](args)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if err := validateTimeout(input.Timeout); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			execution, err := prepareExecution(ctx, input.Command, options.CommandPrefix, options.Prepare, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{}, runCommand(ctx, []any{execution.Command}, execution, input.Timeout, api)
		},
	}
}

// runCommand runs each command form in turn until one starts, streaming output to api.Output within the retained window,
// and turns the result into the tool's outcome: a spill diagnostic, and an error for a failure or a nonzero exit. A
// program that could not start produced no output; the next form may start.
//
// Ports packages/durable/src/tools/bash.ts (runCommand).
func runCommand(ctx context.Context, commands []any, execution BashExecution, timeout *float64, api durable.ToolExecutionApi) error {
	executionEnv, err := requireEnv(api)
	if err != nil {
		return err
	}
	// upstream: bash.ts runCommand throws when no command form ran (an empty `programs` list).
	if len(commands) == 0 {
		return errors.New("No command to run")
	}
	var result env.ShellExecResult
	var execErr error
	var executionErr *env.ExecutionError
	var failedWithExecutionError bool
	for _, command := range commands {
		result, execErr = executionEnv.Exec(ctx, command, &env.ShellExecOptions{
			Cwd:        execution.Cwd,
			Env:        execution.Env,
			InheritEnv: &execution.InheritEnv,
			Timeout:    timeout,
			OnOutput: func(_ context.Context, text string, info env.ShellOutputInfo) {
				if info.Skipped != nil {
					api.Output(text, *info.Skipped)
					return
				}
				api.Output(text)
			},
			Spill: &env.ShellSpillOptions{AfterBytes: durable.DEFAULT_MAX_BYTES, AfterLines: durable.DEFAULT_MAX_LINES},
			// An environment may then omit output outside the retained tail and report the omission.
			Window: api.OutputWindow(),
		})
		executionErr, failedWithExecutionError = errors.AsType[*env.ExecutionError](execErr)
		if execErr == nil || !failedWithExecutionError || executionErr.Code != env.ExecutionErrorSpawnError {
			break
		}
	}
	spillPath := result.SpillPath
	if failedWithExecutionError {
		spillPath = executionErr.SpillPath
	}
	if spillPath != "" {
		api.Diagnostic(durable.ToolDiagnostic{Severity: durable.SeverityInfo, Code: "full_output", Message: "Full output: " + spillPath})
	}
	if execErr != nil {
		return bashFailure(ctx, execErr, executionErr, timeout)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("Command exited with code %d", result.ExitCode)
	}
	return nil
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

const powershellParameters = `{"type":"object","required":["command"],"properties":{"command":{"type":"string","description":"PowerShell command to execute"},"timeout":{"type":"number","description":"Timeout in seconds (optional, no default timeout)"}}}`

// PowerShellToolInput is the arguments of the powershell tool.
type PowerShellToolInput = BashToolInput

// PowerShellToolOptions configure the powershell tool.
type PowerShellToolOptions struct {
	// CommandPrefix is prepended, with a newline, to every command.
	CommandPrefix string
	Prepare       BashPrepare
	// Programs are the PowerShell programs to try in order; the default is pwsh, then powershell. A program that cannot
	// start is skipped.
	Programs []string
}

// utf8Output makes the output UTF-8 whatever the console's code page, as the coding agent's powershell tool does.
const utf8Output = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}"

var powershellArgs = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"}

// CreatePowerShellTool creates the powershell tool. It runs a PowerShell command like the bash tool, but through
// PowerShell instead of the environment's shell: pwsh (PowerShell 7), else Windows PowerShell, started directly with the
// command as an argument, so no other shell parses it.
//
// Ports packages/durable/src/tools/bash.ts (createPowerShellTool).
func CreatePowerShellTool(options *PowerShellToolOptions) *durable.ToolRegistration {
	if options == nil {
		options = &PowerShellToolOptions{}
	}
	programs := options.Programs
	if programs == nil {
		programs = []string{"pwsh", "powershell"}
	}
	description := fmt.Sprintf("Execute a PowerShell command in the current working directory. Returns combined stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.", durable.DEFAULT_MAX_LINES, durable.DEFAULT_MAX_BYTES/1024)
	return &durable.ToolRegistration{
		ToolSchema:   toolSchema("powershell", description, powershellParameters),
		OutputLimits: &durable.ToolOutputLimits{Retain: durable.RetainTail},
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			input, err := durable.FromJsonValue[PowerShellToolInput](args)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if err := validateTimeout(input.Timeout); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			execution, err := prepareExecution(ctx, input.Command, options.CommandPrefix, options.Prepare, api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			script := utf8Output + "\n" + execution.Command
			commands := make([]any, len(programs))
			for i, program := range programs {
				commands[i] = append(append([]string{program}, powershellArgs...), script)
			}
			return durable.ToolExecutionResult{}, runCommand(ctx, commands, execution, input.Timeout, api)
		},
	}
}
