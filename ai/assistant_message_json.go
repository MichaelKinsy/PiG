package ai

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/jsonopts"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// UnmarshalJSON decodes the upstream AssistantMessage wire shape, including
// its closed content-block union.
func (message *AssistantMessage) UnmarshalJSON(data []byte) error {
	type plain AssistantMessage
	var wire struct {
		plain
		Content []json.RawMessage `json:"content"`
	}
	if err := jsstring.UnmarshalKeepingSurrogates(data, &wire); err != nil {
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
	decoder := jsontext.NewDecoder(bytes.NewReader(data), jsonopts.Parse)
	if token, err := decoder.ReadToken(); err != nil || token.Kind() != '{' {
		return false
	}
	for decoder.PeekKind() != '}' {
		key, err := decoder.ReadToken()
		if err != nil {
			return false
		}
		switch key.String() {
		case "errorMessage":
			return true
		case "timestamp":
			return false
		}
		if decoder.SkipValue() != nil {
			return false
		}
	}
	return false
}
