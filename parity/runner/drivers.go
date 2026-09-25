//go:build parity

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Driver knows how to run one binary against one scenario.
// Implementations are registered in DriverRegistry.
type Driver interface {
	// Name is the value matched against Scenario.Driver.
	Name() string
	// Run executes one run of the scenario against the binary and returns
	// a Result. Errors that prevent the run (binary missing, tmux error)
	// go into Result.Err; assertion-level failures are evaluated later.
	Run(ctx context.Context, t *testing.T, bin BinaryRef, sc *Scenario) Result
}

// DriverRegistry maps Scenario.Driver to its implementation.
var DriverRegistry = map[string]Driver{
	"print-mode":        &printModeDriver{},
	"interactive-tmux":  &tmuxDriver{},
	"headless-terminal": &htDriver{},
	"cli-mode":          &cliModeDriver{},
	"rpc-mode":          &rpcModeDriver{},
	"extension-host":    &extensionHostDriver{},
}

// uniqueID returns a process-unique short ID for tmux sessions.
var counter atomic.Uint64

func uniqueID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano()%1_000_000, counter.Add(1))
}

// hermeticEnviron returns the host environment with ambient HTTP proxy
// settings removed and COLORTERM pinned to truecolor, so scenarios are not
// perturbed by a CI runner's proxy or color-depth. Applies to pig and pi
// identically.
func hermeticEnviron() []string {
	drop := map[string]bool{
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
		"http_proxy": true, "https_proxy": true, "no_proxy": true, "ALL_PROXY": true, "all_proxy": true,
	}
	out := make([]string, 0, len(os.Environ())+1)
	hasColor := false
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] {
			continue
		}
		if k == "COLORTERM" {
			kv = "COLORTERM=truecolor"
			hasColor = true
		}
		out = append(out, kv)
	}
	if !hasColor {
		out = append(out, "COLORTERM=truecolor")
	}
	return out
}

// runCmd executes a command and returns combined output + exit code.
// Used by drivers that need a one-shot subprocess (not tmux).
func runCmd(ctx context.Context, name string, args, env []string, timeout time.Duration, cwd string) (string, int, int64, error) {
	return runCmdWithInput(ctx, name, args, env, nil, 0, timeout, cwd, nil)
}

func runCmdWithInput(ctx context.Context, name string, args, env, inputLines []string, settle, timeout time.Duration, cwd string, outputFile *os.File) (string, int, int64, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(subCtx, name, args...)
	cmd.Env = append(hermeticEnviron(), env...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stdinPipe io.WriteCloser
	if len(inputLines) > 0 {
		var err error
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			return "", 0, 0, fmt.Errorf("stdin pipe: %w", err)
		}
	}

	t0 := time.Now()
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if outputFile != nil {
		cmd.Stdout, cmd.Stderr = outputFile, outputFile
	}
	var err error
	if stdinPipe == nil {
		err = cmd.Run()
	} else {
		if err = cmd.Start(); err == nil {
			writeDone := make(chan error, 1)
			go func() {
				for _, line := range inputLines {
					if _, err := io.WriteString(stdinPipe, line+"\n"); err != nil {
						_ = stdinPipe.Close()
						writeDone <- fmt.Errorf("write stdin: %w", err)
						return
					}
				}
				if settle > 0 {
					timer := time.NewTimer(settle)
					select {
					case <-timer.C:
					case <-subCtx.Done():
						timer.Stop()
						_ = stdinPipe.Close()
						writeDone <- subCtx.Err()
						return
					}
				}
				writeDone <- stdinPipe.Close()
			}()
			err = errors.Join(cmd.Wait(), <-writeDone)
		} else {
			_ = stdinPipe.Close()
		}
	}
	elapsed := time.Since(t0).Milliseconds()
	out := output.Bytes()
	if outputFile != nil {
		var readErr error
		out, readErr = os.ReadFile(outputFile.Name())
		if readErr != nil {
			return "", 0, elapsed, fmt.Errorf("read command output: %w", errors.Join(err, readErr))
		}
	}

	code := 0
	exitErr := &exec.ExitError{}
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
		err = nil // exit code is signalled separately, not as Go error
	}
	if subCtx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timed out after %s", timeout)
	}
	return string(out), code, elapsed, err
}
