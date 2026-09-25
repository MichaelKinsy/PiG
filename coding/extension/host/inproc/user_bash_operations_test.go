package inproc_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

type fakeBashOperations struct{}

func (fakeBashOperations) Exec(context.Context, string, string, extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	return extension.BashOperationsResult{}, nil
}

// Ports the operations half of upstream "accepts valid user_bash operations
// and result overrides": a handler's executable operations are accepted.
func TestEmitUserBash_AcceptsOperations(t *testing.T) {
	ops := fakeBashOperations{}
	exts := []extension.Extension{extWithUserBashHandler("/ext/ssh", func(extension.UserBashEvent, context.Context) *extension.UserBashEventResult {
		return &extension.UserBashEventResult{Operations: ops}
	})}
	got, err := inproc.NewRunner(exts, ".").EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash", Command: "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Operations != extension.BashOperations(ops) || got.Result != nil {
		t.Fatalf("result = %#v", got)
	}
}

// Operations and a result together are still rejected.
func TestEmitUserBash_RejectsOperationsWithResult(t *testing.T) {
	exts := []extension.Extension{extWithUserBashHandler("/ext/both", func(extension.UserBashEvent, context.Context) *extension.UserBashEventResult {
		return &extension.UserBashEventResult{Operations: fakeBashOperations{}, Result: map[string]any{"output": "", "cancelled": false, "truncated": false}}
	})}
	if _, err := inproc.NewRunner(exts, ".").EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash"}); err == nil {
		t.Fatal("operations with a result were accepted")
	}
}
