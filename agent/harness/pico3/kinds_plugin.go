package pico3

import (
	"context"
	"errors"
	"fmt"
)

// pluginKind runs one registered plugin handler: input {handler, input}.
var pluginKind = &Kind{
	Name:     "pi.plugin",
	Inflight: []string{"started"},
	Phases: map[string]PhaseHandler{
		"started": runPlugin,
	},
	Initial: func(ctx context.Context, task Task, rt *Runtime) (Step, error) {
		if _, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
			return nil, tx.Checkpoint(Checkpoint{"phase": "started"})
		}); err != nil {
			return Step{}, err
		}
		return runPlugin(ctx, task, rt)
	},
	Abort: func(context.Context, Task, *Runtime) (AbortClosure, error) {
		return func(context.Context, *Tx, Task) (JsonValue, error) { return nil, nil }, nil
	},
}

func runPlugin(ctx context.Context, task Task, rt *Runtime) (Step, error) {
	input := asObject(task.Input)
	name := str(input, "handler")
	handler, ok := rt.Plugins[name]
	if !ok || handler == nil {
		return done(Failed(JsonObject{"reason": "missing_handler", "detail": name})), nil
	}
	result, err := callPlugin(ctx, handler, input["input"], taskApi(task, rt))
	var stored JsonValue
	if err == nil {
		stored, err = ToStored(result)
	}
	if err != nil {
		if ctx.Err() != nil {
			return Step{}, err
		}
		return done(Failed(JsonObject{"reason": "threw", "detail": errorString(err)})), nil
	}
	return done(Completed(stored)), nil
}

func callPlugin(ctx context.Context, handler PluginHandler, input JsonValue, api *ToolApi) (result JsonValue, err error) {
	defer recoverInto(&err)
	return handler(ctx, input, api)
}

// errorString renders an error as JavaScript String(error) does: the error
// class name, a colon, and the message.
func errorString(err error) string {
	return fmt.Sprintf("%s: %v", errorName(err), err)
}

func errorName(err error) string {
	for _, named := range namedErrors {
		if named.matches(err) {
			return named.name
		}
	}
	return "Error"
}

type namedError struct {
	name    string
	matches func(error) bool
}

func errorIs[T error](err error) bool {
	var target T
	return errors.As(err, &target)
}

// namedErrors are the kernel error classes String(error) names.
var namedErrors = []namedError{
	{"Forbidden", errorIs[*Forbidden]},
	{"TypeError", errorIs[*TypeError]},
	{"TaskContractFault", errorIs[*TaskContractFault]},
	{"ReadAfterWrite", errorIs[*ReadAfterWrite]},
	{"ConversationBusy", errorIs[*ConversationBusy]},
	{"GenerationInProgress", errorIs[*GenerationInProgress]},
	{"CollapseInProgress", errorIs[*CollapseInProgress]},
	{"Faulted", errorIs[*Faulted]},
	{"Closed", errorIs[*Closed]},
	{"NestedLineOperation", errorIs[*NestedLineOperation]},
}
