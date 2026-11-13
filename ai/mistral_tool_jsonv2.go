//go:build go1.27 && goexperiment.jsonv2 && !pig_strip_mistral_conversations

package ai

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// MarshalJSONTo is MarshalJSON written straight into the request encoder (oaiToolFunction.MarshalJSONTo): each function tool is not encoded into
// its own buffer and copied into the request again. A tool whose name or description holds a lone UTF-16 surrogate is written as MarshalJSON
// writes it, with the escape.
func (f mistralToolFn) MarshalJSONTo(encoder *jsontext.Encoder) error {
	if jsstring.HoldsSurrogate(f.Name) || jsstring.HoldsSurrogate(f.Description) {
		return encodeMarshaled(encoder, f)
	}
	wire, err := f.wire()
	if err != nil {
		return err
	}
	return jsonv2.MarshalEncode(encoder, wire)
}
