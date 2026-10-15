package codingagent

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// A `!cmd` started during a run appears in the pending-message area while it runs and stays there after it
// completes and after the run ends; the next prompt submitted while idle, including an extension command, moves it
// into the chat (interactive-mode.ts handleBashCommand pendingMessagesContainer.addChild, onSubmit
// flushPendingBashComponents; agent_end leaves it). PiG kept the block out of view while it ran and moved it into
// the chat when it completed and again at agent end.
func TestInteractiveBashDuringRunStaysPendingUntilTheNextIdlePrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx, cancel := userBashOwnerProbe(t, func(...any) (any, error) { return userBashOwnedResult(), nil })
		m.newRunner = inproc.NewRunner([]extension.Extension{{
			Path:     "owned",
			Handlers: map[string][]extension.HandlerFn{"user_bash": {func(...any) (any, error) { return userBashOwnedResult(), nil }}},
			Commands: map[string]extension.RegisteredCommand{"owned-command": {Name: "owned-command", Handler: func(context.Context, string) error { return nil }}},
		}}, m.opts.CWD)
		m.slashRegistry = NewSlashRegistry()
		m.turnActive.Store(true)
		m.isIdle = false
		loopDone := make(chan struct{})
		go m.drainLoop(ctx, loopDone)
		defer func() { cancel(); <-loopDone }()
		m.runOnMain(ctx, func() { m.handleBashCommand(ctx, "during-run", false) })
		synctest.Wait()
		m.backgroundTasks.Wait()
		synctest.Wait()

		check := func(step string, wantPending bool) {
			t.Helper()
			done := make(chan struct{})
			m.runOnMain(ctx, func() {
				defer close(done)
				if len(m.bashOrder) != 1 {
					t.Errorf("%s: bash blocks = %d", step, len(m.bashOrder))
					return
				}
				block := tui.Component(m.bashOrder[0])
				inPending := slices.Contains(m.pendingMessagesContainer.Children(), block)
				inChat := slices.Contains(m.chatContainer.Children(), block)
				if inPending != wantPending || inChat == wantPending {
					t.Errorf("%s: block in pending area %v, in chat %v; want pending %v", step, inPending, inChat, wantPending)
				}
			})
			<-done
		}
		check("after the command completed during the run", true)

		m.runOnMain(ctx, func() { m.turnActive.Store(false); m.isIdle = true })
		check("after the run ended", true)

		m.runOnMain(ctx, func() { m.handleSubmit(ctx, "/owned-command") })
		synctest.Wait()
		check("after an idle extension command", false)
	})
}

// onSubmit (interactive-mode.ts) calls flushPendingBashComponents only on its idle path, after the built-in command
// chain, the `!` branch, the isCompacting branch and the isStreaming branch have returned. A built-in command, an
// extension command during compaction and a steer during a run leave a pending block in the pending-message area; an
// idle plain prompt moves it into the chat before the input reaches prompt(), whose input handler then consumes it.
func TestInteractivePendingBashFlushesOnlyOnTheIdleSubmitPath(t *testing.T) {
	for _, row := range []struct {
		name        string
		prompt      string
		streaming   bool
		compacting  bool
		wantPending bool
	}{
		{name: "built-in command while idle", prompt: "/local-builtin", wantPending: true},
		{name: "extension command during compaction", prompt: "/owned-command", compacting: true, wantPending: true},
		{name: "steer during a run", prompt: "steer text", streaming: true, wantPending: true},
		{name: "plain prompt while idle", prompt: "idle text", wantPending: false},
	} {
		t.Run(row.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				handled := func(...any) (any, error) { return extension.InputEventResultHandled{}, nil }
				m, ctx, cancel := userBashOwnerProbe(t, func(...any) (any, error) { return userBashOwnedResult(), nil })
				m.newRunner = inproc.NewRunner([]extension.Extension{{
					Path: "owned",
					Handlers: map[string][]extension.HandlerFn{
						"user_bash": {func(...any) (any, error) { return userBashOwnedResult(), nil }},
						"input":     {handled},
					},
					Commands: map[string]extension.RegisteredCommand{"owned-command": {Name: "owned-command", Handler: func(context.Context, string) error { return nil }}},
				}}, m.opts.CWD)
				m.slashRegistry = NewSlashRegistry()
				m.slashRegistry.Register(BuiltinSlashCommand{Name: "local-builtin", Handler: func(*SlashContext) error { return nil }})
				m.turnActive.Store(true)
				m.isIdle = false
				loopDone := make(chan struct{})
				go m.drainLoop(ctx, loopDone)
				defer func() { cancel(); <-loopDone; m.backgroundTasks.Wait() }()
				m.runOnMain(ctx, func() { m.handleBashCommand(ctx, "during-run", false) })
				synctest.Wait()
				m.backgroundTasks.Wait()
				m.runOnMain(ctx, func() {
					m.turnActive.Store(row.streaming)
					m.isIdle = !row.streaming
					m.isCompacting = row.compacting
					m.handleSubmit(ctx, row.prompt)
				})
				synctest.Wait()
				done := make(chan struct{})
				m.runOnMain(ctx, func() {
					defer close(done)
					block := tui.Component(m.bashOrder[0])
					inPending := slices.Contains(m.pendingMessagesContainer.Children(), block)
					inChat := slices.Contains(m.chatContainer.Children(), block)
					if inPending != row.wantPending || inChat == row.wantPending {
						t.Errorf("block in pending area %v, in chat %v; want pending %v", inPending, inChat, row.wantPending)
					}
				})
				<-done
			})
		})
	}
}
