package cli

import (
	"bytes"
	"fmt"
	"reflect"
	"strconv"

	"github.com/MichaelKinsy/PiG/ai"
	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// rpcObject carries source insertion order and UTF-16 string content for JSON/RPC objects. Nested wire values remain raw JSON when read so removing a field does not reorder its siblings or descendants.
type rpcObject []rpcField

type rpcField struct {
	name  string
	value any
}

func (object rpcObject) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, field := range object {
		if i > 0 {
			out.WriteByte(',')
		}
		name, err := json.Marshal(field.name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, err
		}
		out.Write(name)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

var rpcObjectType = reflect.TypeFor[rpcObject]()

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (json.StringifyAppender), writing each member into dst itself instead of encoding it apart and compacting the result.
//
//pig:hotpath
func (object rpcObject) AppendStringify(dst []byte) ([]byte, error) {
	start := len(dst)
	dst = append(dst, '{')
	for i, field := range object {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = json.AppendStringifyString(dst, field.name)
		dst = append(dst, ':')
		var err error
		if dst, err = appendRPCValue(dst, field.value); err != nil {
			return dst[:start], err
		}
	}
	return append(dst, '}'), nil
}

// appendRPCValue appends json.Marshal(value) in its JSON.stringify form: the member types an event holds most are written directly, anything else by the encoder. A nested object's error is wrapped as the encoder wraps what a nested MarshalJSON returns, so the text of an error does not depend on which route wrote it.
func appendRPCValue(dst []byte, value any) ([]byte, error) {
	switch value := value.(type) {
	case nil:
		return append(dst, "null"...), nil
	case string:
		return json.AppendStringifyString(dst, value), nil
	case ai.AssistantEventType:
		return json.AppendStringifyString(dst, string(value)), nil
	case ai.StopReason:
		return json.AppendStringifyString(dst, string(value)), nil
	case ai.API:
		return json.AppendStringifyString(dst, string(value)), nil
	case ai.ModelThinkingLevel:
		return json.AppendStringifyString(dst, string(value)), nil
	case int:
		return strconv.AppendInt(dst, int64(value), 10), nil
	case int64:
		return strconv.AppendInt(dst, value, 10), nil
	case bool:
		return strconv.AppendBool(dst, value), nil
	case rpcObject:
		out, err := value.AppendStringify(dst)
		if err != nil {
			return dst, &json.MarshalerError{Type: rpcObjectType, Err: err}
		}
		return out, nil
	case []any:
		if value == nil {
			return append(dst, "null"...), nil
		}
		start := len(dst)
		dst = append(dst, '[')
		for i, element := range value {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			if dst, err = appendRPCValue(dst, element); err != nil {
				return dst[:start], err
			}
		}
		return append(dst, ']'), nil
	default:
		return json.AppendStringify(dst, value)
	}
}

func (object *rpcObject) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("RPC event is not an object")
	}
	var fields rpcObject
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := name.(string)
		if !ok {
			return fmt.Errorf("RPC event key is not a string")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		fields = append(fields, rpcField{key, value})
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	*object = fields
	return nil
}
