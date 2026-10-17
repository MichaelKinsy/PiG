//go:build go1.27 && goexperiment.jsonv2

package ai

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// MarshalJSONTo is MarshalJSON written straight into the request encoder under the caller's options. encoding/json prefers it to MarshalJSON, which would encode each message into its own buffer and then validate and copy that buffer into the request. Without the json/v2 experiment encoding/json calls MarshalJSON.
//
// It is on the pointer: encoding/json calls it on an element of a slice without copying the element, and handing the encoder a pointer to the fields keeps it from boxing and then copying each message again. A message the encoder could only reach by a copy takes MarshalJSON.
func (message *oaiMessage) MarshalJSONTo(encoder *jsontext.Encoder) error {
	if message.Tools != nil {
		return jsonv2.MarshalEncode(encoder, message.wire())
	}
	return jsonv2.MarshalEncode(encoder, (*oaiMessageFields)(message))
}

// oaiMessageFields is oaiMessage without its marshaler methods.
type oaiMessageFields oaiMessage

// MarshalJSONTo is MarshalJSON written straight into the request encoder (oaiMessage.MarshalJSONTo): each function tool is not encoded
// into its own buffer and copied into the request again.
func (f oaiToolFunction) MarshalJSONTo(encoder *jsontext.Encoder) error {
	wire, err := f.wire()
	if err != nil {
		return err
	}
	return jsonv2.MarshalEncode(encoder, wire)
}

// spliceMessages is the marshaler that takes each message of the request from the message cache (requestMessageSplicer).
func (oaiRequest) spliceMessages(s *stringSplice) *jsonv2.Marshalers {
	return spliceMessages(s, oaiMessageKey)
}
