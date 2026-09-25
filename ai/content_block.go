package ai

// ContentBlocks decodes Pi's closed content union.

import (
	"encoding/json"
	"fmt"
)

// ContentBlocks is a slice alias that knows how to decode the
// discriminated-union JSON form.
//
// Use this when persisting/loading messages from disk:
//
//	var msg struct {
//	    Role    string        `json:"role"`
//	    Content ContentBlocks `json:"content"`
//	}
//
// When you only need the slice as `[]ContentBlock` (the historical
// shape used by streams + tool execution), call `.Blocks()`.
type ContentBlocks []ContentBlock

func (cb ContentBlocks) Blocks() []ContentBlock { return []ContentBlock(cb) }

// MarshalJSON delegates to the default slice encoder.
func (cb ContentBlocks) MarshalJSON() ([]byte, error) {
	return json.Marshal([]ContentBlock(cb))
}

// UnmarshalJSON sniffs each element's `type` field and decodes into
// the matching concrete type.
func (cb *ContentBlocks) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return fmt.Errorf("ContentBlocks: %w", err)
	}
	out := make([]ContentBlock, 0, len(raws))
	for i, r := range raws {
		blk, err := UnmarshalContentBlock(r)
		if err != nil {
			return fmt.Errorf("ContentBlocks[%d]: %w", i, err)
		}
		out = append(out, blk)
	}
	*cb = out
	return nil
}

// UnmarshalContentBlock decodes a single content-block JSON value into
// the appropriate concrete type. Unknown `type` values are accepted as
// TextContent with the JSON re-encoded as the text payload: this
// preserves data we don't recognise rather than silently dropping it.
func UnmarshalContentBlock(data []byte) (ContentBlock, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("UnmarshalContentBlock: type probe: %w", err)
	}
	switch probe.Type {
	case "text":
		var t TextContent
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, err
		}
		return t, nil
	case "image":
		var t ImageContent
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, err
		}
		return t, nil
	case "toolCall":
		var content ToolCall
		if err := json.Unmarshal(data, &content); err != nil {
			return nil, err
		}
		return content, nil
	case "thinking":
		var t ThinkingContent
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, err
		}
		return t, nil
	default:
		return nil, fmt.Errorf("unknown content block type %q", probe.Type)
	}
}
