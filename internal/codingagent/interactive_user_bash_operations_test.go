package codingagent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

type interactiveFakeOperations struct {
	mu      sync.Mutex
	command string
}

func (o *interactiveFakeOperations) Exec(_ context.Context, command, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	o.mu.Lock()
	o.command = command
	o.mu.Unlock()
	options.OnData([]byte("from operations\n"))
	code := 0
	return extension.BashOperationsResult{ExitCode: &code}, nil
}

func newUserBashInteractive(t *testing.T, handler func() *extension.UserBashEventResult) (*InteractiveMode, context.CancelFunc, chan struct{}) {
	t.Helper()
	dir := t.TempDir()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model, AgentDir: t.TempDir()})
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	ext := extension.Extension{Path: "/ext/remote", Handlers: map[string][]extension.HandlerFn{
		"user_bash": {func(...any) (any, error) { return handler(), nil }},
	}}
	m.newRunner = inproc.NewRunner([]extension.Extension{ext}, dir)
	return m, cancel, loopDone
}

func waitBlockOutput(t *testing.T, m *InteractiveMode, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		done := make(chan bool, 1)
		m.runOnMain(m.runCtx, func() {
			done <- len(m.bashOrder) == 1 && strings.Contains(strings.Join(m.bashOrder[0].Render(100), "\n"), want)
		})
		if <-done {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("bash block never showed %q", want)
}

// Interactive `!cmd` runs through the operations a user_bash handler
// returned instead of the local shell (upstream handleBashCommand passes
// eventResult.operations to executeBash).
func TestInteractiveBashRunsThroughUserBashOperations(t *testing.T) {
	ops := &interactiveFakeOperations{}
	m, cancel, loopDone := newUserBashInteractive(t, func() *extension.UserBashEventResult {
		return &extension.UserBashEventResult{Operations: ops}
	})
	defer func() { cancel(); <-loopDone }()
	marker := filepath.Join(t.TempDir(), "ran-locally")
	m.handleBashCommand(m.runCtx, "touch "+marker, false)
	waitBlockOutput(t, m, "from operations")
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if ops.command != "touch "+marker {
		t.Fatalf("operations got %q", ops.command)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran locally despite the handler's operations")
	}
}

// A handler's full result is shown without running the command.
func TestInteractiveBashShowsUserBashResultOverride(t *testing.T) {
	m, cancel, loopDone := newUserBashInteractive(t, func() *extension.UserBashEventResult {
		return &extension.UserBashEventResult{Result: map[string]any{"output": "handled elsewhere", "exitCode": 0.0, "cancelled": false, "truncated": false}}
	})
	defer func() { cancel(); <-loopDone }()
	marker := filepath.Join(t.TempDir(), "ran-locally")
	m.handleBashCommand(m.runCtx, "touch "+marker, false)
	waitBlockOutput(t, m, "handled elsewhere")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran locally despite the handler's result")
	}
}
