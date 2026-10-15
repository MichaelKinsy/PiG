package ai

// mapAssistantEventPartial returns event with its partial replaced by mapMessage's result. An event whose partial maps to itself is returned as it is, without a copy.
func mapAssistantEventPartial(event AssistantMessageEvent, mapMessage func(*AssistantMessage) *AssistantMessage) AssistantMessageEvent {
	switch typed := event.(type) {
	case StartEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	case TextStartEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	case TextDeltaEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	case TextEndEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	case ThinkingStartEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	case ThinkingDeltaEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	case ThinkingEndEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	case ToolCallStartEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	case ToolCallDeltaEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	case ToolCallEndEvent:
		if partial := mapMessage(typed.Partial); partial != typed.Partial {
			typed.Partial = partial
			return typed
		}
	}
	return event
}
