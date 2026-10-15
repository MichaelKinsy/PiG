package agent

import (
	"reflect"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// decodeLax decodes a message object that decode rejects because a member has another JSON type than its field holds. JSON.parse keeps a member of any type, so Pi keeps such a message in the session, its context and get_messages (session-manager.ts parseSessionEntryLine, buildSessionContext). The fields read such a member as absent: an object or array member keeps the parts its field holds, and a content block or record element the field cannot hold is left out. A role that is not a non-empty string or one of the typed roles keeps the object as a custom message. A message that is a JSON number, string, boolean or array is a custom message without members; Pi keeps it too, while a null message fails Pi's context build (session-manager.ts getSessionContextSettings reads message.role), so decodeLax rejects null. The message keeps its text, so it is written as Pi writes it. decodeLax reports whether content is null or missing in data.
func (m *AgentMessage) decodeLax(data []byte) (bool, error) {
	if len(data) > 0 && data[0] != '{' && data[0] != 'n' {
		*m = AgentMessage{Custom: map[string]any{}}
		return false, nil
	}
	object, err := orderedjson.Parse(data)
	if err != nil {
		return false, err
	}
	content, _ := object.Get("content")
	nullContent := len(content) == 0 || string(content) == "null"
	var role string
	if raw, ok := object.Get("role"); ok {
		_ = json.Unmarshal(raw, &role)
	}
	switch role {
	case "system":
		laxMembers(object, reflect.TypeFor[systemMessageWire]())
		laxMembers(object, reflect.TypeFor[agentMessageWire]())
		laxContent(object, nil)
	case RoleUser:
		laxMembers(object, reflect.TypeFor[agentMessageWire]())
		laxContent(object, func(raw []byte) error { _, err := unmarshalUserContent(raw); return err })
	case RoleAssistant:
		laxMembers(object, reflect.TypeFor[agentMessageWire]())
		laxContent(object, func(raw []byte) error { _, err := unmarshalAssistantContent(raw); return err })
	case RoleToolResult:
		laxMembers(object, reflect.TypeFor[agentMessageWire]())
		laxContent(object, func(raw []byte) error { _, err := unmarshalToolResultContent(raw); return err })
	default:
		var custom map[string]any
		if err := json.Unmarshal(data, &custom); err != nil {
			return false, err
		}
		if details, ok := object.Get("details"); ok {
			custom["details"] = orderedjson.Value(details)
		}
		*m = AgentMessage{Custom: custom}
		return nullContent, nil
	}
	sanitized, err := object.MarshalJSON()
	if err != nil {
		return false, err
	}
	if _, err := m.decode(sanitized); err != nil {
		return false, err
	}
	return nullContent, nil
}

// laxContent keeps the content blocks decodeContent accepts one by one, each with the members its block type holds. Content that is neither a string nor an array reads as absent. A nil decodeContent decodes a system message's content.
func laxContent(object *orderedjson.Object, decodeContent func([]byte) error) {
	raw, ok := object.Get("content")
	if !ok || len(raw) == 0 {
		return
	}
	if decodeContent == nil {
		decodeContent = func(raw []byte) error { _, err := decodeSystemContent(raw); return err }
	}
	if decodeContent(raw) == nil {
		return
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		object.Delete("content")
		return
	}
	kept := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		if decodeContent(append(append([]byte("["), item...), ']')) == nil {
			kept = append(kept, item)
			continue
		}
		block, err := orderedjson.Parse(item)
		if err != nil {
			continue
		}
		var blockType string
		if raw, ok := block.Get("type"); ok && json.Unmarshal(raw, &blockType) != nil {
			continue // a type that is not a string names no content block shape
		}
		shape, ok := contentBlockWire[blockType]
		if !ok {
			continue
		}
		laxMembers(block, shape)
		sanitized, err := block.MarshalJSON()
		if err != nil || decodeContent(append(append([]byte("["), sanitized...), ']')) != nil {
			continue
		}
		kept = append(kept, sanitized)
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		encoded = []byte("[]")
	}
	object.Set("content", encoded)
}

// contentBlockWire is the struct each content block type decodes into.
var contentBlockWire = map[string]reflect.Type{
	"text":     reflect.TypeFor[ai.TextContent](),
	"thinking": reflect.TypeFor[ai.ThinkingContent](),
	"toolCall": reflect.TypeFor[ai.ToolCall](),
	"image": reflect.TypeFor[struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MIMEType string `json:"mimeType"`
	}](),
}

// laxMembers removes from object each member that the field of structure named by the member cannot hold. An object or array member that its field cannot hold whole keeps the members and elements the field can hold.
func laxMembers(object *orderedjson.Object, structure reflect.Type) {
	for _, name := range object.Keys() {
		field, ok := jsonField(structure, name)
		if !ok {
			continue
		}
		raw, _ := object.Get(name)
		if value, ok := laxValue(raw, field); ok {
			object.Set(name, value)
		} else {
			object.Delete(name)
		}
	}
}

// laxValue returns raw, or the part of raw a value of type t holds, and reports false when t holds none of it.
func laxValue(raw []byte, t reflect.Type) ([]byte, bool) {
	if holds(raw, t) {
		return raw, true
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var sanitized []byte
	switch t.Kind() {
	case reflect.Struct:
		object, err := orderedjson.Parse(raw)
		if err != nil {
			return nil, false
		}
		laxMembers(object, t)
		if sanitized, err = object.MarshalJSON(); err != nil {
			return nil, false
		}
	case reflect.Slice:
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil, false
		}
		kept := make([]json.RawMessage, 0, len(items))
		for _, item := range items {
			if value, ok := laxValue(item, t.Elem()); ok {
				kept = append(kept, value)
			}
		}
		var err error
		if sanitized, err = json.Marshal(kept); err != nil {
			return nil, false
		}
	default:
		return nil, false
	}
	return sanitized, holds(sanitized, t)
}

// holds reports whether raw decodes into a value of type t.
func holds(raw []byte, t reflect.Type) bool {
	return json.Unmarshal(raw, reflect.New(t).Interface()) == nil
}

// jsonField returns the type of the field of structure that decodes the member name.
func jsonField(structure reflect.Type, name string) (reflect.Type, bool) {
	for structure.Kind() == reflect.Pointer {
		structure = structure.Elem()
	}
	if structure.Kind() != reflect.Struct {
		return nil, false
	}
	for field := range structure.Fields() {
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == name && field.IsExported() {
			return field.Type, true
		}
	}
	return nil, false
}
