package ai

import (
	"encoding/json/jsontext"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonwire"
)

// The MarshalJSONTo methods below are MarshalJSON written straight into the encoder of the encoding/json that is marshaling the value, from the
// appender of the JSON.stringify text each type already has (see jsonwire.WriteStringify). encoding/json prefers MarshalJSONTo to MarshalJSON,
// which would encode into a slice of its own that the encoder then validates and copies. The sdk encoder, which does not know MarshalJSONTo,
// still calls MarshalJSON, so both write the same bytes (content_json_to_test.go).

// MarshalJSONTo writes the content block the way MarshalJSON does.
func (content TextContent) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonwire.WriteStringify(encoder, content.AppendStringify)
}

// MarshalJSONTo writes the content block the way MarshalJSON does.
func (content ImageContent) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonwire.WriteStringify(encoder, content.AppendStringify)
}

// MarshalJSONTo writes the content block the way MarshalJSON does.
func (content ThinkingContent) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonwire.WriteStringify(encoder, content.AppendStringify)
}

// MarshalJSONTo writes the content block the way MarshalJSON does.
func (content ToolCall) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonwire.WriteStringify(encoder, content.AppendStringify)
}

// MarshalJSONTo writes the usage the way MarshalJSON does.
func (usage Usage) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonwire.WriteStringify(encoder, usage.AppendStringify)
}

// MarshalJSONTo writes the blocks the way MarshalJSON does.
func (cb ContentBlocks) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonwire.WriteStringify(encoder, func(dst []byte) ([]byte, error) { return sdkjson.AppendStringify(dst, []ContentBlock(cb)) })
}
