package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestTurnEndIncludesPersistedEntryIDs(t *testing.T) {
	turns := make(chan extension.TurnEndEvent, 2)
	ext := extension.Extension{Path: "turn-ids", Handlers: map[string][]extension.HandlerFn{
		"turn_end": {func(args ...any) (any, error) {
			turns <- args[0].(extension.TurnEndEvent)
			return nil, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{
		tools:     []agent.AgentTool{&fakeTool{name: "echo"}},
		extension: ext,
	}, fauxToolCall("echo"), fauxReply("done", ai.StopReasonStop, time.Millisecond))
	if _, err := h.session.Send(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	h.settle(t)

	first := <-turns
	if first.MessageEntryID == "" {
		t.Fatal("turn_end omitted persisted assistant entry ID")
	}
	if len(first.ToolResultEntryIds) != 1 || first.ToolResultEntryIds[0] == "" {
		t.Fatalf("turn_end tool result entry IDs = %q", first.ToolResultEntryIds)
	}
	second := <-turns
	if second.MessageEntryID == "" || second.MessageEntryID == first.MessageEntryID || len(second.ToolResultEntryIds) != 0 {
		t.Fatalf("second turn retained the first turn's IDs: %+v", second)
	}
	for _, pair := range []struct {
		id      string
		message any
	}{
		{first.MessageEntryID, first.Message},
		{first.ToolResultEntryIds[0], first.ToolResults[0]},
		{second.MessageEntryID, second.Message},
	} {
		entry, ok := h.session.Inner().EntryByID(pair.id)
		if !ok {
			t.Fatalf("turn_end ID %q does not resolve to a persisted entry", pair.id)
		}
		message, ok := entry.AsMessage()
		if !ok {
			t.Fatalf("turn_end ID %q resolved to a %s entry", pair.id, entry.Base.Type)
		}
		got, err := json.Marshal(message.Message)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(pair.message)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("entry %s = %s, want event message %s", pair.id, got, want)
		}
	}
}
