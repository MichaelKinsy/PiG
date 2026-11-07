package sdk

import "github.com/MichaelKinsy/PiG/extensions/sdk/json"

// unmarshalFrameMember is json.Unmarshal for a member of a frame the read loop took, such as a request's Args. json.UnmarshalFrame validated
// the whole frame, and a member of a valid JSON document is valid JSON, so the member is decoded without a second validity pass. A fused
// extension's Args are the host encoder's own output (RequestPayload.encodedArgs), valid by construction.
//
// pig additive (D19): measured on a 43 MiB before_provider_request payload, the repeated validity pass of each decode was a quarter of the decode.
func unmarshalFrameMember(member []byte, v any) error {
	return json.UnmarshalTrusted(member, v)
}

// unmarshalFrameObject is unmarshalFrameMember into a map[string]any that also returns the member order of the objects it holds (json.UnmarshalTrustedObject).
func unmarshalFrameObject(member []byte) (map[string]any, *json.ObjectOrder, error) {
	return json.UnmarshalTrustedObject(member)
}
