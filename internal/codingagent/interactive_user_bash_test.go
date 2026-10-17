package codingagent

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
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
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: dir, Model: model})
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
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: dir, Model: model, SessionHandle: handle})
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	defer func() { cancel(); <-loopDone }()
	ext := extension.Extension{Path: "/ext/remote", Handlers: map[string][]extension.HandlerFn{
		"user_bash": {func(...any) (any, error) {
			return &extension.UserBashEventResult{Result: map[string]any{"output": "ran remotely\n", "exitCode": float64(0), "cancelled": false, "truncated": false}}, nil
		}},
	}}
	m.newRunner = inproc.NewRunner([]extension.Extension{ext}, dir)

	marker := filepath.Join(dir, "ran")
	submitted := make(chan struct{})
	m.runOnMain(ctx, func() {
		m.handleBashCommand(ctx, "touch "+marker, false)
		close(submitted)
	})
	<-submitted
	m.backgroundTasks.Wait()
	// The owned task has acknowledged rendering and persistence before it finishes.
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran locally although user_bash returned a result")
	}
	chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	if !strings.Contains(chat, "ran remotely") {
		t.Fatalf("the replacement result is not shown:\n%s", chat)
	}
	recorded := false
	for _, entry := range session.GetEntries() {
		if message, ok := entry.(MessageEntry); ok && message.Message.Role() == "bashExecution" {
			recorded = strings.Contains(string(entry.Raw()), "ran remotely")
		}
	}
	if !recorded {
		t.Fatal("the replacement result was not recorded in the session")
	}
}

// Upstream handleBashCommand awaits session.executeBash, which records the result through recordBashResult; when
// that throws, the catch completes the block with setComplete(undefined, false) and shows
// showError(`Bash command failed: ${message}`) (interactive-mode.ts handleBashCommand). PiG appended to the inner
// Session itself, kept the block's result and printed a hard-coded yellow "bash session persist warning".
func TestInteractiveBashRecordFailureShowsBashCommandFailed(t *testing.T) {
	dir := t.TempDir()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	session, err := NewSessionManagerWithDir(dir, t.TempDir()).Create("user-bash-record-failure", "")
	if err != nil {
		t.Fatal(err)
	}
	handle := &recordingCompactHandle{inner: session, bashRecordErr: errors.New("ENOSPC: no space left on device")}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: dir, Model: model, SessionHandle: handle})
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	defer func() { cancel(); <-loopDone }()
	m.newRunner = inproc.NewRunner(nil, dir)

	submitted := make(chan struct{})
	m.runOnMain(ctx, func() {
		m.handleBashCommand(ctx, "echo recorded-output; exit 3", false)
		close(submitted)
	})
	<-submitted
	m.backgroundTasks.Wait()
	chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	if !strings.Contains(chat, "Error: Bash command failed: ENOSPC: no space left on device") {
		t.Fatalf("the recording failure is not shown as upstream's showError:\n%s", chat)
	}
	if strings.Contains(chat, "persist warning") || strings.Contains(chat, "(exit 3)") {
		t.Fatalf("the block kept its result or a PiG-only warning after the recording failure:\n%s", chat)
	}
}

// On the user_bash result path upstream handleBashCommand completes the block and then calls
// session.recordBashResult outside any try (interactive-mode.ts handleBashCommand). A persistence failure rejects the
// onSubmit promise the editor does not await, and Node raises it to the uncaughtException handler, which ends the
// process. The owner loop therefore raises uncaughtError for Run's recover after completing the block, rather than
// adding an error line and continuing.
func TestInteractiveBashResultRecordFailureIsUncaught(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx, cancel := userBashOwnerProbe(t, func(...any) (any, error) { return userBashOwnedResult(), nil })
		recordErr := errors.New("ENOSPC: no space left on device")
		m.opts.SessionHandle = &recordingCompactHandle{bashRecordErr: recordErr}
		raised := make(chan any, 1)
		loopDone := make(chan struct{})
		go func() {
			defer close(loopDone)
			for {
				select {
				case fn := <-m.uiTaskCh:
					func() {
						defer func() {
							if value := recover(); value != nil {
								raised <- value
							}
						}()
						fn()
					}()
				case <-ctx.Done():
					return
				}
			}
		}()
		m.runOnMain(ctx, func() { m.handleBashCommand(ctx, "owned", false) })
		synctest.Wait()
		var value any
		select {
		case value = <-raised:
		default:
		}
		chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
		cancel()
		<-loopDone
		m.backgroundTasks.Wait()
		rejection, ok := value.(uncaughtError)
		if !ok || !errors.Is(rejection, recordErr) {
			t.Fatalf("raised %T (%v), want uncaughtError wrapping the record failure", value, value)
		}
		if !strings.Contains(chat, "(exit 7)") {
			t.Fatalf("the block was not completed with the extension's result before the failure was raised:\n%s", chat)
		}
		if strings.Contains(chat, "Bash command failed") {
			t.Fatalf("the uncaught failure was also shown as a handled error:\n%s", chat)
		}
	})
}
