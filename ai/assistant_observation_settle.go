package ai

import "slices"

// cellArguments is a cell's parse of one streamed tool call's deferred argument text (ToolCall.deferArguments): the parser
// resumes where the last settlement stopped, so a reader that settles every publication parses each delta once, as the
// producer did when it parsed eagerly.
type cellArguments struct {
	parser streamingArgumentsParser
	// settled is the arguments the last settlement stored, which the next one shares its unchanged members with.
	settled JsonObject
	// input is the text the last settlement parsed and call the settled call it stored for it, nil before the first. A block
	// that defers the same text (a call that streamed nothing since) has this parse, whatever its identity: the parse is a
	// function of the text alone. Neither a later settlement nor a later publication then parses, copies or boxes it again.
	// The call is an immutable value that the content of every publication holding it shares, as a published block always is
	// (cloneAssistantContentWith).
	input string
	call  AssistantContentBlock
}

// hasDeferredArguments reports whether a tool call in blocks waits for its argument parse.
func hasDeferredArguments(blocks []AssistantContentBlock) bool {
	for _, block := range blocks {
		if call, ok := block.(ToolCall); ok && call.deferredArguments != "" {
			return true
		}
	}
	return false
}

// readLock locks the cell for a reader of its published state, settled (settleLocked): the read lock when nothing waits
// for a parse, otherwise the write lock, which it takes to settle first. It reports which; readUnlock releases it.
func (cell *assistantMessageCell) readLock(shallow *assistantMessageReferences) (exclusive bool) {
	cell.mu.RLock()
	if !cell.unsettled && (shallow == nil || !hasDeferredArguments(*shallow.content)) {
		return false
	}
	cell.mu.RUnlock()
	cell.mu.Lock()
	cell.settleLocked(shallow)
	return true
}

func (cell *assistantMessageCell) readUnlock(exclusive bool) {
	if exclusive {
		cell.mu.Unlock()
	} else {
		cell.mu.RUnlock()
	}
}

// settleLocked parses the deferred arguments of the cell's current publication, and of the content object shallow
// retains when a later publication replaced it, and stores each parse in place of the deferred text. What a reader sees
// is what the publication would have held had the producer parsed eagerly: parseStreamingJsonArguments of the same text,
// copied from it as publication copies arguments (cloneAssistantContentFrom). The caller holds the write lock.
func (cell *assistantMessageCell) settleLocked(shallow *assistantMessageReferences) {
	if cell.unsettled {
		current := cell.current.Content
		settled := cell.settleContent(current, true)
		// The content reference holds the current publication's content unless the cell has none.
		if reference := cell.nested.content; reference != nil && sameContent(*reference, current) {
			*reference = settled
		}
		cell.current.Content = settled
		cell.unsettled = false
	}
	if shallow != nil && hasDeferredArguments(*shallow.content) {
		*shallow.content = cell.settleContent(*shallow.content, false)
	}
}

// settleContent returns a copy of blocks whose tool calls hold the parse of their deferred text. The current
// publication's calls resume the cell's parsers (cached) and keep the settled call of a text they parsed already; a
// content object an earlier publication left behind parses its text afresh.
func (cell *assistantMessageCell) settleContent(blocks []AssistantContentBlock, cached bool) []AssistantContentBlock {
	out := slices.Clone(blocks)
	for index, block := range out {
		call, ok := block.(ToolCall)
		if !ok || call.deferredArguments == "" {
			continue
		}
		state := &cellArguments{}
		if cached {
			state = cell.argumentsAt(index)
		}
		if state.call != nil && state.input == call.deferredArguments {
			parsed := state.call.(ToolCall)
			if sameToolCallIdentity(parsed, call) {
				out[index] = state.call
				continue
			}
			call.setParsedArguments(parsed.Arguments, parsed.argumentOrder, parsed.nonObjectArguments)
		} else {
			input := call.deferredArguments
			arguments, order, nonObject := state.parser.parse(input)
			if arguments != nil {
				arguments = cloneArgumentsFrom(arguments, state.settled)
			}
			state.settled, state.input = arguments, input
			call.setParsedArguments(arguments, order, nonObject)
		}
		out[index] = call
		if cached {
			state.call = out[index]
		}
	}
	return out
}

// settledCalls returns, at the index of each tool call of blocks that defers a text the cell has settled for a call of the same
// identity, the settled call: the block that settlement would make. It returns nil when there is none. The caller holds a lock.
func (cell *assistantMessageCell) settledCalls(blocks []AssistantContentBlock) (settled []AssistantContentBlock) {
	for index, block := range blocks {
		call, ok := block.(ToolCall)
		if !ok || call.deferredArguments == "" {
			continue
		}
		state := cell.arguments[index]
		if state == nil || state.call == nil || state.input != call.deferredArguments || !sameToolCallIdentity(state.call.(ToolCall), call) {
			continue
		}
		if settled == nil {
			settled = make([]AssistantContentBlock, len(blocks))
		}
		settled[index] = state.call
	}
	return settled
}

func (cell *assistantMessageCell) argumentsAt(index int) *cellArguments {
	if cell.arguments == nil {
		cell.arguments = map[int]*cellArguments{}
	}
	state := cell.arguments[index]
	if state == nil {
		state = &cellArguments{}
		cell.arguments[index] = state
	}
	return state
}

// settledContent is settleContent for content outside the cell, such as a capture's: it parses afresh and caches nothing.
func settledContent(blocks []AssistantContentBlock) []AssistantContentBlock {
	if !hasDeferredArguments(blocks) {
		return blocks
	}
	return (&assistantMessageCell{}).settleContent(blocks, false)
}

// sameContent reports whether two content slices are the same object.
func sameContent(a, b []AssistantContentBlock) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}
