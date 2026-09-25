package session_test

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func userText(content string, timestamp int64) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: content}}, Timestamp: timestamp}}
}

func rawMessage(data []byte) json.RawMessage { return json.RawMessage(data) }
