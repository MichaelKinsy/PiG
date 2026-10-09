package codingagent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

func BenchmarkUserBashOwnerRoundTrip(b *testing.B) {
	for _, size := range []int{0, 1024, 64 * 1024, 1024 * 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: b.TempDir(), Model: &ai.Model{ID: "m"}})
			m.chatContainer, m.pendingMessagesContainer = tui.NewContainer(), tui.NewContainer()
			m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
			ctx, cancel := context.WithCancel(b.Context())
			m.runCtx, m.backgroundCtx = ctx, ctx
			m.abortCtx, m.abortFn = context.WithCancel(ctx)
			result := &extension.UserBashEventResult{Result: map[string]any{"output": strings.Repeat("x", size), "exitCode": 7, "cancelled": false, "truncated": false}}
			m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "bench", Handlers: map[string][]extension.HandlerFn{
				"user_bash": {func(...any) (any, error) { return result, nil }},
			}}}, m.opts.CWD)
			loopDone := make(chan struct{})
			go m.drainLoop(ctx, loopDone)
			defer func() { cancel(); <-loopDone; m.backgroundTasks.Wait() }()
			b.ReportAllocs()
			for b.Loop() {
				submitted := make(chan struct{})
				m.runOnMain(ctx, func() {
					m.chatContainer.Clear()
					m.bashOrder = nil
					m.handleBashCommand(ctx, "handled", false)
					close(submitted)
				})
				<-submitted
				m.backgroundTasks.Wait()
			}
		})
	}
}
