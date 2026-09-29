package ai

import "sync"

// assistantMessageCell publishes producer-owned state without sharing mutable construction data with readers.
// Upstream queues the same partial reference (packages/ai/src/utils/event-stream.ts:44-91).
type assistantMessageCell struct {
	mu      sync.RWMutex
	current AssistantMessage
	nested  assistantMessageReferences
	full    *AssistantMessage
}

// These pointers identify nested objects; only their latest immutable values are retained. The cell mutex guards every dereference.
type assistantMessageReferences struct {
	content     *[]AssistantContentBlock
	usage       *Usage
	diagnostics *[]AssistantMessageDiagnostic
	deferred    **DeferredHandle
}

type assistantMessageObservation struct {
	cell    *assistantMessageCell
	shallow *assistantMessageReferences
}

// assistantMessageReplacements distinguishes top-level assignment from mutation of an existing nested object.
// In particular, assigning output.usage replaces the object retained by earlier Agent shallow copies.
type assistantMessageReplacements struct {
	Content, Usage, Diagnostics, Deferred bool
}

func newAssistantMessageCell(message *AssistantMessage) *assistantMessageCell {
	cell := &assistantMessageCell{}
	cell.publish(message, assistantMessageReplacements{})
	return cell
}

// publish runs under producer ownership of message, before the producer suspends. It never changes delivered public fields.
func (cell *assistantMessageCell) publish(message *AssistantMessage, replacements assistantMessageReplacements) {
	next := cloneAssistantMessage(*message)
	cell.mu.Lock()
	defer cell.mu.Unlock()
	// Absent optional properties are not object references; adding or removing one is an assignment even without an explicit replacement flag.
	replacements.Content = replacements.Content || (cell.current.Content == nil) != (next.Content == nil)
	replacements.Diagnostics = replacements.Diagnostics || (cell.current.Diagnostics == nil) != (next.Diagnostics == nil)
	replacements.Deferred = replacements.Deferred || (cell.current.Deferred == nil) != (next.Deferred == nil)
	publishAssistantReference(&cell.nested.content, next.Content, replacements.Content)
	publishAssistantReference(&cell.nested.usage, next.Usage, replacements.Usage)
	publishAssistantReference(&cell.nested.diagnostics, next.Diagnostics, replacements.Diagnostics)
	publishAssistantReference(&cell.nested.deferred, next.Deferred, replacements.Deferred)
	cell.current = next
}

func publishAssistantReference[T any](reference **T, value T, replace bool) {
	if *reference == nil || replace {
		*reference = new(value)
	} else {
		**reference = value
	}
}

func (cell *assistantMessageCell) view() *AssistantMessage {
	cell.mu.Lock()
	defer cell.mu.Unlock()
	if cell.full == nil {
		view := cloneAssistantMessage(cell.current)
		view.observation = &assistantMessageObservation{cell: cell}
		cell.full = &view
	}
	// Queue entries share this stable handle rather than retaining one public-field snapshot per publication.
	return cell.full
}

// Observe returns independently owned message data at the current publication. Full views advance all fields; shallow views retain copied scalars and the nested objects present at their copy boundary. An ordinary message is deep-copied; native JSON scalar values and nil containers retain their representation. Nil stays nil.
func (message *AssistantMessage) Observe() *AssistantMessage {
	if message == nil {
		return nil
	}
	current := *message
	if message.observation != nil {
		current, _ = message.observation.capture(message)
	}
	owned := cloneAssistantMessage(current)
	return &owned
}

// ObserveUsage returns an owned copy of the current referenced usage without traversing message content, arguments, diagnostics or deferred data.
func (message *AssistantMessage) ObserveUsage() Usage {
	if message == nil {
		return Usage{}
	}
	if observation := message.observation; observation != nil {
		observation.cell.mu.RLock()
		defer observation.cell.mu.RUnlock()
		usage := observation.cell.current.Usage
		if observation.shallow != nil {
			usage = *observation.shallow.usage
		}
		return cloneUsage(usage)
	}
	return cloneUsage(message.Usage)
}

// ObserveToolCallIdentity reads only the selected block's kind, ID and name. indexValid distinguishes an absent index from a present non-tool block. Full and shallow views use their retained content reference.
func (message *AssistantMessage) ObserveToolCallIdentity(index int) (id, name string, toolCall, indexValid bool) {
	if message == nil {
		return "", "", false, false
	}
	content := message.Content
	if observation := message.observation; observation != nil {
		observation.cell.mu.RLock()
		defer observation.cell.mu.RUnlock()
		content = observation.cell.current.Content
		if observation.shallow != nil {
			content = *observation.shallow.content
		}
	}
	if index < 0 || index >= len(content) {
		return "", "", false, false
	}
	call, ok := content[index].(ToolCall)
	if !ok {
		return "", "", false, true
	}
	return call.ID, call.Name, true, true
}

// ShallowCopy copies top-level fields. For a live view it retains synchronized nested references, as the Agent's object spread does in packages/agent/src/agent-loop.ts:416-438. For ordinary data it shares the existing nested values. Nil stays nil.
func (message *AssistantMessage) ShallowCopy() *AssistantMessage {
	if message == nil {
		return nil
	}
	if message.observation == nil {
		copy := *message
		// endTurn is an optional JavaScript boolean, not an object reference.
		if copy.EndTurn != nil {
			copy.EndTurn = new(*copy.EndTurn)
		}
		return &copy
	}
	current, references := message.observation.capture(message)
	copy := cloneAssistantMessage(current)
	copy.observation = &assistantMessageObservation{cell: message.observation.cell, shallow: &references}
	return &copy
}

func (observation *assistantMessageObservation) capture(message *AssistantMessage) (AssistantMessage, assistantMessageReferences) {
	cell := observation.cell
	cell.mu.RLock()
	defer cell.mu.RUnlock()
	if observation.shallow == nil {
		return cell.current, cell.nested
	}
	current := *message
	references := *observation.shallow
	current.Content = *references.content
	current.Usage = *references.usage
	current.Diagnostics = *references.diagnostics
	current.Deferred = *references.deferred
	return current, references
}

// delivered returns a fresh handle whose exported fields equal the message's state at this call, keeping its link to the producer's cell so a later dispatch boundary can refresh it again. Plain messages are returned unchanged.
func (message *AssistantMessage) delivered() *AssistantMessage {
	if message == nil || message.observation == nil {
		return message
	}
	current, _ := message.observation.capture(message)
	owned := cloneAssistantMessage(current)
	owned.observation = &assistantMessageObservation{cell: message.observation.cell, shallow: message.observation.shallow}
	return &owned
}

// RefreshEvent returns the event with its partial's exported fields set to the stream state at this call. A host dispatcher calls it when it delivers a retained event to a listener after an awaited step, so the listener's direct field reads match what a JavaScript listener sees at that tick. Events without a partial are returned unchanged.
func RefreshEvent(event AssistantMessageEvent) AssistantMessageEvent {
	return mapAssistantEventPartial(event, (*AssistantMessage).delivered)
}
