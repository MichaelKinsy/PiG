package agent

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func toolDeclarations(current []AgentTool) []ai.ToolSchema {
	tools := make([]ai.ToolSchema, len(current))
	for i, t := range current {
		tools[i] = ai.ToToolDeclaration(t.Schema())
	}
	return tools
}

// toolSchemas are the loadout's schemas as the tools return them. Comparing them needs no declaration copy, because
// ai.DeclarationsEqual ignores prompt guidelines and ai.GetToolStateChanges copies only the declarations it adds.
func toolSchemas(current []AgentTool) []ai.ToolSchema {
	schemas := make([]ai.ToolSchema, len(current))
	for i, t := range current {
		schemas[i] = t.Schema()
	}
	return schemas
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
func (h *loopHost) declareToolChanges(context, pending []AgentMessage) []AgentMessage {
	return declareToolChanges(h.tools(), context, pending)
}

func declareToolChanges(tools []AgentTool, context, pending []AgentMessage) []AgentMessage {
	index := -1
	for i, pendingMessage := range slices.Backward(pending) {
		if pendingMessage.System != nil {
			index = i
			break
		}
	}
	if len(pending) == 0 {
		changes := ai.GetToolStateChangesFrom(systemOnly(context), toolSchemas(tools))
		if len(changes.ToolsAdded) == 0 && len(changes.ToolsRemoved) == 0 {
			return pending
		}
		return []AgentMessage{withToolChanges(AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), Timestamp: time.Now().UnixMilli()}}, changes)}
	}
	baseline := slices.Clone(pending)
	if index >= 0 {
		baseline[index] = withToolChanges(pending[index], ai.ToolStateChanges{})
	}
	changes := ai.GetToolStateChangesFrom(slices.Concat(systemOnly(context), systemOnly(baseline)), toolSchemas(tools))
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

// systemOnly is messages without the ones that are not system messages. The replay of tool declarations reads only system messages, so a replay of the result equals a replay of messages; it walks a history of any length with a pointer test per message, and the replay itself walks the few system messages.
func systemOnly(messages []AgentMessage) []AgentMessage {
	count := 0
	for i := range messages {
		if messages[i].System != nil {
			count++
		}
	}
	if count == len(messages) {
		return messages
	}
	out := make([]AgentMessage, 0, count)
	for i := range messages {
		if messages[i].System != nil {
			out = append(out, messages[i])
		}
	}
	return out
}

func withToolChanges(message AgentMessage, changes ai.ToolStateChanges) AgentMessage {
	value := *message.System
	value.ToolsAdded = changes.ToolsAdded
	value.ToolsRemoved = changes.ToolsRemoved
	return AgentMessage{System: &value}
}

// systemMessageWire is the object a system message decodes from.
type systemMessageWire struct {
	Content      json.RawMessage    `json:"content"`
	Sections     ai.OrderedSections `json:"sections"`
	ToolsAdded   []ai.ToolSchema    `json:"toolsAdded"`
	ToolsRemoved []ai.ToolReference `json:"toolsRemoved"`
	Timestamp    jsInt64            `json:"timestamp"`
}

func decodeSystemMessage(data []byte) (*ai.SystemMessage, error) {
	var wire systemMessageWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	content, err := decodeSystemContent(wire.Content)
	if err != nil {
		return nil, err
	}
	return &ai.SystemMessage{Content: content, Sections: wire.Sections, ToolsAdded: wire.ToolsAdded, ToolsRemoved: wire.ToolsRemoved, Timestamp: int64(wire.Timestamp)}, nil
}

// decodeSystemContent decodes a system message's content: a string or text blocks, and nil when it is null or missing (Pi parses a session line without validation; the context projects { ...message, content: "" }, session-manager.ts sessionEntryToContextMessages).
func decodeSystemContent(raw []byte) (ai.SystemContent, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return ai.SystemText(text), nil
	}
	var blocks ai.SystemTextBlocks
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
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
