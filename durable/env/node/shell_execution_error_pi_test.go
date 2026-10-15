package node

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// TestExecExpectedFailuresCarryPisCodesMessagesAndCauses ports the ExecutionError constructions of node.ts:62,67,222,658,677,714,817: each expected
// failure of Exec comes back as an ExecutionError with Pi's code and exact message, and the ones Pi builds with a cause (callback_error,
// spawn_error) keep it reachable through Unwrap.
func TestExecExpectedFailuresCarryPisCodesMessagesAndCauses(t *testing.T) {
	root := t.TempDir()
	env := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root})
	zero, nan := 0.0, math.NaN()
	tooLong := 1e12
	callbackCause := errors.New("callback failed")

	type tc struct {
		name     string
		run      func() error
		code     durableenv.ExecutionErrorCode
		message  string
		cause    error // when non-nil, errors.Is must find it behind Unwrap
		hasCause bool
	}
	cases := []tc{
		{name: "pre-aborted (node.ts:658)", run: func() error { _, err := env.Exec(abortedContext(), "true", nil); return err },
			code: durableenv.ExecutionErrorAborted, message: "aborted"},
		{name: "empty argv (node.ts:677)", run: func() error { _, err := env.Exec(background, []string{}, nil); return err },
			code: durableenv.ExecutionErrorSpawnError, message: "Empty argv: no program to run"},
		{name: "zero timeout (node.ts:62)", run: func() error {
			_, err := env.Exec(background, "true", &durableenv.ShellExecOptions{Timeout: &zero})
			return err
		},
			code: durableenv.ExecutionErrorTimeout, message: "Invalid timeout: must be a finite number of seconds"},
		{name: "NaN timeout (node.ts:62)", run: func() error {
			_, err := env.Exec(background, "true", &durableenv.ShellExecOptions{Timeout: &nan})
			return err
		},
			code: durableenv.ExecutionErrorTimeout, message: "Invalid timeout: must be a finite number of seconds"},
		{name: "huge timeout (node.ts:67)", run: func() error {
			_, err := env.Exec(background, "true", &durableenv.ShellExecOptions{Timeout: &tooLong})
			return err
		},
			code: durableenv.ExecutionErrorTimeout, message: "Invalid timeout: maximum is " + formatNumber(maxTimeoutSeconds) + " seconds"},
		{name: "missing custom shell (node.ts:222)", run: func() error {
			missing := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: filepath.Join(root, "missing-shell")})
			_, err := missing.Exec(background, "true", nil)
			return err
		}, code: durableenv.ExecutionErrorShellUnavailable, message: "Custom shell path not found: " + filepath.Join(root, "missing-shell")},
		{name: "callback throws (node.ts:714)", run: func() error {
			_, err := env.Exec(background, "printf out", &durableenv.ShellExecOptions{OnOutput: func(context.Context, string, durableenv.ShellOutputInfo) { panic(callbackCause) }})
			return err
		}, code: durableenv.ExecutionErrorCallbackError, message: "callback failed", cause: callbackCause, hasCause: true},
	}
	if runtime.GOOS != "windows" {
		shellPath := filepath.Join(root, "not-executable-shell")
		if err := os.WriteFile(shellPath, []byte("not executable"), 0o644); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, tc{name: "shell that cannot start (node.ts:817)", run: func() error {
			broken := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: shellPath})
			_, err := broken.Exec(background, "true", nil)
			return err
		}, code: durableenv.ExecutionErrorSpawnError, hasCause: true})
	}
	for _, c := range cases {
		failure := execErr(t, c.run(), c.code)
		if c.message != "" && failure.Message != c.message {
			t.Errorf("%s: message = %q, want %q", c.name, failure.Message, c.message)
		}
		if c.hasCause {
			if failure.Unwrap() == nil {
				t.Errorf("%s: the cause is missing", c.name)
			} else if c.cause != nil && !errors.Is(failure, c.cause) {
				t.Errorf("%s: cause %v does not reach %v", c.name, failure.Unwrap(), c.cause)
			}
		} else if failure.Unwrap() != nil {
			t.Errorf("%s: unexpected cause %v", c.name, failure.Unwrap())
		}
	}
}
