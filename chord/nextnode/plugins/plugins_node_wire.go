package plugins

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// The JSON form of values on the control channel (node/wire.mjs): a `bytes()` leaf ([]byte) is `{"$bytes": "<base64>"}`, and an
// object key that starts with `$` gets one more `$` on the wire, so no plain datum is mistaken for bytes. Every value a member,
// call, watch or frame carries crosses in this form, both ways.

// wireEncode is v in the control channel's JSON form.
func wireEncode(v any) any {
	switch typed := v.(type) {
	case []byte:
		return delta.JsonObjectOf("$bytes", base64.StdEncoding.EncodeToString(typed))
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = wireEncode(item)
		}
		return out
	case *delta.JsonObject:
		if typed == nil {
			return nil
		}
		out := delta.NewJsonObject(typed.Len())
		for _, key := range typed.Keys() {
			member, _ := typed.Get(key)
			name := key
			if len(key) > 0 && key[0] == '$' {
				name = "$" + key
			}
			out.Set(name, wireEncode(member))
		}
		return out
	}
	return v
}

// wireDecode is wireEncode's inverse over decoded JSON.
func wireDecode(v any) (any, error) {
	switch typed := v.(type) {
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			decoded, err := wireDecode(item)
			if err != nil {
				return nil, err
			}
			out[i] = decoded
		}
		return out, nil
	case *delta.JsonObject:
		if typed.Len() == 1 && typed.Keys()[0] == "$bytes" {
			if text, ok := typed.Value("$bytes").(string); ok {
				bytes, err := base64.StdEncoding.DecodeString(text)
				if err != nil {
					return nil, fmt.Errorf("a bytes value is not base64: %w", err)
				}
				return bytes, nil
			}
		}
		out := delta.NewJsonObject(typed.Len())
		for _, key := range typed.Keys() {
			member, _ := typed.Get(key)
			decoded, err := wireDecode(member)
			if err != nil {
				return nil, err
			}
			name := key
			if len(key) > 1 && key[0] == '$' && key[1] == '$' {
				name = key[1:]
			}
			out.Set(name, decoded)
		}
		return out, nil
	}
	return v, nil
}

// marshalWire is v as the control channel's JSON text.
func marshalWire(v any) (json.RawMessage, error) { return json.Marshal(wireEncode(v)) }

// unmarshalWire is the value of control JSON text raw.
func unmarshalWire(raw json.RawMessage) (any, error) {
	decoded, err := delta.DecodeJson(raw)
	if err != nil {
		return nil, err
	}
	return wireDecode(decoded)
}

// unwireOps is the ops of a frame, their values in the form of the channel decoded.
func unwireOps(ops delta.Ops) (delta.Ops, error) {
	out := make(delta.Ops, len(ops))
	for i, op := range ops {
		decoded, err := wireDecode([]any(op))
		if err != nil {
			return nil, err
		}
		out[i] = decoded.([]any)
	}
	return out, nil
}
