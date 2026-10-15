//go:build go1.27 && goexperiment.jsonv2

package ai

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// MarshalJSONTo is MarshalJSON written straight into the request encoder under the caller's options, so the body (its whole input
// included) is not encoded into a buffer of its own and then validated and copied again. Without the json/v2 experiment
// encoding/json calls MarshalJSON.
func (req respRequest) MarshalJSONTo(encoder *jsontext.Encoder) error {
	return jsonv2.MarshalEncode(encoder, req.wire())
}

// MarshalJSONTo is MarshalJSON written straight into the request encoder (respRequest.MarshalJSONTo).
func (t ResponsesTool) MarshalJSONTo(encoder *jsontext.Encoder) error {
	wire, err := t.wire()
	if err != nil {
		return err
	}
	return jsonv2.MarshalEncode(encoder, wire)
}
