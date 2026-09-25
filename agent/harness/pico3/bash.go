package pico3

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

// BashTool runs a shell command. Output is piped to the kernel; bounds come
// from output.
func BashTool(output *ToolOutput) *ToolDeclaration {
	if output == nil {
		output = &ToolOutput{}
	}
	return &ToolDeclaration{
		Name:        "bash",
		Description: "Run a shell command",
		Parameters: JsonObject{
			"type":       "object",
			"properties": JsonObject{"command": JsonObject{"type": "string"}, "cwd": JsonObject{"type": "string"}},
			"required":   []any{"command"},
		},
		Replay:  "unsafe",
		Output:  output,
		Execute: runBash,
	}
}

func runBash(ctx context.Context, args JsonValue, api *ToolApi) (ToolResult, error) {
	started := time.Now()
	input := asObject(args)
	// The command runs without ctx so abort maps to SIGKILL explicitly, as
	// upstream kills the child on abort.
	command := exec.Command("bash", "-c", str(input, "command"))
	command.Dir = str(input, "cwd")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return ToolResult{}, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return ToolResult{}, err
	}
	if err := command.Start(); err != nil {
		return ToolResult{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = command.Process.Kill() })
	defer stop()
	var readers sync.WaitGroup
	for _, pipe := range []io.Reader{stdout, stderr} {
		readers.Go(func() { pumpBash(pipe, api) })
	}
	readers.Wait()
	waitErr := command.Wait()
	details := JsonObject{"exitCode": nil, "signal": nil, "ms": float64(time.Since(started).Milliseconds())}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return ToolResult{}, waitErr
	}
	state := command.ProcessState
	if state.Exited() {
		details["exitCode"] = float64(state.ExitCode())
	} else if signal := signalName(state); signal != "" {
		details["signal"] = signal
	}
	return ToolResult{IsError: !state.Exited() || state.ExitCode() != 0, Details: details}, nil
}

func pumpBash(pipe io.Reader, api *ToolApi) {
	buffer := make([]byte, 32*1024)
	for {
		count, err := pipe.Read(buffer)
		if count > 0 {
			_ = api.Stream(append([]byte(nil), buffer[:count]...))
		}
		if err != nil {
			return
		}
	}
}
