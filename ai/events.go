package ai

import (
	"encoding/json"
	"fmt"
)

// AssistantMessageEvent is Pi's closed stream-event union. External providers
// can construct exported variants but cannot define additional variants.
type AssistantMessageEvent interface {
	EventType() AssistantEventType
	assistantMessageEvent()
}

type StartEvent struct {
	Partial *AssistantMessage `json:"partial"`
}

func (StartEvent) EventType() AssistantEventType { return EventStart }
func (StartEvent) assistantMessageEvent()        {}
func (event StartEvent) MarshalJSON() ([]byte, error) {
	type plain StartEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type TextStartEvent struct {
	ContentIndex int               `json:"contentIndex"`
	Partial      *AssistantMessage `json:"partial"`
}

func (TextStartEvent) EventType() AssistantEventType { return EventTextStart }
func (TextStartEvent) assistantMessageEvent()        {}
func (event TextStartEvent) MarshalJSON() ([]byte, error) {
	type plain TextStartEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type TextDeltaEvent struct {
	ContentIndex int               `json:"contentIndex"`
	Delta        string            `json:"delta"`
	Partial      *AssistantMessage `json:"partial"`
}

func (TextDeltaEvent) EventType() AssistantEventType { return EventTextDelta }
func (TextDeltaEvent) assistantMessageEvent()        {}
func (event TextDeltaEvent) MarshalJSON() ([]byte, error) {
	type plain TextDeltaEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type TextEndEvent struct {
	ContentIndex int               `json:"contentIndex"`
	Content      string            `json:"content"`
	Partial      *AssistantMessage `json:"partial"`
}

func (TextEndEvent) EventType() AssistantEventType { return EventTextEnd }
func (TextEndEvent) assistantMessageEvent()        {}
func (event TextEndEvent) MarshalJSON() ([]byte, error) {
	type plain TextEndEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type ThinkingStartEvent struct {
	ContentIndex int               `json:"contentIndex"`
	Partial      *AssistantMessage `json:"partial"`
}

func (ThinkingStartEvent) EventType() AssistantEventType { return EventThinkingStart }
func (ThinkingStartEvent) assistantMessageEvent()        {}
func (event ThinkingStartEvent) MarshalJSON() ([]byte, error) {
	type plain ThinkingStartEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type ThinkingDeltaEvent struct {
	ContentIndex int               `json:"contentIndex"`
	Delta        string            `json:"delta"`
	Partial      *AssistantMessage `json:"partial"`
}

func (ThinkingDeltaEvent) EventType() AssistantEventType { return EventThinkingDelta }
func (ThinkingDeltaEvent) assistantMessageEvent()        {}
func (event ThinkingDeltaEvent) MarshalJSON() ([]byte, error) {
	type plain ThinkingDeltaEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type ThinkingEndEvent struct {
	ContentIndex int               `json:"contentIndex"`
	Content      string            `json:"content"`
	Partial      *AssistantMessage `json:"partial"`
}

func (ThinkingEndEvent) EventType() AssistantEventType { return EventThinkingEnd }
func (ThinkingEndEvent) assistantMessageEvent()        {}
func (event ThinkingEndEvent) MarshalJSON() ([]byte, error) {
	type plain ThinkingEndEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type ToolCallStartEvent struct {
	ContentIndex int               `json:"contentIndex"`
	Partial      *AssistantMessage `json:"partial"`
}

func (ToolCallStartEvent) EventType() AssistantEventType { return EventToolCallStart }
func (ToolCallStartEvent) assistantMessageEvent()        {}
func (event ToolCallStartEvent) MarshalJSON() ([]byte, error) {
	type plain ToolCallStartEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type ToolCallDeltaEvent struct {
	ContentIndex int               `json:"contentIndex"`
	Delta        string            `json:"delta"`
	Partial      *AssistantMessage `json:"partial"`
}

func (ToolCallDeltaEvent) EventType() AssistantEventType { return EventToolCallDelta }
func (ToolCallDeltaEvent) assistantMessageEvent()        {}
func (event ToolCallDeltaEvent) MarshalJSON() ([]byte, error) {
	type plain ToolCallDeltaEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type ToolCallEndEvent struct {
	ContentIndex int               `json:"contentIndex"`
	ToolCall     ToolCall          `json:"toolCall"`
	Partial      *AssistantMessage `json:"partial"`
}

func (ToolCallEndEvent) EventType() AssistantEventType { return EventToolCallEnd }
func (ToolCallEndEvent) assistantMessageEvent()        {}
func (event ToolCallEndEvent) MarshalJSON() ([]byte, error) {
	type plain ToolCallEndEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type DoneEvent struct {
	Reason  StopReason        `json:"reason"`
	Message *AssistantMessage `json:"message"`
}

func (DoneEvent) EventType() AssistantEventType { return EventDone }
func (DoneEvent) assistantMessageEvent()        {}
func (event DoneEvent) MarshalJSON() ([]byte, error) {
	type plain DoneEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

type ErrorEvent struct {
	Reason StopReason        `json:"reason"`
	Error  *AssistantMessage `json:"error"`
}

func (ErrorEvent) EventType() AssistantEventType { return EventError }
func (ErrorEvent) assistantMessageEvent()        {}
func (event ErrorEvent) MarshalJSON() ([]byte, error) {
	type plain ErrorEvent
	return marshalAssistantEvent(event.EventType(), plain(event))
}

func marshalAssistantEvent(eventType AssistantEventType, event any) ([]byte, error) {
	body, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	fields["type"] = json.RawMessage(fmt.Sprintf("%q", eventType))
	return json.Marshal(fields)
}
