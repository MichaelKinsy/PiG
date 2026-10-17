package ai

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsstring"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ParseJSONWithRepair parses json into T and, when strict parsing fails, parses the repairJSON rewrite instead. It returns the strict error when the repair changes nothing (upstream utils/json-parse.ts parseJsonWithRepair).
func ParseJSONWithRepair[T any](data string) (T, error) {
	var value T
	err := sdkjson.Unmarshal([]byte(data), &value)
	if err == nil {
		return value, nil
	}
	repaired := repairJSON(data)
	if repaired == data {
		return value, err
	}
	var repairedValue T
	if err := sdkjson.Unmarshal([]byte(repaired), &repairedValue); err != nil {
		return repairedValue, err
	}
	return repairedValue, nil
}

// ParseToolCallArguments is json-parse.ts parseToolCallArguments: the complete arguments text of a finished tool call
// becomes Pick<ToolCall, "arguments" | "argumentsError">, returned as a ToolCall holding only those two members. Text that
// is not a JSON object leaves Arguments empty and ArgumentsError describing the problem, never the parsed prefix.
func ParseToolCallArguments(text string) ToolCall {
	var call ToolCall
	call.SetFinalArguments(text)
	return call
}

// parseToolCallArguments mirrors Pi's parseToolCallArguments: the complete arguments text of a finished call parses strictly (with the string repair). Text that is not a JSON object yields empty arguments and a message with V8's JSON.parse error and the text received.
func parseToolCallArguments(text string) (JsonObject, schemaObjectOrder, string) {
	if jsstring.Trim(text) == "" {
		return JsonObject{}, nil, ""
	}
	// The common case, a finished call whose text is a valid JSON object, is read once: JSON.parse succeeds on the text
	// itself, so neither the repair nor a second parse can change the result.
	if object, order, ok := parseValidJSONObject(text); ok {
		return object, order, ""
	}
	candidate := text
	var parseErr error
	if _, ok := jsonParseValue(candidate); !ok {
		parseErr = jsonparse.Validate([]byte(candidate))
		if repaired := repairJSON(text); repaired != text {
			candidate = repaired
			if _, ok := jsonParseValue(candidate); !ok {
				parseErr = jsonparse.Validate([]byte(candidate))
			} else {
				parseErr = nil
			}
		}
		if parseErr == nil {
			if _, ok := jsonParseValue(candidate); !ok {
				parseErr = errors.New("Unexpected token in JSON")
			}
		}
	}
	if parseErr == nil {
		if object, order, nonObject := parseStreamingJsonValue(candidate); object != nil && nonObject == nil {
			return object, order, ""
		}
		parseErr = errors.New("Tool arguments must be a JSON object")
	}
	return JsonObject{}, nil, parseErr.Error() + "\n\nReceived arguments:\n" + text
}

// repairJSON mirrors Pi's repairJson for provider payloads that contain raw
// control characters or backslashes before non-JSON escape characters. A \u
// not followed by four hex digits is an invalid escape (Pi 4b994d5e, #10868).
//
// Ports packages/ai/src/utils/json-parse.ts
func repairJSON(data string) string {
	var repaired strings.Builder
	repaired.Grow(len(data))
	inString := false

	for index := 0; index < len(data); index++ {
		char := data[index]
		if !inString {
			repaired.WriteByte(char)
			if char == '"' {
				inString = true
			}
			continue
		}

		switch char {
		case '"':
			repaired.WriteByte(char)
			inString = false
		case '\\':
			if index+1 >= len(data) {
				repaired.WriteString(`\\`)
				continue
			}
			next := data[index+1]
			if next == 'u' && index+5 < len(data) && allHex(data[index+2:index+6]) {
				repaired.WriteString(data[index : index+6])
				index += 5
				continue
			}
			if strings.ContainsRune(`"\\/bfnrt`, rune(next)) {
				repaired.WriteByte(char)
				repaired.WriteByte(next)
				index++
				continue
			}
			repaired.WriteString(`\\`)
		default:
			switch char {
			case '\b':
				repaired.WriteString(`\b`)
			case '\f':
				repaired.WriteString(`\f`)
			case '\n':
				repaired.WriteString(`\n`)
			case '\r':
				repaired.WriteString(`\r`)
			case '\t':
				repaired.WriteString(`\t`)
			default:
				if char < 0x20 {
					_, _ = fmt.Fprintf(&repaired, `\u%04x`, char)
				} else {
					repaired.WriteByte(char)
				}
			}
		}
	}
	return repaired.String()
}

func allHex(value string) bool {
	for index := range len(value) {
		char := value[index]
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}
