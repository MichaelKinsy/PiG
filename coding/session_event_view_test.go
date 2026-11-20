package coding

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// publishedAssistantText is the text of the one text block of an assistant message as its JSON writes it, which is how a consumer of Events that serializes the message reads it.
func publishedAssistantText(t *testing.T, message agent.AgentMessage) string {
	t.Helper()
	if message.Assistant == nil {
		t.Fatalf("not an assistant message: %+v", message)
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Content []struct{ Text string }
	}
	if err := json.Unmarshal(raw, &wire); err != nil || len(wire.Content) != 1 {
		t.Fatalf("assistant message %s (%v)", raw, err)
	}
	return wire.Content[0].Text
}

// Pi runs its listeners at the event, so a listener sees the message as it is when the event is published and a later handler's in-place edit of the one object (turn_end's event.message, agent_end's event.messages) reaches the events published after it and no event published before. The consumer of Events here reads after the run, so the message it holds in a message_end or a turn_end event is a view taken at the publication: the one object the handlers edit would show it every edit.
func TestEventsConsumerReadsMessagesAsPublishedAcrossLaterInPlaceEdits(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		turnEndEdit, agentEdit  bool
		wantMessageEnd, wantEnd string
		wantTurnEnd, wantFinal  string
	}{
		{name: "turn_end edit", turnEndEdit: true, wantMessageEnd: "original", wantTurnEnd: "edited by turn_end", wantEnd: "edited by turn_end", wantFinal: "edited by turn_end"},
		{name: "agent_end edit", agentEdit: true, wantMessageEnd: "original", wantTurnEnd: "original", wantEnd: "edited by agent_end", wantFinal: "edited by agent_end"},
		{name: "both edits", turnEndEdit: true, agentEdit: true, wantMessageEnd: "original", wantTurnEnd: "edited by turn_end", wantEnd: "edited by agent_end", wantFinal: "edited by agent_end"},
		{name: "no edit", wantMessageEnd: "original", wantTurnEnd: "original", wantEnd: "original", wantFinal: "original"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edit := func(target agent.AgentMessage, text string) {
				replacement := *target.Assistant
				replacement.Content = []ai.AssistantContentBlock{ai.TextContent{Text: text}}
				agent.ReplaceMessageInPlace(target, agent.AgentMessage{Assistant: &replacement})
			}
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{}}
			if tc.turnEndEdit {
				ext.Handlers["turn_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
					edit(args[0].(extension.TurnEndEvent).Message, "edited by turn_end")
					return nil, nil
				}}
			}
			if tc.agentEdit {
				ext.Handlers["agent_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
					for _, message := range args[0].(extension.AgentEndEvent).Messages {
						if message.Assistant != nil {
							edit(message, "edited by agent_end")
						}
					}
					return nil, nil
				}}
			}
			h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("original", ai.StopReasonStop, 0))
			boundaryPrompt(t, h, "hello")
			h.settle(t)

			var messageEnd, turnEnd, agentEnd agent.AgentMessage
			for _, event := range boundaryEvents[agent.MessageEndEvent](h) {
				if event.Message.Assistant != nil {
					messageEnd = event.Message
				}
			}
			if turnEnds := boundaryEvents[agent.TurnEndEvent](h); len(turnEnds) == 1 {
				turnEnd = turnEnds[0].Message
			} else {
				t.Fatalf("%d turn_end events", len(turnEnds))
			}
			for _, event := range boundaryEvents[agent.AgentEndEvent](h) {
				for _, message := range event.Messages {
					if message.Assistant != nil {
						agentEnd = message
					}
				}
			}
			var transcript agent.AgentMessage
			for _, message := range h.session.Messages() {
				if message.Assistant != nil {
					transcript = message
				}
			}
			if messageEnd.Assistant == nil || agentEnd.Assistant == nil || transcript.Assistant == nil {
				t.Fatalf("message_end %v, agent_end %v, transcript %v", messageEnd.Assistant, agentEnd.Assistant, transcript.Assistant)
			}
			for _, row := range []struct {
				event   string
				message agent.AgentMessage
				want    string
			}{
				{"message_end", messageEnd, tc.wantMessageEnd},
				{"turn_end", turnEnd, tc.wantTurnEnd},
				{"agent_end", agentEnd, tc.wantEnd},
				{"transcript", transcript, tc.wantFinal},
			} {
				if got := publishedAssistantText(t, row.message); got != row.want {
					t.Errorf("%s message reads %q, want %q", row.event, got, row.want)
				}
			}
			// The events and the transcript still name one message.
			if messageEnd.Identity() != transcript.Identity() || turnEnd.Identity() != transcript.Identity() || agentEnd.Identity() != transcript.Identity() {
				t.Errorf("identities: message_end %v, turn_end %v, agent_end %v, transcript %v", messageEnd.Identity(), turnEnd.Identity(), agentEnd.Identity(), transcript.Identity())
			}
		})
	}
}
