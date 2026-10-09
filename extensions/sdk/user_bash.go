package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"sync"
)

// requestUserBashExec (host→ext) runs the exec of the BashOperations a user_bash reply named; notifyBashOperationsRelease (host→ext) drops that object.
const (
	requestUserBashExec         = "user_bash_exec"
	notifyBashOperationsRelease = "bash_operations_release"
)

// BashExecOptions are the options Pi's BashOperations.exec receives (core/tools/bash.ts). OnData receives the command's output as it arrives, one chunk at a time and in order. Signal is cancelled when the host aborts the command. Timeout is seconds. Env, when non-nil, replaces the inherited environment.
type BashExecOptions struct {
	OnData  func(data []byte)
	Signal  context.Context
	Timeout *float64
	Env     map[string]string
}

// BashExecResult is Pi's `{ exitCode: number | null }`. A nil ExitCode is a failed command; report a signal termination as 128 plus the signal number.
type BashExecResult struct {
	ExitCode *int
}

// BashOperations is Pi's BashOperations: pluggable command execution, local by default and remote (for example SSH) when an extension returns it from a user_bash handler as `map[string]any{"operations": BashOperations{Exec: ...}}`. Exec returns an error to reject, as Pi's exec rejects; return an error whose message is "aborted" when Signal was cancelled.
type BashOperations struct {
	Exec func(command, cwd string, options BashExecOptions) (BashExecResult, error)
}

// userBashEventResult retains a present undefined exit code across JSON. Native nil represents the number-or-undefined field; pre-encoded JSON null remains null. An `operations` object stays in the extension: the reply names it by handle and the host calls it with requestUserBashExec.
func (e *Extension) userBashEventResult(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch value.(type) {
	case json.RawMessage, *json.RawMessage:
		return value, nil
	}
	object, ok := value.(map[string]any)
	if ok {
		if _, hasResult := object["result"]; !hasResult {
			var operations BashOperations
			switch typed := object["operations"].(type) {
			case BashOperations:
				operations = typed
			case *BashOperations:
				if typed != nil {
					operations = *typed
				}
			}
			if operations.Exec != nil {
				e.bashOperationsMu.Lock()
				if e.bashOperations == nil {
					e.bashOperations = map[string]BashOperations{}
				}
				e.bashOperationsSeq++
				handle := fmt.Sprintf("bash-%d", e.bashOperationsSeq)
				e.bashOperations[handle] = operations
				e.bashOperationsMu.Unlock()
				return map[string]any{"operations": map[string]any{"handle": handle}}, nil
			}
		}
	}
	if !ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &object); err != nil {
			return value, nil
		}
	}
	result, ok := object["result"].(map[string]any)
	if !ok {
		return value, nil
	}
	object = maps.Clone(object)
	result = maps.Clone(result)
	exitCode, present := result["exitCode"]
	undefined := present && exitCode == nil
	object["_pigUserBashExitCodeUndefined"] = undefined
	if undefined {
		delete(result, "exitCode")
	}
	if path, present := result["fullOutputPath"]; present && path == nil {
		delete(result, "fullOutputPath")
	}
	object["result"] = result
	return object, nil
}

// dispatchUserBashExec runs operations.exec for a requestUserBashExec. The output chunks stream as tool_update notifications before the answer, as Pi's onData runs before exec resolves; the request's cancellation is the options' Signal.
func (e *Extension) dispatchUserBashExec(ctx Context, id string, req *requestMsg) (any, error) {
	e.bashOperationsMu.Lock()
	operations, ok := e.bashOperations[req.Tool]
	e.bashOperationsMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown bash operations: %s", req.Tool)
	}
	var args struct {
		Command string            `json:"command"`
		Cwd     string            `json:"cwd"`
		Timeout *float64          `json:"timeout"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return nil, err
	}
	var notifyErr error
	var finished bool
	var mu sync.Mutex
	onData := func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		if finished || notifyErr != nil {
			return
		}
		notifyErr = e.conn.notify("tool_update", map[string]any{"request_id": id, "result": map[string]string{"data": base64.StdEncoding.EncodeToString(data)}})
	}
	result, err := operations.Exec(args.Command, args.Cwd, BashExecOptions{OnData: onData, Signal: ctx.ctx, Timeout: args.Timeout, Env: args.Env})
	mu.Lock()
	finished = true
	failed := notifyErr
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	if failed != nil {
		return nil, failed
	}
	return map[string]any{"exitCode": result.ExitCode}, nil
}
