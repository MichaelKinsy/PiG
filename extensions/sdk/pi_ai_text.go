package sdk

// Ports pi-ai's pure text and transcript helpers (packages/ai/src/utils/text.ts and transcript.ts at the Pi pin) and StringEnum (utils/typebox-helpers.ts). A message, a context
// and a tool are the flat JSON objects the wire carries. The transcript helpers read and write them as raw JSON because their result depends on the order of an object's keys (a
// system message's `sections`, Pi's Map and Object.entries order) which a decoded Go map cannot give, as in [SerializeConversation]. A value that Pi's TypeScript types rule out
// but that Pi tolerates (a missing optional field) is read as Pi reads it; one Pi throws a TypeError on (content that is neither a string nor an array) is an error.

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

// StringEnumOptions is the options argument of StringEnum.
type StringEnumOptions struct {
	// Description is kept when it is not empty.
	Description string
	// Default is kept when it is not empty.
	Default string
}

// StringEnum is pi-ai's StringEnum: a JSON Schema string enum that Google's API and other providers that reject anyOf/const accept. The schema's keys are type, enum, then description and
// default when options carry them. A nil options or an empty description or default adds nothing, as in Pi (`options?.description && ...`).
func StringEnum(values []string, options *StringEnumOptions) *OrderedObject {
	enum := make([]any, len(values))
	for i, value := range values {
		enum[i] = value
	}
	schema := NewOrderedObject("type", "string", "enum", enum)
	if options != nil && options.Description != "" {
		schema.set("description", options.Description)
	}
	if options != nil && options.Default != "" {
		schema.set("default", options.Default)
	}
	return schema
}

// ContentText is pi-ai's contentText: a string is its own text, and an array of content blocks is the text of its text blocks joined with separator (default "\n"). A block is a
// map[string]any or an *OrderedObject. Any other content makes Pi throw a TypeError, which is an error here.
func ContentText(content any, separator ...string) (string, error) {
	sep := "\n"
	if len(separator) > 0 {
		sep = separator[0]
	}
	switch content := content.(type) {
	case string:
		return content, nil
	case []any:
		return joinTextBlocks(content, sep, func(block any) (map[string]any, bool) {
			switch block := block.(type) {
			case map[string]any:
				return block, true
			case *OrderedObject:
				if block == nil {
					return nil, false
				}
				return block.values, true
			}
			return nil, false
		})
	case []map[string]any:
		blocks := make([]any, len(content))
		for i, block := range content {
			blocks[i] = block
		}
		return ContentText(blocks, sep)
	}
	return "", errors.New("TypeError: content.filter is not a function")
}

func joinTextBlocks(blocks []any, sep string, read func(any) (map[string]any, bool)) (string, error) {
	var texts []string
	for _, element := range blocks {
		if element == nil {
			return "", errors.New("TypeError: Cannot read properties of null (reading 'type')")
		}
		block, ok := read(element)
		if !ok {
			continue
		}
		if kind, _ := block["type"].(string); kind == "text" {
			texts = append(texts, jsJoinElement(block["text"]))
		}
	}
	return strings.Join(texts, sep), nil
}

// jsJoinElement is how Array.prototype.join reads one element: null and undefined are the empty string, anything else its String().
func jsJoinElement(value any) string {
	switch value := value.(type) {
	case nil:
		return ""
	case string:
		return value
	case float64:
		return jsNumberString(value)
	case bool:
		if value {
			return "true"
		}
		return "false"
	}
	return ""
}

// jsUndefined is the value of a JavaScript property that is absent, to tell it from a null.
type jsUndefinedValue struct{}

var jsUndefined = jsUndefinedValue{}

func (object *jsObject) get(key string) any {
	if value, present := object.values[key]; present {
		return value
	}
	return jsUndefined
}

func newJSObject(pairs ...any) *jsObject {
	object := &jsObject{values: make(map[string]any, len(pairs)/2)}
	for i := 0; i+1 < len(pairs); i += 2 {
		key := pairs[i].(string)
		object.keys = append(object.keys, key)
		object.values[key] = pairs[i+1]
	}
	return object
}

func (object *jsObject) set(key string, value any) {
	if _, present := object.values[key]; !present {
		object.keys = append(object.keys, key)
	}
	object.values[key] = value
}

// jsIsNullish is `value == null`: null and undefined.
func jsIsNullish(value any) bool {
	_, undefined := value.(jsUndefinedValue)
	return value == nil || undefined
}

// orderedJSMap is a JavaScript Map keyed by string: setting an existing key keeps its position, and deleting a key then setting it again moves it to the end.
type orderedJSMap struct {
	keys   []string
	values map[string]any
}

func (m *orderedJSMap) set(key string, value any) {
	if m.values == nil {
		m.values = map[string]any{}
	}
	if _, present := m.values[key]; !present {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

func (m *orderedJSMap) delete(key string) {
	if _, present := m.values[key]; !present {
		return
	}
	delete(m.values, key)
	m.keys = slices.DeleteFunc(m.keys, func(existing string) bool { return existing == key })
}

func (m *orderedJSMap) len() int { return len(m.keys) }

// systemMessageOf is isSystemMessage: the message when its role is "system".
func systemMessageOf(value any) (*jsObject, bool) {
	message, ok := value.(*jsObject)
	if !ok {
		return nil, false
	}
	role, _ := message.values["role"].(string)
	return message, role == "system"
}

// nullishArray is `array ?? []`.
func nullishArray(value any) []any {
	array, _ := value.([]any)
	return array
}

func parseTranscriptMessages(messages []json.RawMessage) ([]any, error) {
	out := make([]any, len(messages))
	for i, raw := range messages {
		value, err := parseOrderedJSON(raw)
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}

func stringifyAll(values []any) []json.RawMessage {
	out := make([]json.RawMessage, len(values))
	for i, value := range values {
		out[i] = json.RawMessage(jsonStringify(value))
	}
	return out
}

func toolName(tool any) string {
	if object, ok := tool.(*jsObject); ok {
		name, _ := object.values["name"].(string)
		return name
	}
	return ""
}

func currentTools(messages []any) []any {
	var tools orderedJSMap
	for _, value := range messages {
		message, ok := systemMessageOf(value)
		if !ok {
			continue
		}
		for _, tool := range nullishArray(message.values["toolsRemoved"]) {
			tools.delete(toolName(tool))
		}
		for _, tool := range nullishArray(message.values["toolsAdded"]) {
			tools.set(toolName(tool), tool)
		}
	}
	out := make([]any, 0, tools.len())
	for _, key := range tools.keys {
		out = append(out, tools.values[key])
	}
	return out
}

// GetCurrentTools is pi-ai's getCurrentTools: the tools available after every system message's toolsRemoved and toolsAdded are applied in order, in the order they were first
// added (a tool removed and added again moves to the end).
func GetCurrentTools(messages []json.RawMessage) ([]json.RawMessage, error) {
	parsed, err := parseTranscriptMessages(messages)
	if err != nil {
		return nil, err
	}
	return stringifyAll(currentTools(parsed)), nil
}

func currentSystemMessage(messages []any) (*jsObject, error) {
	var content []string
	var sections orderedJSMap
	var timestamp any = jsUndefined
	for _, value := range messages {
		message, ok := systemMessageOf(value)
		if !ok {
			continue
		}
		if jsIsNullish(timestamp) {
			timestamp = message.get("timestamp")
		}
		text, err := contentTextOf(message.get("content"), "\n")
		if err != nil {
			return nil, err
		}
		if text != "" {
			content = append(content, text)
		}
		if declared, ok := message.values["sections"].(*jsObject); ok {
			for _, name := range declared.orderedKeys() {
				if section := declared.values[name]; section == nil {
					sections.delete(name)
				} else {
					sections.set(name, section)
				}
			}
		}
	}
	tools := currentTools(messages)
	if _, undefined := timestamp.(jsUndefinedValue); undefined && len(tools) == 0 {
		return nil, nil
	}
	result := newJSObject("role", "system", "content", strings.Join(content, "\n\n"))
	if sections.len() > 0 {
		merged := newJSObject()
		for _, name := range sections.keys {
			merged.set(name, sections.values[name])
		}
		result.set("sections", merged)
	}
	if len(tools) > 0 {
		result.set("toolsAdded", tools)
	}
	if jsIsNullish(timestamp) {
		timestamp = float64(0)
	}
	result.set("timestamp", timestamp)
	return result, nil
}

// GetCurrentSystemMessage is pi-ai's getCurrentSystemMessage: every system message replayed into one holding the current prompt and tools. Later content is appended to the base
// prompt with a blank line between, sections are patched by name (null removes one) and tools are resolved as [GetCurrentTools] does. It returns nil when the transcript has no
// system message and no tool.
func GetCurrentSystemMessage(messages []json.RawMessage) (json.RawMessage, error) {
	parsed, err := parseTranscriptMessages(messages)
	if err != nil {
		return nil, err
	}
	message, err := currentSystemMessage(parsed)
	if err != nil || message == nil {
		return nil, err
	}
	return json.RawMessage(jsonStringify(message)), nil
}

func systemMessageText(message *jsObject) (string, error) {
	text, err := contentTextOf(message.get("content"), "\n")
	if err != nil {
		return "", err
	}
	parts := []string{text}
	if sections, ok := message.values["sections"].(*jsObject); ok {
		for _, name := range sections.orderedKeys() {
			if section, ok := sections.values[name].(string); ok {
				parts = append(parts, section)
			} else if sections.values[name] != nil {
				parts = append(parts, jsJoinElement(sections.values[name]))
			}
		}
	}
	return strings.Join(slices.DeleteFunc(parts, func(part string) bool { return part == "" }), "\n\n"), nil
}

// GetCurrentSystemPrompt is pi-ai's getCurrentSystemPrompt: the text of [GetCurrentSystemMessage], or "" when there is none.
func GetCurrentSystemPrompt(messages []json.RawMessage) (string, error) {
	parsed, err := parseTranscriptMessages(messages)
	if err != nil {
		return "", err
	}
	message, err := currentSystemMessage(parsed)
	if err != nil || message == nil {
		return "", err
	}
	return systemMessageText(message)
}

func parseObject(raw json.RawMessage, what string) (*jsObject, error) {
	value, err := parseOrderedJSON(raw)
	if err != nil {
		return nil, err
	}
	object, ok := value.(*jsObject)
	if !ok {
		return nil, errors.New("TypeError: " + what + " is not an object")
	}
	return object, nil
}

// GetSystemMessageText is pi-ai's getSystemMessageText: a system message rendered as a complete prompt, its content followed by its non-null sections, separated by blank lines.
func GetSystemMessageText(message json.RawMessage) (string, error) {
	object, err := parseObject(message, "message")
	if err != nil {
		return "", err
	}
	return systemMessageText(object)
}

// RenderSystemMessageUpdate is pi-ai's renderSystemMessageUpdate: a later system message rendered for APIs that accept system messages mid-conversation. Section changes are framed
// by name. Pi marks the framing request-time only and subject to change between versions.
func RenderSystemMessageUpdate(message json.RawMessage) (string, error) {
	object, err := parseObject(message, "message")
	if err != nil {
		return "", err
	}
	var parts []string
	text, err := contentTextOf(object.get("content"), "\n")
	if err != nil {
		return "", err
	}
	if text != "" {
		parts = append(parts, text)
	}
	if sections, ok := object.values["sections"].(*jsObject); ok {
		for _, name := range sections.orderedKeys() {
			if sections.values[name] == nil {
				parts = append(parts, `Removed system prompt section "`+name+`".`)
			} else {
				parts = append(parts, `Updated system prompt section "`+name+`":`+"\n\n"+jsJoinElement(sections.values[name]))
			}
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func initialSystemMessage(systemPrompt any, tools any) *jsObject {
	prompt, hasPrompt := systemPrompt.(string)
	hasPrompt = hasPrompt && prompt != ""
	toolList := nullishArray(tools)
	hasTools := len(toolList) > 0
	if !hasPrompt && !hasTools {
		return nil
	}
	content := ""
	if systemPrompt != nil {
		content = prompt
	}
	message := newJSObject("role", "system", "content", content)
	if hasTools {
		message.set("toolsAdded", toolList)
	}
	message.set("timestamp", float64(0))
	return message
}

// CreateInitialSystemMessage is pi-ai's createInitialSystemMessage: the leading system message for a prompt and tool set, or nil when both are empty. A nil systemPrompt is
// undefined in Pi.
func CreateInitialSystemMessage(systemPrompt *string, tools []json.RawMessage) (json.RawMessage, error) {
	parsedTools, err := parseTranscriptMessages(tools)
	if err != nil {
		return nil, err
	}
	var prompt any
	if systemPrompt != nil {
		prompt = *systemPrompt
	}
	message := initialSystemMessage(prompt, parsedTools)
	if message == nil {
		return nil, nil
	}
	return json.RawMessage(jsonStringify(message)), nil
}

// NormalizeContext is pi-ai's normalizeContext: the context's systemPrompt and tools folded into a leading system message, as the {"messages": [...]} transcript every
// provider-facing function expects. A context with neither keeps its messages as they are.
func NormalizeContext(context json.RawMessage) (json.RawMessage, error) {
	object, err := parseObject(context, "context")
	if err != nil {
		return nil, err
	}
	messages := nullishArray(object.values["messages"])
	if initial := initialSystemMessage(object.get("systemPrompt"), object.values["tools"]); initial != nil {
		messages = append([]any{initial}, messages...)
	}
	if messages == nil {
		messages = []any{}
	}
	return json.RawMessage(jsonStringify(newJSObject("messages", messages))), nil
}

// GetInitialSystemMessage is pi-ai's getInitialSystemMessage: the leading message when it is a system message, else nil.
func GetInitialSystemMessage(messages []json.RawMessage) (json.RawMessage, error) {
	parsed, err := parseTranscriptMessages(messages)
	if err != nil {
		return nil, err
	}
	if len(parsed) > 0 {
		if message, ok := systemMessageOf(parsed[0]); ok {
			return json.RawMessage(jsonStringify(message)), nil
		}
	}
	return nil, nil
}

// WithoutInitialSystemMessage is pi-ai's withoutInitialSystemMessage: the messages without a leading system message, for APIs that carry the prompt outside the message list.
func WithoutInitialSystemMessage(messages []json.RawMessage) ([]json.RawMessage, error) {
	parsed, err := parseTranscriptMessages(messages)
	if err != nil {
		return nil, err
	}
	if len(parsed) > 0 {
		if _, ok := systemMessageOf(parsed[0]); ok {
			parsed = parsed[1:]
		}
	}
	return stringifyAll(parsed), nil
}

// CollapseSystemMessages is pi-ai's collapseSystemMessages: the transcript for APIs without mid-conversation system messages. The replayed system message leads and every later
// system message is dropped.
func CollapseSystemMessages(context json.RawMessage) (json.RawMessage, error) {
	object, err := parseObject(context, "context")
	if err != nil {
		return nil, err
	}
	messages := nullishArray(object.values["messages"])
	head, err := currentSystemMessage(messages)
	if err != nil {
		return nil, err
	}
	collapsed := []any{}
	if head != nil {
		collapsed = append(collapsed, head)
	}
	for _, value := range messages {
		if object, ok := value.(*jsObject); !ok || object.values["role"] != "system" {
			collapsed = append(collapsed, value)
		}
	}
	return json.RawMessage(jsonStringify(newJSObject("messages", collapsed))), nil
}

// ResolveTranscript is pi-ai's resolveTranscript: the context itself when the model accepts later system messages, else [CollapseSystemMessages] of it. A nil supportsMidConvoSystemMessages is
// undefined in Pi and means false.
func ResolveTranscript(context json.RawMessage, supportsMidConvoSystemMessages bool) (json.RawMessage, error) {
	if supportsMidConvoSystemMessages {
		value, err := parseOrderedJSON(context)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(jsonStringify(value)), nil
	}
	return CollapseSystemMessages(context)
}

// GetDeclaredTools is pi-ai's getDeclaredTools: every definition the transcript's toolsAdded names, one per name, in first-declaration order with the latest definition of each.
func GetDeclaredTools(messages []json.RawMessage) ([]json.RawMessage, error) {
	parsed, err := parseTranscriptMessages(messages)
	if err != nil {
		return nil, err
	}
	var declared orderedJSMap
	for _, value := range parsed {
		message, ok := systemMessageOf(value)
		if !ok {
			continue
		}
		for _, tool := range nullishArray(message.values["toolsAdded"]) {
			declared.set(toolName(tool), tool)
		}
	}
	out := make([]any, 0, declared.len())
	for _, key := range declared.keys {
		out = append(out, declared.values[key])
	}
	return stringifyAll(out), nil
}

// systemMessages is `messages.filter(isSystemMessage)`.
func systemMessages(messages []any) []any {
	var out []any
	for _, value := range messages {
		if _, ok := systemMessageOf(value); ok {
			out = append(out, value)
		}
	}
	return out
}

// findLastNonAdditiveToolChange is the index of the last system message with a removal or a same-name redeclaration, or -1. A name declared before counts as a redeclaration even
// after its removal: Kimi K3 ignores a removed tool that a later tool message declares again.
func findLastNonAdditiveToolChange(systemMessages []any) int {
	declared := map[string]bool{}
	last := -1
	for index, value := range systemMessages {
		message, _ := systemMessageOf(value)
		if len(nullishArray(message.values["toolsRemoved"])) > 0 {
			last = index
		}
		for _, tool := range nullishArray(message.values["toolsAdded"]) {
			name := toolName(tool)
			if declared[name] {
				last = index
			}
			declared[name] = true
		}
	}
	return last
}

// HasNonAdditiveToolChanges is pi-ai's hasNonAdditiveToolChanges: whether the tool history holds a removal or a same-name redeclaration that an addition-only transport cannot replay.
func HasNonAdditiveToolChanges(messages []json.RawMessage) (bool, error) {
	parsed, err := parseTranscriptMessages(messages)
	if err != nil {
		return false, err
	}
	return findLastNonAdditiveToolChange(systemMessages(parsed)) >= 0, nil
}

// TranscriptToolSupport is pi-ai's TranscriptToolSupport: how a transport can express tool changes after the first request.
//
//   - TranscriptToolSupportNone: only the top-level tool list exists; it always holds the current tools.
//   - TranscriptToolSupportAdditions: tools can be added at a later message, but not removed or redefined.
//   - TranscriptToolSupportChanges: every tool, including the initial ones, is declared in place, and a later declaration under the same name replaces the earlier one. Removals
//     are not expressible on the wire, so the transport announces them as text and the removed tools stay declared.
//
// Any other value is treated as TranscriptToolSupportAdditions, as Pi's `support` comparisons do.
type TranscriptToolSupport string

const (
	TranscriptToolSupportNone      TranscriptToolSupport = "none"
	TranscriptToolSupportAdditions TranscriptToolSupport = "additions"
	TranscriptToolSupportChanges   TranscriptToolSupport = "changes"
)

// TranscriptTools is pi-ai's TranscriptTools.
type TranscriptTools struct {
	// RequestTools are the tools sent in the top-level request field.
	RequestTools []json.RawMessage
	// FoldedSystemMessages is the number of leading system messages whose tool changes RequestTools already holds. Every later system message carries its own tool changes in place.
	FoldedSystemMessages int
}

// ResolveTranscriptTools is pi-ai's resolveTranscriptTools: the tool declarations split between the top-level request field and in-place changes.
//
// With [TranscriptToolSupportChanges], the top-level list stays empty and every system message, including the leading one, declares its tools in place: an in-place declaration can
// replace an earlier in-place one, but not a top-level one. With [TranscriptToolSupportAdditions], a removal or redefinition cannot be expressed in place, so the top-level list
// becomes the tool state after the last such change; later additions stay anchored, and the cached prefix is invalidated once per removal instead of on every later addition. With
// [TranscriptToolSupportNone], the top-level list is the current tool set.
func ResolveTranscriptTools(messages []json.RawMessage, support TranscriptToolSupport) (TranscriptTools, error) {
	parsed, err := parseTranscriptMessages(messages)
	if err != nil {
		return TranscriptTools{}, err
	}
	system := systemMessages(parsed)
	switch support {
	case TranscriptToolSupportNone:
		return TranscriptTools{RequestTools: stringifyAll(currentTools(system)), FoldedSystemMessages: len(system)}, nil
	case TranscriptToolSupportChanges:
		return TranscriptTools{RequestTools: []json.RawMessage{}}, nil
	}
	folded := findLastNonAdditiveToolChange(system) + 1
	if len(parsed) > 0 {
		if _, ok := systemMessageOf(parsed[0]); ok {
			folded = max(folded, 1)
		}
	}
	return TranscriptTools{RequestTools: stringifyAll(currentTools(system[:folded])), FoldedSystemMessages: folded}, nil
}
