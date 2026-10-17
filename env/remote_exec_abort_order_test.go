package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// onlyCheckContext reports cancellation on exactly its failAt-th Err call, so a test can cancel one named abort check of Exec and no other.
type onlyCheckContext struct {
	context.Context
	failAt int
	calls  int
}

func (c *onlyCheckContext) Err() error {
	c.calls++
	if c.calls == c.failAt {
		return context.Canceled
	}
	return nil
}

func isAborted(err error) bool {
	failure, ok := errors.AsType[*durableenv.ExecutionError](err)
	return ok && failure.Code == durableenv.ExecutionErrorAborted && failure.Message == "aborted"
}

// remote-env.ts:839-840 exec checks the abort signal first, before it validates the options: an aborted call with an invalid timeout is `aborted`, not the timeout error.
// remote-env.ts:857 checks it again after the working directory is resolved and before the command is sent, so a signal raised in between stops the command from starting.
// mutation-checked: removing the check at the top of RemoteExecutionEnv.Exec fails the first case; removing the check after the cwd is resolved fails the second.
func TestRemoteExecChecksTheAbortSignalBeforeValidatingAndBeforeSending(t *testing.T) {
	t.Parallel()
	env, _ := remoteEnvironment(t)
	invalid := -1.0
	if _, err := env.Exec(&onlyCheckContext{Context: background, failAt: 1}, "true", &durableenv.ShellExecOptions{Timeout: &invalid}); !isAborted(err) {
		t.Errorf("an aborted call with an invalid timeout returned %v, want the aborted ExecutionError", err)
	}
	if _, err := env.Exec(background, "true", &durableenv.ShellExecOptions{Timeout: &invalid}); err == nil || isAborted(err) {
		t.Errorf("the same call without an abort returned %v, want the timeout error", err)
	}

	marker := filepath.Join(env.Cwd(), "ran")
	if _, err := env.Exec(&onlyCheckContext{Context: background, failAt: 2}, "touch ran", nil); !isAborted(err) {
		t.Errorf("a call aborted after validation returned %v, want the aborted ExecutionError", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a command aborted before it was sent still ran")
	}
	if _, err := env.Exec(background, "touch ran", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the control command did not run: %v", err)
	}
}
