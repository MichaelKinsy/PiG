package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts flushCompactionQueue (4879-4956): every message of the flush is awaited
// (`await this.session.prompt(message.text)` for an extension command), so the messages behind a command are handled after its handler returned.

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// flushOrderMode is a flush mode whose extension commands are "hold", which starts, waits for release and ends, "after", which only runs, and
// "startrun", which starts a run as an extension command does with sendUserMessage.
func flushOrderMode(t *testing.T, release <-chan struct{}) (*InteractiveMode, *flushQueueHandle, *commandOrder) {
	t.Helper()
	m, handle := newFlushQueueMode(t)
	order := &commandOrder{}
	command := func(name string, run func(ctx context.Context)) extension.RegisteredCommand {
		return extension.RegisteredCommand{Name: name, Handler: func(ctx context.Context, _ string) error {
			order.record(name + ":start")
			run(ctx)
			order.record(name + ":end")
			return nil
		}}
	}
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		Name:         "commands",
		CommandOrder: []string{"hold", "after", "startrun"},
		Commands: map[string]extension.RegisteredCommand{
			"hold": command("hold", func(ctx context.Context) {
				select {
				case <-release:
				case <-ctx.Done():
				}
			}),
			"after":    command("after", func(context.Context) {}),
			"startrun": command("startrun", func(context.Context) { m.turnActive.Store(true) }),
		},
	}}, t.TempDir())
	return m, handle, order
}

// A command that is still running holds back everything behind it in the flush, whichever branch handles the queue: all commands, the retry turn, a run
// still settling, and the commands ahead of the first prompt, which starts its turn only afterwards.
func TestFlushCompactionQueueAwaitsEachExtensionCommand(t *testing.T) {
	for _, tc := range []struct {
		name       string
		willRetry  bool
		busy       bool
		queue      []compactionQueuedMessage
		wantInputs []string
	}{
		{"all commands, idle", false, false, []compactionQueuedMessage{{text: "/hold"}, {text: "/after"}}, nil},
		{"all commands, retry", true, true, []compactionQueuedMessage{{text: "/hold"}, {text: "/after"}}, nil},
		{"all commands, run settling", false, true, []compactionQueuedMessage{{text: "/hold"}, {text: "/after"}}, nil},
		{"commands ahead of the first prompt", false, false, []compactionQueuedMessage{{text: "/hold"}, {text: "/after"}, {text: "prompt"}}, []string{"prompt"}},
		{"a command among the later messages", false, false, []compactionQueuedMessage{{text: "prompt"}, {text: "/hold"}, {text: "/after"}}, []string{"prompt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				m, handle, order := flushOrderMode(t, release)
				m.turnActive.Store(tc.busy)
				m.compactionQueue = slices.Clone(tc.queue)
				m.flushCompactionQueue(t.Context(), tc.willRetry)
				settleSubmissions(m)
				wantWhileHeld := []string{"hold:start"}
				heldInputs := []string(nil)
				if tc.queue[0].text == "prompt" {
					// The first prompt precedes the command and starts without waiting for it.
					heldInputs = []string{"prompt"}
				}
				if got := order.snapshot(); !slices.Equal(got, wantWhileHeld) || !slices.Equal(handle.inputs, heldInputs) {
					t.Fatalf("while /hold runs: commands %q, input events %q; want %q and %q: the messages behind the command were handled before it returned", got, handle.inputs, wantWhileHeld, heldInputs)
				}
				close(release)
				settleSubmissions(m)
				if got, want := order.snapshot(), []string{"hold:start", "hold:end", "after:start", "after:end"}; !slices.Equal(got, want) {
					t.Fatalf("commands = %q, want %q", got, want)
				}
				if !slices.Equal(handle.inputs, tc.wantInputs) {
					t.Fatalf("input events = %q, want %q", handle.inputs, tc.wantInputs)
				}
			})
		})
	}
}

// The first prompt is sent after the commands ahead of it returned, so a run such a command started is the run it joins: a follow-up queued during the
// compaction follows up into it, as Pi's prompt(firstPrompt, { streamingBehavior: firstPrompt.mode }) does once the awaited command started a run.
func TestFlushCompactionQueueFirstPromptJoinsTheRunACommandStarted(t *testing.T) {
	for _, tc := range []struct {
		mode         compactionQueueMode
		wantBehavior string
	}{{compactionQueueFollowUp, "followUp"}, {compactionQueueSteer, "steer"}} {
		t.Run(tc.wantBehavior, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, handle, order := flushOrderMode(t, nil)
				m.compactionQueue = []compactionQueuedMessage{{text: "/startrun"}, {text: "prompt", mode: tc.mode}}
				m.flushCompactionQueue(t.Context(), false)
				settleSubmissions(m)
				if got := order.snapshot(); !slices.Equal(got, []string{"startrun:start", "startrun:end"}) {
					t.Fatalf("commands = %q", got)
				}
				if !slices.Equal(handle.inputs, []string{"prompt"}) || !slices.Equal(handle.behaviors, []string{tc.wantBehavior}) {
					t.Fatalf("input events %q with behaviors %q, want the prompt with %q: it joined the run the command started", handle.inputs, handle.behaviors, tc.wantBehavior)
				}
				steering, followUp := pendingTexts(m)
				wantSteering, wantFollowUp := []string{"prompt"}, []string(nil)
				if tc.mode == compactionQueueFollowUp {
					wantSteering, wantFollowUp = nil, []string{"prompt"}
				}
				if !slices.Equal(steering, wantSteering) || !slices.Equal(followUp, wantFollowUp) {
					t.Fatalf("steering=%q followUp=%q, want %q and %q", steering, followUp, wantSteering, wantFollowUp)
				}
			})
		})
	}
}
