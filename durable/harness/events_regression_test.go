package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

type foreignHarness struct{ Harness }

// watchEvents over a value that is not a Harness rejects with "Not a Harness" (view.ts:63-67, events.ts:147); it does not crash the caller.
func TestWatchEventsRejectsForeignHarness(t *testing.T) {
	stream, err := WatchEvents(testContext, foreignHarness{}, durable.ROOT_CONVERSATION_ID)
	if stream != nil || err == nil || err.Error() != "Not a Harness" {
		t.Fatalf("stream %v, err %v", stream, err)
	}
}

func generationWrite(id durable.TaskId, status durable.TaskStatus) durable.CommitPublication {
	record := durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
		Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "pi.generation",
		State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: status},
	}
	if status == durable.TaskCompleting || status == durable.TaskTerminal {
		record.State.Outcome = &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted}
	}
	return durable.CommitPublication{Changes: []durable.CommitChange{durable.TaskWrite{Value: record}}}
}

func turnEnds(events []AgentEvent) int { return len(eventsOfType(events, "turn_end")) }

// A generation's turn ends once: at its completing hold or at terminal, whichever comes first; a hold seen at attachment already ended its turn (events.ts:126-130, 277-289).
func TestTranslateEventsEndsATurnOncePerGeneration(t *testing.T) {
	view := ConversationView{}
	translate := func(held map[durable.TaskId]bool, status durable.TaskStatus) int {
		return turnEnds(translateEvents(durable.ROOT_CONVERSATION_ID, view, view, nil, generationWrite(7, status), held))
	}

	held := map[durable.TaskId]bool{}
	if ends := translate(held, durable.TaskCompleting); ends != 1 {
		t.Fatalf("completing hold: %d turn ends", ends)
	}
	if ends := translate(held, durable.TaskCompleting); ends != 0 {
		t.Fatalf("repeated completing hold: %d turn ends", ends)
	}
	if ends := translate(held, durable.TaskTerminal); ends != 0 {
		t.Fatalf("terminal after hold: %d turn ends", ends)
	}
	if held[7] {
		t.Fatal("terminal kept the hold")
	}

	if ends := translate(map[durable.TaskId]bool{}, durable.TaskTerminal); ends != 1 {
		t.Fatalf("terminal without hold: %d turn ends", ends)
	}
	if ends := translate(map[durable.TaskId]bool{7: true}, durable.TaskTerminal); ends != 0 {
		t.Fatalf("terminal of a hold seen at attachment: %d turn ends", ends)
	}
}
