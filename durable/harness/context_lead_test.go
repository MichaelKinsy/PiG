package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// A task runtime reads through the kept context range. Leading with the system message (harness-context.test.ts "leads with
// a system message that only user messages precede") must hold for a read the cache rebuilds, extends, and cuts off.
func TestTaskRuntimeContextLeadsWithTheSystemMessage(t *testing.T) {
	setup := setupContext(t)
	var reads syncList[[]string]
	reader := tkOneStep("test.context-lead", func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
		view, err := runtime.Context(ctx, runtime.ConversationId(), nil)
		if err != nil {
			return err
		}
		reads.add(describeMessages(view.Messages))
		return tkComplete(ctx, runtime)
	})
	opened := tkOpenRoot(t, []durable.AnyTask{reader.Erased()}, tkOptions{storage: cacheTestStorage(t)})
	setup.root = opened.root
	setup.harness = opened.harness
	read := func() []string {
		id := tkStart(t, opened.root, reader.Erased())
		opened.harness.Resume()
		tkWaitOutcome(t, opened.harness, id)
		all := reads.all()
		return all[len(all)-1]
	}
	setup.message(user("first"))
	setup.message(user("steered"))
	setup.message(system(ai.PromptSection{Name: "preamble", Value: new("You help.")}), "pi.system")
	expectStrings(t, read(), []string{"system:preamble", "user:first", "user:steered"})

	// The next read extends the kept range.
	setup.message(assistant("answer"))
	setup.message(user("next"))
	setup.message(system(ai.PromptSection{Name: "cwd", Value: new("/repo")}), "pi.system")
	expectStrings(t, read(), []string{"system:preamble", "user:first", "user:steered", "assistant:answer", "user:next", "system:cwd"})

	// After a reset, the baseline written after the handoff leads too.
	setup.append(durable.EntryDraft{Kind: "reset", HeadSelf: true, Model: []ai.Message{user("handoff")}})
	setup.message(system(ai.PromptSection{Name: "preamble", Value: new("Baseline.")}), "pi.system")
	expectStrings(t, read(), []string{"system:preamble", "user:handoff"})

	// Pi 5713c950 (#10904): after a compaction that keeps assistant messages the baseline leads, in a read that rebuilds the
	// range and in one that extends it.
	kept := setup.message(user("kept"))
	setup.message(assistant("kept answer"))
	setup.append(durable.EntryDraft{Kind: "summary", Head: new(kept.Id), Model: []ai.Message{user("summary")}})
	setup.message(user("next"))
	expectStrings(t, read(), []string{"user:summary", "user:kept", "assistant:kept answer", "user:next"})
	setup.message(system(ai.PromptSection{Name: "plan", Value: new("Plan.")}), "pi.system")
	expectStrings(t, read(), []string{"system:plan", "user:summary", "user:kept", "assistant:kept answer", "user:next"})
	setup.message(assistant("done"))
	setup.message(system(ai.PromptSection{Name: "cwd", Value: new("/repo")}), "pi.system")
	expectStrings(t, read(), []string{"system:plan", "user:summary", "user:kept", "assistant:kept answer", "user:next", "assistant:done", "system:cwd"})
	mustClose(t, opened.harness)
}
