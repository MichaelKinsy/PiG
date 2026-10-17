//go:build go1.27 && goexperiment.jsonv2

package ai

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// MarshalJSONTo is MarshalJSON written straight into the request encoder under the caller's options: each content block of a long
// history is not encoded into its own buffer and then validated and copied into the request again. Without the json/v2 experiment
// encoding/json calls MarshalJSON.
func (block anthContentBlock) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonv2.MarshalEncode(encoder, block.wire())
}

// MarshalJSONTo is MarshalJSON written straight into the request encoder (anthContentBlock.MarshalJSONTo).
func (t anthTool) MarshalJSONTo(encoder *jsontext.Encoder) error {
	wire, err := t.wire()
	if err != nil {
		return err
	}
	return jsonv2.MarshalEncode(encoder, wire)
}

// spliceMessages is the marshaler that takes each message of the request from the message cache (requestMessageSplicer).
func (anthRequest) spliceMessages(s *stringSplice) *jsonv2.Marshalers {
	return spliceMessages(s, anthMessageKey)
}
