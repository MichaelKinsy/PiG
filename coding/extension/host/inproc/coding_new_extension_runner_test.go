package inproc_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// runner.ts:395-408 ExtensionRunner constructor(extensions, runtime, cwd, sessionManager, modelRegistry), through the production constructor coding.NewExtensionRunner: the runner keeps the
// given runtime, hands the constructor's cwd to every handler context, and a missing SessionManager (a nil *coding.SessionManager must not become a non-nil interface) is reported as a nil
// interface. Pi's constructor only stores its arguments; flag defaults come from the loader (loader.ts registerFlag), so this test does not pin them.
func TestCodingNewExtensionRunnerBindsTheRuntimeCwdAndAbsentSessionManager(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	var seenCWD string
	var seenManager extension.ReadonlySessionManager
	flagged := extension.Extension{Path: "flagged"}
	flagged.Handlers = map[string][]extension.HandlerFn{"session_start": {func(args ...any) (any, error) {
		ctx := extension.FromContext(args[1].(context.Context))
		seenCWD, _ = ctx.CWD()
		seenManager, _ = ctx.SessionManager()
		return nil, nil
	}}}
	const cwd = "/work/project"
	runner := coding.NewExtensionRunner([]extension.Extension{flagged}, runtime, cwd, nil, nil)
	if runner.Runtime() != runtime {
		t.Fatal("the runner replaced the given runtime")
	}
	if _, err := runner.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
		t.Fatal(err)
	}
	if seenCWD != cwd {
		t.Errorf("handler context cwd = %q, want the constructor's %q", seenCWD, cwd)
	}
	if seenManager != nil {
		t.Errorf("a runner built without a SessionManager reported %v, want it absent", seenManager)
	}
}
