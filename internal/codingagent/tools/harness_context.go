package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"golang.org/x/text/unicode/norm"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/agent/src/harness/tools/tool-context.ts.
// ExecutionToolContext is the filesystem and shell context for execution tools.
// Applications embed it in their own turn context to retain additional fields.
type ExecutionToolContext struct{ Env harness.ExecutionEnv }

// Environment returns this turn's execution environment.
func (c ExecutionToolContext) Environment() harness.ExecutionEnv { return c.Env }

func harnessToolEnv(toolContext any) (harness.ExecutionEnv, error) {
	context, ok := toolContext.(interface{ Environment() harness.ExecutionEnv })
	if !ok || context.Environment() == nil {
		return nil, errors.New("execution tool context has no environment")
	}
	return context.Environment(), nil
}

func harnessText(text string) harness.AgentToolResult {
	return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
}

func harnessParams(params map[string]any, target any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// Ports packages/agent/src/harness/tools/path-utils.ts.
func resolveHarnessToolPath(ctx context.Context, env harness.ExecutionEnv, path string) (string, error) {
	path = normalizeUnicodeSpaces(path)
	return env.AbsolutePath(ctx, strings.TrimPrefix(path, "@"))
}
func resolveHarnessReadPath(ctx context.Context, env harness.ExecutionEnv, path string) (string, error) {
	resolved, err := resolveHarnessToolPath(ctx, env, path)
	if err != nil {
		return "", err
	}
	variants := []string{resolved, tryMacOSScreenshotPath(resolved), norm.NFD.String(resolved), strings.ReplaceAll(resolved, "'", "\u2019"), strings.ReplaceAll(norm.NFD.String(resolved), "'", "\u2019")}
	for _, variant := range variants {
		exists, err := env.Exists(ctx, variant)
		if err != nil {
			return "", err
		}
		if exists {
			return variant, nil
		}
	}
	return resolved, nil
}

// Ports packages/agent/src/harness/tools/file-mutation-queue.ts.
// Registration is serialized per environment so asynchronous canonicalization
// cannot reorder same-file mutations. A canceled mutation retains its slot until
// its filesystem operation settles, including operations that ignore cancellation.
type harnessMutationState struct {
	registration sync.Mutex
	queues       map[string]chan struct{}
	users        int
}

var harnessMutations = struct {
	sync.Mutex
	states map[harness.ExecutionEnv]*harnessMutationState
}{states: make(map[harness.ExecutionEnv]*harnessMutationState)}

func withHarnessFileMutation(ctx context.Context, env harness.ExecutionEnv, path string, fn func() (harness.AgentToolResult, error)) (harness.AgentToolResult, error) {
	// Execution environments have object identity in Pi. Comparable Go values
	// (normally environment pointers) retain that identity as the registry key.
	if !reflect.TypeOf(env).Comparable() {
		return harness.AgentToolResult{}, errors.New("execution environment must have comparable identity")
	}
	harnessMutations.Lock()
	state := harnessMutations.states[env]
	if state == nil {
		state = &harnessMutationState{queues: make(map[string]chan struct{})}
		harnessMutations.states[env] = state
	}
	state.users++
	harnessMutations.Unlock()
	defer func() {
		harnessMutations.Lock()
		defer harnessMutations.Unlock()
		state.users--
		if state.users == 0 {
			delete(harnessMutations.states, env)
		}
	}()
	previous, next, key, err := registerHarnessMutation(ctx, env, state, path)
	if err != nil {
		return harness.AgentToolResult{}, err
	}
	if previous != nil {
		<-previous
	}
	defer func() {
		state.registration.Lock()
		defer state.registration.Unlock()
		close(next)
		if state.queues[key] == next {
			delete(state.queues, key)
		}
	}()
	return fn()
}
func registerHarnessMutation(ctx context.Context, env harness.ExecutionEnv, state *harnessMutationState, path string) (chan struct{}, chan struct{}, string, error) {
	state.registration.Lock()
	defer state.registration.Unlock()
	absolute, err := env.AbsolutePath(ctx, path)
	if err != nil {
		return nil, nil, "", err
	}
	key, err := env.CanonicalPath(ctx, absolute)
	if err != nil {
		var fileError *harness.FileError
		if !errors.As(err, &fileError) || fileError.Code != harness.FileErrorNotFound && fileError.Code != harness.FileErrorNotSupported {
			return nil, nil, "", err
		}
		key = absolute
	}
	previous := state.queues[key]
	next := make(chan struct{})
	state.queues[key] = next
	return previous, next, key, nil
}
func harnessAbort(ctx context.Context) error {
	if ctx.Err() != nil {
		return errors.New("Operation aborted")
	}
	return nil
}
func harnessEditAccessError(path string, err error) error {
	if fileError, ok := errors.AsType[*harness.FileError](err); ok {
		return &harnessToolFailure{message: fmt.Sprintf("Could not edit file: %s. Error code: %s.", path, fileError.Code), cause: err}
	}
	return err
}
