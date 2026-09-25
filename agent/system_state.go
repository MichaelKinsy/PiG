package agent

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func (a *Agent) toolDeclarations() []ai.ToolSchema {
	tools := make([]ai.ToolSchema, len(a.opts.Tools))
	for i, t := range a.opts.Tools {
		tools[i] = ai.ToToolDeclaration(t.Schema())
	}
	return tools
}

func systemMessages(messages []AgentMessage) []ai.Message {
	var systems []ai.Message
	for _, m := range messages {
		if m.System != nil {
			systems = append(systems, *m.System)
		}
	}
	return systems
}

// declareToolChanges replaces pending tool intent with the delta from the committed transcript to the executable loadout.
func (a *Agent) declareToolChanges(context, pending []AgentMessage) []AgentMessage {
	index := -1
	for i, pendingMessage := range slices.Backward(pending) {
		if pendingMessage.System != nil {
			index = i
			break
		}
	}
	baseline := slices.Clone(pending)
	if index >= 0 {
		baseline[index] = withToolChanges(pending[index], ai.ToolStateChanges{})
	}
	changes := ai.GetToolStateChanges(ai.GetCurrentTools(systemMessages(slices.Concat(context, baseline))), a.toolDeclarations())
	unchanged := len(changes.ToolsAdded) == 0 && len(changes.ToolsRemoved) == 0
	if index >= 0 {
		old := pending[index].System
		if unchanged && len(old.ToolsAdded) == 0 && len(old.ToolsRemoved) == 0 {
			return pending
		}
		baseline[index] = withToolChanges(pending[index], changes)
		return baseline
	}
	if unchanged {
		return pending
	}
	update := withToolChanges(AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), Timestamp: time.Now().UnixMilli()}}, changes)
	index = slices.IndexFunc(pending, func(m AgentMessage) bool { return m.System == nil })
	if index < 0 {
		index = len(pending)
	}
	return slices.Concat(pending[:index], []AgentMessage{update}, pending[index:])
}

func withToolChanges(message AgentMessage, changes ai.ToolStateChanges) AgentMessage {
	value := *message.System
	value.ToolsAdded = changes.ToolsAdded
	value.ToolsRemoved = changes.ToolsRemoved
	return AgentMessage{System: &value}
}

func decodeSystemMessage(data []byte) (*ai.SystemMessage, error) {
	var wire struct {
		Content      json.RawMessage    `json:"content"`
		Sections     ai.OrderedSections `json:"sections"`
		ToolsAdded   []ai.ToolSchema    `json:"toolsAdded"`
		ToolsRemoved []ai.ToolReference `json:"toolsRemoved"`
		Timestamp    int64              `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	var content ai.SystemContent
	var text string
	if err := json.Unmarshal(wire.Content, &text); err == nil {
		content = ai.SystemText(text)
	} else {
		var blocks ai.SystemTextBlocks
		if err := json.Unmarshal(wire.Content, &blocks); err != nil {
			return nil, err
		}
		content = blocks
	}
	return &ai.SystemMessage{Content: content, Sections: wire.Sections, ToolsAdded: wire.ToolsAdded, ToolsRemoved: wire.ToolsRemoved, Timestamp: wire.Timestamp}, nil
}

// projectSystemPrompt preserves the active tool declarations while replacing instructions at the provider boundary.
func projectSystemPrompt(messages []ai.Message, prompt string) []ai.Message {
	head := ai.GetCurrentSystemMessage(messages)
	if head == nil {
		head = &ai.SystemMessage{}
	}
	head.Content = ai.SystemText(prompt)
	head.Sections = nil
	projected := []ai.Message{*head}
	for _, m := range messages {
		if _, ok := m.(ai.SystemMessage); !ok {
			projected = append(projected, m)
		}
	}
	return projected
}
