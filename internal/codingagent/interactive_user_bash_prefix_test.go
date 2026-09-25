package codingagent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// TOOL-15: interactive `!cmd` applies shellCommandPrefix, as upstream
// handleBashCommand runs through session.executeBash.
func TestInteractiveBashAppliesShellCommandPrefix(t *testing.T) {
	dir := t.TempDir()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model, AgentDir: t.TempDir(), Settings: Settings{CommandPrefix: "X=from-prefix"}})
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.runCtx = ctx
	m.abortCtx, m.abortFn = context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)

	out := filepath.Join(dir, "out")
	// Quoted with forward slashes: bash reads a backslash in a Windows path
	// as an escape.
	m.handleBashCommand(ctx, `printf %s "$X" > '`+filepath.ToSlash(out)+`'`, false)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, err := os.ReadFile(out); err == nil && strings.TrimSpace(string(data)) != "" {
			if string(data) != "from-prefix" {
				t.Fatalf("command saw X=%q, want the prefix's value", data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no output: the command did not see the prefix's X")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-loopDone
}
