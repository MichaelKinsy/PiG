package ai

import (
	"sync"
	"unsafe"
)

// assistantMessageCell publishes producer-owned state without sharing mutable construction data with readers.
// Upstream queues the same partial reference (packages/ai/src/utils/event-stream.ts:44-91).
type assistantMessageCell struct {
	mu      sync.RWMutex
	current AssistantMessage
	nested  assistantMessageReferences
	full    *AssistantMessage
	// seq counts publications: every view of the cell reads the same state while it is unchanged.
	seq uint64
	// unsettled says a tool call of the current publication waits for its argument parse (settleLocked); arguments holds
	// the parse state of each streamed call by content index.
	unsettled bool
	arguments map[int]*cellArguments
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
	// seq is the cell publication the holder's exported fields were copied at: while the cell's seq still equals it, those fields are the current state and delivered keeps the holder.
	seq uint64
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
	// The previous snapshot's content is immutable once published, so the copy below may share what has not changed. A call that
	// still defers a text the cell has settled is published as its settled call (settledCalls).
	cell.mu.RLock()
	previous, settled := cell.current.Content, cell.settledCalls(message.Content)
	cell.mu.RUnlock()
	next := cloneAssistantMessageFrom(*message, previous, settled)
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
	cell.unsettled = hasDeferredArguments(next.Content)
	cell.seq++
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
		cell.settleLocked(nil)
		view := clonePublishedAssistantMessage(cell.current)
		view.observation = &assistantMessageObservation{cell: cell, seq: cell.seq}
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
	if message.observation == nil {
		owned := cloneAssistantMessage(*message)
		return &owned
	}
	current, _, _ := message.observation.capture(message)
	owned := clonePublishedAssistantMessage(current)
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

// Publication identifies the state message observes: the producer cell its live view reads and the number of publications the cell has made. While a message's cell and seq are unchanged, every observation of that message returns the same state (a shallow view adds its own top-level fields, which only its holder assigns), so a reader may reuse what it derived from an earlier one. A message that is not a live view returns (0, 0); its fields are the caller's to track.
func (message *AssistantMessage) Publication() (cell uintptr, seq uint64) {
	if message == nil || message.observation == nil {
		return 0, 0
	}
	observed := message.observation.cell
	observed.mu.RLock()
	seq = observed.seq
	observed.mu.RUnlock()
	return uintptr(unsafe.Pointer(observed)), seq //nolint:gosec // G103: the address is only an identity token for the publication cell; it is never converted back to a pointer or dereferenced
}

// AssistantContentProjection is what a renderer reads of one content block. Kind is the block's type ("text", "thinking" or "toolCall"); Text, Thinking, ToolCallID, ToolCallName and Arguments are set for the kind that holds them.
// The values are the observed state's own, not copies: a live view's published state never changes, and a plain message's are its own fields. Arguments must not be modified.
type AssistantContentProjection struct {
	Kind         string
	Text         string
	Thinking     string
	ToolCallID   string
	ToolCallName string
	Arguments    JsonObject
}

// ObserveProjection is Observe for a reader of block kinds, texts, thinking, tool calls, stop reason and error message: it appends one projection per content block to content, returns that slice with the stop reason and error message of the same publication, and copies nothing else. Full and shallow views read their current publication as Observe does.
// Returning the blocks as their own result keeps a caller's buffer on its stack when the caller stores the stop reason or error message.
func (message *AssistantMessage) ObserveProjection(content []AssistantContentProjection) (blocks []AssistantContentProjection, stopReason StopReason, errorMessage string) {
	if message == nil {
		return content, "", ""
	}
	observed := message.Content
	stopReason, errorMessage = message.StopReason, message.ErrorMessage
	if observation := message.observation; observation != nil {
		exclusive := observation.cell.readLock(observation.shallow)
		defer observation.cell.readUnlock(exclusive)
		if observation.shallow == nil {
			current := &observation.cell.current
			observed, stopReason, errorMessage = current.Content, current.StopReason, current.ErrorMessage
		} else {
			// A shallow view keeps the scalars copied at its copy boundary and reads its retained content reference (capture).
			observed = *observation.shallow.content
		}
	}
	for _, block := range observed {
		switch block := block.(type) {
		case TextContent:
			content = append(content, AssistantContentProjection{Kind: block.contentType(), Text: block.Text})
		case ThinkingContent:
			content = append(content, AssistantContentProjection{Kind: block.contentType(), Thinking: block.Thinking})
		case ToolCall:
			content = append(content, AssistantContentProjection{Kind: block.contentType(), ToolCallID: block.ID, ToolCallName: block.Name, Arguments: block.Arguments})
		}
	}
	return content, stopReason, errorMessage
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
		if copy.DurationMs != nil {
			copy.DurationMs = new(*copy.DurationMs)
		}
		return &copy
	}
	current, references, seq := message.observation.capture(message)
	copy := clonePublishedAssistantMessage(current)
	copy.observation = &assistantMessageObservation{cell: message.observation.cell, shallow: &references, seq: seq}
	return &copy
}

// capture reads the state message observes, settled (assistantMessageCell.settleLocked), and the cell publication it belongs to.
func (observation *assistantMessageObservation) capture(message *AssistantMessage) (AssistantMessage, assistantMessageReferences, uint64) {
	cell := observation.cell
	exclusive := cell.readLock(observation.shallow)
	defer cell.readUnlock(exclusive)
	return observation.captureLocked(message)
}

// captureUnsettled is capture without settling: a tool call of what it returns may still hold its deferred argument text.
func (observation *assistantMessageObservation) captureUnsettled(message *AssistantMessage) (AssistantMessage, assistantMessageReferences, uint64) {
	observation.cell.mu.RLock()
	defer observation.cell.mu.RUnlock()
	return observation.captureLocked(message)
}

func (observation *assistantMessageObservation) captureLocked(message *AssistantMessage) (AssistantMessage, assistantMessageReferences, uint64) {
	cell := observation.cell
	if observation.shallow == nil {
		return cell.current, cell.nested, cell.seq
	}
	current := *message
	references := *observation.shallow
	current.Content = *references.content
	current.Usage = *references.usage
	current.Diagnostics = *references.diagnostics
	current.Deferred = *references.deferred
	return current, references, cell.seq
}

// delivered returns a handle whose exported fields equal the message's state at this call, keeping its link to the producer's cell so a later dispatch boundary can refresh it again. A handle whose fields were copied at the cell's current publication already holds that state and is returned as it is; otherwise the handle is fresh. Plain messages are returned unchanged.
func (message *AssistantMessage) delivered() *AssistantMessage {
	if message == nil || message.observation == nil {
		return message
	}
	observation := message.observation
	cell := observation.cell
	cell.mu.RLock()
	unchanged := observation.seq == cell.seq
	cell.mu.RUnlock()
	if unchanged {
		return message
	}
	current, _, seq := observation.capture(message)
	owned := clonePublishedAssistantMessage(current)
	owned.observation = &assistantMessageObservation{cell: cell, shallow: observation.shallow, seq: seq}
	return &owned
}

// RefreshEvent returns the event with its partial's exported fields set to the stream state at this call. A host dispatcher calls it when it delivers a retained event to a listener after an awaited step, so the listener's direct field reads match what a JavaScript listener sees at that tick. Events without a partial are returned unchanged.
func RefreshEvent(event AssistantMessageEvent) AssistantMessageEvent {
	return mapAssistantEventPartial(event, (*AssistantMessage).delivered)
}
