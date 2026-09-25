package codingagent

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports the interactive half of upstream regressions/9068: handleBashCommand
// returns without running the command when emitUserBash rejects. Pig ran the
// `!cmd` locally after a failing user_bash handler.
func TestInteractiveBashDoesNotRunWhenUserBashHandlerFails(t *testing.T) {
	dir := t.TempDir()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model})
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	ext := extension.Extension{Path: "/ext/router", Handlers: map[string][]extension.HandlerFn{
		"user_bash": {func(...any) (any, error) { return nil, errors.New("Routing failed") }},
	}}
	m.newRunner = inproc.NewRunner([]extension.Extension{ext}, dir)

	marker := filepath.Join(dir, "ran")
	m.handleBashCommand(ctx, "touch "+marker, false)
	time.Sleep(500 * time.Millisecond) // a local run would have created the marker by now
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran locally after its user_bash handler failed")
	}
	if len(m.bashOrder) != 0 {
		t.Fatal("a bash block was shown for a command that must not run")
	}
	cancel()
	<-loopDone
}

// Upstream handleBashCommand shows a user_bash handler's replacement result
// in a bash component and records it with recordBashResult, without running
// the command. Interactive mode ignored the result and ran the command.
func TestInteractiveBashShowsAndRecordsUserBashResult(t *testing.T) {
	dir := t.TempDir()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	session, err := NewSessionManagerWithDir(dir, t.TempDir()).Create("user-bash-result", "")
	if err != nil {
		t.Fatal(err)
	}
	handle := &recordingCompactHandle{inner: session}
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model, SessionHandle: handle})
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	ctx := t.Context()
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	ext := extension.Extension{Path: "/ext/remote", Handlers: map[string][]extension.HandlerFn{
		"user_bash": {func(...any) (any, error) {
			return &extension.UserBashEventResult{Result: map[string]any{"output": "ran remotely\n", "exitCode": float64(0), "cancelled": false, "truncated": false}}, nil
		}},
	}}
	m.newRunner = inproc.NewRunner([]extension.Extension{ext}, dir)

	marker := filepath.Join(dir, "ran")
	m.handleBashCommand(ctx, "touch "+marker, false)
	time.Sleep(300 * time.Millisecond) // a local run would have created the marker by now
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran locally although user_bash returned a result")
	}
	chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	if !strings.Contains(chat, "ran remotely") {
		t.Fatalf("the replacement result is not shown:\n%s", chat)
	}
	recorded := false
	for _, entry := range session.Entries() {
		if entry.Base.Type == "bashExecution" || entry.Base.Type == "bash_execution" {
			recorded = strings.Contains(string(entry.Raw()), "ran remotely")
		}
	}
	if !recorded {
		t.Fatal("the replacement result was not recorded in the session")
	}
}
