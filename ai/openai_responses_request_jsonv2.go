//go:build go1.27 && goexperiment.jsonv2

package ai

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// MarshalJSONTo is MarshalJSON written straight into the request encoder under the caller's options, so the body (its whole input
// included) is not encoded into a buffer of its own and then validated and copied again. Without the json/v2 experiment
// encoding/json calls MarshalJSON.
func (req respRequest) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonv2.MarshalEncode(encoder, req.wire())
}

// MarshalJSONTo is MarshalJSON written straight into the request encoder (respRequest.MarshalJSONTo).
func (t ResponsesTool) MarshalJSONTo(encoder *jsontext.Encoder) error {
	if jsstring.HoldsSurrogate(t.Name) || jsstring.HoldsSurrogate(t.Description) {
		return encodeMarshaled(encoder, t)
	}
	wire, err := t.wire()
	if err != nil {
		return err
	}
	return jsonv2.MarshalEncode(encoder, wire)
}

// MarshalJSONTo is MarshalJSON written straight into the request encoder (oaiMessage.MarshalJSONTo), on the pointer for the same reason:
// an item whose members go out in the order of the fields is not encoded into its own buffer and copied into the request again.
func (item *ResponsesInputItem) MarshalJSONTo(encoder *jsontext.Encoder) error {
	if item.stored != nil || item.Type == "tool_search_call" || item.Type == "tool_search_output" || item.Type == "function_call" || item.Type == "custom_tool_call" {
		return jsonv2.MarshalEncode(encoder, item.wire())
	}
	return jsonv2.MarshalEncode(encoder, (*responsesInputItemFields)(item))
}

// responsesInputItemFields is ResponsesInputItem without its marshaler methods.
type responsesInputItemFields ResponsesInputItem
