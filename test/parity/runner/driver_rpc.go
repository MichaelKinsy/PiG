//go:build parity

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// rpcModeDriver sends commands to a real RPC process and retains complete stdout and stderr. Steps wait only for new complete output; teardown either closes stdin or sends SIGTERM while stdin stays open.
type rpcModeDriver struct{}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (rpcModeDriver) Name() string { return "rpc-mode" }

func (rpcModeDriver) Run(ctx context.Context, t *testing.T, bin BinaryRef, sc *Scenario) Result {
	t.Helper()

	tc := newTokenContext(t, "rpc-"+sc.Name+"-"+bin.Label)
	preserveAuth, injectAuth := scenarioAuthModes(sc)
	pigEnv, piEnv := snapshotAgentDirs(t, sc.SourcePath, sc.Env.Pig, sc.Env.Pi, preserveAuth, injectAuth)

	args := []string{}
	if !sc.Env.OverrideBaseArgs {
		args = append(args, bin.Args...)
	}

	// BinaryRef carries fallback PIG_HOME/PI_CODING_AGENT_DIR sandboxes shared by
	// the suite. Snapshot those base directories for every run as well as any
	// scenario overrides: Pi 0.84 RPC disposal leaves per-home runtime state, so
	// reusing the fallback across durability pairs can hang the next process at
	// EOF and erase its output.
	baseEnv := snapshotBinaryEnv(t, sc.SourcePath, bin.Env, preserveAuth, injectAuth)
	env := append([]string{}, baseEnv...)
	switch bin.Label {
	case "pig":
		env = append(env, tc.expandSlice(resolveScenarioEnvVars(sc.SourcePath, pigEnv))...)
		args = append(args, tc.expandSlice(sc.Env.PigArgs)...)
		for _, ext := range sc.Env.PigExtensions {
			resolved := resolveExtensionPath(sc.SourcePath, ext)
			args = append(args, "-e", resolved)
		}
	case "pi":
		env = append(env, tc.expandSlice(resolveScenarioEnvVars(sc.SourcePath, piEnv))...)
		args = append(args, tc.expandSlice(sc.Env.PiArgs)...)
		for _, ext := range sc.Env.PiExtensions {
			resolved := resolveExtensionPath(sc.SourcePath, ext)
			args = append(args, "-e", resolved)
		}
	}

	// Append --mode rpc. The binary's base args (e.g. pi's --no-extensions)
	// go before --mode rpc; per-binary scenario args (PigArgs/PiArgs)
	// were already appended above.
	args = append(args, "--mode", "rpc")
	if sc.Model != "" {
		args = append(args, "--model", sc.Model)
	}

	timeout := time.Duration(sc.RPC.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	sourceCWD := resolveScenarioCWD(sc.SourcePath, tc.expand(sc.RPC.CWD))
	if sourceCWD == "" {
		sourceCWD = defaultCWDFixture()
	}
	cwd, err := snapshotCWD(t, sourceCWD)
	if err != nil {
		return Result{Err: fmt.Errorf("snapshot cwd: %w", err)}
	}

	env = configureProviderFixture(t, sc, env, cwd, tc.tempRoot)
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(subCtx, bin.Path, args...)
	cmd.Env = append(hermeticEnviron(), env...)
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Use a pipe for stdin so we can control when it closes.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return Result{Err: fmt.Errorf("stdin pipe: %w", err)}
	}

	if len(sc.RPC.InputLines) > 0 && len(sc.RPC.Steps) > 0 {
		return Result{Err: errors.New("rpc input_lines and steps are mutually exclusive")}
	}

	// Capture stdout and stderr separately.
	var stdout synchronizedBuffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t0 := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{Err: fmt.Errorf("start: %w", err)}
	}

	// Write input lines to stdin. Steps wait for an observable output barrier
	// before sending the next command, which makes cancellation probes depend on
	// protocol state instead of timing guesses.
	var inputErr error
	writeLine := func(line string) error {
		expanded := tc.expand(line)
		if strings.Contains(expanded, "{{RPC_REQUEST_ID}}") {
			requestID, err := latestRPCRequestID(stdout.String())
			if err != nil {
				return err
			}
			expanded = strings.ReplaceAll(expanded, "{{RPC_REQUEST_ID}}", requestID)
		}
		_, err := io.WriteString(stdinPipe, expanded+"\n")
		return err
	}
	if len(sc.RPC.Steps) > 0 {
		for _, step := range sc.RPC.Steps {
			start := len(stdout.String())
			if err := writeLine(step.Line); err != nil {
				inputErr = fmt.Errorf("write stdin: %w", err)
				break
			}
			if err := waitForRPCOutput(subCtx, &stdout, start, step.WaitContains, step.WaitEvent, step.WaitTimeoutSeconds); err != nil {
				inputErr = err
				break
			}
		}
	} else {
		for _, line := range sc.RPC.InputLines {
			if err := writeLine(line); err != nil {
				inputErr = fmt.Errorf("write stdin: %w", err)
				break
			}
		}
	}

	// If settle_seconds > 0, wait before closing stdin. This gives
	// the binary time to process async commands (like prompt) and
	// emit events before stdin EOF triggers shutdown.
	if sc.RPC.SettleSeconds > 0 {
		timer := time.NewTimer(time.Duration(sc.RPC.SettleSeconds) * time.Second)
		select {
		case <-timer.C:
		case <-subCtx.Done():
			timer.Stop()
			inputErr = errors.Join(inputErr, subCtx.Err())
		}
	}

	if sc.RPC.Terminate {
		inputErr = errors.Join(inputErr, cmd.Process.Signal(syscall.SIGTERM))
	} else {
		inputErr = errors.Join(inputErr, stdinPipe.Close())
	}

	// Wait for process exit.
	waitErr := cmd.Wait()
	elapsed := time.Since(t0).Milliseconds()

	code := 0
	exitErr := &exec.ExitError{}
	if errors.As(waitErr, &exitErr) {
		code = exitErr.ExitCode()
		waitErr = nil
	}
	if subCtx.Err() == context.DeadlineExceeded {
		waitErr = fmt.Errorf("timed out after %s", timeout)
	}
	waitErr = errors.Join(waitErr, inputErr)

	output := stdout.String()
	if sc.RPC.CanonicalJSON {
		var canonicalErr error
		output, canonicalErr = canonicalJSONL(output)
		waitErr = errors.Join(waitErr, canonicalErr)
	}

	if waitErr != nil && stderr.Len() > 0 {
		waitErr = fmt.Errorf("%w\nstderr: %s", waitErr, stderr.String())
	}

	if output == "" && stderr.Len() > 0 {
		t.Logf("[rpc-mode %s/%s] no stdout; stderr: %s", sc.Name, bin.Label, stderr.String())
	}

	result := Result{
		RawOutput:      stdout.String(),
		Output:         output,
		Stderr:         stderr.String(),
		StderrCaptured: true,
		IdentityRoots:  resultIdentityRoots(cwd, tc.tempRoot, env),
		ExitCode:       code,
		RuntimeMs:      elapsed,
		Err:            waitErr,
	}
	if sc.RPC.ArtifactPath != "" {
		path := resolveScenarioCWD(sc.SourcePath, tc.expand(sc.RPC.ArtifactPath))
		data, err := os.ReadFile(path)
		result.Artifact, result.ArtifactErr = string(data), err
	}
	return result
}

func canonicalJSONL(output string) (string, error) {
	var lines []string
	terminated := strings.HasSuffix(output, "\n")
	for i, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		var value any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("JSONL record %d: %w", i+1, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return "", fmt.Errorf("JSONL record %d has trailing data", i+1)
		}
		data, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		lines = append(lines, string(data))
	}
	result := strings.Join(lines, "\n")
	if terminated {
		result += "\n"
	}
	return result, nil
}

func latestRPCRequestID(output string) (string, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var event struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal([]byte(lines[i]), &event) == nil && event.Type == "extension_ui_request" && event.ID != "" {
			return event.ID, nil
		}
	}
	return "", errors.New("rpc output has no extension_ui_request id")
}

func eventContains(record, expected map[string]any) bool {
	for key, value := range expected {
		actual, present := record[key]
		if !present || !reflect.DeepEqual(actual, value) {
			return false
		}
	}
	return true
}

func waitForRPCOutput(ctx context.Context, output *synchronizedBuffer, start int, wants []string, event string, timeoutSeconds int) error {
	if len(wants) == 0 && event == "" {
		return nil
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var expected map[string]any
	if event != "" {
		records, err := parseJSONRecords(event)
		if err != nil || len(records) != 1 {
			return fmt.Errorf("invalid wait_event %q", event)
		}
		expected, _ = records[0].(map[string]any)
		if len(expected) == 0 {
			return fmt.Errorf("invalid wait_event %q", event)
		}
	}
	for {
		text := output.String()[start:]
		// Partial writes do not satisfy an event or substring barrier.
		if end := strings.LastIndexByte(text, '\n'); end >= 0 {
			text = text[:end+1]
		} else {
			text = ""
		}
		matched := event == ""
		for line := range strings.SplitSeq(text, "\n") {
			records, err := parseJSONRecords(line)
			if err != nil {
				continue
			}
			record, ok := records[0].(map[string]any)
			if ok && len(expected) > 0 && eventContains(record, expected) {
				matched = true
				break
			}
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				matched = false
				break
			}
		}
		if matched {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("rpc new output %q / event %s not reached within %s", wants, event, timeout)
		case <-ticker.C:
		}
	}
}
