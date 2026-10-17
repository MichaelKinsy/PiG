package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
	"unsafe"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Context is the public request shape accepted before provider normalization.
type Context struct {
	SystemPrompt string
	Messages     []Message
	Tools        []ToolSchema
	// Frozen declares that nothing modifies Messages, or anything they reference, while a request or stream made
	// from this context is live. Normalization then shares the messages instead of deep-copying them, as pi-ai's
	// normalizeContext does. TranscriptContext.Messages still returns copies.
	// pig additive (D104): pi-ai copies nothing; PiG copies unless the caller declares the messages immutable.
	Frozen bool
	// Validated lets a Frozen context skip validating a value an earlier request of the same transcript already
	// validated: it answers by the value's identity, so the caller promises that no value the memo has seen changes
	// while the memo is in use. It is ignored unless Frozen is set.
	// pig additive (D104): pi-ai validates nothing; PiG checks every tool result's details and call arguments for JSON, and a transcript that grows by one message per turn would otherwise check them all again every turn.
	Validated *ValidationMemo
	// validated is set only by TranscriptContext.FrozenContext: the messages are a normalized transcript's.
	validated bool
}

// TranscriptContext is the normalized provider-facing request. Only
// NormalizeContext and replay helpers construct one.
type TranscriptContext struct {
	messages []Message
	err      error
	// validated is set by the constructors that ran validateTranscriptContext on these messages. A transcript never
	// changes, so a later request check has nothing left to validate.
	validated bool
}

func newTranscriptContext(messages []Message) TranscriptContext {
	messages = normalizeMissingMessageContent(messages)
	context := TranscriptContext{messages: messages}
	if err := validateTranscriptContext(context); err != nil {
		return TranscriptContext{err: err}
	}
	return TranscriptContext{messages: cloneMessages(messages), validated: true}
}

// Len is the number of messages in the normalized transcript.
// pig additive (D104): reads transcript.messages without the copy Messages makes.
func (context TranscriptContext) Len() int {
	if context.err != nil {
		return 0
	}
	return len(context.messages)
}

// At returns the transcript's i-th message without copying it, as reading pi-ai's transcript.messages[i] does. The
// message shares its content with the transcript: modifying anything it references corrupts the transcript. Use Messages
// for a copy. It panics when i is out of range.
func (context TranscriptContext) At(i int) Message {
	if context.err != nil {
		panic("ai: At on a transcript that failed to normalize")
	}
	return context.messages[i]
}

// Messages returns a deep copy of the normalized transcript.
func (context TranscriptContext) Messages() []Message {
	if context.err != nil {
		return nil
	}
	return cloneMessages(context.messages)
}

// FrozenContext is a Context for a stream made from this transcript: its messages are shared, not copied, and
// normalizing it does not validate them again. The caller must not modify Messages or anything a message references.
// pig additive (D104): pi-ai's normalizeContext neither copies nor revalidates a transcript's messages.
func (context TranscriptContext) FrozenContext() Context {
	return Context{Messages: context.borrowed(), Frozen: true, validated: context.validated}
}

// borrowed returns the transcript's messages without copying them. A transcript never changes and nothing it references changes while it is live (Frozen documents the caller's half of that), so a provider that only reads the messages, or builds new slices and values from them, borrows them instead of paying for the deep copy Messages makes. The caller must not modify the slice, a message, or anything a message references; the converters' tests compare the transcript before and after every API's request build to hold them to it.
func (context TranscriptContext) borrowed() []Message {
	if context.err != nil {
		return nil
	}
	return context.messages
}

// NormalizeContext folds shorthand prompt/tools into a leading system message and normalizes missing content to empty block arrays.
func NormalizeContext(context Context) TranscriptContext {
	candidate := TranscriptContext{messages: normalizeMissingMessageContent(context.Messages)}
	if context.SystemPrompt != "" || len(context.Tools) > 0 {
		initial := SystemMessage{Content: SystemText(context.SystemPrompt), ToolsAdded: context.Tools, Timestamp: 0}
		candidate.messages = append([]Message{initial}, candidate.messages...)
	}
	// A re-wrapped transcript's messages are already validated; a prepended system message is not.
	candidate.validated = context.validated && context.SystemPrompt == "" && len(context.Tools) == 0
	var memo *ValidationMemo
	if context.Frozen {
		memo = context.Validated
	}
	if err := validateTranscriptContextWith(candidate, memo); err != nil {
		return TranscriptContext{err: err}
	}
	memo.finish()
	if context.Frozen {
		// The initial system message still holds the caller's tools, which Frozen does not cover.
		if len(candidate.messages) > 0 && (context.SystemPrompt != "" || len(context.Tools) > 0) {
			candidate.messages[0] = candidate.messages[0].cloneMessage()
		}
		candidate.validated = true
		return candidate
	}
	return TranscriptContext{messages: cloneMessages(candidate.messages), validated: true}
}

// Ports packages/ai/src/api/transform-messages.ts
// Null and omitted content share the empty-array normalization before provider conversion.
func normalizeMissingMessageContent(messages []Message) []Message {
	if !anyMissingMessageContent(messages) {
		return messages
	}
	result := append([]Message(nil), messages...)
	for index, message := range result {
		switch message := message.(type) {
		case SystemMessage:
			if message.Content == nil {
				message.Content = SystemTextBlocks{}
			}
			result[index] = message
		case UserMessage:
			if message.Content == nil {
				message.Content = UserContentBlocks{}
			}
			result[index] = message
		case AssistantMessage:
			if message.Content == nil {
				message.Content = []AssistantContentBlock{}
			}
			result[index] = message
		case ToolResultMessage:
			if message.Content == nil {
				message.Content = []ToolResultMessageContent{}
			}
			result[index] = message
		}
	}
	return result
}

// anyMissingMessageContent reports whether normalizeMissingMessageContent would change a message.
func anyMissingMessageContent(messages []Message) bool {
	for _, message := range messages {
		switch message := message.(type) {
		case SystemMessage:
			if message.Content == nil {
				return true
			}
		case UserMessage:
			if message.Content == nil {
				return true
			}
		case AssistantMessage:
			if message.Content == nil {
				return true
			}
		case ToolResultMessage:
			if message.Content == nil {
				return true
			}
		}
	}
	return false
}

func CreateInitialSystemMessage(systemPrompt string, tools []ToolSchema) *SystemMessage {
	if systemPrompt == "" && len(tools) == 0 {
		return nil
	}
	return &SystemMessage{Content: SystemText(systemPrompt), ToolsAdded: cloneTools(tools), Timestamp: 0}
}

func GetInitialSystemMessage[M RoleMessage](messages TranscriptMessages[M]) *SystemMessage {
	if len(messages) == 0 {
		return nil
	}
	message, ok := systemMessageOf(messages[0])
	if !ok {
		return nil
	}
	cloned := message.cloneMessage().(SystemMessage)
	return &cloned
}

// borrowedInitialSystemMessage is GetInitialSystemMessage without the copy of the message, for a request build that reads it: the message
// shares its tools and sections with the transcript, which does not change while the request is live (TranscriptContext.borrowed).
func borrowedInitialSystemMessage[M RoleMessage](messages TranscriptMessages[M]) *SystemMessage {
	if len(messages) == 0 {
		return nil
	}
	message, ok := systemMessageOf(messages[0])
	if !ok {
		return nil
	}
	return &message
}

func WithoutInitialSystemMessage(messages []Message) []Message {
	return withoutInitialSystemMessage(cloneMessages(messages))
}

// withoutInitialSystemMessage drops the leading system message without copying the messages that remain.
func withoutInitialSystemMessage(messages []Message) []Message {
	if len(messages) > 0 {
		if _, ok := messages[0].(SystemMessage); ok {
			messages = messages[1:]
		}
	}
	return messages
}

func GetCurrentTools[M RoleMessage](messages TranscriptMessages[M]) []ToolSchema {
	return replayTools(messages, true)
}

// borrowedCurrentTools is GetCurrentTools without the copy of every replayed tool: a request build only reads the declarations, and
// the transcript it builds from does not change while the request is live (TranscriptContext.borrowed), so the copies cost a deep
// clone of every tool schema per request and are read once.
func borrowedCurrentTools[M RoleMessage](messages TranscriptMessages[M]) []ToolSchema {
	return replayTools(messages, false)
}

// GetToolStateChangesFrom is GetToolStateChanges(GetCurrentTools(messages), current) without the copy of every replayed
// tool: the replayed declarations are only compared, never returned.
func GetToolStateChangesFrom[M RoleMessage](messages TranscriptMessages[M], current []ToolSchema) ToolStateChanges {
	return GetToolStateChanges(replayTools(messages, false), current)
}

// replayTools applies every transcript delta in order. With clone false the result shares the transcript's declarations,
// and the caller must not modify them.
func replayTools[M RoleMessage](messages TranscriptMessages[M], clone bool) []ToolSchema {
	order := []string{}
	tools := map[string]ToolSchema{}
	for _, item := range messages {
		message, ok := systemMessageOf(item)
		if !ok {
			continue
		}
		for _, removed := range message.ToolsRemoved {
			delete(tools, removed.Name)
			order = slices.DeleteFunc(order, func(name string) bool { return name == removed.Name })
		}
		for _, tool := range message.ToolsAdded {
			if _, exists := tools[tool.Name]; !exists {
				order = append(order, tool.Name)
			}
			if clone {
				tool = cloneTool(tool)
			}
			tools[tool.Name] = tool
		}
	}
	out := make([]ToolSchema, 0, len(order))
	for _, name := range order {
		if tool, exists := tools[name]; exists {
			out = append(out, tool)
		}
	}
	return out
}

func GetCurrentSystemMessage[M RoleMessage](messages TranscriptMessages[M]) *SystemMessage {
	return currentSystemMessage(messages, true)
}

// currentSystemMessage is GetCurrentSystemMessage; with cloneTools false the message's ToolsAdded share the declarations of the
// messages replayed, which the caller must not modify.
func currentSystemMessage[M RoleMessage](messages TranscriptMessages[M], cloneTools bool) *SystemMessage {
	content := []string{}
	sections := OrderedSections{}
	var timestamp int64
	hasSystem := false
	for _, item := range messages {
		message, ok := systemMessageOf(item)
		if !ok {
			continue
		}
		if !hasSystem {
			timestamp = message.Timestamp
			hasSystem = true
		}
		if text := systemContentText(message.Content); text != "" {
			content = append(content, text)
		}
		for _, section := range message.Sections {
			index := slices.IndexFunc(sections, func(current PromptSection) bool { return current.Name == section.Name })
			if section.Value == nil {
				if index >= 0 {
					sections = slices.Delete(sections, index, index+1)
				}
				continue
			}
			value := *section.Value
			if index >= 0 {
				sections[index].Value = &value
			} else {
				sections = append(sections, PromptSection{Name: section.Name, Value: &value})
			}
		}
	}
	tools := replayTools(messages, cloneTools)
	if !hasSystem && len(tools) == 0 {
		return nil
	}
	// Upstream adds sections only when a section remains (transcript.ts getCurrentSystemMessage); an empty object is a
	// different record once encoded.
	if len(sections) == 0 {
		sections = nil
	}
	// Object.fromEntries(sections) lists integer-like names first (types.ts SystemMessage.sections: "JSON objects reorder those").
	names := make([]string, len(sections))
	for i, section := range sections {
		names[i] = section.Name
	}
	ordered := make(OrderedSections, len(sections))
	for i, index := range jsObjectKeyOrder(names) {
		ordered[i] = sections[index]
	}
	if len(sections) > 0 {
		sections = ordered
	}
	return &SystemMessage{
		Content:    SystemText(strings.Join(content, "\n\n")),
		Sections:   sections,
		ToolsAdded: tools,
		Timestamp:  timestamp,
	}
}

// GetCurrentSystemPrompt renders replayed instructions and nonempty sections in order.
func GetCurrentSystemPrompt[M RoleMessage](messages TranscriptMessages[M]) string {
	message := currentSystemMessage(messages, false)
	if message == nil {
		return ""
	}
	parts := []string{}
	if text := systemContentText(message.Content); text != "" {
		parts = append(parts, text)
	}
	for _, section := range message.Sections {
		if section.Value != nil && *section.Value != "" {
			parts = append(parts, *section.Value)
		}
	}
	return strings.Join(parts, "\n\n")
}

// RenderSystemMessageUpdate joins instruction blocks with newlines and frames section changes using literal names.
func RenderSystemMessageUpdate(message SystemMessage) string {
	parts := []string{}
	if text := systemContentText(message.Content); text != "" {
		parts = append(parts, text)
	}
	for _, section := range message.Sections {
		if section.Value == nil {
			parts = append(parts, fmt.Sprintf("Removed system prompt section \"%s\".", section.Name))
		} else {
			parts = append(parts, fmt.Sprintf("Updated system prompt section \"%s\":\n\n%s", section.Name, *section.Value))
		}
	}
	return strings.Join(parts, "\n\n")
}

// CollapseSystemMessages and ResolveTranscript share the messages of the transcript they are given: nothing modifies a
// transcript's messages (Messages hands out copies, At's callers and borrowers must not modify), and every one of them is already
// normalized and validated, so only the collapsed system message is new. Providers borrow the result (collapsedMessages,
// resolvedMessages), so a request build copies no message it does not change.
// pi-ai shares the same way: resolveTranscript returns the context itself and collapseSystemMessages filters it.
// upstream: packages/ai/src/utils/transcript.ts:collapseSystemMessages
func CollapseSystemMessages(context TranscriptContext) TranscriptContext {
	// A failed transcript collapses to an empty one, as Messages reports it.
	var messages []Message
	if context.err == nil {
		messages = context.messages
	}
	head := currentSystemMessage(messages, false)
	out := make([]Message, 0, len(messages))
	if head != nil {
		// The collapsed message is built here, and its tool declarations are shared with the transcript as the other messages are.
		checked := normalizeMissingMessageContent([]Message{*head})
		if err := validateTranscriptContext(TranscriptContext{messages: checked}); err != nil {
			return TranscriptContext{err: err}
		}
		out = append(out, checked[0])
	}
	for _, message := range messages {
		if _, system := message.(SystemMessage); !system {
			out = append(out, message)
		}
	}
	return TranscriptContext{messages: out}
}

// collapsedMessages borrows the messages of CollapseSystemMessages: the system messages folded into one leading message, the
// others shared with the transcript. An invalid collapsed system message gives no messages, as the failed transcript's Messages does.
func collapsedMessages(context TranscriptContext) []Message {
	return CollapseSystemMessages(context).borrowed()
}

// resolvedMessages borrows the messages of ResolveTranscript: the transcript's own when the provider takes mid-conversation
// system messages, else the collapsed ones.
func resolvedMessages(context TranscriptContext, supportsMidConversationSystemMessages bool) []Message {
	if supportsMidConversationSystemMessages {
		return context.borrowed()
	}
	return collapsedMessages(context)
}

func ResolveTranscript(context TranscriptContext, supportsMidConversationSystemMessages bool) TranscriptContext {
	if supportsMidConversationSystemMessages {
		if context.err != nil {
			return newTranscriptContext(nil)
		}
		return context
	}
	return CollapseSystemMessages(context)
}

type ToolStateChanges struct {
	ToolsAdded   []ToolSchema
	ToolsRemoved []ToolReference
}

func ToToolDeclaration(tool ToolSchema) ToolSchema {
	tool = cloneTool(tool)
	tool.PromptGuidelines = nil
	return tool
}

// DeclarationsEqual reports whether two tools marshal to the same declaration, prompt guidelines aside. Plain trees are
// compared in place; anything else is marshaled, as before.
func DeclarationsEqual(left, right ToolSchema) bool {
	return declarationsEqualPlain(left, right) || declarationsEqualMarshaled(left, right)
}

// declarationsEqualMarshaled compares the declarations' JSON, as Pi's declarationsEqual compares JSON.stringify output.
func declarationsEqualMarshaled(left, right ToolSchema) bool {
	leftJSON, leftErr := json.Marshal(ToToolDeclaration(left))
	rightJSON, rightErr := json.Marshal(ToToolDeclaration(right))
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

// declarationsEqualPlain proves two declarations marshal alike without marshaling them. It reports false when it cannot
// prove it, which is not a statement that they differ.
func declarationsEqualPlain(left, right ToolSchema) bool {
	if left.Name != right.Name || left.Description != right.Description {
		return false
	}
	if (left.ConstrainedSampling == nil) != (right.ConstrainedSampling == nil) {
		return false
	}
	if left.ConstrainedSampling != nil {
		if !reflect.DeepEqual(left.ConstrainedSampling, right.ConstrainedSampling) {
			return false
		}
	} else if left.ConstrainedSamplingDisabled != right.ConstrainedSamplingDisabled {
		return false
	}
	if (left.parameterOrder == nil) != (right.parameterOrder == nil) || !maps.EqualFunc(left.parameterOrder, right.parameterOrder, slices.Equal[[]string]) {
		return false
	}
	return equalPlainJSON(left.Parameters, right.Parameters, &plainWalk{})
}

func GetToolStateChanges(previous, current []ToolSchema) ToolStateChanges {
	previousByName := make(map[string]ToolSchema, len(previous))
	currentByName := make(map[string]ToolSchema, len(current))
	for _, tool := range previous {
		previousByName[tool.Name] = tool
	}
	for _, tool := range current {
		currentByName[tool.Name] = tool
	}
	changes := ToolStateChanges{}
	for _, tool := range current {
		old, exists := previousByName[tool.Name]
		if !exists || !DeclarationsEqual(old, tool) {
			changes.ToolsAdded = append(changes.ToolsAdded, ToToolDeclaration(tool))
		}
	}
	for _, tool := range previous {
		updated, exists := currentByName[tool.Name]
		if !exists || !DeclarationsEqual(tool, updated) {
			changes.ToolsRemoved = append(changes.ToolsRemoved, ToolReference{Name: tool.Name})
		}
	}
	return changes
}

func GetDeclaredTools[M RoleMessage](messages TranscriptMessages[M]) []ToolSchema {
	order := []string{}
	definitions := map[string]ToolSchema{}
	for _, item := range messages {
		message, ok := systemMessageOf(item)
		if !ok {
			continue
		}
		for _, tool := range message.ToolsAdded {
			if _, exists := definitions[tool.Name]; !exists {
				order = append(order, tool.Name)
			}
			definitions[tool.Name] = cloneTool(tool)
		}
	}
	out := make([]ToolSchema, 0, len(order))
	for _, name := range order {
		out = append(out, definitions[name])
	}
	return out
}

// HasToolRedefinitions reports whether a tool name was declared twice with
// different definitions. A transport that can only reference previously
// declared tools by name cannot replay such a history.
//
// Deprecated: No built-in transport needs this anymore: Anthropic expresses
// redefinitions with inline tool_definition blocks. Pi keeps it for API
// compatibility.
func HasToolRedefinitions[M RoleMessage](messages TranscriptMessages[M]) bool {
	declared := map[string]ToolSchema{}
	for _, item := range messages {
		message, ok := systemMessageOf(item)
		if !ok {
			continue
		}
		for _, tool := range message.ToolsAdded {
			if previous, exists := declared[tool.Name]; exists && !DeclarationsEqual(previous, tool) {
				return true
			}
			declared[tool.Name] = tool
		}
	}
	return false
}

func HasNonAdditiveToolChanges[M RoleMessage](messages TranscriptMessages[M]) bool {
	declared := map[string]struct{}{}
	for _, item := range messages {
		message, ok := systemMessageOf(item)
		if !ok {
			continue
		}
		if len(message.ToolsRemoved) > 0 {
			return true
		}
		for _, tool := range message.ToolsAdded {
			if _, exists := declared[tool.Name]; exists {
				return true
			}
			declared[tool.Name] = struct{}{}
		}
	}
	return false
}

type TranscriptTools struct {
	RequestTools     []ToolSchema
	AnchorsAdditions bool
}

func ResolveTranscriptTools[M RoleMessage](messages TranscriptMessages[M], supportsToolAdditions bool) TranscriptTools {
	anchors := supportsToolAdditions && !HasNonAdditiveToolChanges(messages)
	if anchors {
		initial := GetInitialSystemMessage(messages)
		if initial == nil {
			return TranscriptTools{AnchorsAdditions: true}
		}
		return TranscriptTools{RequestTools: cloneTools(initial.ToolsAdded), AnchorsAdditions: true}
	}
	return TranscriptTools{RequestTools: GetCurrentTools(messages)}
}

func systemContentText(content SystemContent) string {
	switch content := content.(type) {
	case SystemText:
		return ContentText(content)
	case SystemTextBlocks:
		return ContentText(content)
	default:
		return ""
	}
}

func cloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	out := make([]Message, len(messages))
	for i, message := range messages {
		out[i] = message.cloneMessage()
	}
	return out
}

func cloneSections(sections OrderedSections) OrderedSections {
	if sections == nil {
		return nil
	}
	out := make(OrderedSections, len(sections))
	for i, section := range sections {
		out[i].Name = section.Name
		if section.Value != nil {
			out[i].Value = new(*section.Value)
		}
	}
	return out
}

func cloneTools(tools []ToolSchema) []ToolSchema {
	if tools == nil {
		return nil
	}
	out := make([]ToolSchema, len(tools))
	for i, tool := range tools {
		out[i] = cloneTool(tool)
	}
	return out
}

func cloneTool(tool ToolSchema) ToolSchema {
	out := tool
	if tool.Parameters != nil {
		out.Parameters = cloneJSONValue(tool.Parameters).(map[string]any)
	}
	out.PromptGuidelines = append([]string(nil), tool.PromptGuidelines...)
	if tool.ConstrainedSampling != nil {
		value := *tool.ConstrainedSampling
		if value.Variants != nil {
			value.Variants = make(map[GrammarFormat]string, len(tool.ConstrainedSampling.Variants))
			maps.Copy(value.Variants, tool.ConstrainedSampling.Variants)
		}
		out.ConstrainedSampling = &value
	}
	return out
}

func validateProviderRequest(ctx context.Context, transcript TranscriptContext) error {
	if ctx == nil {
		return fmt.Errorf("provider context is nil")
	}
	if err := validateTranscriptContext(transcript); err != nil {
		return err
	}
	checkTranscriptFlow(ctx, transcript)
	return nil
}

func validateTranscriptContext(context TranscriptContext) error {
	return validateTranscriptContextWith(context, nil)
}

// validateTranscriptContextWith skips the values memo has already validated.
func validateTranscriptContextWith(context TranscriptContext, memo *ValidationMemo) error {
	if context.err != nil {
		return context.err
	}
	if context.validated {
		return nil
	}
	for messageIndex, message := range context.messages {
		switch message := message.(type) {
		case SystemMessage:
			for toolIndex, tool := range message.ToolsAdded {
				if err := memo.validate(tool.Parameters); err != nil {
					return fmt.Errorf("message %d tool %d parameters: %w", messageIndex, toolIndex, err)
				}
			}
		case AssistantMessage:
			for contentIndex, block := range message.Content {
				if call, ok := block.(ToolCall); ok {
					if err := memo.validate(call.Arguments); err != nil {
						return fmt.Errorf("message %d content %d arguments: %w", messageIndex, contentIndex, err)
					}
				}
			}
			for diagnosticIndex, diagnostic := range message.Diagnostics {
				if err := memo.validate(diagnostic.Details); err != nil {
					return fmt.Errorf("message %d diagnostic %d details: %w", messageIndex, diagnosticIndex, err)
				}
				if diagnostic.Error != nil {
					if err := memo.validate(diagnostic.Error.Code); err != nil {
						return fmt.Errorf("message %d diagnostic %d error code: %w", messageIndex, diagnosticIndex, err)
					}
				}
			}
			if message.Deferred != nil {
				if err := memo.validate(message.Deferred.Data); err != nil {
					return fmt.Errorf("message %d deferred data: %w", messageIndex, err)
				}
			}
		case ToolResultMessage:
			if err := memo.validate(message.Details); err != nil {
				return fmt.Errorf("message %d details: %w", messageIndex, err)
			}
		}
	}
	return nil
}

func validateJsonValue(value any) error {
	if isPlainJSON(value, &plainWalk{}) || typeAlwaysMarshals(reflect.TypeOf(value)) {
		return nil
	}
	// Validation needs the marshaled bytes to be one valid JSON value, not a decoded copy: the decoder fails only where the scanner does.
	encoded, err := sdkjson.Marshal(value)
	if err != nil {
		return err
	}
	if sdkjson.Valid(encoded) {
		return nil
	}
	_, err = normalizeJSONValue(value)
	return err
}

func normalizeJSONValue(value any) (any, error) {
	// sdkjson writes a lone surrogate as its escape and reads it back as one, as JSON.stringify and JSON.parse do; encoding/json would turn it into U+FFFD.
	encoded, err := sdkjson.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := sdkjson.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// cloneJSONValue retains normalization's validation and single marshaler invocation, then restores native scalar types and nil containers without retaining mutable input objects.
func cloneJSONValue(value any) any {
	if copied, ok := clonePlainJSON(value, &plainWalk{}); ok {
		return copied
	}
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		panic(fmt.Sprintf("clone validated JSON value: %v", err))
	}
	return preserveJSONValueTypes(value, normalized)
}

// clonePublishedJSONValue is cloneJSONValue for a value that is itself a cloneJSONValue result: a plain tree copies without validating its strings again, and anything else takes cloneJSONValue.
func clonePublishedJSONValue(value any) any {
	if copied, ok := clonePlainJSON(value, &plainWalk{validatedStrings: true}); ok {
		return copied
	}
	return cloneJSONValue(value)
}

func preserveJSONValueTypes(value, normalized any) any {
	switch value := value.(type) {
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		// A custom marshaler can mutate a sibling after JSON captured it. Restore a native number's type only when its value still matches that captured value.
		if number, ok := normalized.(json.Number); ok {
			if encoded, err := json.Marshal(value); err == nil && string(encoded) == string(number) {
				return value
			}
		}
		return normalized
	case json.RawMessage:
		// Raw JSON is a value whose members keep the order it was written in, as JSON.parse keeps them; decoding it into a map would sort them. normalizeJSONValue has already proved it valid.
		if len(value) == 0 {
			return nil
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, value); err != nil {
			return normalized
		}
		return json.RawMessage(compact.Bytes())
	case JsonObject:
		return preserveJSONValueTypes(map[string]any(value), normalized)
	case map[string]any:
		if value == nil && normalized == nil {
			return value
		}
		copy, ok := normalized.(map[string]any)
		if !ok {
			return normalized
		}
		// Invalid UTF-8 keys can collide after JSON replacement. Keep the normalizer's key and last-value result rather than restoring a different original entry.
		for key := range value {
			if !utf8.ValidString(key) {
				return normalized
			}
		}
		for key, item := range copy {
			if original, exists := value[key]; exists {
				copy[key] = preserveJSONValueTypes(original, item)
			}
		}
		return copy
	case []any:
		if value == nil && normalized == nil {
			return value
		}
		copy, ok := normalized.([]any)
		if !ok {
			return normalized
		}
		for i := range min(len(value), len(copy)) {
			copy[i] = preserveJSONValueTypes(value[i], copy[i])
		}
		return copy
	default:
		return normalized
	}
}

// systemMessageOf reads a transcript entry as a system message. The replay helpers read only entries whose role is "system" (transcript.ts:35-37), so an entry of any other role, including an application-defined one, is skipped.
func systemMessageOf[M RoleMessage](entry M) (SystemMessage, bool) {
	// Boxing a message struct in an interface copies it to the heap, and a replay walks every message of the transcript on every turn: a struct entry whose role is not "system" is skipped before it is boxed. A pointer-sized entry boxes without a copy, and an interface entry (Message) is already boxed; either may be nil or hold a nil *SystemMessage, which the switch below skips, so neither calls MessageRole.
	if unsafe.Sizeof(entry) > unsafe.Sizeof(uintptr(0)) && reflect.TypeFor[M]().Kind() != reflect.Interface && entry.MessageRole() != "system" {
		return SystemMessage{}, false
	}
	switch message := any(entry).(type) {
	case SystemMessage:
		return message, true
	case *SystemMessage:
		if message != nil {
			return *message, true
		}
	case interface{ AsSystemMessage() (SystemMessage, bool) }:
		// An app message type that wraps a system message (agent.AgentMessage) says so; Pi reads its role property alone.
		return message.AsSystemMessage()
	}
	return SystemMessage{}, false
}
