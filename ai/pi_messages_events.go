package ai

// Converts serialized pi-messages backend events into assistant-message
// events. Mirrors createEventConverter in upstream api/pi-messages.ts.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsjson"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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

// PiMessagesEventType is the discriminator of the PiMessagesEvent union.
type PiMessagesEventType string

// The event types a pi-messages backend sends (pi-messages.ts PiMessagesEvent).
const (
	PiMessagesEventStart         PiMessagesEventType = "start"
	PiMessagesEventTextStart     PiMessagesEventType = "text_start"
	PiMessagesEventTextDelta     PiMessagesEventType = "text_delta"
	PiMessagesEventTextEnd       PiMessagesEventType = "text_end"
	PiMessagesEventThinkingStart PiMessagesEventType = "thinking_start"
	PiMessagesEventThinkingDelta PiMessagesEventType = "thinking_delta"
	PiMessagesEventThinkingEnd   PiMessagesEventType = "thinking_end"
	PiMessagesEventToolcallStart PiMessagesEventType = "toolcall_start"
	PiMessagesEventToolcallDelta PiMessagesEventType = "toolcall_delta"
	PiMessagesEventToolcallEnd   PiMessagesEventType = "toolcall_end"
	PiMessagesEventDone          PiMessagesEventType = "done"
	PiMessagesEventError         PiMessagesEventType = "error"
)

// PiMessagesEvent is one serialized assistant-message event sent by a
// pi-messages backend. Type selects which fields apply.
type PiMessagesEvent struct {
	Type                  PiMessagesEventType `json:"type"`
	ContentIndex          int                 `json:"contentIndex"`
	Delta                 string              `json:"delta"`
	Content               string              `json:"content"`
	ContentSignature      string              `json:"contentSignature"`
	Redacted              bool                `json:"redacted"`
	ID                    string              `json:"id"`
	ToolName              string              `json:"toolName"`
	ToolCall              *ToolCall           `json:"toolCall"`
	Reason                StopReason          `json:"reason"`
	Usage                 Usage               `json:"usage"`
	ErrorMessage          string              `json:"errorMessage"`
	ResponseID            string              `json:"responseId"`
	ResponseModel         *string             `json:"responseModel"`
	ProviderThinkingLevel *string             `json:"providerThinkingLevel"`
	Rewrite               map[string]any      `json:"rewrite"`
	// rewriteOrder is the rewrite object's wire text, whose member order the diagnostic keeps (`{ ...rewrite }`).
	rewriteOrder json.RawMessage
}

type piMessagesEventConverter struct {
	partial *AssistantMessage
	// toolArguments reads the argument deltas of each open tool call, off the content block.
	toolArguments map[int]*ToolCallArgumentsStream
	// streamed appends deltas to the open blocks without copying their earlier bytes again.
	streamed streamedBlocks
	// observe is the bound OnProviderStreamEvent callback, or nil.
	observe func(data any) error
	// replaced records the nested objects the last converted event assigned to partial. Shallow copies taken earlier keep the objects they retained.
	replaced assistantMessageReplacements
}

func newPiMessagesEventConverter(providerID, modelID string) *piMessagesEventConverter {
	return &piMessagesEventConverter{
		partial: &AssistantMessage{
			Content: []AssistantContentBlock{}, API: APIPiMessages, Provider: providerID, Model: modelID,
			StopReason: StopReasonPending, Timestamp: time.Now().UnixMilli(),
		},
		toolArguments: map[int]*ToolCallArgumentsStream{},
	}
}

// convert returns the event to push for one backend event: createEventConverter's returned function (pi-messages.ts:190-284).
func (c *piMessagesEventConverter) convert(raw json.RawMessage) ([]AssistantMessageEvent, error) {
	// readPiMessagesEvents drops a falsy event (pi-messages.ts:290-292), and a truthy number, string, boolean or array has no `type` member, so
	// createEventConverter returns `{ ...event, partial }` for it like any other unknown event (:193-272).
	if len(raw) > 0 && raw[0] != '{' {
		return nil, nil
	}
	var wire piMessagesWireEvent
	if err := json.Unmarshal(jsjson.NormalizeIntegers(raw), &wire); err != nil {
		return nil, err
	}
	event := wire.PiMessagesEvent
	if wire.ContentIndex.present {
		if err := c.checkContentIndex(raw, event.Type, wire.ContentIndex); err != nil {
			return nil, err
		}
		event.ContentIndex = wire.ContentIndex.index
	}
	if event.Rewrite != nil {
		if parsed, err := orderedjson.Parse(raw); err == nil {
			event.rewriteOrder, _ = parsed.Get("rewrite")
		}
	}
	if event.Type == PiMessagesEventStart {
		return []AssistantMessageEvent{StartEvent{Partial: c.partial}}, nil
	}
	converted, err := c.apply(event)
	if err != nil || converted == nil {
		return nil, err
	}
	switch end := converted.(type) {
	case TextEndEvent:
		forwarded, err := forwardedEndFields(raw)
		if err != nil {
			return nil, err
		}
		end.contentSignature = forwarded.ContentSignature
		converted = end
	case ThinkingEndEvent:
		forwarded, err := forwardedEndFields(raw)
		if err != nil {
			return nil, err
		}
		end.contentSignature, end.redacted = forwarded.ContentSignature, forwarded.Redacted
		converted = end
	}
	return []AssistantMessageEvent{converted}, nil
}

// piMessagesEndFields are the members of a text or thinking end event that are forwarded when present, as opposed to defaulted when absent.
type piMessagesEndFields struct {
	ContentSignature *string `json:"contentSignature"`
	Redacted         *bool   `json:"redacted"`
}

// forwardedEndFields reads them from the event's JSON. Only an end event needs them: the event decode above already rejects a member of the wrong type for any event.
func forwardedEndFields(raw json.RawMessage) (piMessagesEndFields, error) {
	var forwarded piMessagesEndFields
	err := json.Unmarshal(raw, &forwarded)
	return forwarded, err
}

func (c *piMessagesEventConverter) apply(event PiMessagesEvent) (AssistantMessageEvent, error) {
	switch event.Type {
	case PiMessagesEventDone:
		c.finish(event)
		return DoneEvent{Reason: event.Reason, Message: c.partial}, nil
	case PiMessagesEventError:
		c.finish(event)
		c.partial.ErrorMessage = event.ErrorMessage
		return ErrorEvent{Reason: event.Reason, Error: c.partial}, nil
	case PiMessagesEventTextStart, PiMessagesEventThinkingStart, PiMessagesEventToolcallStart:
		return c.startBlock(event)
	case PiMessagesEventTextDelta, PiMessagesEventThinkingDelta, PiMessagesEventToolcallDelta:
		return c.deltaBlock(event)
	case PiMessagesEventTextEnd, PiMessagesEventThinkingEnd, PiMessagesEventToolcallEnd:
		return c.endBlock(event)
	}
	// upstream: pi-messages.ts createEventConverter returns `{ ...event, partial }` for any other type; the stream carries it and every consumer's switch ignores it, so it changes nothing here.
	return nil, nil
}

// piMessagesWireEvent is a backend event as it arrives: the contentIndex is read for what it is, not as the integer the event struct holds.
type piMessagesWireEvent struct {
	PiMessagesEvent
	ContentIndex piMessagesContentIndex `json:"contentIndex"`
}

// piMessagesContentIndex is an event's contentIndex member: present when the member is there, and index with valid set only when it is an integer.
type piMessagesContentIndex struct {
	present bool
	valid   bool
	index   int
}

func (field *piMessagesContentIndex) UnmarshalJSON(data []byte) error {
	field.present = true
	if index, err := strconv.ParseInt(string(data), 10, strconv.IntSize); err == nil {
		field.index, field.valid = int(index), true
	}
	return nil
}

// checkContentIndex is the check at the top of createEventConverter's returned function (pi-messages.ts:198-205): the index comes from the wire and keys the
// content array, so an event that carries one must name the next block (a start, which adds it) or an existing one (any other event).
func (c *piMessagesEventConverter) checkContentIndex(raw json.RawMessage, eventType PiMessagesEventType, field piMessagesContentIndex) error {
	limit := len(c.partial.Content) - 1
	if strings.HasSuffix(string(eventType), "_start") {
		limit++
	}
	if field.valid && field.index >= 0 && field.index <= limit {
		return nil
	}
	var member struct {
		ContentIndex json.RawMessage `json:"contentIndex"`
	}
	_ = json.Unmarshal(raw, &member)
	text, err := jsonstringify.Canonicalize(member.ContentIndex)
	if err != nil {
		text = member.ContentIndex
	}
	return fmt.Errorf("Invalid contentIndex %s in pi-messages %s event", text, eventType)
}

func (c *piMessagesEventConverter) finish(event PiMessagesEvent) {
	c.partial.StopReason = event.Reason
	// upstream: packages/ai/src/api/pi-messages.ts:195-217 `Object.assign(partial, { usage: event.usage })` assigns a new usage object.
	c.partial.Usage = event.Usage
	c.replaced.Usage = true
	c.partial.ResponseID = event.ResponseID
	// upstream: pi-messages.ts createEventConverter assigns responseModel on done and error only when the event carries it (Pi 14c811841).
	if event.ResponseModel != nil {
		c.partial.ResponseModel = *event.ResponseModel
	}
	if event.ProviderThinkingLevel != nil {
		c.partial.ProviderThinkingLevel = *event.ProviderThinkingLevel
	}
	if event.Rewrite != nil {
		AppendAssistantMessageDiagnostic(c.partial, AssistantMessageDiagnostic{
			Type: "pi_messages_rewrite", Timestamp: time.Now().UnixMilli(), Details: event.Rewrite, detailsOrder: event.rewriteOrder,
		})
		c.replaced.Diagnostics = true
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
	case PiMessagesEventTextStart:
		block, result = TextContent{}, TextStartEvent{ContentIndex: index, Partial: c.partial}
	case PiMessagesEventThinkingStart:
		block, result = ThinkingContent{}, ThinkingStartEvent{ContentIndex: index, Partial: c.partial}
	default:
		block, result = ToolCall{ID: event.ID, Name: event.ToolName, Arguments: JsonObject{}}, ToolCallStartEvent{ContentIndex: index, Partial: c.partial}
		c.toolArguments[index] = NewToolCallArgumentsStream()
		c.streamed.release(index)
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
		block.Text = c.streamed.appendContent(index, block.Text, event.Delta)
		c.partial.Content[index] = block
		return TextDeltaEvent{ContentIndex: index, Delta: event.Delta, Partial: c.partial}, nil
	case ThinkingContent:
		block.Thinking = c.streamed.appendContent(index, block.Thinking, event.Delta)
		c.partial.Content[index] = block
		return ThinkingDeltaEvent{ContentIndex: index, Delta: event.Delta, Partial: c.partial}, nil
	case ToolCall:
		if arguments := c.toolArguments[index]; arguments != nil {
			parsed, order := arguments.Append(event.Delta)
			block.setParsedArguments(parsed, order, nil)
			c.partial.Content[index] = block
		}
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
		c.streamed.release(index)
		return TextEndEvent{ContentIndex: index, Content: event.Content, Partial: c.partial}, nil
	case ThinkingContent:
		c.partial.Content[index] = ThinkingContent{Thinking: event.Content, ThinkingSignature: event.ContentSignature, Redacted: event.Redacted}
		c.streamed.release(index)
		return ThinkingEndEvent{ContentIndex: index, Content: event.Content, Partial: c.partial}, nil
	case ToolCall:
		if event.ToolCall != nil {
			block = mergeToolCall(block, *event.ToolCall)
		}
		c.partial.Content[index] = block
		delete(c.toolArguments, index)
		c.streamed.release(index)
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
	if incoming.Arguments != nil || incoming.nonObjectArguments != nil {
		existing.setParsedArguments(incoming.Arguments, incoming.argumentOrder, incoming.nonObjectArguments)
	}
	if incoming.ThoughtSignature != "" {
		existing.ThoughtSignature = incoming.ThoughtSignature
	}
	if incoming.Namespace != "" {
		existing.Namespace = incoming.Namespace
	}
	if incoming.ArgumentsError != "" {
		existing.ArgumentsError = incoming.ArgumentsError
	}
	return existing
}
