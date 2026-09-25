package ai

// Converts serialized pi-messages backend events into assistant-message
// events. Mirrors createEventConverter in upstream api/pi-messages.ts.

import (
	"encoding/json"
	"fmt"
	"time"
)

// PiMessagesRewriteImpact summarizes a server-side message rewrite (for
// example a gateway policy). It is recorded as a pi_messages_rewrite diagnostic.
type PiMessagesRewriteImpact struct {
	PolicyID            string  `json:"policyId"`
	PolicyVersion       float64 `json:"policyVersion"`
	Changed             bool    `json:"changed"`
	TokenCountChange    float64 `json:"tokenCountChange"`
	MessageCountChange  float64 `json:"messageCountChange"`
	SystemPromptChanged bool    `json:"systemPromptChanged"`
}

// PiMessagesEvent is one serialized assistant-message event sent by a
// pi-messages backend. Type selects which fields apply.
type PiMessagesEvent struct {
	Type                  string         `json:"type"`
	ContentIndex          int            `json:"contentIndex"`
	Delta                 string         `json:"delta"`
	Content               string         `json:"content"`
	ContentSignature      string         `json:"contentSignature"`
	Redacted              bool           `json:"redacted"`
	ID                    string         `json:"id"`
	ToolName              string         `json:"toolName"`
	ToolCall              *ToolCall      `json:"toolCall"`
	Reason                StopReason     `json:"reason"`
	Usage                 Usage          `json:"usage"`
	ErrorMessage          string         `json:"errorMessage"`
	ResponseID            string         `json:"responseId"`
	ProviderThinkingLevel *string        `json:"providerThinkingLevel"`
	Rewrite               map[string]any `json:"rewrite"`
}

type piMessagesEventConverter struct {
	partial  *AssistantMessage
	toolJSON map[int]string
	started  bool
}

func newPiMessagesEventConverter(providerID, modelID string) *piMessagesEventConverter {
	return &piMessagesEventConverter{
		partial: &AssistantMessage{
			Content: []AssistantContentBlock{}, API: APIPiMessages, Provider: providerID, Model: modelID,
			StopReason: StopReasonPending, Timestamp: time.Now().UnixMilli(),
		},
		toolJSON: map[int]string{},
	}
}

// convert returns the events to push for one backend event. A backend that
// omits "start" gets one before its first non-terminal-error event, because
// PiG's event stream requires start before partial updates and done.
func (c *piMessagesEventConverter) convert(raw json.RawMessage) ([]AssistantMessageEvent, error) {
	var event PiMessagesEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	var events []AssistantMessageEvent
	if !c.started && event.Type != "error" {
		c.started = true
		events = append(events, StartEvent{Partial: c.partial})
	}
	if event.Type == "start" {
		return events, nil
	}
	converted, err := c.apply(event)
	if err != nil {
		return events, err
	}
	return append(events, converted), nil
}

func (c *piMessagesEventConverter) apply(event PiMessagesEvent) (AssistantMessageEvent, error) {
	switch event.Type {
	case "done":
		c.finish(event)
		return DoneEvent{Reason: event.Reason, Message: c.partial}, nil
	case "error":
		c.finish(event)
		c.partial.ErrorMessage = event.ErrorMessage
		return ErrorEvent{Reason: event.Reason, Error: c.partial}, nil
	case "text_start", "thinking_start", "toolcall_start":
		return c.startBlock(event)
	case "text_delta", "thinking_delta", "toolcall_delta":
		return c.deltaBlock(event)
	case "text_end", "thinking_end", "toolcall_end":
		return c.endBlock(event)
	}
	return nil, fmt.Errorf("unknown pi-messages event type %q", event.Type)
}

func (c *piMessagesEventConverter) finish(event PiMessagesEvent) {
	c.partial.StopReason = event.Reason
	c.partial.Usage = event.Usage
	c.partial.ResponseID = event.ResponseID
	if event.ProviderThinkingLevel != nil {
		c.partial.ProviderThinkingLevel = *event.ProviderThinkingLevel
	}
	if event.Rewrite != nil {
		c.partial.Diagnostics = append(c.partial.Diagnostics, AssistantMessageDiagnostic{
			Type: "pi_messages_rewrite", Timestamp: time.Now().UnixMilli(), Details: event.Rewrite,
		})
	}
}

func (c *piMessagesEventConverter) startBlock(event PiMessagesEvent) (AssistantMessageEvent, error) {
	index := event.ContentIndex
	if index < 0 || index > len(c.partial.Content) {
		return nil, fmt.Errorf("pi-messages %s has invalid contentIndex %d", event.Type, index)
	}
	var block AssistantContentBlock
	var result AssistantMessageEvent
	switch event.Type {
	case "text_start":
		block, result = TextContent{}, TextStartEvent{ContentIndex: index, Partial: c.partial}
	case "thinking_start":
		block, result = ThinkingContent{}, ThinkingStartEvent{ContentIndex: index, Partial: c.partial}
	default:
		block, result = ToolCall{ID: event.ID, Name: event.ToolName, Arguments: JsonObject{}}, ToolCallStartEvent{ContentIndex: index, Partial: c.partial}
		c.toolJSON[index] = ""
	}
	if index == len(c.partial.Content) {
		c.partial.Content = append(c.partial.Content, block)
	} else {
		c.partial.Content[index] = block
	}
	return result, nil
}

func (c *piMessagesEventConverter) block(event PiMessagesEvent) (AssistantContentBlock, error) {
	if event.ContentIndex < 0 || event.ContentIndex >= len(c.partial.Content) {
		return nil, fmt.Errorf("pi-messages %s has invalid contentIndex %d", event.Type, event.ContentIndex)
	}
	return c.partial.Content[event.ContentIndex], nil
}

func (c *piMessagesEventConverter) deltaBlock(event PiMessagesEvent) (AssistantMessageEvent, error) {
	current, err := c.block(event)
	if err != nil {
		return nil, err
	}
	index := event.ContentIndex
	switch block := current.(type) {
	case TextContent:
		block.Text += event.Delta
		c.partial.Content[index] = block
		return TextDeltaEvent{ContentIndex: index, Delta: event.Delta, Partial: c.partial}, nil
	case ThinkingContent:
		block.Thinking += event.Delta
		c.partial.Content[index] = block
		return ThinkingDeltaEvent{ContentIndex: index, Delta: event.Delta, Partial: c.partial}, nil
	case ToolCall:
		c.toolJSON[index] += event.Delta
		block.Arguments = parseStreamingJsonObject(c.toolJSON[index])
		c.partial.Content[index] = block
		return ToolCallDeltaEvent{ContentIndex: index, Delta: event.Delta, Partial: c.partial}, nil
	}
	return nil, fmt.Errorf("pi-messages %s does not match content block %d", event.Type, index)
}

func (c *piMessagesEventConverter) endBlock(event PiMessagesEvent) (AssistantMessageEvent, error) {
	current, err := c.block(event)
	if err != nil {
		return nil, err
	}
	index := event.ContentIndex
	switch block := current.(type) {
	case TextContent:
		c.partial.Content[index] = TextContent{Text: event.Content, TextSignature: event.ContentSignature}
		return TextEndEvent{ContentIndex: index, Content: event.Content, Partial: c.partial}, nil
	case ThinkingContent:
		c.partial.Content[index] = ThinkingContent{Thinking: event.Content, ThinkingSignature: event.ContentSignature, Redacted: event.Redacted}
		return ThinkingEndEvent{ContentIndex: index, Content: event.Content, Partial: c.partial}, nil
	case ToolCall:
		if event.ToolCall != nil {
			block = mergeToolCall(block, *event.ToolCall)
		}
		c.partial.Content[index] = block
		delete(c.toolJSON, index)
		return ToolCallEndEvent{ContentIndex: index, ToolCall: block, Partial: c.partial}, nil
	}
	return nil, fmt.Errorf("pi-messages %s does not match content block %d", event.Type, index)
}

// mergeToolCall mirrors Object.assign(existing, incoming) for the fields a
// serialized tool call carries.
func mergeToolCall(existing, incoming ToolCall) ToolCall {
	if incoming.ID != "" {
		existing.ID = incoming.ID
	}
	if incoming.Name != "" {
		existing.Name = incoming.Name
	}
	if incoming.Arguments != nil {
		existing.Arguments = incoming.Arguments
	}
	if incoming.ThoughtSignature != "" {
		existing.ThoughtSignature = incoming.ThoughtSignature
	}
	if incoming.Namespace != "" {
		existing.Namespace = incoming.Namespace
	}
	return existing
}
