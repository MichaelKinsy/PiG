package main

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestRPCSystemMessagePreservesTranscriptState(t *testing.T) {
	message := agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("instructions"), Sections: ai.OrderedSections{{Name: "note", Value: new("kept")}}, ToolsRemoved: []ai.ToolReference{{Name: "old"}}, Timestamp: 123}}
	events, err := rpcAgentEvent(agent.MessageEndEvent{Message: message})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Message agent.AgentMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	system := decoded.Message.System
	if system == nil || system.Content != ai.SystemText("instructions") || len(system.Sections) != 1 || *system.Sections[0].Value != "kept" || len(system.ToolsRemoved) != 1 || system.ToolsRemoved[0].Name != "old" || system.Timestamp != 123 {
		t.Fatalf("system wire lost state: %s", raw)
	}
}
