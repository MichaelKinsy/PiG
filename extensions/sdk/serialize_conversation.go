package sdk

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// toolResultMaxChars is Pi's TOOL_RESULT_MAX_CHARS (core/compaction/utils.ts:94).
const toolResultMaxChars = 2000

// SerializeConversation is Pi's serializeConversation (core/compaction/utils.ts:120-155, exported from index.ts): the text a summarization request carries
// for a conversation, so the model does not treat it as one to continue. messages are LLM messages as JSON objects (the result of convertToLlm); the
// tool calls print their arguments in the order the JSON has them, which a decoded Go map cannot give, so this takes the raw JSON. A message with another
// role than user, assistant or toolResult adds nothing. Thinking blocks are omitted (Pi 1.2.0): visible text, tool calls and tool results record what
// happened. A message whose content Pi cannot read (it throws a TypeError there) is an error.
func SerializeConversation(messages []json.RawMessage) (string, error) {
	var parts []string
	for _, raw := range messages {
		value, err := parseOrderedJSON(raw)
		if err != nil {
			return "", err
		}
		message, isObject := value.(*jsObject)
		if value == nil {
			return "", errors.New("TypeError: Cannot read properties of null (reading 'role')")
		}
		role := ""
		if isObject {
			role, _ = message.values["role"].(string)
		}
		switch role {
		case "user":
			content, err := contentTextOf(message.values["content"], "")
			if err != nil {
				return "", err
			}
			if content != "" {
				parts = append(parts, "[User]: "+content)
			}
		case "assistant":
			assistant, err := serializeAssistant(message)
			if err != nil {
				return "", err
			}
			parts = append(parts, assistant...)
		case "toolResult":
			content, err := contentTextOf(message.values["content"], "")
			if err != nil {
				return "", err
			}
			if content != "" {
				parts = append(parts, "[Tool result]: "+truncateForSummary(content, toolResultMaxChars))
			}
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func serializeAssistant(message *jsObject) ([]string, error) {
	blocks, ok := message.values["content"].([]any)
	if !ok {
		return nil, errors.New("TypeError: msg.content is not iterable")
	}
	var toolCalls, parts []string
	hasText := false
	for _, element := range blocks {
		block, isObject := element.(*jsObject)
		if element == nil {
			return nil, errors.New("TypeError: Cannot read properties of null (reading 'type')")
		}
		if !isObject {
			continue
		}
		switch kind, _ := block.values["type"].(string); kind {
		case "toolCall":
			arguments, err := entriesString(block.values["arguments"])
			if err != nil {
				return nil, err
			}
			toolCalls = append(toolCalls, jsToString(block.values, "name")+"("+arguments+")")
		case "text":
			hasText = true
		}
	}
	if hasText {
		text, _ := contentTextOf(blocks, "\n")
		parts = append(parts, "[Assistant]: "+text)
	}
	if len(toolCalls) > 0 {
		parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
	}
	return parts, nil
}

// jsStringOrEmpty is what Array.prototype.join makes of block[key]: an absent or null member is the empty string and any other value its ToString.
func jsStringOrEmpty(object *jsObject, key string) string {
	value, present := object.values[key]
	if !present || value == nil {
		return ""
	}
	return jsValueString(value)
}

// entriesString is `Object.entries(args).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(", ")`. Object.entries throws for null and undefined, reads
// the indexes of a string or array as keys, and yields nothing for a number or boolean.
func entriesString(arguments any) (string, error) {
	var entries []string
	switch arguments := arguments.(type) {
	case nil:
		return "", errors.New("TypeError: Cannot convert undefined or null to object")
	case *jsObject:
		for _, key := range arguments.orderedKeys() {
			entries = append(entries, key+"="+jsonStringify(arguments.values[key]))
		}
	case []any:
		for i, element := range arguments {
			entries = append(entries, strconv.Itoa(i)+"="+jsonStringify(element))
		}
	case string:
		for i, unit := range jsUnits(arguments) {
			entries = append(entries, strconv.Itoa(i)+"="+unitJSON(unit))
		}
	}
	return strings.Join(entries, ", "), nil
}

// contentTextOf is pi-ai's contentText (utils/text.ts:6-12): a string is its own text and an array of blocks is the text of its text blocks joined with
// separator. Any other content makes Pi throw a TypeError.
func contentTextOf(content any, separator string) (string, error) {
	switch content := content.(type) {
	case string:
		return content, nil
	case []any:
		var texts []string
		for _, element := range content {
			if element == nil {
				return "", errors.New("TypeError: Cannot read properties of null (reading 'type')")
			}
			block, isObject := element.(*jsObject)
			if !isObject {
				continue
			}
			if kind, _ := block.values["type"].(string); kind == "text" {
				texts = append(texts, jsStringOrEmpty(block, "text"))
			}
		}
		return strings.Join(texts, separator), nil
	}
	return "", errors.New("TypeError: content.filter is not a function")
}

// truncateForSummary is Pi's truncateForSummary (utils.ts:98-102): text.length counts UTF-16 units, and the cut keeps the first maxChars of them.
func truncateForSummary(text string, maxChars int) string {
	units := jsUnits(text)
	if len(units) <= maxChars {
		return text
	}
	return jsUnitsString(units[:maxChars]) + "\n\n[... " + strconv.Itoa(len(units)-maxChars) + " more characters truncated]"
}

// unitJSON is JSON.stringify of a one-unit string: an unpaired surrogate is written as the lowercase \uXXXX escape QuoteJSONString gives it.
func unitJSON(unit uint16) string {
	if unit >= 0xD800 && unit <= 0xDFFF {
		return `"\u` + strconv.FormatUint(uint64(unit), 16) + `"`
	}
	return jsonStringify(string(utf16.Decode([]uint16{unit})))
}

// jsUnits is the UTF-16 code units of a string in the SDK's WTF-8 convention (extensions/sdk/json): the three-byte encoding of a surrogate is one
// unpaired unit, as the JavaScript string it came from had it.
func jsUnits(text string) []uint16 {
	units := make([]uint16, 0, len(text))
	for i := 0; i < len(text); {
		if text[i] == 0xED && i+2 < len(text) && text[i+1] >= 0xA0 && text[i+1] <= 0xBF && text[i+2]&0xC0 == 0x80 {
			units = append(units, uint16(text[i]&0x0F)<<12|uint16(text[i+1]&0x3F)<<6|uint16(text[i+2]&0x3F))
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		units = utf16.AppendRune(units, r)
		i += size
	}
	return units
}

// jsUnitsString is the inverse of jsUnits: a high and a low surrogate in order are one character, and an unpaired surrogate keeps its WTF-8 bytes, so a
// cut through a pair leaves the high surrogate where Pi's slice leaves it.
func jsUnitsString(units []uint16) string {
	var out strings.Builder
	for i := 0; i < len(units); i++ {
		unit := rune(units[i])
		if !utf16.IsSurrogate(unit) {
			out.WriteRune(unit)
			continue
		}
		if unit < 0xDC00 && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			out.WriteRune(utf16.DecodeRune(unit, rune(units[i+1])))
			i++
			continue
		}
		out.Write([]byte{0xE0 | byte(unit>>12), 0x80 | byte(unit>>6)&0x3F, 0x80 | byte(unit)&0x3F})
	}
	return out.String()
}
