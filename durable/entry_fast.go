package durable

import (
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// decodeEntryFast decodes the stored form of an entry record in one pass. It accepts the shapes pi-durable and this
// package write: exact-case keys that appear once, users', assistants' and tool results' messages with text content,
// plain JSON data. For those it returns what the general codec returns; for anything else, and for any text the
// general codec would reject, ok is false and the general codec decides. Reading is where the cost is: the general codec
// scans a record several times through reflection.
func decodeEntryFast(data []byte) (record EntryRecord, ok bool) {
	scanner := fastScanner{data: data}
	scanner.skipSpace()
	if !scanner.consume('{') {
		return EntryRecord{}, false
	}
	var seen uint16
	scanner.skipSpace()
	if scanner.consume('}') {
		return record, scanner.finished()
	}
	for {
		scanner.skipSpace()
		key, keyOK := scanner.keyBytes()
		if !keyOK {
			return EntryRecord{}, false
		}
		scanner.skipSpace()
		if !scanner.consume(':') {
			return EntryRecord{}, false
		}
		scanner.skipSpace()
		field := entryFastKey(key)
		if field == 0 || seen&field != 0 {
			return EntryRecord{}, false
		}
		seen |= field
		var valueOK bool
		switch field {
		case entryKeyID:
			var id int64
			id, valueOK = scanner.integer()
			record.Id = EntryId(id)
		case entryKeyConversationID:
			var id int64
			id, valueOK = scanner.integer()
			record.ConversationId = ConversationId(id)
		case entryKeyKind:
			record.Kind, valueOK = scanner.str()
		case entryKeyModel:
			record.Model, valueOK = scanner.messages()
		case entryKeyData:
			scanner.ordered = true
			record.Data, valueOK = scanner.value(0)
			scanner.ordered = false
		case entryKeyHead:
			if scanner.literal("null") {
				valueOK = true
				break
			}
			var id int64
			if id, valueOK = scanner.integer(); valueOK {
				record.Head = new(EntryId(id))
			}
		case entryKeyByTaskID:
			if scanner.literal("null") {
				valueOK = true
				break
			}
			var id int64
			if id, valueOK = scanner.integer(); valueOK {
				record.ByTaskId = new(TaskId(id))
			}
		case entryKeyEdits:
			if scanner.literal("null") {
				valueOK = true
				break
			}
			// Only an empty list; edits carry messages the general codec decodes.
			if scanner.consume('[') {
				scanner.skipSpace()
				if scanner.consume(']') {
					record.Edits, valueOK = []ContextEdit{}, true
				}
			}
		}
		if !valueOK {
			return EntryRecord{}, false
		}
		scanner.skipSpace()
		if scanner.consume(',') {
			continue
		}
		if scanner.consume('}') {
			break
		}
		return EntryRecord{}, false
	}
	if !scanner.finished() {
		return EntryRecord{}, false
	}
	return record, true
}

const (
	entryKeyID uint16 = 1 << iota
	entryKeyConversationID
	entryKeyKind
	entryKeyModel
	entryKeyData
	entryKeyHead
	entryKeyByTaskID
	entryKeyEdits
)

func entryFastKey(key []byte) uint16 {
	switch string(key) {
	case "id":
		return entryKeyID
	case "conversationId":
		return entryKeyConversationID
	case "kind":
		return entryKeyKind
	case "model":
		return entryKeyModel
	case "data":
		return entryKeyData
	case "head":
		return entryKeyHead
	case "byTaskId":
		return entryKeyByTaskID
	case "edits":
		return entryKeyEdits
	}
	return 0
}

// fastScanner reads JSON text. Every method that returns ok false leaves the scanner unusable.
type fastScanner struct {
	data []byte
	at   int
	// ordered makes value decode objects as insertion-ordered *delta.JsonObject, the durable JsonValue form; otherwise objects are
	// Go maps, the form ai message members take.
	ordered bool
}

func (scanner *fastScanner) skipSpace() {
	for scanner.at < len(scanner.data) {
		switch scanner.data[scanner.at] {
		case ' ', '\t', '\n', '\r':
			scanner.at++
		default:
			return
		}
	}
}

func (scanner *fastScanner) finished() bool {
	scanner.skipSpace()
	return scanner.at == len(scanner.data)
}

func (scanner *fastScanner) consume(c byte) bool {
	if scanner.at < len(scanner.data) && scanner.data[scanner.at] == c {
		scanner.at++
		return true
	}
	return false
}

func (scanner *fastScanner) literal(text string) bool {
	if len(scanner.data)-scanner.at >= len(text) && string(scanner.data[scanner.at:scanner.at+len(text)]) == text {
		scanner.at += len(text)
		return true
	}
	return false
}

// rawString scans a string literal. It returns the bytes between the quotes and whether they hold an escape. Control
// characters, invalid UTF-8 and escapes other than the plain ones are refused: the general codec rewrites or rejects them.
func (scanner *fastScanner) rawString() (body []byte, escaped, ok bool) {
	if !scanner.consume('"') {
		return nil, false, false
	}
	data := scanner.data
	start := scanner.at
	at := start
	for {
		for at < len(data) && !stringSpecial[data[at]] {
			at++
		}
		if at >= len(data) {
			return nil, false, false
		}
		switch c := data[at]; {
		case c == '"':
			body = data[start:at]
			scanner.at = at + 1
			if escaped && !utf8.Valid(body) {
				return nil, false, false
			}
			return body, escaped, true
		case c == '\\':
			escaped = true
			at++
			if at >= len(data) {
				return nil, false, false
			}
			switch data[at] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				at++
			case 'u':
				if at+4 >= len(data) {
					return nil, false, false
				}
				at += 5
			default:
				return nil, false, false
			}
		case c < 0x20:
			return nil, false, false
		default:
			// Multi-byte text is validated once, with the escapes resolved.
			escaped = true
			at++
		}
	}
}

// stringSpecial marks the bytes that end the plain run of a string literal.
var stringSpecial = func() (table [256]bool) {
	for c := range table {
		table[c] = c < 0x20 || c == '"' || c == '\\' || c >= utf8.RuneSelf
	}
	return table
}()

// keyBytes scans an object key; a key with an escape is refused.
func (scanner *fastScanner) keyBytes() ([]byte, bool) {
	body, escaped, ok := scanner.rawString()
	if !ok || escaped {
		return nil, false
	}
	return body, true
}

// str scans a string value and resolves its escapes.
func (scanner *fastScanner) str() (string, bool) {
	body, escaped, ok := scanner.rawString()
	if !ok {
		return "", false
	}
	if !escaped {
		return string(body), true
	}
	return unescapeFast(body)
}

// unescapeFast resolves the escapes of a string body. Surrogate escapes are refused, as are invalid UTF-8 bytes.
func unescapeFast(body []byte) (string, bool) {
	if !utf8.Valid(body) {
		return "", false
	}
	out := make([]byte, 0, len(body))
	for index := 0; index < len(body); index++ {
		c := body[index]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		index++
		switch body[index] {
		case '"', '\\', '/':
			out = append(out, body[index])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			var r rune
			for _, h := range body[index+1 : index+5] {
				switch {
				case h >= '0' && h <= '9':
					r = r<<4 | rune(h-'0')
				case h >= 'a' && h <= 'f':
					r = r<<4 | rune(h-'a'+10)
				case h >= 'A' && h <= 'F':
					r = r<<4 | rune(h-'A'+10)
				default:
					return "", false
				}
			}
			if r >= 0xD800 && r < 0xE000 {
				return "", false
			}
			out = utf8.AppendRune(out, r)
			index += 4
		}
	}
	return string(out), true
}

// number scans a JSON number and returns its text.
func (scanner *fastScanner) number() (text []byte, integral, ok bool) {
	start := scanner.at
	scanner.consume('-')
	switch {
	case scanner.consume('0'):
	case scanner.at < len(scanner.data) && scanner.data[scanner.at] >= '1' && scanner.data[scanner.at] <= '9':
		for scanner.at < len(scanner.data) && scanner.data[scanner.at] >= '0' && scanner.data[scanner.at] <= '9' {
			scanner.at++
		}
	default:
		return nil, false, false
	}
	integral = true
	if scanner.consume('.') {
		integral = false
		digits := scanner.at
		for scanner.at < len(scanner.data) && scanner.data[scanner.at] >= '0' && scanner.data[scanner.at] <= '9' {
			scanner.at++
		}
		if scanner.at == digits {
			return nil, false, false
		}
	}
	if scanner.at < len(scanner.data) && (scanner.data[scanner.at] == 'e' || scanner.data[scanner.at] == 'E') {
		integral = false
		scanner.at++
		if scanner.at < len(scanner.data) && (scanner.data[scanner.at] == '+' || scanner.data[scanner.at] == '-') {
			scanner.at++
		}
		digits := scanner.at
		for scanner.at < len(scanner.data) && scanner.data[scanner.at] >= '0' && scanner.data[scanner.at] <= '9' {
			scanner.at++
		}
		if scanner.at == digits {
			return nil, false, false
		}
	}
	return scanner.data[start:scanner.at], integral, true
}

// integer scans a number that decodes into an int64 field: digits only, in range.
func (scanner *fastScanner) integer() (int64, bool) {
	text, integral, ok := scanner.number()
	if !ok || !integral {
		return 0, false
	}
	value, err := strconv.ParseInt(string(text), 10, 64)
	return value, err == nil
}

// float scans a number that decodes into a float64 field.
func (scanner *fastScanner) float() (float64, bool) {
	text, _, ok := scanner.number()
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseFloat(string(text), 64)
	return value, err == nil && !math.IsInf(value, 0)
}

const fastMaxDepth = 100

// value decodes any JSON value as the general codec decodes it into an interface: numbers are float64, objects
// map[string]any, arrays []any.
func (scanner *fastScanner) value(depth int) (any, bool) {
	if depth > fastMaxDepth || scanner.at >= len(scanner.data) {
		return nil, false
	}
	switch scanner.data[scanner.at] {
	case '"':
		return scanner.str()
	case '{':
		scanner.at++
		// An ordered object keeps the text's key order, as JSON.parse gives Pi; a repeated key keeps its first position and takes the last
		// value.
		var object *delta.JsonObject
		var plain map[string]any
		result := func() any {
			if object != nil {
				return object
			}
			return plain
		}
		if scanner.ordered {
			object = delta.NewJsonObject(0)
		} else {
			plain = map[string]any{}
		}
		scanner.skipSpace()
		if scanner.consume('}') {
			return result(), true
		}
		for {
			scanner.skipSpace()
			body, escaped, ok := scanner.rawString()
			if !ok {
				return nil, false
			}
			key := string(body)
			if escaped {
				if key, ok = unescapeFast(body); !ok {
					return nil, false
				}
			}
			scanner.skipSpace()
			if !scanner.consume(':') {
				return nil, false
			}
			scanner.skipSpace()
			item, ok := scanner.value(depth + 1)
			if !ok {
				return nil, false
			}
			if object != nil {
				object.Set(key, item)
			} else {
				plain[key] = item
			}
			scanner.skipSpace()
			if scanner.consume(',') {
				continue
			}
			if scanner.consume('}') {
				return result(), true
			}
			return nil, false
		}
	case '[':
		scanner.at++
		items := []any{}
		scanner.skipSpace()
		if scanner.consume(']') {
			return items, true
		}
		for {
			scanner.skipSpace()
			item, ok := scanner.value(depth + 1)
			if !ok {
				return nil, false
			}
			items = append(items, item)
			scanner.skipSpace()
			if scanner.consume(',') {
				continue
			}
			if scanner.consume(']') {
				return items, true
			}
			return nil, false
		}
	case 't':
		return true, scanner.literal("true")
	case 'f':
		return false, scanner.literal("false")
	case 'n':
		return nil, scanner.literal("null")
	default:
		number, ok := scanner.float()
		return number, ok
	}
}

// skipValue advances past one JSON value, validating it as value does, and returns its extent.
func (scanner *fastScanner) skipValue(depth int) (start, end int, ok bool) {
	start = scanner.at
	if !scanner.skip(depth) {
		return 0, 0, false
	}
	return start, scanner.at, true
}

// skip validates one JSON value without building it.
func (scanner *fastScanner) skip(depth int) bool {
	if depth > fastMaxDepth || scanner.at >= len(scanner.data) {
		return false
	}
	switch scanner.data[scanner.at] {
	case '"':
		body, escaped, ok := scanner.rawString()
		if !ok {
			return false
		}
		if escaped {
			_, ok = unescapeFast(body)
		}
		return ok
	case '{':
		scanner.at++
		scanner.skipSpace()
		if scanner.consume('}') {
			return true
		}
		for {
			scanner.skipSpace()
			body, escaped, ok := scanner.rawString()
			if !ok {
				return false
			}
			if escaped {
				if _, ok = unescapeFast(body); !ok {
					return false
				}
			}
			scanner.skipSpace()
			if !scanner.consume(':') {
				return false
			}
			scanner.skipSpace()
			if !scanner.skip(depth + 1) {
				return false
			}
			scanner.skipSpace()
			if scanner.consume(',') {
				continue
			}
			return scanner.consume('}')
		}
	case '[':
		scanner.at++
		scanner.skipSpace()
		if scanner.consume(']') {
			return true
		}
		for {
			scanner.skipSpace()
			if !scanner.skip(depth + 1) {
				return false
			}
			scanner.skipSpace()
			if scanner.consume(',') {
				continue
			}
			return scanner.consume(']')
		}
	case 't':
		return scanner.literal("true")
	case 'f':
		return scanner.literal("false")
	case 'n':
		return scanner.literal("null")
	default:
		_, ok := scanner.float()
		return ok
	}
}

// messages decodes the model array of an entry, or null.
func (scanner *fastScanner) messages() ([]ai.Message, bool) {
	if scanner.literal("null") {
		return nil, true
	}
	if !scanner.consume('[') {
		return nil, false
	}
	messages := []ai.Message{}
	scanner.skipSpace()
	if scanner.consume(']') {
		return messages, true
	}
	for {
		scanner.skipSpace()
		message, ok := scanner.message()
		if !ok {
			return nil, false
		}
		messages = append(messages, message)
		scanner.skipSpace()
		if scanner.consume(',') {
			continue
		}
		if scanner.consume(']') {
			return messages, true
		}
		return nil, false
	}
}

const (
	messageKeyRole uint32 = 1 << iota
	messageKeyContent
	messageKeyTimestamp
	messageKeyAPI
	messageKeyProvider
	messageKeyModel
	messageKeyUsage
	messageKeyStopReason
	messageKeyToolCallID
	messageKeyToolName
	messageKeyIsError
	messageKeyDetails
)

func messageFastKey(key []byte) uint32 {
	switch string(key) {
	case "role":
		return messageKeyRole
	case "content":
		return messageKeyContent
	case "timestamp":
		return messageKeyTimestamp
	case "api":
		return messageKeyAPI
	case "provider":
		return messageKeyProvider
	case "model":
		return messageKeyModel
	case "usage":
		return messageKeyUsage
	case "stopReason":
		return messageKeyStopReason
	case "toolCallId":
		return messageKeyToolCallID
	case "toolName":
		return messageKeyToolName
	case "isError":
		return messageKeyIsError
	case "details":
		return messageKeyDetails
	}
	return 0
}

const (
	userKeys      = messageKeyRole | messageKeyContent | messageKeyTimestamp
	assistantKeys = userKeys | messageKeyAPI | messageKeyProvider | messageKeyModel | messageKeyUsage | messageKeyStopReason
	toolKeys      = userKeys | messageKeyToolCallID | messageKeyToolName | messageKeyIsError | messageKeyDetails
)

// message decodes one message object. Content is kept as the extent of its JSON until the role says what it is.
func (scanner *fastScanner) message() (ai.Message, bool) {
	if !scanner.consume('{') {
		return nil, false
	}
	var (
		seen                       uint32
		role                       string
		timestamp                  int64
		api, provider, model, stop string
		toolCallID, toolName       string
		isError                    bool
		usage                      ai.Usage
		details                    any
		hasDetails, detailsNull    bool
		contentStart, contentEnd   int
	)
	scanner.skipSpace()
	if scanner.consume('}') {
		return nil, false
	}
	for {
		scanner.skipSpace()
		key, ok := scanner.keyBytes()
		if !ok {
			return nil, false
		}
		field := messageFastKey(key)
		if field == 0 || seen&field != 0 {
			return nil, false
		}
		seen |= field
		scanner.skipSpace()
		if !scanner.consume(':') {
			return nil, false
		}
		scanner.skipSpace()
		switch field {
		case messageKeyRole:
			role, ok = scanner.str()
		case messageKeyContent:
			contentStart, contentEnd, ok = scanner.skipValue(0)
		case messageKeyTimestamp:
			timestamp, ok = scanner.integer()
		case messageKeyAPI:
			api, ok = scanner.str()
		case messageKeyProvider:
			provider, ok = scanner.str()
		case messageKeyModel:
			model, ok = scanner.str()
		case messageKeyStopReason:
			stop, ok = scanner.str()
		case messageKeyToolCallID:
			toolCallID, ok = scanner.str()
		case messageKeyToolName:
			toolName, ok = scanner.str()
		case messageKeyIsError:
			switch {
			case scanner.literal("true"):
				isError, ok = true, true
			case scanner.literal("false"):
				ok = true
			}
		case messageKeyUsage:
			usage, ok = scanner.usage()
		case messageKeyDetails:
			hasDetails = true
			if details, ok = scanner.value(0); ok {
				detailsNull = details == nil
			}
		}
		if !ok {
			return nil, false
		}
		scanner.skipSpace()
		if scanner.consume(',') {
			continue
		}
		if scanner.consume('}') {
			break
		}
		return nil, false
	}
	if seen&messageKeyRole == 0 || seen&messageKeyContent == 0 {
		return nil, false
	}
	content := fastScanner{data: scanner.data[contentStart:contentEnd]}
	switch role {
	case "user":
		if seen&^userKeys != 0 {
			return nil, false
		}
		decoded, ok := content.userContent()
		if !ok {
			return nil, false
		}
		return ai.UserMessage{Content: decoded, Timestamp: timestamp}, true
	case "assistant":
		if seen&^assistantKeys != 0 {
			return nil, false
		}
		blocks, ok := content.assistantBlocks()
		if !ok {
			return nil, false
		}
		return ai.AssistantMessage{
			Content: blocks, API: ai.API(api), Provider: provider, Model: model, Usage: usage,
			StopReason: ai.StopReason(stop), Timestamp: timestamp,
		}, true
	case "toolResult":
		if seen&^toolKeys != 0 {
			return nil, false
		}
		blocks, ok := content.toolResultBlocks()
		if !ok {
			return nil, false
		}
		result := ai.ToolResultMessage{ToolCallID: toolCallID, ToolName: toolName, Content: blocks, IsError: isError, Timestamp: timestamp}
		if hasDetails {
			result.Details, result.DetailsNull = details, detailsNull
		}
		return result, true
	}
	return nil, false
}

// usage decodes a usage object: the counts and the cost breakdown.
func (scanner *fastScanner) usage() (ai.Usage, bool) {
	var usage ai.Usage
	if !scanner.consume('{') {
		return usage, false
	}
	var seen uint16
	scanner.skipSpace()
	if scanner.consume('}') {
		return usage, true
	}
	for {
		scanner.skipSpace()
		key, ok := scanner.keyBytes()
		if !ok {
			return usage, false
		}
		scanner.skipSpace()
		if !scanner.consume(':') {
			return usage, false
		}
		scanner.skipSpace()
		var bit uint16
		var count int64
		switch string(key) {
		case "input":
			bit = 1
			count, ok = scanner.integer()
			usage.Input = int(count)
		case "output":
			bit = 2
			count, ok = scanner.integer()
			usage.Output = int(count)
		case "cacheRead":
			bit = 4
			count, ok = scanner.integer()
			usage.CacheRead = int(count)
		case "cacheWrite":
			bit = 8
			count, ok = scanner.integer()
			usage.CacheWrite = int(count)
		case "totalTokens":
			bit = 16
			count, ok = scanner.integer()
			usage.TotalTokens = int(count)
		case "cost":
			bit = 32
			usage.Cost, ok = scanner.cost()
		default:
			return usage, false
		}
		if !ok || seen&bit != 0 {
			return usage, false
		}
		seen |= bit
		scanner.skipSpace()
		if scanner.consume(',') {
			continue
		}
		if scanner.consume('}') {
			return usage, true
		}
		return usage, false
	}
}

func (scanner *fastScanner) cost() (ai.UsageCost, bool) {
	var cost ai.UsageCost
	if !scanner.consume('{') {
		return cost, false
	}
	var seen uint16
	scanner.skipSpace()
	if scanner.consume('}') {
		return cost, true
	}
	for {
		scanner.skipSpace()
		key, ok := scanner.keyBytes()
		if !ok {
			return cost, false
		}
		scanner.skipSpace()
		if !scanner.consume(':') {
			return cost, false
		}
		scanner.skipSpace()
		var bit uint16
		var target *float64
		switch string(key) {
		case "input":
			bit, target = 1, &cost.Input
		case "output":
			bit, target = 2, &cost.Output
		case "cacheRead":
			bit, target = 4, &cost.CacheRead
		case "cacheWrite":
			bit, target = 8, &cost.CacheWrite
		case "total":
			bit, target = 16, &cost.Total
		default:
			return cost, false
		}
		if seen&bit != 0 {
			return cost, false
		}
		seen |= bit
		if *target, ok = scanner.float(); !ok {
			return cost, false
		}
		scanner.skipSpace()
		if scanner.consume(',') {
			continue
		}
		if scanner.consume('}') {
			return cost, true
		}
		return cost, false
	}
}

// toolCallBlock decodes {"type":"toolCall","id":...,"name":...,"arguments":{...}} from the block's own text. Any other
// member, or arguments that are not an object or null, leave the block to the general codec.
func toolCallBlock(block []byte) (ai.ToolCall, bool) {
	scanner := fastScanner{data: block}
	if !scanner.consume('{') {
		return ai.ToolCall{}, false
	}
	var id, name, signature, namespace string
	var arguments ai.JsonObject
	var rawArguments []byte
	var seen uint8
	for {
		scanner.skipSpace()
		key, ok := scanner.keyBytes()
		if !ok {
			return ai.ToolCall{}, false
		}
		scanner.skipSpace()
		if !scanner.consume(':') {
			return ai.ToolCall{}, false
		}
		scanner.skipSpace()
		var bit uint8
		switch string(key) {
		case "type":
			bit = 1
			var kind string
			if kind, ok = scanner.str(); ok && kind != "toolCall" {
				return ai.ToolCall{}, false
			}
		case "id":
			bit = 2
			id, ok = scanner.str()
		case "name":
			bit = 4
			name, ok = scanner.str()
		case "arguments":
			bit = 8
			start := scanner.at
			var value any
			if value, ok = scanner.value(0); ok {
				rawArguments = scanner.data[start:scanner.at]
				switch typed := value.(type) {
				case nil:
				case map[string]any:
					arguments = typed
				default:
					ok = false
				}
			}
		case "thoughtSignature":
			bit = 16
			signature, ok = scanner.str()
		case "namespace":
			bit = 32
			namespace, ok = scanner.str()
		default:
			return ai.ToolCall{}, false
		}
		if !ok || seen&bit != 0 {
			return ai.ToolCall{}, false
		}
		seen |= bit
		scanner.skipSpace()
		if scanner.consume(',') {
			continue
		}
		if !scanner.consume('}') || !scanner.finished() || seen&1 == 0 {
			return ai.ToolCall{}, false
		}
		call, err := ai.NewDecodedToolCall(id, name, signature, namespace, arguments, rawArguments)
		return call, err == nil
	}
}

// blocks walks an array of content blocks, decoding each through decodeBlock.
func (scanner *fastScanner) blocks(decodeBlock func(start, end int) bool) bool {
	if !scanner.consume('[') {
		return false
	}
	scanner.skipSpace()
	if scanner.consume(']') {
		return scanner.finished()
	}
	for {
		scanner.skipSpace()
		start, end, ok := scanner.skipValue(0)
		if !ok || !decodeBlock(start, end) {
			return false
		}
		scanner.skipSpace()
		if scanner.consume(',') {
			continue
		}
		if scanner.consume(']') {
			return scanner.finished()
		}
		return false
	}
}

// textBlock decodes {"type":"text","text":...}, with an optional textSignature, from the block's own text.
func textBlock(block []byte) (ai.TextContent, bool) {
	scanner := fastScanner{data: block}
	if !scanner.consume('{') {
		return ai.TextContent{}, false
	}
	var text ai.TextContent
	var seen uint8
	for {
		scanner.skipSpace()
		key, ok := scanner.keyBytes()
		if !ok {
			return text, false
		}
		scanner.skipSpace()
		if !scanner.consume(':') {
			return text, false
		}
		scanner.skipSpace()
		var bit uint8
		switch string(key) {
		case "type":
			bit = 1
			var kind string
			if kind, ok = scanner.str(); ok && kind != "text" {
				return text, false
			}
		case "text":
			bit = 2
			text.Text, ok = scanner.str()
		case "textSignature":
			bit = 4
			text.TextSignature, ok = scanner.str()
		default:
			return text, false
		}
		if !ok || seen&bit != 0 {
			return text, false
		}
		seen |= bit
		scanner.skipSpace()
		if scanner.consume(',') {
			continue
		}
		if scanner.consume('}') {
			return text, seen&3 == 3 && scanner.finished()
		}
		return text, false
	}
}

func (scanner *fastScanner) userContent() (ai.UserContent, bool) {
	scanner.skipSpace()
	if scanner.at < len(scanner.data) && scanner.data[scanner.at] == '"' {
		text, ok := scanner.str()
		return ai.UserText(text), ok && scanner.finished()
	}
	blocks := ai.UserContentBlocks{}
	ok := scanner.blocks(func(start, end int) bool {
		if text, ok := textBlock(scanner.data[start:end]); ok {
			blocks = append(blocks, text)
			return true
		}
		block, err := ai.UnmarshalContentBlock(scanner.data[start:end])
		typed, isUser := block.(ai.UserContentBlock)
		if err != nil || !isUser {
			return false
		}
		blocks = append(blocks, typed)
		return true
	})
	return blocks, ok
}

func (scanner *fastScanner) assistantBlocks() ([]ai.AssistantContentBlock, bool) {
	scanner.skipSpace()
	blocks := []ai.AssistantContentBlock{}
	ok := scanner.blocks(func(start, end int) bool {
		if text, ok := textBlock(scanner.data[start:end]); ok {
			blocks = append(blocks, text)
			return true
		}
		if call, ok := toolCallBlock(scanner.data[start:end]); ok {
			blocks = append(blocks, call)
			return true
		}
		block, err := ai.UnmarshalContentBlock(scanner.data[start:end])
		typed, isAssistant := block.(ai.AssistantContentBlock)
		if err != nil || !isAssistant {
			return false
		}
		blocks = append(blocks, typed)
		return true
	})
	return blocks, ok
}

func (scanner *fastScanner) toolResultBlocks() ([]ai.ToolResultMessageContent, bool) {
	scanner.skipSpace()
	blocks := []ai.ToolResultMessageContent{}
	ok := scanner.blocks(func(start, end int) bool {
		if text, ok := textBlock(scanner.data[start:end]); ok {
			blocks = append(blocks, text)
			return true
		}
		block, err := ai.UnmarshalContentBlock(scanner.data[start:end])
		typed, isResult := block.(ai.ToolResultMessageContent)
		if err != nil || !isResult {
			return false
		}
		blocks = append(blocks, typed)
		return true
	})
	return blocks, ok
}
