// Ports packages/agent/src/types.ts.
package agent

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Clone copies the message envelope and mutable top-level slices/maps. A streaming assistant becomes an independently owned current observation.
func (m AgentMessage) Clone() AgentMessage {
	clone := AgentMessage{}
	switch {
	case m.System != nil:
		clone.System = ai.GetInitialSystemMessage([]ai.Message{*m.System})
		if m.System.Content == nil {
			// The copy of a message decoded without content has none either.
			clone.System.Content = nil
		}
		clone.source = m.source
	case m.User != nil:
		value := *m.User
		if blocks, ok := m.User.Content.(ai.UserContentBlocks); ok {
			value.Content = slices.Clone(blocks)
		}
		clone.User = &value
	case m.Assistant != nil:
		// Observe returns a new message the caller owns.
		clone.Assistant = m.Assistant.Observe()
	case m.ToolResult != nil:
		value := *m.ToolResult
		value.Content = slices.Clone(m.ToolResult.Content)
		if m.ToolResult.Usage != nil {
			usage := *m.ToolResult.Usage
			value.Usage = &usage
		}
		clone.ToolResult = &value
	case m.Custom != nil:
		clone.Custom = maps.Clone(m.Custom)
		clone.source = m.source
	}
	return clone
}

// MarshalJSON observes retained streaming messages at the serialization boundary.
func (m AssistantMessage) MarshalJSON() ([]byte, error) {
	type plain AssistantMessage
	return json.Marshal((*plain)(m.Observe()))
}

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (json.StringifyAppender).
func (m AssistantMessage) AppendStringify(dst []byte) ([]byte, error) {
	type plain AssistantMessage
	return json.AppendStringify(dst, (*plain)(m.Observe()))
}

// MarshalJSON emits the pi-ai ToolResultMessage object an AgentMessage holding it emits: the "toolResult" role first and an explicit `"details":null` when the tool wrote it.
func (m ToolResultMessage) MarshalJSON() ([]byte, error) {
	return AgentMessage{ToolResult: &m}.MarshalJSON()
}

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (json.StringifyAppender).
func (m ToolResultMessage) AppendStringify(dst []byte) ([]byte, error) {
	return AgentMessage{ToolResult: &m}.AppendStringify(dst)
}

// MarshalJSON emits the upstream flat role-discriminated AgentMessage union. User strings and text blocks retain their JavaScript UTF-16 units.
func (m AgentMessage) MarshalJSON() ([]byte, error) {
	value, err := m.wire(true)
	if err != nil {
		return nil, err
	}
	if text, ok := value.(json.RawMessage); ok {
		return text, nil
	}
	return json.Marshal(value)
}

// AppendStringify appends the JSON.stringify text of MarshalJSON's output to dst (json.StringifyAppender), encoding the message's members
// into dst itself.
func (m AgentMessage) AppendStringify(dst []byte) ([]byte, error) {
	value, err := m.wire(false)
	if err != nil {
		return dst, err
	}
	return json.AppendStringify(dst, value)
}

// wire returns what MarshalJSON encodes: the JSON text the message writes as it is (its decoded source, cloned when clone is set, or a
// custom message's members) as a json.RawMessage, or the value whose encoding is the message.
func (m AgentMessage) wire(clone bool) (any, error) {
	variants := 0
	if m.System != nil {
		variants++
	}
	if m.User != nil {
		variants++
	}
	if m.Assistant != nil {
		variants++
	}
	if m.ToolResult != nil {
		variants++
	}
	if m.Custom != nil {
		variants++
	}
	if variants != 1 {
		return nil, fmt.Errorf("AgentMessage must contain exactly one variant, got %d", variants)
	}
	if text := m.sourceText(); text != nil {
		if clone {
			text = bytes.Clone(text)
		}
		return json.RawMessage(text), nil
	}
	if source, err := m.SourceJSON(); source != nil || err != nil {
		return json.RawMessage(source), err
	}

	switch {
	case m.System != nil:
		return m.System, nil
	case m.User != nil:
		var content any
		switch value := m.User.Content.(type) {
		case ai.UserText:
			content = value
		case ai.UserContentBlocks:
			encoded, err := marshalAgentContent(value)
			if err != nil {
				return nil, err
			}
			content = encoded
		case nil:
			content = []any{}
		}
		return struct {
			Role      string `json:"role"`
			Content   any    `json:"content"`
			Timestamp int64  `json:"timestamp"`
		}{Role: RoleUser, Content: content, Timestamp: m.User.Timestamp}, nil
	case m.Assistant != nil:
		// Observe copies a settled message field by field; only a streaming one needs its current state read.
		message := m.Assistant
		if message.streamView != nil {
			message = message.Observe()
		}
		content, err := marshalAgentContent(message.Content)
		if err != nil {
			return nil, err
		}
		if message.ErrorBeforeTimestamp && message.ErrorMessage != "" {
			// faux.ts builds its messages with errorMessage before timestamp; JavaScript keeps the order a message was built in.
			return struct {
				Role                  string                          `json:"role"`
				Content               []any                           `json:"content"`
				API                   ai.API                          `json:"api"`
				Provider              string                          `json:"provider"`
				Model                 string                          `json:"model"`
				Usage                 ai.Usage                        `json:"usage"`
				StopReason            ai.StopReason                   `json:"stopReason"`
				ErrorMessage          string                          `json:"errorMessage"`
				Timestamp             int64                           `json:"timestamp"`
				ResponseID            string                          `json:"responseId,omitempty"`
				ResponseModel         string                          `json:"responseModel,omitempty"`
				ProviderThinkingLevel string                          `json:"providerThinkingLevel,omitempty"`
				Diagnostics           []ai.AssistantMessageDiagnostic `json:"diagnostics,omitzero"`
				Deferred              *ai.DeferredHandle              `json:"deferred,omitempty"`
				RawStopReason         string                          `json:"rawStopReason,omitempty"`
				EndTurn               *bool                           `json:"endTurn,omitempty"`
				DurationMs            *int64                          `json:"durationMs,omitempty"`
				ThinkingLevel         ai.ModelThinkingLevel           `json:"thinkingLevel,omitempty"`
			}{
				Role: RoleAssistant, Content: content, API: message.API,
				Provider: message.Provider, Model: message.ModelID, Usage: usageToWire(message.Usage), StopReason: message.StopReason,
				ErrorMessage: message.ErrorMessage, Timestamp: message.Timestamp,
				ResponseModel: message.ResponseModel, ResponseID: message.ResponseID, ProviderThinkingLevel: message.ProviderThinkingLevel,
				Diagnostics: message.Diagnostics, Deferred: message.Deferred, RawStopReason: message.RawStopReason, EndTurn: message.EndTurn,
				DurationMs: message.DurationMs, ThinkingLevel: message.ThinkingLevel,
			}, nil
		}
		return struct {
			Role       string        `json:"role"`
			Content    []any         `json:"content"`
			API        ai.API        `json:"api"`
			Provider   string        `json:"provider"`
			Model      string        `json:"model"`
			Usage      ai.Usage      `json:"usage"`
			StopReason ai.StopReason `json:"stopReason"`
			Timestamp  int64         `json:"timestamp"`
			// anthropic-messages.ts:651 assigns providerThinkingLevel after buildParams (Pi dc939913), after the initial output literal and
			// before the members the stream creates.
			ProviderThinkingLevel string `json:"providerThinkingLevel,omitempty"`
			// The members below are created on the output a provider built with the members above, in stream order, and the event encoder
			// (coding/cli/rpc_events.go rpcAgentMessage) writes them in the same order: the providers create responseId at the first chunk
			// (openai-completions.ts:564 `||=` creates the slot even when the chunk has no id), then responseModel (:566), rawStopReason at
			// the finish reason (:582), and errorMessage at the finish reason or in its catch block (:586, :719). Codex, the one provider that
			// sets endTurn, creates it on the terminal event before the shared Responses code creates rawStopReason
			// (openai-codex-responses.ts:774-778, openai-responses-shared.ts:589). pi-messages.ts:202 and agent proxy.ts:374 assign
			// providerThinkingLevel during the stream instead; that order is not kept.
			ResponseID    string                          `json:"responseId,omitempty"`
			ResponseModel string                          `json:"responseModel,omitempty"`
			Diagnostics   []ai.AssistantMessageDiagnostic `json:"diagnostics,omitzero"`
			Deferred      *ai.DeferredHandle              `json:"deferred,omitempty"`
			EndTurn       *bool                           `json:"endTurn,omitempty"`
			RawStopReason string                          `json:"rawStopReason,omitempty"`
			ErrorMessage  string                          `json:"errorMessage,omitempty"`
			// durationMs is set by the response stream on its final message, after the provider's own keys and before the agent loop assigns thinkingLevel (event-stream.ts:127-128, agent-loop.ts:409).
			DurationMs *int64 `json:"durationMs,omitempty"`
			// thinkingLevel is assigned to the finished response after the provider built it, so it follows the provider's own keys.
			ThinkingLevel ai.ModelThinkingLevel `json:"thinkingLevel,omitempty"`
		}{
			Role: RoleAssistant, Content: content, API: message.API,
			Provider: message.Provider, Model: message.ModelID,
			ResponseModel: message.ResponseModel, ResponseID: message.ResponseID,
			ProviderThinkingLevel: message.ProviderThinkingLevel,
			Diagnostics:           message.Diagnostics, Usage: usageToWire(message.Usage),
			StopReason: message.StopReason, Deferred: message.Deferred,
			ErrorMessage: message.ErrorMessage, RawStopReason: message.RawStopReason,
			EndTurn: message.EndTurn, DurationMs: message.DurationMs, Timestamp: message.Timestamp, ThinkingLevel: message.ThinkingLevel,
		}, nil
	case m.ToolResult != nil:
		content, err := marshalAgentContent(m.ToolResult.Content)
		if err != nil {
			return nil, err
		}
		message := m.ToolResult
		details := message.Details
		if details == nil && message.DetailsNull {
			details = json.RawMessage("null")
		}
		return struct {
			Role       string    `json:"role"`
			ToolCallID string    `json:"toolCallId"`
			ToolName   string    `json:"toolName"`
			Content    []any     `json:"content"`
			Details    any       `json:"details,omitempty"`
			Usage      *ai.Usage `json:"usage,omitempty"`
			IsError    bool      `json:"isError"`
			DurationMs *int64    `json:"durationMs,omitempty"`
			Timestamp  int64     `json:"timestamp"`
			// nestedCalls follows timestamp, where upstream's agent session sets it on the message after creating it (agent-session.ts:1065-1066).
			NestedCalls *ai.NestedToolCalls `json:"nestedCalls,omitempty"`
		}{
			Role: RoleToolResult, ToolCallID: message.ToolCallID, ToolName: message.ToolName,
			Content: content, Details: details, Usage: optionalUsageToWire(message.Usage),
			IsError: message.IsError, DurationMs: message.DurationMs, Timestamp: message.Timestamp, NestedCalls: message.NestedCalls,
		}, nil
	default:
		role, ok := m.Custom["role"].(string)
		if !ok || role == "" {
			return nil, fmt.Errorf("custom AgentMessage requires a non-empty string role")
		}
		encoded, err := marshalCustomMessage(role, m.Custom)
		return json.RawMessage(encoded), err
	}
}

// customMessageMemberOrder is the order Pi builds the members of each custom role's object in, which JSON.stringify keeps. A member outside the list follows, sorted; a role outside the table is sorted entirely. Content blocks are written in Pi's member order too (packages/ai/src/types.ts TextContent, ImageContent).
// upstream: coding-agent/src/core/messages.ts:29-67 (shapes), 100-137 (createBranchSummaryMessage, createCompactionSummaryMessage, createCustomMessage), agent-session.ts:3781-3792 (recordBashResult)
var customMessageMemberOrder = map[string][]string{
	RoleCustom:            {"role", "customType", "content", "display", "details", "timestamp"},
	"bashExecution":       {"role", "command", "output", "exitCode", "cancelled", "truncated", "fullOutputPath", "timestamp", "excludeFromContext"},
	RoleBranchSummary:     {"role", "summary", "fromId", "timestamp"},
	RoleCompactionSummary: {"role", "summary", "tokensBefore", "timestamp"},
}

// contentBlockMemberOrder is the order Pi's tools and extensions write a TextContent or ImageContent in (packages/ai/src/types.ts).
var contentBlockMemberOrder = []string{"type", "text", "textSignature", "data", "mimeType"}

func marshalCustomMessage(role string, members map[string]any) ([]byte, error) {
	if blocks, ok := members["content"].([]any); ok {
		ordered, err := orderedjson.MarshalArray(blocks, contentBlockMemberOrder...)
		if err != nil {
			return nil, err
		}
		members = maps.Clone(members)
		members["content"] = json.RawMessage(ordered)
	}
	return orderedjson.MarshalMap(members, customMessageMemberOrder[role]...)
}

// SourceJSON returns the JSON.stringify text of the object m was decoded from, as Pi keeps the parsed object and every writer of the message stringifies it, when that text differs from the encoding of m's fields; otherwise it returns nil. A Custom message encodes its current members in the decoded object's member order; one decoded from a value that is not an object returns that value.
func (m AgentMessage) SourceJSON() ([]byte, error) {
	if text := m.sourceText(); text != nil {
		return bytes.Clone(text), nil
	}
	if m.Custom != nil && m.source != nil {
		encoded, err := orderedjson.MarshalInSourceOrder(m.Custom, m.source.text)
		if err != nil {
			return nil, err
		}
		return jsonstringify.Canonicalize(encoded)
	}
	return nil, nil
}

// sourceText is the text SourceJSON returns as it is held, when that is the text m was decoded from (not a custom object's members).
func (m AgentMessage) sourceText() []byte {
	switch {
	case m.System != nil && m.source != nil:
		return m.source.text
	case m.User != nil && m.User.source != nil:
		return m.User.source.text
	case m.Assistant != nil && m.Assistant.source != nil:
		return m.Assistant.source.text
	case m.ToolResult != nil && m.ToolResult.source != nil:
		return m.ToolResult.source.text
	case m.Custom != nil && m.source != nil && len(m.source.text) > 0 && m.source.text[0] != '{':
		return m.source.text
	}
	return nil
}

// TextBytes is the length of the strings that make up most of m's JSON: the text it was decoded from, or its content's text, thinking,
// image data and tool-call argument strings and keys, a tool result's details strings, and a custom message's strings. A writer sizes the buffer it encodes m into from it.
func (m AgentMessage) TextBytes() int {
	if text := m.sourceText(); text != nil {
		return len(text)
	}
	switch {
	case m.User != nil:
		switch content := m.User.Content.(type) {
		case ai.UserText:
			return len(content)
		case ai.UserContentBlocks:
			return contentTextBytes(content)
		}
	case m.Assistant != nil:
		return contentTextBytes(m.Assistant.Content)
	case m.ToolResult != nil:
		return contentTextBytes(m.ToolResult.Content) + valueTextBytes(m.ToolResult.Details, new(textBytesBudget))
	case m.Custom != nil:
		return valueTextBytes(map[string]any(m.Custom), new(textBytesBudget))
	}
	return 0
}

func contentTextBytes[T any](content []T) int {
	size, budget := 0, textBytesBudget
	for _, block := range content {
		switch block := any(block).(type) {
		case ai.TextContent:
			size += len(block.Text)
		case ai.ThinkingContent:
			size += len(block.Thinking) + len(block.ThinkingSignature)
		case ai.ImageContent:
			size += len(block.Data)
		case ai.ToolCall:
			size += valueTextBytes(map[string]any(block.Arguments), &budget)
		}
	}
	return size
}

// textBytesBudget bounds the values valueTextBytes visits for one message: the walk is a size hint, so a large or cyclic value is cut short.
// pig additive (D112): Pi sizes no buffer before JSON.stringify; the count only sizes the buffer a writer encodes into, so a cut walk changes how often it grows, not the bytes written.
const textBytesBudget = 4096

// valueTextBytes is the length of the strings and keys in a decoded JSON value, counting at most *budget values.
func valueTextBytes(value any, budget *int) int {
	if *budget <= 0 {
		return 0
	}
	*budget--
	size := 0
	switch value := value.(type) {
	case string:
		size = len(value)
	case map[string]any:
		for key, member := range value {
			size += len(key) + valueTextBytes(member, budget)
		}
	case ai.JsonObject:
		size = valueTextBytes(map[string]any(value), budget)
	case []any:
		for _, item := range value {
			size += valueTextBytes(item, budget)
		}
	case nil, bool, float64, json.RawMessage:
		if raw, ok := value.(json.RawMessage); ok {
			size = len(raw)
		}
	default:
		// A Go value of another type, such as a tool's typed details, encodes its exported strings, string map keys and byte slices.
		size = reflectTextBytes(reflect.ValueOf(value), budget)
	}
	return size
}

// reflectTextBytes is valueTextBytes for a Go value: the length of its exported strings and map keys, and of its byte slices in base64,
// counting at most *budget nested values.
func reflectTextBytes(value reflect.Value, budget *int) int {
	child := func(v reflect.Value) int {
		if *budget <= 0 {
			return 0
		}
		*budget--
		return reflectTextBytes(v, budget)
	}
	size := 0
	switch value.Kind() {
	case reflect.String:
		size = value.Len()
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			size = child(value.Elem())
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				size += child(value.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			//portlint:allow numbers base64 length of a byte count: exact integer arithmetic, no JS equivalent
			return (value.Len() + 2) / 3 * 4
		}
		for i := range value.Len() {
			size += child(value.Index(i))
		}
	case reflect.Map:
		for iter := value.MapRange(); iter.Next(); {
			if iter.Key().Kind() == reflect.String {
				size += iter.Key().Len()
			}
			size += child(iter.Value())
		}
	}
	return size
}

// WithEmptyContent returns a copy of m with empty content, as the JavaScript spread { ...m, content: [] } builds it, and { ...m, content: "" } for a system message: a content member of the decoded object keeps its place, and a missing one follows the other members.
func (m AgentMessage) WithEmptyContent() AgentMessage {
	switch {
	case m.System != nil:
		system := *m.System
		system.Content = ai.SystemText("")
		m.System = &system
		m.source = withEmptyContent(m.source, `""`)
	case m.User != nil:
		user := *m.User
		user.Content = ai.UserContentBlocks{}
		user.source = withEmptyContent(user.source, "[]")
		m.User = &user
	case m.Assistant != nil:
		assistant := *m.Assistant.Observe()
		assistant.Content = []ai.AssistantContentBlock{}
		assistant.source = withEmptyContent(assistant.source, "[]")
		m.Assistant = &assistant
	case m.ToolResult != nil:
		result := *m.ToolResult
		result.Content = []ai.ToolResultMessageContent{}
		result.source = withEmptyContent(result.source, "[]")
		m.ToolResult = &result
	case m.Custom != nil:
		m.Custom = maps.Clone(m.Custom)
		m.Custom["content"] = []any{}
	}
	return m
}

// NullContent reports a system, user, assistant or toolResult message whose content is null or missing: in the object it was decoded from, or in its fields when it was not decoded.
func (m AgentMessage) NullContent() bool {
	switch {
	case m.System != nil:
		if m.source != nil {
			return m.source.nullContent
		}
		return m.System.Content == nil
	case m.User != nil:
		if m.User.source != nil {
			return m.User.source.nullContent
		}
		return m.User.Content == nil
	case m.Assistant != nil:
		if m.Assistant.source != nil {
			return m.Assistant.source.nullContent
		}
		return m.Assistant.Content == nil
	case m.ToolResult != nil:
		if m.ToolResult.source != nil {
			return m.ToolResult.source.nullContent
		}
		return m.ToolResult.Content == nil
	}
	return false
}

// withEmptyContent sets the content member of a decoded message's text to empty, or returns nil when there is no text or it cannot be rewritten, so the fields encode the message.
func withEmptyContent(source *messageSource, empty string) *messageSource {
	if source == nil {
		return nil
	}
	members, err := orderedjson.Parse(source.text)
	if err != nil {
		return nil
	}
	members.Set("content", json.RawMessage(empty))
	encoded, err := members.MarshalJSON()
	if err != nil {
		return nil
	}
	canonical, err := jsonstringify.Canonicalize(encoded)
	if err != nil {
		return nil
	}
	return &messageSource{text: canonical}
}

// UnmarshalJSON decodes the upstream flat role-discriminated AgentMessage union and retains lone UTF-16 units in user content as WTF-8. When the decoded fields would encode to other text than JSON.stringify writes for data (member order, members PiG does not model, or a spelling the fields cannot keep), the message keeps that text: Pi holds the parsed object and writes it unchanged.
//
// The fields decode JSON.stringify's text of data, so a number keeps the message in any spelling, as JSON.parse keeps it: an integral value spelled with a fraction or exponent decodes as the integer in every nested record too, and a fraction or a value beyond the range of a nested record's integer field leaves that field zero. A member of another JSON type than its field holds reads as absent (decodeLax).
func (m *AgentMessage) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	text, canonical := data, jsonstringify.IsCanonical(data)
	if !canonical {
		var err error
		if text, err = jsonstringify.Canonicalize(data); err != nil {
			_, err = m.decode(data)
			return err
		}
	}
	nullContent, err := m.decode(text)
	if err != nil {
		nullContent, err = m.decodeLax(text)
	}
	if err != nil {
		return err
	}
	m.keepSource(text, canonical, nullContent)
	return nil
}

// keepSource retains text, JSON.stringify(JSON.parse(data)), when it differs from the encoding of the decoded fields; a message Pi wrote retains nothing and, being canonical text in the encoder's member order, is accepted by one scan of its members without encoding. borrowed reports that text is the caller's data, which the message copies before retaining.
func (m *AgentMessage) keepSource(text []byte, borrowed, nullContent bool) {
	if m.encodesAs(text) {
		return
	}
	// Text the member scan cannot prove is compared with the fields' encoding. text is canonical, so an encoding that differs from it only by the escapes Go writes and JSON.stringify does not canonicalizes to it too, and the encoding is canonicalized only otherwise.
	if encoded, err := m.MarshalJSON(); err == nil {
		if equalUpToGoEscapes(encoded, text) {
			return
		}
		if got, err := jsonstringify.Canonicalize(encoded); err == nil && bytes.Equal(got, text) {
			return
		}
	}
	want := text
	if borrowed {
		want = bytes.Clone(text)
	}
	source := &messageSource{text: want, nullContent: nullContent}
	switch {
	case m.User != nil:
		m.User.source = source
	case m.Assistant != nil:
		m.Assistant.source = source
	case m.ToolResult != nil:
		m.ToolResult.source = source
	default:
		m.source = source
	}
}

// goEscapes are the escapes encoding/json writes inside a string for characters JSON.stringify writes as they are.
var goEscapes = [...]struct{ escape, raw string }{
	{`\u003c`, "<"},
	{`\u003e`, ">"},
	{`\u0026`, "&"},
	{`\u2028`, "\u2028"},
	{`\u2029`, "\u2029"},
}

// equalUpToGoEscapes reports whether encoded, which encoding/json wrote, spells canonical, which JSON.stringify wrote, with some characters of its strings escaped as encoding/json escapes them. Then both parse to one value, so canonicalizing encoded gives canonical. Where the two first differ, both prefixes are equal valid JSON text, so canonical stands outside an escape there and a backslash in encoded starts one.
func equalUpToGoEscapes(encoded, canonical []byte) bool {
	i, j := 0, 0
	for i < len(encoded) && j < len(canonical) {
		if encoded[i] == canonical[j] {
			i++
			j++
			continue
		}
		matched := false
		for _, e := range goEscapes {
			if bytes.HasPrefix(encoded[i:], []byte(e.escape)) && bytes.HasPrefix(canonical[j:], []byte(e.raw)) {
				i += len(e.escape)
				j += len(e.raw)
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return i == len(encoded) && j == len(canonical)
}

// decode decodes data into m and reports whether its content member is null or missing.
func (m *AgentMessage) decode(data []byte) (bool, error) {
	var wire agentMessageWire
	if err := json.Unmarshal(data, &wire); err != nil && !numberOutsideIntegerField(err) {
		return false, fmt.Errorf("AgentMessage: %w", err)
	}
	nullContent := len(wire.Content) == 0 || string(wire.Content) == "null"
	if wire.Role == "" {
		return false, fmt.Errorf("AgentMessage requires a non-empty string role")
	}

	*m = AgentMessage{}
	switch wire.Role {
	case "system":
		message, err := decodeSystemMessage(data)
		if err != nil {
			return false, err
		}
		m.System = message
	case RoleUser:
		content, err := unmarshalUserContent(wire.Content)
		if err != nil {
			return false, err
		}
		m.User = &UserMessage{Role: wire.Role, Content: content, Timestamp: int64(wire.Timestamp)}
	case RoleAssistant:
		content, err := unmarshalAssistantContent(wire.Content)
		if err != nil {
			return false, err
		}
		m.Assistant = &AssistantMessage{
			Role: wire.Role, Content: content, API: wire.API, Provider: wire.Provider, ModelID: wire.Model,
			ResponseModel: wire.ResponseModel, ResponseID: wire.ResponseID,
			ProviderThinkingLevel: wire.ProviderThinkingLevel, Diagnostics: wire.Diagnostics,
			Usage: usageFromWire(wire.Usage), StopReason: wire.StopReason, Deferred: wire.Deferred,
			ErrorMessage: wire.ErrorMessage, ErrorBeforeTimestamp: ai.ErrorKeyBeforeTimestamp(data), RawStopReason: wire.RawStopReason,
			EndTurn: wire.EndTurn, DurationMs: wire.DurationMs.ptr(), Timestamp: int64(wire.Timestamp), ThinkingLevel: wire.ThinkingLevel,
		}
	case RoleToolResult:
		content, err := unmarshalToolResultContent(wire.Content)
		if err != nil {
			return false, err
		}
		m.ToolResult = &ToolResultMessage{
			Role: wire.Role, ToolCallID: wire.ToolCallID, ToolName: wire.ToolName, Content: content,
			Details: orderedjson.Value(wire.Details), Usage: usageFromWire(wire.Usage), NestedCalls: wire.NestedCalls,
			IsError: wire.IsError, DurationMs: wire.DurationMs.ptr(), Timestamp: int64(wire.Timestamp),
		}
		m.ToolResult.DetailsNull = wire.Details != nil && m.ToolResult.Details == nil
	default:
		var custom map[string]any
		if err := json.Unmarshal(data, &custom); err != nil {
			return false, err
		}
		// `details` is the opaque value an extension wrote; keep its member order.
		if _, present := custom["details"]; present {
			custom["details"] = orderedjson.Value(wire.Details)
		}
		m.Custom = custom
	}
	return nullContent, nil
}

// numberOutsideIntegerField reports that the decoder met a number an integer field cannot hold: a fraction, or a value beyond the field's range. JSON.parse keeps any number, so Pi keeps the message; the decoder fills every other field and leaves that one zero. Records of package ai decode with encoding/json.
func numberOutsideIntegerField(err error) bool {
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return numberIntoInteger(typeErr.Value, typeErr.Type)
	}
	if typeErr, ok := errors.AsType[*stdjson.UnmarshalTypeError](err); ok {
		return numberIntoInteger(typeErr.Value, typeErr.Type)
	}
	return false
}

func numberIntoInteger(value string, target reflect.Type) bool {
	if !strings.HasPrefix(value, "number") || target == nil {
		return false
	}
	switch target.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}

type agentMessageWire struct {
	Role                  string                          `json:"role"`
	Content               json.RawMessage                 `json:"content"`
	Timestamp             jsInt64                         `json:"timestamp"`
	API                   ai.API                          `json:"api"`
	Provider              string                          `json:"provider"`
	Model                 string                          `json:"model"`
	ResponseModel         string                          `json:"responseModel"`
	ResponseID            string                          `json:"responseId"`
	ProviderThinkingLevel string                          `json:"providerThinkingLevel"`
	ThinkingLevel         ai.ModelThinkingLevel           `json:"thinkingLevel"`
	Diagnostics           []ai.AssistantMessageDiagnostic `json:"diagnostics"`
	Usage                 *ai.Usage                       `json:"usage"`
	StopReason            ai.StopReason                   `json:"stopReason"`
	Deferred              *ai.DeferredHandle              `json:"deferred"`
	ErrorMessage          string                          `json:"errorMessage"`
	RawStopReason         string                          `json:"rawStopReason"`
	EndTurn               *bool                           `json:"endTurn"`
	ToolCallID            string                          `json:"toolCallId"`
	ToolName              string                          `json:"toolName"`
	Details               json.RawMessage                 `json:"details"`
	NestedCalls           *ai.NestedToolCalls             `json:"nestedCalls"`
	IsError               bool                            `json:"isError"`
	DurationMs            *jsInt64                        `json:"durationMs"`
}

// usageToWire returns the usage a message writes; ai.Usage keeps where its source created cacheWrite1h and reasoning.
func usageToWire(usage *ai.Usage) ai.Usage {
	if usage == nil {
		return ai.Usage{}
	}
	return *usage
}

func optionalUsageToWire(usage *ai.Usage) *ai.Usage {
	if usage == nil {
		return nil
	}
	wire := *usage
	return &wire
}

func usageFromWire(wire *ai.Usage) *ai.Usage {
	if wire == nil {
		return nil
	}
	usage := *wire
	return &usage
}

// jsInt64 reads an integer member the way JSON.parse reads a number: any spelling of a whole number (1.767225603e12, 1e2, 5.0) is that integer, and a
// fraction truncates toward zero, so a session message whose count or time was written that way is kept as Pi keeps it. A value outside the type's range clamps.
// Anything that is not a JSON number (a string, a boolean, an array) is an error, as for an int field.
type jsInt64 int64

func (n *jsInt64) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	if len(data) == 0 || data[0] == '"' || data[0] == 't' || data[0] == 'f' || data[0] == '[' || data[0] == '{' {
		return fmt.Errorf("cannot unmarshal %s into an integer", data)
	}
	//portlint:allow numbers the decoder hands UnmarshalJSON a valid JSON number token, which ParseFloat reads as JSON.parse does; overflow is ±Inf with ErrRange and clamps below
	f, err := strconv.ParseFloat(string(data), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return fmt.Errorf("cannot unmarshal %s into an integer: %w", data, err)
	}
	switch {
	case math.IsNaN(f):
		return fmt.Errorf("cannot unmarshal %s into an integer", data)
	case f >= math.MaxInt64:
		*n = math.MaxInt64
	case f <= math.MinInt64:
		*n = math.MinInt64
	default:
		//portlint:allow numbers NaN and values outside int64 are handled above; in range the conversion truncates toward zero as documented
		*n = jsInt64(f)
	}
	return nil
}

func (n *jsInt64) ptr() *int64 {
	if n == nil {
		return nil
	}
	value := int64(*n)
	return &value
}

func marshalAgentContent[T any](content []T) ([]any, error) {
	items := make([]any, 0, len(content))
	for i, item := range content {
		block, ok := any(item).(ai.ContentBlock)
		if !ok {
			return nil, fmt.Errorf("AgentMessage content[%d]: unsupported content block %T", i, item)
		}
		encoded, err := marshalAgentContentBlock(block)
		if err != nil {
			return nil, fmt.Errorf("AgentMessage content[%d]: %w", i, err)
		}
		items = append(items, encoded)
	}
	return items, nil
}

func marshalAgentContentBlock(block ai.ContentBlock) (any, error) {
	switch value := block.(type) {
	case ai.TextContent:
		return value, nil
	case ai.ImageContent:
		return struct {
			Type     string `json:"type"`
			Data     string `json:"data"`
			MIMEType string `json:"mimeType"`
		}{Type: "image", Data: value.Data, MIMEType: value.MimeType}, nil
	case ai.ThinkingContent:
		return value, nil
	case ai.ToolCall:
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported content block %T", block)
	}
}

func unmarshalUserContent(data []byte) (ai.UserContent, error) {
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return nil, err
		}
		return ai.UserText(text), nil
	}
	blocks, err := unmarshalAgentContent(data)
	if err != nil {
		return nil, err
	}
	content := make(ai.UserContentBlocks, len(blocks))
	for i, block := range blocks {
		value, ok := block.(ai.UserContentBlock)
		if !ok {
			return nil, fmt.Errorf("AgentMessage user content[%d]: unsupported content block %T", i, block)
		}
		content[i] = value
	}
	return content, nil
}

func unmarshalAssistantContent(data []byte) ([]ai.AssistantContentBlock, error) {
	blocks, err := unmarshalAgentContent(data)
	if err != nil {
		return nil, err
	}
	content := make([]ai.AssistantContentBlock, len(blocks))
	for i, block := range blocks {
		value, ok := block.(ai.AssistantContentBlock)
		if !ok {
			return nil, fmt.Errorf("AgentMessage assistant content[%d]: unsupported content block %T", i, block)
		}
		content[i] = value
	}
	return content, nil
}

func unmarshalToolResultContent(data []byte) ([]ai.ToolResultMessageContent, error) {
	blocks, err := unmarshalAgentContent(data)
	if err != nil {
		return nil, err
	}
	content := make([]ai.ToolResultMessageContent, len(blocks))
	for i, block := range blocks {
		value, ok := block.(ai.ToolResultMessageContent)
		if !ok {
			return nil, fmt.Errorf("AgentMessage tool result content[%d]: unsupported content block %T", i, block)
		}
		content[i] = value
	}
	return content, nil
}

func unmarshalAgentContent(data []byte) ([]ai.ContentBlock, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	if data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return nil, err
		}
		return []ai.ContentBlock{ai.TextContent{Text: text}}, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("AgentMessage content: %w", err)
	}
	content := make([]ai.ContentBlock, 0, len(raws))
	for i, raw := range raws {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("AgentMessage content[%d]: %w", i, err)
		}
		var block ai.ContentBlock
		switch probe.Type {
		case "text":
			var value ai.TextContent
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = value
		case "image":
			var value struct {
				Data     string `json:"data"`
				MIMEType string `json:"mimeType"`
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = ai.ImageContent{Data: value.Data, MimeType: value.MIMEType}
		case "thinking":
			var value ai.ThinkingContent
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = value
		case "toolCall":
			var value ai.ToolCall
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = value
		case "tool_use":
			// Persisted PiG sessions before the closed transcript contract used
			// Anthropic's provider spelling. Decode it at the persistence boundary.
			var value struct {
				ID               string        `json:"id"`
				Name             string        `json:"name"`
				Input            ai.JsonObject `json:"input"`
				ThoughtSignature string        `json:"thoughtSignature"`
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = ai.ToolCall{ID: value.ID, Name: value.Name, Arguments: value.Input, ThoughtSignature: value.ThoughtSignature}
		case "tool_result":
			// Persisted tool-result wrapper blocks are reduced to the closed
			// ToolResultMessage content union; message fields carry call identity.
			var value struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
			block = ai.TextContent{Text: value.Content}
		default:
			value, err := ai.UnmarshalContentBlock(raw)
			if err != nil {
				return nil, fmt.Errorf("AgentMessage content[%d]: %w", i, err)
			}
			block = value
		}
		content = append(content, block)
	}
	return content, nil
}
