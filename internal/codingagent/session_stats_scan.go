// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// scanToolCallCount counts the blocks of a validated JSON array whose "type" member is "toolCall" or "tool_use", as decoding the array into
// []struct{Type string `json:"type"`} would. ok is false for any shape the scan does not decide exactly (an element that is not an object or
// null, a key or type value with escapes, a key that matches "type" only case-insensitively, a type value that is not a string), and the caller
// decodes instead.
func scanToolCallCount(data []byte) (count int, ok bool) {
	i := skipJSONSpace(data, 1)
	if i < len(data) && data[i] == ']' {
		return 0, true
	}
	for i < len(data) {
		switch data[i] {
		case 'n':
			i += len("null")
		case '{':
			typ, next, ok := scanBlockType(data, i)
			if !ok {
				return 0, false
			}
			if typ == "toolCall" || typ == "tool_use" {
				count++
			}
			i = next
		default:
			return 0, false
		}
		i = skipJSONSpace(data, i)
		if i >= len(data) {
			return 0, false
		}
		if data[i] == ']' {
			return count, true
		}
		if data[i] != ',' {
			return 0, false
		}
		i = skipJSONSpace(data, i+1)
	}
	return 0, false
}

// scanBlockType returns the last "type" string member of the object at data[i] and the index after it.
func scanBlockType(data []byte, i int) (typ string, next int, ok bool) {
	i = skipJSONSpace(data, i+1)
	if i < len(data) && data[i] == '}' {
		return "", i + 1, true
	}
	for i < len(data) && data[i] == '"' {
		key, end, plain := scanPlainJSONString(data, i)
		if !plain {
			return "", 0, false
		}
		i = skipJSONSpace(data, end)
		if i >= len(data) || data[i] != ':' {
			return "", 0, false
		}
		i = skipJSONSpace(data, i+1)
		switch {
		case string(key) == "type":
			if i >= len(data) || data[i] != '"' {
				return "", 0, false
			}
			value, end, plain := scanPlainJSONString(data, i)
			if !plain {
				return "", 0, false
			}
			typ, i = string(value), end
		case bytes.EqualFold(key, []byte("type")):
			return "", 0, false
		default:
			if i = skipJSONValue(data, i); i < 0 {
				return "", 0, false
			}
		}
		i = skipJSONSpace(data, i)
		if i >= len(data) {
			return "", 0, false
		}
		if data[i] == '}' {
			return typ, i + 1, true
		}
		if data[i] != ',' {
			return "", 0, false
		}
		i = skipJSONSpace(data, i+1)
	}
	return "", 0, false
}

// scanPlainJSONString returns the contents of the string at data[i] and the index after it; plain is false when the string has an escape.
func scanPlainJSONString(data []byte, i int) (value []byte, end int, plain bool) {
	for j := i + 1; j < len(data); j++ {
		switch data[j] {
		case '"':
			return data[i+1 : j], j + 1, true
		case '\\':
			return nil, 0, false
		}
	}
	return nil, 0, false
}

// skipJSONValue returns the index after the validated value at data[i], or -1.
func skipJSONValue(data []byte, i int) int {
	depth := 0
	for ; i < len(data); i++ {
		switch c := data[i]; c {
		case '"':
			if i = skipJSONStringBody(data, i+1); i < 0 {
				return -1
			}
			if depth == 0 {
				return i + 1
			}
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return i
			}
			depth--
			if depth == 0 {
				return i + 1
			}
		case ',':
			if depth == 0 {
				return i
			}
		case ' ', '\t', '\r', '\n':
			if depth == 0 {
				return i
			}
		}
	}
	if depth == 0 {
		return i
	}
	return -1
}

// skipJSONStringBody returns the index of the quote that closes the string whose contents start at data[i], or -1.
func skipJSONStringBody(data []byte, i int) int {
	quote := -1
	for i < len(data) {
		if quote < i {
			if quote = bytes.IndexByte(data[i:], '"'); quote < 0 {
				return -1
			}
			quote += i
		}
		escape := bytes.IndexByte(data[i:quote], '\\')
		if escape < 0 {
			return quote
		}
		i += escape + 2
	}
	return -1
}

func skipJSONSpace(data []byte, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\r' || data[i] == '\n') {
		i++
	}
	return i
}

// scanObjectMembers visits the members of the object at data[i] whose plain key is one of names, in order, and returns the index after the
// object. ok is false when a key has an escape or matches one of names only case-insensitively, as only the decoder answers those.
func scanObjectMembers(data []byte, i int, names []string, visit func(name string, value []byte)) (next int, ok bool) {
	return walkObjectMembers(data, i, names, func(name string, at int) (int, bool) {
		end := skipJSONValue(data, at)
		if end < 0 {
			return 0, false
		}
		visit(name, data[at:end])
		return end, true
	})
}

// walkObjectMembers is scanObjectMembers whose visitor reads the member's value starting at data[at] itself and returns the index after it,
// so a value the visitor descends into is scanned once.
func walkObjectMembers(data []byte, i int, names []string, visit func(name string, at int) (end int, ok bool)) (next int, ok bool) {
	i = skipJSONSpace(data, i+1)
	if i < len(data) && data[i] == '}' {
		return i + 1, true
	}
	for i < len(data) && data[i] == '"' {
		key, end, plain := scanPlainJSONString(data, i)
		if !plain {
			return 0, false
		}
		i = skipJSONSpace(data, end)
		if i >= len(data) || data[i] != ':' {
			return 0, false
		}
		i = skipJSONSpace(data, i+1)
		matched := false
		for _, name := range names {
			if string(key) == name {
				if i, ok = visit(name, i); !ok {
					return 0, false
				}
				matched = true
				break
			} else if bytes.EqualFold(key, []byte(name)) {
				return 0, false
			}
		}
		if !matched {
			if i = skipJSONValue(data, i); i < 0 {
				return 0, false
			}
		}
		i = skipJSONSpace(data, i)
		if i >= len(data) {
			return 0, false
		}
		if data[i] == '}' {
			return i + 1, true
		}
		if data[i] != ',' {
			return 0, false
		}
		i = skipJSONSpace(data, i+1)
	}
	return 0, false
}

// textPresence is whether a JSON string holds a character String.prototype.trim keeps.
type textPresence int

const (
	textUnknown textPresence = iota
	textBlank
	textVisible
)

// jsonStringPresence decides textPresence for the JSON string value raw from its escaped form: a printable ASCII character or an escape of
// one is visible, a space or a \t \n \r \f escape is blank, and anything else (non-ASCII, \u escapes) is left to the decoder.
func jsonStringPresence(raw []byte) textPresence {
	presence := textBlank
	for i := 1; i < len(raw)-1; i++ {
		switch c := raw[i]; {
		case c == '\\':
			i++
			switch raw[i] {
			case 't', 'n', 'r', 'f':
			case 'u':
				presence = textUnknown
				i += 4
			default:
				return textVisible
			}
		case c == ' ':
		case c > ' ' && c < 0x7f:
			return textVisible
		default:
			presence = textUnknown
		}
	}
	return presence
}

// scanTreeFacts reads the /tree facts of a message line as treeFactsOfLine's decode does; ok is false for any shape it does not decide
// exactly.
func scanTreeFacts(line []byte) (facts *treeFacts, ok bool) {
	i := skipJSONSpace(line, 0)
	if i >= len(line) || line[i] != '{' {
		return nil, false
	}
	var message []byte
	if _, ok := scanObjectMembers(line, i, []string{"message"}, func(_ string, v []byte) { message = v }); !ok || len(message) == 0 || message[0] != '{' {
		return nil, false
	}
	var role, stop, content []byte
	if _, ok := scanObjectMembers(message, 0, []string{"role", "stopReason", "content"}, func(name string, v []byte) {
		switch name {
		case "role":
			role = v
		case "stopReason":
			stop = v
		case "content":
			content = v
		}
	}); !ok {
		return nil, false
	}
	plainString := func(v []byte) (string, bool) {
		if v == nil || string(v) == "null" {
			return "", true
		}
		if v[0] != '"' {
			return "", false
		}
		s, end, plain := scanPlainJSONString(v, 0)
		return string(s), plain && end == len(v) && utf8.Valid(s)
	}
	roleText, okRole := plainString(role)
	stopText, okStop := plainString(stop)
	if !okRole || !okStop {
		return nil, false
	}
	facts = &treeFacts{}
	if roleText != "assistant" {
		return facts, true
	}
	presence := textBlank
	if content != nil && string(content) != "null" {
		if content[0] != '[' {
			return nil, false
		}
		j := skipJSONSpace(content, 1)
		for j < len(content) && content[j] != ']' {
			switch content[j] {
			case 'n':
				j += len("null")
			case '{':
				var typ, id, text, inner []byte
				next, ok := scanObjectMembers(content, j, []string{"type", "id", "text", "content"}, func(name string, v []byte) {
					switch name {
					case "type":
						typ = v
					case "id":
						id = v
					case "text":
						text = v
					case "content":
						inner = v
					}
				})
				if !ok {
					return nil, false
				}
				typText, okType := plainString(typ)
				idText, okID := plainString(id)
				okText := text == nil || string(text) == "null" || text[0] == '"'
				if !okType || !okID || !okText {
					return nil, false
				}
				var blockText []byte
				switch typText {
				case "text":
					blockText = text
				case "tool_result":
					if inner != nil && inner[0] == '"' {
						blockText = inner
					}
				case "toolCall", "tool_use":
					if idText != "" {
						facts.callIDs = append(facts.callIDs, idText)
					}
				}
				if len(blockText) > 0 && string(blockText) != "null" {
					switch jsonStringPresence(blockText) {
					case textUnknown:
						return nil, false
					case textVisible:
						presence = textVisible
					}
				}
				j = next
			default:
				return nil, false
			}
			j = skipJSONSpace(content, j)
			if j < len(content) && content[j] == ',' {
				j = skipJSONSpace(content, j+1)
			}
		}
	}
	if stopText == "" || stopText == "stop" || stopText == "toolUse" {
		facts.suppressible = presence == textBlank
	}
	return facts, true
}

// jsonStringPrefix decodes the start of the JSON string raw, at least units UTF-16 units of it when it is that long: a prefix that ends after
// units+1 characters or escapes, closed and decoded, holds every unit of the first units and does not split a surrogate pair inside them.
func jsonStringPrefix(raw []byte, units int) (string, bool) {
	end, tokens := 1, 0
	for end < len(raw)-1 && tokens <= units {
		switch {
		case raw[end] == '\\' && end+1 < len(raw) && raw[end+1] == 'u':
			end += 6
		case raw[end] == '\\':
			end += 2
		default:
			_, width := utf8.DecodeRune(raw[end:])
			end += width
		}
		tokens++
	}
	if end > len(raw)-1 {
		return "", false
	}
	body := raw[1:end]
	if bytes.IndexByte(body, '\\') < 0 && utf8.Valid(body) {
		return string(body), true
	}
	quoted := raw[:end+1]
	if end < len(raw)-1 {
		quoted = append(append([]byte{}, raw[:end]...), '"')
	}
	var text string
	if json.Unmarshal(quoted, &text) != nil {
		return "", false
	}
	return text, true
}

// scanSearchBody reads the search body of a message line as treeSearchBody's decode does (role, the first 200 UTF-16 units of the text,
// a bash command); ok is false for any shape it does not decide exactly. It reads each byte of the line about once.
func scanSearchBody(line []byte) (body searchBody, ok bool) {
	i := skipJSONSpace(line, 0)
	if i >= len(line) || line[i] != '{' {
		return searchBody{}, false
	}
	str := func(v []byte) (string, bool) {
		if v == nil || string(v) == "null" {
			return "", true
		}
		if v[0] != '"' {
			return "", false
		}
		if text, end, plain := scanPlainJSONString(v, 0); plain && end == len(v) && utf8.Valid(text) {
			return string(text), true
		}
		var s string
		return s, json.Unmarshal(v, &s) == nil
	}
	var role, command []byte
	var content searchContent
	found := false
	if _, ok := walkObjectMembers(line, i, []string{"message"}, func(_ string, at int) (int, bool) {
		if line[at] != '{' {
			return 0, false
		}
		// A later member of the same name replaces an earlier one, as JSON.parse keeps the last.
		role, command, content, found = nil, nil, searchContent{}, true
		return walkObjectMembers(line, at, []string{"role", "command", "content"}, func(name string, at int) (int, bool) {
			if name == "content" {
				var end int
				var ok bool
				content, end, ok = scanSearchContent(line, at, str)
				return end, ok
			}
			end := skipJSONValue(line, at)
			if end < 0 {
				return 0, false
			}
			if name == "role" {
				role = line[at:end]
			} else {
				command = line[at:end]
			}
			return end, true
		})
	}); !ok || !found {
		return searchBody{}, false
	}
	roleText, okRole := str(role)
	commandText, okCommand := str(command)
	if !okRole || !okCommand {
		return searchBody{}, false
	}
	parts := []string{roleText}
	if content.part {
		parts = append(parts, content.text)
	}
	if roleText == agent.RoleBashExecution && commandText != "" {
		parts = append(parts, commandText)
	}
	return searchBody{text: strings.Join(parts, " "), parts: true}, true
}

// searchContent is treeSearchContent's result: the first 200 UTF-16 units of the text, and whether it is a part.
type searchContent struct {
	text string
	part bool
}

// scanSearchContent reads the content value at data[at] as treeSearchContent decodes it and returns the index after the value.
func scanSearchContent(data []byte, at int, str func([]byte) (string, bool)) (content searchContent, end int, ok bool) {
	switch data[at] {
	case 'n':
		return searchContent{}, at + len("null"), true
	case 'f':
		return searchContent{}, at + len("false"), true
	case '"':
		end := skipJSONValue(data, at)
		if end < 0 {
			return searchContent{}, 0, false
		}
		text, ok := jsonStringPrefix(data[at:end], 200)
		if !ok {
			return searchContent{}, 0, false
		}
		return searchContent{text: jsPrefix(text, 200), part: text != ""}, end, true
	case '[':
	default:
		return searchContent{}, 0, false
	}
	var out strings.Builder
	need := 200
	j := skipJSONSpace(data, at+1)
	for j < len(data) && data[j] != ']' {
		if data[j] != '{' {
			// Pi reads only object blocks.
			if j = skipJSONValue(data, j); j < 0 {
				return searchContent{}, 0, false
			}
		} else {
			var typ []byte
			var text string
			hasText := false
			next, ok := walkObjectMembers(data, j, []string{"type", "text"}, func(name string, at int) (int, bool) {
				end := skipJSONValue(data, at)
				if end < 0 {
					return 0, false
				}
				value := data[at:end]
				if name == "type" {
					typ = value
					return end, true
				}
				if value[0] != '"' {
					return 0, false
				}
				prefix, ok := jsonStringPrefix(value, 200)
				if !ok {
					return 0, false
				}
				text, hasText = prefix, true
				return end, true
			})
			if !ok {
				return searchContent{}, 0, false
			}
			if typText, isString := str(typ); isString && typText == "text" && need > 0 {
				if !hasText {
					text = "undefined"
				}
				out.WriteString(text)
				need -= jsstring.Length(text)
			}
			j = next
		}
		j = skipJSONSpace(data, j)
		if j < len(data) && data[j] == ',' {
			j = skipJSONSpace(data, j+1)
		}
	}
	if j >= len(data) {
		return searchContent{}, 0, false
	}
	return searchContent{text: jsPrefix(out.String(), 200), part: true}, j + 1, true
}

// indexProbe is what the load reads of every line: the base members and the accounting.
type indexProbe struct {
	sessionAccountingEntry
	ID       string  `json:"id"`
	ParentID *string `json:"parentId"`
}

// walkIndexProbe reads the indexProbe of a valid JSON line in one walk, decoding only the small members it names; ok is false, and the
// caller decodes the line, for any line on which that could differ from json.Unmarshal into indexProbe: a value of another JSON type, a
// member named twice or in another case, or a message that is not an object.
func walkIndexProbe(line []byte) (probe indexProbe, ok bool) {
	i := skipJSONSpace(line, 0)
	if i >= len(line) || line[i] != '{' {
		return indexProbe{}, false
	}
	var seen uint32
	once := func(bit uint32) bool {
		if seen&bit != 0 {
			return false
		}
		seen |= bit
		return true
	}
	str := func(v []byte, out *string) bool {
		if string(v) == "null" {
			return true
		}
		if text, end, plain := scanPlainJSONString(v, 0); plain && end == len(v) && utf8.Valid(text) {
			*out = string(text)
			return true
		}
		return v[0] == '"' && json.Unmarshal(v, out) == nil
	}
	decode := func(v []byte, out any) bool { return json.Unmarshal(v, out) == nil }
	message := &probe.Message
	messageMember := func(name string, at int) (int, bool) {
		end := skipJSONValue(line, at)
		if end < 0 {
			return 0, false
		}
		v := line[at:end]
		switch name {
		case "role":
			return end, once(1<<10) && str(v, &message.Role)
		case "content":
			return end, once(1<<11) && message.Content.UnmarshalJSON(v) == nil
		case "usage":
			return end, once(1<<12) && decode(v, &message.Usage)
		case "provider":
			return end, once(1<<13) && str(v, &message.Provider)
		case "model":
			return end, once(1<<14) && str(v, &message.ModelID)
		case "api":
			message.API = v
			return end, once(1 << 15)
		case "responseModel":
			return end, once(1<<16) && decode(v, &message.ResponseModel)
		case "timestamp":
			return end, once(1<<17) && decode(v, &message.Timestamp)
		}
		return end, true
	}
	_, ok = walkObjectMembers(line, i, []string{"type", "id", "parentId", "timestamp", "provider", "model", "usage", "kind", "message"}, func(name string, at int) (int, bool) {
		if name == "message" {
			if !once(1 << 8) {
				return 0, false
			}
			if line[at] == 'n' {
				return at + len("null"), true
			}
			if line[at] != '{' {
				return 0, false
			}
			return walkObjectMembers(line, at, []string{"role", "content", "usage", "provider", "model", "api", "responseModel", "timestamp"}, messageMember)
		}
		end := skipJSONValue(line, at)
		if end < 0 {
			return 0, false
		}
		v := line[at:end]
		switch name {
		case "type":
			return end, once(1<<0) && str(v, &probe.Type)
		case "id":
			return end, once(1<<1) && str(v, &probe.ID)
		case "parentId":
			return end, once(1<<2) && decode(v, &probe.ParentID)
		case "timestamp":
			probe.Timestamp = v
			return end, once(1 << 3)
		case "provider":
			return end, once(1<<4) && str(v, &probe.Provider)
		case "model":
			return end, once(1<<5) && str(v, &probe.Model)
		case "usage":
			return end, once(1<<6) && decode(v, &probe.Usage)
		case "kind":
			return end, once(1<<7) && str(v, &probe.Kind)
		}
		return end, true
	})
	if !ok {
		return indexProbe{}, false
	}
	return probe, true
}

// jsPrefix is jsstring.Slice(text, 0, units). Text that is well-formed UTF-8 up to the cut, with the cut between two characters, is cut in
// place; any other text takes jsstring.Slice, which converts it to UTF-16.
func jsPrefix(text string, units int) string {
	n := 0
	for i := 0; i < len(text); {
		if n >= units {
			return text[:i]
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			return jsstring.Slice(text, 0, units)
		case r > 0xFFFF:
			if n+2 > units {
				return jsstring.Slice(text, 0, units)
			}
			n += 2
		default:
			n++
		}
		i += size
	}
	return text
}
