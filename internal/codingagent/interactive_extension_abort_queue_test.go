package codingagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi's interactive mode hands the Session `abortHandler: () => this.restoreQueuedMessagesToEditor({ abort: true })` (interactive-mode.ts:2097-2099), and the
// Session's ctx.abort() calls it (agent-session.ts:3597-3603): the queued steering and follow-up messages go back to the editor ahead of its text, and only
// then is the run aborted, all before ctx.abort() returns. Measured with Pi 1.5.2 in a terminal: a message queued while a turn streams, then ctx.abort() from
// an input handler, leaves ctx.hasPendingMessages() false at once, the queued text in the editor, and no request for it. PiG aborted the run and left the
// queue, so the queued message ran as a turn of its own right after the abort.
func TestExtensionAbortMovesQueuedMessagesToTheEditor(t *testing.T) {
	for _, via := range []string{"subprocess host action", "in-process context"} {
		for _, queue := range []struct {
			name               string
			steering, followUp string
			want               string
		}{
			{"a steering message", "queued one", "", "queued one\n\ndraft"},
			{"a follow-up message", "", "queued two", "queued two\n\ndraft"},
			{"a steering and a follow-up message", "queued one", "queued two", "queued one\n\nqueued two\n\ndraft"},
			{"nothing queued", "", "", "draft"},
		} {
			t.Run(via+"/"+queue.name, func(t *testing.T) {
				m, ctx, release := newStreamingRoutingMode(t)
				var abort func()
				var pending func() bool
				if via == "subprocess host action" {
					bridge := &captureUIBridge{}
					m.opts.SubprocessUIBridge = bridge
					detach := m.wireSubprocessHostCallbacks()
					defer detach()
					abort = bridge.actions["abort"].(func())
					pending = bridge.actions["hasPendingMessages"].(func() bool)
				} else {
					runner := inproc.NewRunner(nil, t.TempDir())
					onLoop(m, ctx, func() {
						m.newRunner = runner
						m.wireInprocContextActions()
					})
					extensionContext := runner.CreateCommandContext()
					abort = func() { _ = extensionContext.Abort() }
					pending = func() bool {
						queued, _ := extensionContext.HasPendingMessages()
						return queued
					}
				}
				queued := 0
				onLoop(m, ctx, func() {
					if queue.steering != "" {
						queued++
						m.promptUserInput(ctx, queue.steering, nil, false, extension.InputSourceUser, true)
					}
					if queue.followUp != "" {
						queued++
						m.promptUserInput(ctx, queue.followUp, nil, true, extension.InputSourceUser, true)
					}
				})
				deadline := time.Now().Add(5 * time.Second)
				for {
					var have int
					onLoop(m, ctx, func() {
						steering, followUps := m.agent.PendingMessages()
						have = len(steering) + len(followUps)
					})
					if have == queued {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("%d messages are queued, want %d", have, queued)
					}
					time.Sleep(5 * time.Millisecond)
				}
				onLoop(m, ctx, func() { m.editor.SetText("draft") })

				pendingAfter := make(chan bool, 1)
				var once sync.Once
				// The agent awaits this listener as it awaits an extension's handler.
				unsubscribe := m.agent.Subscribe(func(context.Context, agent.AgentEvent) error {
					once.Do(func() {
						abort()
						pendingAfter <- pending()
					})
					return nil
				})
				defer unsubscribe()
				release()
				select {
				case stillQueued := <-pendingAfter:
					if stillQueued {
						t.Error("ctx.hasPendingMessages() = true right after ctx.abort() returned, want false: the queues are cleared before it returns")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("abort did not return to the handler the run awaits")
				}
				if err := m.waitForIdle(ctx); err != nil {
					t.Fatalf("waitForIdle after abort = %v, want nil", err)
				}
				var text string
				var users int
				onLoop(m, ctx, func() {
					text = m.editor.GetExpandedText()
					for _, message := range m.agent.Messages() {
						if message.User != nil {
							users++
						}
					}
				})
				if text != queue.want {
					t.Errorf("editor after ctx.abort() = %q, want %q", text, queue.want)
				}
				if users != 1 {
					t.Errorf("the transcript holds %d user messages, want 1: a queued message must not run after the abort", users)
				}
			})
		}
	}
}

// The other actions an extension calls from a handler the run awaits return without waiting for that run, as Pi's do: sendUserMessage and sendMessage start
// their work and only catch its rejection (agent-session.ts:3543-3560), and shutdown sets a flag the mode reads when the run settles
// (interactive-mode.ts:2150, :4559).
func TestExtensionActionsReturnToAHandlerTheRunAwaits(t *testing.T) {
	for _, row := range []struct {
		name string
		call func(actions map[string]any) error
	}{
		{"sendUserMessage steer", func(actions map[string]any) error {
			return actions["sendUserMessage"].(func(any, subprocess.SendUserMessageOptions) error)("from the handler", subprocess.SendUserMessageOptions{DeliverAs: "steer"})
		}},
		{"sendUserMessage followUp", func(actions map[string]any) error {
			return actions["sendUserMessage"].(func(any, subprocess.SendUserMessageOptions) error)("from the handler", subprocess.SendUserMessageOptions{DeliverAs: "followUp"})
		}},
		{"sendMessage steer", func(actions map[string]any) error {
			return actions["sendMessage"].(func(extension.CustomMessageRef, subprocess.SendMessageOptions) error)(extension.CustomMessageRef{CustomType: "note", Content: "from the handler"}, subprocess.SendMessageOptions{DeliverAs: "steer"})
		}},
		{"shutdown", func(actions map[string]any) error {
			actions["shutdown"].(func())()
			return nil
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			m, _, release := newStreamingRoutingMode(t)
			m.opts.AgentDir = t.TempDir()
			bridge := &captureUIBridge{}
			m.opts.SubprocessUIBridge = bridge
			detach := m.wireSubprocessHostCallbacks()
			defer detach()
			returned := make(chan error, 1)
			var once sync.Once
			unsubscribe := m.agent.Subscribe(func(context.Context, agent.AgentEvent) error {
				once.Do(func() { returned <- row.call(bridge.actions) })
				return nil
			})
			defer unsubscribe()
			release()
			select {
			case err := <-returned:
				t.Logf("the action returned %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("the action did not return to the handler the run awaits")
			}
		})
	}
}
