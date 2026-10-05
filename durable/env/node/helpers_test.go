package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

func newTestEnv(t *testing.T) (*NodeExecutionEnv, string) {
	t.Helper()
	root := t.TempDir()
	return NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root}), root
}

// must returns value or panics, which fails the running test with a stack.
func must[T any](value T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}
	return value
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func executionError(t *testing.T, err error) *durableenv.ExecutionError {
	t.Helper()
	var executionErr *durableenv.ExecutionError
	if !errors.As(err, &executionErr) {
		t.Fatalf("error %v (%T) is not an ExecutionError", err, err)
	}
	return executionErr
}

func abortedContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

var background = context.Background()

// cancellable returns a context and the function that cancels it, as the abort signal of a test.
func cancellable() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// collectShellOutput runs command and returns everything OnOutput received.
func collectShellOutput(env *NodeExecutionEnv, command string, options *durableenv.ShellExecOptions, ctx context.Context) (durableenv.ShellExecResult, string, error) {
	var output strings.Builder
	var mu sync.Mutex
	merged := durableenv.ShellExecOptions{}
	if options != nil {
		merged = *options
	}
	merged.OnOutput = func(_ context.Context, text string, _ durableenv.ShellOutputInfo) {
		mu.Lock()
		defer mu.Unlock()
		output.WriteString(text)
	}
	result, err := env.Exec(ctx, command, &merged)
	mu.Lock()
	defer mu.Unlock()
	return result, output.String(), err
}
