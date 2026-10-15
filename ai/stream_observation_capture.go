package ai

import "unsafe"

// ShallowCapture is ShallowCopy taken now and copied later. Capture records what ShallowCopy would copy, without copying message data or parsing deferred tool arguments: a live view's published state is immutable once published and its nested-object references keep their identity, so the copy ShallowCopy builds from the capture at any later time equals the copy ShallowCopy would have built at the capture. A consumer that may never read the copy pays only for the capture.
type ShallowCapture struct {
	// current and references are what the view's observation captured at the cell's publication seq; cell is the producer cell the view reads. A message that is not a live view is kept in plain: it is an immutable emission-time snapshot (or a caller's message that nothing changes while the capture is held).
	current    AssistantMessage
	references assistantMessageReferences
	cell       *assistantMessageCell
	seq        uint64
	plain      *AssistantMessage
}

// Capture records message as it is now. Nil captures nil.
func (capture *ShallowCapture) Capture(message *AssistantMessage) {
	if message == nil || message.observation == nil {
		*capture = ShallowCapture{plain: message}
		return
	}
	current, references, seq := message.observation.captureUnsettled(message)
	*capture = ShallowCapture{current: current, references: references, cell: message.observation.cell, seq: seq}
}

// Publication is the captured message's Publication at the capture: the producer cell and its publication count for a live view, (0, 0) otherwise. The copies the capture returns hold the state of that publication.
func (capture *ShallowCapture) Publication() (cell uintptr, seq uint64) {
	if capture.cell == nil {
		return 0, 0
	}
	return uintptr(unsafe.Pointer(capture.cell)), capture.seq //nolint:gosec // G103: the address is only an identity token for the publication cell; it is never converted back to a pointer or dereferenced
}

// ShallowCopy returns what message.ShallowCopy returned at the capture.
func (capture *ShallowCapture) ShallowCopy() *AssistantMessage {
	if capture.cell == nil {
		return capture.plain.ShallowCopy()
	}
	capture.settle()
	copy := clonePublishedAssistantMessage(capture.current)
	references := capture.references
	copy.observation = &assistantMessageObservation{cell: capture.cell, shallow: &references, seq: capture.seq}
	return &copy
}

// Observe returns what message.ShallowCopy().Observe() returned at the capture: owned message data at that publication.
func (capture *ShallowCapture) Observe() *AssistantMessage {
	if capture.cell == nil {
		return capture.plain.ShallowCopy().Observe()
	}
	capture.settle()
	owned := clonePublishedAssistantMessage(capture.current)
	return &owned
}

// settle parses the captured content's deferred tool arguments once, as a reader of the publication would have: through
// the cell's parsers while the capture holds the cell's current publication, otherwise afresh.
func (capture *ShallowCapture) settle() {
	content := capture.current.Content
	if !hasDeferredArguments(content) {
		return
	}
	cell := capture.cell
	cell.mu.Lock()
	if sameContent(content, cell.current.Content) {
		cell.settleLocked(nil)
		content = cell.current.Content
	} else {
		content = settledContent(content)
	}
	cell.mu.Unlock()
	capture.current.Content = content
}
