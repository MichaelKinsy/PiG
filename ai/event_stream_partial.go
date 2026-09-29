package ai

func mapAssistantEventPartial(event AssistantMessageEvent, mapMessage func(*AssistantMessage) *AssistantMessage) AssistantMessageEvent {
	switch event := event.(type) {
	case StartEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	case TextStartEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	case TextDeltaEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	case TextEndEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	case ThinkingStartEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	case ThinkingDeltaEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	case ThinkingEndEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	case ToolCallStartEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	case ToolCallDeltaEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	case ToolCallEndEvent:
		event.Partial = mapMessage(event.Partial)
		return event
	default:
		return event
	}
}
