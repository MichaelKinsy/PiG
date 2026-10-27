package ai

import "errors"

// anthStartInput is the input member of a content_block_start record, read as JSON.parse reads it. encoding/json refuses a number out of float64 range
// where an interface is wanted and ends the turn; JSON.parse reads it as an infinity, which the stream keeps in the call's arguments until the streamed
// text replaces them at content_block_stop (anthropic-messages.ts:659-674, :743-747).
type anthStartInput struct{ value any }

func (input *anthStartInput) UnmarshalJSON(data []byte) error {
	value, ok := jsonParseValue(string(data))
	if !ok {
		return errors.New("invalid JSON in a tool_use input")
	}
	input.value = value
	return nil
}
