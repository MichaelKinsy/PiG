package codingagent

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestInteractiveUserBashEmptyResultPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/9068-user-bash-fail-closed.test.ts:223-225 (both input rows).
	for _, tc := range []struct {
		input   string
		exclude bool
	}{{"!pwd", false}, {"!!pwd", true}} {
		t.Run("fails closed for "+tc.input+" when a handler returns an empty result", func(t *testing.T) {
			dir := t.TempDir()
			mode := NewInteractiveMode(InteractiveOptions{CWD: dir, AgentDir: t.TempDir()})
			mode.chatContainer = tui.NewContainer()
			mode.pendingMessagesContainer = tui.NewContainer()
			mode.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
			ctx, cancel := context.WithCancel(t.Context())
			mode.runCtx = ctx
			mode.abortCtx, mode.abortFn = context.WithCancel(ctx)
			defer cancel()
			var events []extension.UserBashEvent
			mode.newRunner = inproc.NewRunner([]extension.Extension{{Path: "/ext/empty", Handlers: map[string][]extension.HandlerFn{"user_bash": {func(args ...any) (any, error) {
				events = append(events, args[0].(extension.UserBashEvent))
				return &extension.UserBashEventResult{}, nil
			}}}}}, dir)
			mode.handleSubmit(ctx, tc.input)
			// Upstream awaits onSubmit. Join the owned task while servicing its UI completion on this owner.
			for task := range mode.userBashTasks {
				if err := mode.waitUserBashTask(task); err != nil {
					t.Fatal(err)
				}
			}
			want := []extension.UserBashEvent{{Type: "user_bash", Command: "pwd", Cwd: dir, ExcludeFromContext: tc.exclude}}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events = %+v, want %+v", events, want)
			}
			// handleBashCommand installs these before invoking the executor, so absence
			// proves that no execution started without a timing-based filesystem probe.
			if mode.bashCancel != nil || len(mode.bashOrder) != 0 {
				t.Fatal("bash execution started after an empty handler result")
			}
		})
	}
}
