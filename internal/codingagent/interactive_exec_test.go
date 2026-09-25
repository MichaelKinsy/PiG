package codingagent

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestExecHostActionRegisteredForInteractiveMode is the "interactive still
// works" side of the exec-all-modes fix: interactive mode has always
// registered the "exec" host action directly (wireSubprocessHostCallbacks),
// unlike print/JSON/RPC which previously left it unregistered. This guards
// against a future refactor accidentally dropping interactive's own
// registration while unifying the other modes.
func TestExecHostActionRegisteredForInteractiveMode(t *testing.T) {
	cwd := t.TempDir()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: cwd, Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})

	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	m.wireSubprocessHostCallbacks()

	fn, ok := bridge.actions["exec"].(func(context.Context, string, []string, *extension.ExecOptions) (extension.ExecResult, error))
	if !ok {
		t.Fatalf("exec host action missing or wrong type: %T", bridge.actions["exec"])
	}

	// Use this test binary itself as a portable "echo" (see TestMain's
	// execEchoHelperEnv branch) instead of an external echo binary, so this
	// test also works on native Windows, where no external echo executable
	// is guaranteed on PATH.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(execEchoHelperEnv, "1")

	result, err := fn(t.Context(), self, []string{"still-works"}, nil)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if result.Code != 0 {
		t.Fatalf("exec code = %d, stderr = %q", result.Code, result.Stderr)
	}
	if got := result.Stdout; got != "still-works\n" {
		t.Fatalf("exec stdout = %q", got)
	}
}
