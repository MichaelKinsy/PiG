package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalJSON decodes the upstream AssistantMessage wire shape, including
// its closed content-block union.
func (message *AssistantMessage) UnmarshalJSON(data []byte) error {
	type plain AssistantMessage
	var wire struct {
		plain
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	decoded := AssistantMessage(wire.plain)
	decoded.ErrorBeforeTimestamp = ErrorKeyBeforeTimestamp(data)
	if wire.Content != nil {
		decoded.Content = make([]AssistantContentBlock, 0, len(wire.Content))
		for index, raw := range wire.Content {
			block, err := UnmarshalContentBlock(raw)
			if err != nil {
				return fmt.Errorf("assistant content[%d]: %w", index, err)
			}
			assistantBlock, ok := block.(AssistantContentBlock)
			if !ok {
				return fmt.Errorf("assistant content[%d]: %s is not an assistant content block", index, block.contentType())
			}
			decoded.Content = append(decoded.Content, assistantBlock)
		}
	}
	*message = decoded
	return nil
}

// ErrorKeyBeforeTimestamp reports whether the top-level "errorMessage" member of an assistant message object precedes its
// "timestamp" member: JavaScript keeps the order a message was built in, and a stored message decodes to the order it holds.
func ErrorKeyBeforeTimestamp(data []byte) bool {
	if !bytes.Contains(data, []byte(`"errorMessage"`)) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return false
		}
		switch key {
		case "errorMessage":
			return true
		case "timestamp":
			return false
		}
		var skip json.RawMessage
		if decoder.Decode(&skip) != nil {
			return false
		}
	}
	return false
}
