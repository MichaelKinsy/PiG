package subprocess

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// remoteBashOperations is the extension.BashOperations a subprocess extension's user_bash handler returned: Pi's runner holds the extension's own object (runner.ts isUserBashEventResult), which here lives in the extension process and runs through RequestUserBashExec.
type remoteBashOperations struct {
	conn   *Conn
	handle string
}

// userBashOperationsResult turns a user_bash reply naming an operations handle into the typed result the Runner accepts. ok is false when the reply names no handle, so the Runner judges the raw JSON as Pi judges an operations object without exec. A reply that also carries a result is invalid in Pi and is returned raw after its handle is released.
func userBashOperationsResult(conn *Conn, reply json.RawMessage) (*extension.UserBashEventResult, bool) {
	var fields struct {
		Operations *struct {
			Handle string `json:"handle"`
		} `json:"operations"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(reply, &fields) != nil || fields.Operations == nil || fields.Operations.Handle == "" {
		return nil, false
	}
	operations := &remoteBashOperations{conn: conn, handle: fields.Operations.Handle}
	if len(fields.Result) > 0 {
		releaseBashOperations(conn, operations.handle)
		return nil, false
	}
	// The consumer runs one command through the object and then drops it; the extension's table entry follows the object.
	runtime.AddCleanup(operations, func(handle string) { releaseBashOperations(conn, handle) }, operations.handle)
	return &extension.UserBashEventResult{Operations: operations}, true
}

func releaseBashOperations(conn *Conn, handle string) {
	args, _ := json.Marshal(BashOperationsRelease{Handle: handle})
	// A closed connection took the extension's table with it.
	_ = conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyBashOperationsRelease, Args: args}})
}

// Exec runs the command through the extension's operations.exec. Cancellation is the exec's AbortSignal: Exec keeps delivering onData chunks and returns when the extension's exec settles, as Pi's executeBashWithOperations awaits it (bash-executor.ts:120-128). A connection that fails after the cancellation reports Pi's "aborted" error.
// upstream: packages/coding-agent/src/core/tools/bash.ts BashOperations.exec
func (o *remoteBashOperations) Exec(ctx context.Context, command, cwd string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	args := UserBashExecArgs{Command: command, Cwd: cwd, Timeout: options.Timeout}
	if options.Env != nil {
		env := make(map[string]string, len(options.Env))
		for _, entry := range options.Env {
			if name, value, ok := strings.Cut(entry, "="); ok {
				env[name] = value
			}
		}
		args.Env = &env
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return extension.BashOperationsResult{}, err
	}
	var decodeErr error
	response, err := o.conn.requestWithUpdates(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestUserBashExec, Tool: o.handle, Args: payload}}, func(update json.RawMessage) {
		var chunk UserBashExecData
		if err := json.Unmarshal(update, &chunk); err != nil {
			decodeErr = errors.Join(decodeErr, fmt.Errorf("decode user_bash output: %w", err))
			return
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			decodeErr = errors.Join(decodeErr, fmt.Errorf("decode user_bash output: %w", err))
			return
		}
		if options.OnData != nil {
			options.OnData(data)
		}
	})
	if err != nil {
		if ctx.Err() != nil {
			return extension.BashOperationsResult{}, errors.New("aborted")
		}
		return extension.BashOperationsResult{}, err
	}
	if response.Response != nil && response.Response.Error != nil {
		return extension.BashOperationsResult{}, response.Response.Error.ToError()
	}
	if decodeErr != nil {
		return extension.BashOperationsResult{}, decodeErr
	}
	var result UserBashExecResult
	if response.Response != nil && len(response.Response.Result) > 0 {
		if err := json.Unmarshal(response.Response.Result, &result); err != nil {
			return extension.BashOperationsResult{}, fmt.Errorf("decode user_bash exec result: %w", err)
		}
	}
	return extension.BashOperationsResult{ExitCode: result.ExitCode}, nil
}
