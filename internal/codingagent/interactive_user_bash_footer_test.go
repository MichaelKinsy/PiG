package codingagent

import (
	"context"
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's footer asks session.getContextUsage() on every render (footer.ts:143), so a ! command's recorded output counts toward the context percentage as
// soon as it is recorded (compaction.ts estimateTokens "bashExecution"). PiG's footer holds a stored estimate that agent events and transcript rebuilds
// refresh; the bash path did not, so the percentage stayed at its old value until the next model turn. Found by the bash journey (2.1% in Pi, 0.2% in PiG).
func TestInteractiveBashRefreshesTheFooterContextUsage(t *testing.T) {
	dir := t.TempDir()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	session, err := NewSessionManagerWithDir(dir, t.TempDir()).Create("user-bash-footer", "")
	if err != nil {
		t.Fatal(err)
	}
	handle := &recordingCompactHandle{inner: session}
	recorded := 0
	opts := InteractiveModeOptions{CWD: dir, Model: model, SessionHandle: handle, ContextUsage: func() (*int, int) {
		for _, entry := range session.GetEntries() {
			if message, ok := entry.(MessageEntry); ok && message.Message.Role() == "bashExecution" {
				recorded++
			}
		}
		tokens := recorded * 1000
		recorded = 0
		return &tokens, 8000
	}}
	m := NewInteractiveMode(nil, opts)
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewFooterComponent(model, "", nil)
	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	defer func() { cancel(); <-loopDone }()
	m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "/ext/remote", Handlers: map[string][]extension.HandlerFn{
		"user_bash": {func(...any) (any, error) {
			return &extension.UserBashEventResult{Result: map[string]any{"output": "ran remotely\n", "exitCode": float64(0), "cancelled": false, "truncated": false}}, nil
		}},
	}}}, dir)

	submitted := make(chan struct{})
	m.runOnMain(ctx, func() {
		m.handleBashCommand(ctx, "echo hi", false)
		close(submitted)
	})
	<-submitted
	m.backgroundTasks.Wait()

	if got := m.statusLine.contextTokens; got != 1000 {
		t.Fatalf("footer context tokens after a recorded ! command = %d, want 1000 (one recorded execution)", got)
	}
}
