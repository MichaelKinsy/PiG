package agent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A task follows what the provider's run delivers, as Pi's loop does. Pi's faux provider waits for each chunk in scheduleChunk
// (packages/ai/src/providers/faux.ts): without a rate the wait resolves from queueMicrotask, so the whole response and the loop's end events are
// one task and no timer callback runs inside it; with a rate each chunk waits for a setTimeout, so the loop turns before every chunk and each is
// a task of its own, which ends where the reader waits for the next timer.
func TestTaskFollowsThePacingOfTheProvider(t *testing.T) {
	run := func(t *testing.T, tokensPerSecond int) []string {
		t.Helper()
		provider := ai.NewFauxProvider(ai.FauxConfig{TokensPerSecond: tokensPerSecond})
		provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{
			ai.FauxText("an answer that the provider streams in several chunks of text"),
		}})})
		a, log, ctx, _ := taskBounds(AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}}})
		if _, err := a.Send(ctx, "run"); err != nil {
			t.Fatal(err)
		}
		return log.list()
	}
	count := func(log []string, entry string) int {
		n := 0
		for _, e := range log {
			if e == entry {
				n++
			}
		}
		return n
	}

	t.Run("microtask", func(t *testing.T) {
		log := run(t, 0)
		if updates := count(log, "message_update"); updates < 4 {
			t.Fatalf("the response has %d message_update events, want several chunks: %q", updates, log)
		}
		open, closed := slices.Index(log, "task{"), slices.Index(log, "}task")
		if count(log, "task{") != 1 || count(log, "}task") != 1 || open+1 != slices.Index(log, "message_start") || closed != len(log)-1 || log[closed-1] != "agent_end" {
			t.Fatalf("events and task bounds = %q, want one task from message_start to agent_end", log)
		}
	})

	t.Run("timer", func(t *testing.T) {
		// A rate this high makes each chunk's timer the shortest faux.ts arms (1 ms).
		log := run(t, 1_000_000)
		tasks := count(log, "task{")
		if tasks != count(log, "}task") || tasks < 3 {
			t.Fatalf("events and task bounds = %q, want a task per chunk the provider's timer released", log)
		}
		// Every task but the last ends on a message_update: the reader waits for the next chunk's timer there.
		for i, entry := range log {
			if entry == "}task" && i != len(log)-1 && log[i-1] != "message_update" {
				t.Fatalf("a task ends after %s at %d, want after a message_update: %q", log[i-1], i, log)
			}
		}
		if log[len(log)-1] != "}task" || log[len(log)-2] != "agent_end" {
			t.Fatalf("events and task bounds = %q, want the last task to end behind agent_end", log)
		}
	})
}
