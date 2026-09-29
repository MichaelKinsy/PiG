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
	"runtime"
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

const agentHomeEnvKeys = "PIG_CODING_AGENT_DIR PI_CODING_AGENT_DIR PIG_CODING_AGENT_SESSION_DIR PIG_HOME PI_HOME"

// hostTerminalEnvKeys are host-terminal and host-OS signals that both Pi and PiG read to
// choose keybinding columns, image protocols, clipboard paths, theme background and Windows
// Terminal heuristics (Pi keybindings.ts useWindowsKeybindings, keys.ts isWindowsTerminalSession,
// terminal-image.ts detectCapabilities, clipboard.ts, wsl.ts, theme.ts). Only PiG reads the
// HERDR_* keys (D44, tui/terminal_image.go); Pi has no Herdr branch. Pi and PiG treat an empty
// value as unset for every key here, so headless terminals can clear them with an empty
// assignment. A scenario that needs one sets it in its own [env], which is applied after this scrub.
const hostTerminalEnvKeys = "WT_SESSION WSL_DISTRO_NAME WSL_INTEROP WSLENV SSH_CONNECTION SSH_CLIENT SSH_TTY " +
	"KITTY_WINDOW_ID GHOSTTY_RESOURCES_DIR ITERM_SESSION_ID WEZTERM_PANE WARP_SESSION_ID WARP_TERMINAL_SESSION_UUID TERMINAL_EMULATOR TERMUX_VERSION MOSH_CONNECTION COLORFGBG " +
	"HERDR_ENV HERDR_KITTY_GRAPHICS HERDR_PANE_ID HERDR_SOCKET_PATH HERDR_TAB_ID HERDR_WORKSPACE_ID"

// hostPresenceEnvKeys are tested for presence (Pi tui-alt-screen.ts for ZELLIJ and STY, chalk's
// supports-color for FORCE_COLOR, TF_BUILD with AGENT_NAME, CI and TEAMCITY_VERSION), so they must
// be removed, never emptied.
const hostPresenceEnvKeys = "ZELLIJ STY FORCE_COLOR TF_BUILD AGENT_NAME CI TEAMCITY_VERSION"

// clearedHostTerminalEnv returns empty assignments for hostTerminalEnvKeys.
func clearedHostTerminalEnv() []string {
	keys := strings.Fields(hostTerminalEnvKeys)
	for i := range keys {
		keys[i] += "="
	}
	return keys
}

// Headless-terminal replaces the child environment with its --env list; explicit empty values
// keep the home overrides disabled before the driver appends its fixture homes.
func clearedAgentHomeEnv() []string {
	keys := strings.Fields(agentHomeEnvKeys)
	for i := range keys {
		keys[i] += "="
	}
	return keys
}

// hermeticEnviron removes ambient proxy, agent-home and host-terminal variables and pins
// COLORTERM to truecolor. Driver-declared fixture environment is applied afterwards.
func hermeticEnviron() []string {
	return hermeticEnvironFrom(os.Environ(), runtime.GOOS == "windows")
}

// hermeticEnvironFrom filters environ as hermeticEnviron does. Windows environment names are
// case-insensitive (Node process.env and Go os/exec both fold them), so a host variable such as
// Ci or Wt_Session reaches Pi and PiG as CI or WT_SESSION and is matched case-insensitively there.
func hermeticEnvironFrom(environ []string, caseInsensitive bool) []string {
	fold := func(k string) string {
		if caseInsensitive {
			return strings.ToUpper(k)
		}
		return k
	}
	drop := map[string]bool{}
	for _, key := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "ALL_PROXY", "all_proxy",
	} {
		drop[fold(key)] = true
	}
	for key := range strings.FieldsSeq(agentHomeEnvKeys + " " + hostTerminalEnvKeys + " " + hostPresenceEnvKeys) {
		drop[fold(key)] = true
	}
	out := make([]string, 0, len(environ)+1)
	hasColor := false
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if drop[fold(k)] {
			continue
		}
		if fold(k) == "COLORTERM" {
			kv = k + "=truecolor"
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

func runCmdWithInput(ctx context.Context, name string, args, env, inputLines []string, settle, timeout time.Duration, cwd string, outputFile *os.File, stderrFiles ...*os.File) (string, int, int64, error) {
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
	if len(stderrFiles) > 0 && stderrFiles[0] != nil {
		cmd.Stderr = stderrFiles[0]
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
