package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// Context is the public request shape accepted before provider normalization.
type Context struct {
	SystemPrompt string
	Messages     []Message
	Tools        []ToolSchema
}

// TranscriptContext is the normalized provider-facing request. Only
// NormalizeContext and replay helpers construct one.
type TranscriptContext struct {
	messages []Message
	err      error
}

func newTranscriptContext(messages []Message) TranscriptContext {
	messages = normalizeMissingMessageContent(messages)
	context := TranscriptContext{messages: messages}
	if err := validateTranscriptContext(context); err != nil {
		return TranscriptContext{err: err}
	}
	return TranscriptContext{messages: cloneMessages(messages)}
}

// Messages returns a deep copy of the normalized transcript.
func (context TranscriptContext) Messages() []Message {
	if context.err != nil {
		return nil
	}
	return cloneMessages(context.messages)
}

// NormalizeContext folds shorthand prompt/tools into a leading system message and normalizes missing content to empty block arrays.
func NormalizeContext(context Context) TranscriptContext {
	candidate := TranscriptContext{messages: normalizeMissingMessageContent(context.Messages)}
	if context.SystemPrompt != "" || len(context.Tools) > 0 {
		initial := SystemMessage{Content: SystemText(context.SystemPrompt), ToolsAdded: context.Tools, Timestamp: 0}
		candidate.messages = append([]Message{initial}, candidate.messages...)
	}
	if err := validateTranscriptContext(candidate); err != nil {
		return TranscriptContext{err: err}
	}
	return TranscriptContext{messages: cloneMessages(candidate.messages)}
}

// Ports packages/ai/src/api/transform-messages.ts
// Null and omitted content share the empty-array normalization before provider conversion.
func normalizeMissingMessageContent(messages []Message) []Message {
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

func CreateInitialSystemMessage(systemPrompt string, tools []ToolSchema) *SystemMessage {
	if systemPrompt == "" && len(tools) == 0 {
		return nil
	}
	return &SystemMessage{Content: SystemText(systemPrompt), ToolsAdded: cloneTools(tools), Timestamp: 0}
}

func GetInitialSystemMessage(messages []Message) *SystemMessage {
	if len(messages) == 0 {
		return nil
	}
	message, ok := messages[0].(SystemMessage)
	if !ok {
		return nil
	}
	cloned := message.cloneMessage().(SystemMessage)
	return &cloned
}

func WithoutInitialSystemMessage(messages []Message) []Message {
	messages = cloneMessages(messages)
	if len(messages) > 0 {
		if _, ok := messages[0].(SystemMessage); ok {
			messages = messages[1:]
		}
	}
	return messages
}

func GetCurrentTools(messages []Message) []ToolSchema {
	order := []string{}
	tools := map[string]ToolSchema{}
	for _, item := range messages {
		message, ok := item.(SystemMessage)
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
			tools[tool.Name] = cloneTool(tool)
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

func GetCurrentSystemMessage(messages []Message) *SystemMessage {
	content := []string{}
	sections := OrderedSections{}
	var timestamp int64
	hasSystem := false
	for _, item := range messages {
		message, ok := item.(SystemMessage)
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
	tools := GetCurrentTools(messages)
	if !hasSystem && len(tools) == 0 {
		return nil
	}
	return &SystemMessage{
		Content:    SystemText(strings.Join(content, "\n\n")),
		Sections:   sections,
		ToolsAdded: tools,
		Timestamp:  timestamp,
	}
}

// GetCurrentSystemPrompt renders replayed instructions and nonempty sections in order.
func GetCurrentSystemPrompt(messages []Message) string {
	message := GetCurrentSystemMessage(messages)
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

func CollapseSystemMessages(context TranscriptContext) TranscriptContext {
	messages := context.Messages()
	head := GetCurrentSystemMessage(messages)
	out := make([]Message, 0, len(messages))
	if head != nil {
		out = append(out, *head)
	}
	for _, message := range messages {
		if _, system := message.(SystemMessage); !system {
			out = append(out, message)
		}
	}
	return newTranscriptContext(out)
}

func ResolveTranscript(context TranscriptContext, supportsMidConversationSystemMessages bool) TranscriptContext {
	if supportsMidConversationSystemMessages {
		return newTranscriptContext(context.Messages())
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

func DeclarationsEqual(left, right ToolSchema) bool {
	leftJSON, leftErr := json.Marshal(ToToolDeclaration(left))
	rightJSON, rightErr := json.Marshal(ToToolDeclaration(right))
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
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

func GetDeclaredTools(messages []Message) []ToolSchema {
	order := []string{}
	definitions := map[string]ToolSchema{}
	for _, item := range messages {
		message, ok := item.(SystemMessage)
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
func HasToolRedefinitions(messages []Message) bool {
	declared := map[string]ToolSchema{}
	for _, item := range messages {
		message, ok := item.(SystemMessage)
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

func HasNonAdditiveToolChanges(messages []Message) bool {
	declared := map[string]struct{}{}
	for _, item := range messages {
		message, ok := item.(SystemMessage)
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

func ResolveTranscriptTools(messages []Message, supportsToolAdditions bool) TranscriptTools {
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
	return validateTranscriptContext(transcript)
}

func validateTranscriptContext(context TranscriptContext) error {
	if context.err != nil {
		return context.err
	}
	for messageIndex, message := range context.messages {
		switch message := message.(type) {
		case SystemMessage:
			for toolIndex, tool := range message.ToolsAdded {
				if err := validateJsonValue(tool.Parameters); err != nil {
					return fmt.Errorf("message %d tool %d parameters: %w", messageIndex, toolIndex, err)
				}
			}
		case AssistantMessage:
			for contentIndex, block := range message.Content {
				if call, ok := block.(ToolCall); ok {
					if err := validateJsonValue(call.Arguments); err != nil {
						return fmt.Errorf("message %d content %d arguments: %w", messageIndex, contentIndex, err)
					}
				}
			}
			for diagnosticIndex, diagnostic := range message.Diagnostics {
				if err := validateJsonValue(diagnostic.Details); err != nil {
					return fmt.Errorf("message %d diagnostic %d details: %w", messageIndex, diagnosticIndex, err)
				}
				if diagnostic.Error != nil {
					if err := validateJsonValue(diagnostic.Error.Code); err != nil {
						return fmt.Errorf("message %d diagnostic %d error code: %w", messageIndex, diagnosticIndex, err)
					}
				}
			}
			if message.Deferred != nil {
				if err := validateJsonValue(message.Deferred.Data); err != nil {
					return fmt.Errorf("message %d deferred data: %w", messageIndex, err)
				}
			}
		case ToolResultMessage:
			if err := validateJsonValue(message.Details); err != nil {
				return fmt.Errorf("message %d details: %w", messageIndex, err)
			}
		}
	}
	return nil
}

func validateJsonValue(value any) error {
	_, err := normalizeJSONValue(value)
	return err
}

func normalizeJSONValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// cloneJSONValue retains normalization's validation and single marshaler invocation, then restores native scalar types and nil containers without retaining mutable input objects.
func cloneJSONValue(value any) any {
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		panic(fmt.Sprintf("clone validated JSON value: %v", err))
	}
	return preserveJSONValueTypes(value, normalized)
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
