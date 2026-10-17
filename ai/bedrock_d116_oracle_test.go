package ai_test

import "encoding/json"

// piBedrockPayloadWithD116 rewrites Pi's Bedrock Converse payload into the shape D116 specifies: adjacent messages of one role become one message whose content is the concatenation in order. Pi's order is bedrock-converse-stream.ts convertMessages; the payload oracle compares PiG with this rewrite.
func piBedrockPayloadWithD116(payload string) string {
	var body map[string]any
	if json.Unmarshal([]byte(payload), &body) != nil {
		return payload
	}
	messages, ok := body["messages"].([]any)
	if !ok {
		return payload
	}
	var out []any
	for _, message := range messages {
		current, _ := message.(map[string]any)
		if len(out) > 0 {
			previous, _ := out[len(out)-1].(map[string]any)
			if previous["role"] == current["role"] {
				previous["content"] = append(previous["content"].([]any), current["content"].([]any)...)
				continue
			}
		}
		out = append(out, message)
	}
	body["messages"] = out
	encoded, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return string(encoded)
}
