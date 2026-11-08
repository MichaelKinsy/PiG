package coding

import (
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
)

// messageSequenceLimit bounds how many messages a Session numbers at once; the oldest are forgotten first.
const messageSequenceLimit = 1024

// messageSequences numbers the messages of one Session, so that an extension process can tell which of its payloads Pi shares one object between: the message_start, message_end, turn_end and agent_end events and the session entry message_end persists (agent-session.ts _entryIdsByMessage is its analogue for entry ids). The Session sends each number with the payloads that carry the message and with its entry.
type messageSequences struct {
	mu        sync.Mutex
	last      uint64
	byMessage map[any]uint64
	// byToolCall is the number of the newest tool result message with a tool call id: a turn_end event carries copies of its tool results.
	byToolCall map[string]uint64
	order      []messageSequenceKey
}

type messageSequenceKey struct {
	message    any
	toolCallID string
}

// Sequence implements icodingagent.MessageSequencer.
func (m *messageSequences) Sequence(message agent.AgentMessage) uint64 {
	key := message.Identity()
	if key == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if seq, ok := m.byMessage[key]; ok {
		return seq
	}
	m.last++
	if m.byMessage == nil {
		m.byMessage = make(map[any]uint64)
	}
	m.byMessage[key] = m.last
	entry := messageSequenceKey{message: key}
	if message.ToolResult != nil && message.ToolResult.ToolCallID != "" {
		if m.byToolCall == nil {
			m.byToolCall = make(map[string]uint64)
		}
		m.byToolCall[message.ToolResult.ToolCallID] = m.last
		entry.toolCallID = message.ToolResult.ToolCallID
	}
	m.order = append(m.order, entry)
	if len(m.order) > messageSequenceLimit {
		oldest := m.order[0]
		m.order[0] = messageSequenceKey{}
		m.order = m.order[1:]
		seq := m.byMessage[oldest.message]
		delete(m.byMessage, oldest.message)
		if oldest.toolCallID != "" && m.byToolCall[oldest.toolCallID] == seq {
			delete(m.byToolCall, oldest.toolCallID)
		}
	}
	return m.last
}

// Known implements icodingagent.MessageSequencer.
func (m *messageSequences) Known(message agent.AgentMessage) uint64 {
	key := message.Identity()
	if key == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if seq, ok := m.byMessage[key]; ok {
		return seq
	}
	if message.ToolResult != nil {
		return m.byToolCall[message.ToolResult.ToolCallID]
	}
	return 0
}
