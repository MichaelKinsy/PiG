package ai

import "slices"

// withRepairedToolFlow returns the transcript a real provider converts: tool results that Pi would send and the API would reject are repaired first. A faux provider and pi-messages receive the transcript as it is.
func (transcript TranscriptContext) withRepairedToolFlow() TranscriptContext {
	if transcript.err != nil {
		return transcript
	}
	repaired := RepairToolFlow(transcript.messages)
	if len(repaired) == len(transcript.messages) && (len(repaired) == 0 || &repaired[0] == &transcript.messages[0]) {
		return transcript
	}
	return TranscriptContext{messages: repaired, validated: transcript.validated}
}

// RepairToolFlow makes every tool result answer an open tool call, before transformMessages accounts for the calls.
//
// pig divergence (D48): upstream transformMessages sends a result whose calling turn was dropped, a repeated result, and a result that arrives after its flow closed (while it also adds a "No result provided" placeholder for the call), and the provider rejects the request. Here an out-of-order result moves into the slot of its call, and an orphan or repeat is dropped. A result is matched in order against open calls rather than by id alone, because a provider may number its calls per response and reuse an id every turn. A call of an errored or aborted turn is not open: transformMessages omits that turn. A result is not moved back past a turn that opens a call with its id again: it answers that turn, as in Pi's request, so a request Pi sends with an intact tool flow reaches the provider unchanged.
func RepairToolFlow(messages []Message) []Message {
	if !slices.ContainsFunc(messages, func(message Message) bool { _, ok := message.(ToolResultMessage); return ok }) {
		return messages
	}
	var later map[string][]int
	var moved map[int]bool
	var pending []string
	seen := map[string]bool{}
	position := 0
	consume := func(id string, inPlace bool) (int, bool) {
		if later == nil {
			later = map[string][]int{}
			for index := position; index < len(messages); index++ {
				if tr, ok := messages[index].(ToolResultMessage); ok && tr.ToolCallID != "" {
					later[tr.ToolCallID] = append(later[tr.ToolCallID], index)
				}
			}
		}
		queued := later[id]
		if len(queued) == 0 || !inPlace && reopened(messages[position:queued[0]], id) {
			return 0, false
		}
		later[id] = queued[1:]
		return queued[0], true
	}
	out := make([]Message, 0, len(messages))
	emit := func(message Message) { out = append(out, message) }
	flush := func() {
		for _, id := range pending {
			if seen[id] {
				continue
			}
			if index, ok := consume(id, false); ok {
				if moved == nil {
					moved = map[int]bool{}
				}
				moved[index] = true
				emit(messages[index])
			}
		}
		pending = pending[:0]
		clear(seen)
	}
	for index, message := range messages {
		position = index
		switch typed := message.(type) {
		case AssistantMessage:
			flush()
			if typed.StopReason != StopReasonError && typed.StopReason != StopReasonAborted {
				for _, block := range typed.Content {
					if call, ok := block.(ToolCall); ok && call.ID != "" {
						pending = append(pending, call.ID)
					}
				}
			}
			emit(message)
		case UserMessage:
			flush()
			emit(message)
		case ToolResultMessage:
			if moved[index] {
				continue
			}
			if later != nil {
				consume(typed.ToolCallID, true)
			}
			seen[typed.ToolCallID] = true
			emit(message)
		default:
			emit(message)
		}
	}
	position = len(messages)
	flush()
	out = dropUnansweredToolResults(out)
	if moved == nil && len(out) == len(messages) {
		return messages
	}
	return out
}

// reopened reports whether a turn in messages opens a call with id. A later result then answers that call where it stands, as it does in Pi's request, and is not moved back to an earlier unanswered call.
func reopened(messages []Message, id string) bool {
	for _, message := range messages {
		if typed, ok := message.(AssistantMessage); ok && typed.StopReason != StopReasonError && typed.StopReason != StopReasonAborted {
			for _, block := range typed.Content {
				if call, ok := block.(ToolCall); ok && call.ID == id {
					return true
				}
			}
		}
	}
	return false
}

// dropUnansweredToolResults removes every tool result that closes no open call of its turn. A late result has already moved to its call, so a result that a later turn finds is an orphan or a repeat.
func dropUnansweredToolResults(messages []Message) []Message {
	open := map[string]int{}
	var kept []Message
	for index, message := range messages {
		keep := true
		switch typed := message.(type) {
		case AssistantMessage:
			if typed.StopReason == StopReasonError || typed.StopReason == StopReasonAborted {
				break
			}
			clear(open)
			for _, block := range typed.Content {
				if call, ok := block.(ToolCall); ok && call.ID != "" {
					open[call.ID]++
				}
			}
		case ToolResultMessage:
			if open[typed.ToolCallID] == 0 {
				keep = false
			} else {
				open[typed.ToolCallID]--
			}
		}
		switch {
		case !keep && kept == nil:
			kept = slices.Clone(messages[:index])
		case keep && kept != nil:
			kept = append(kept, message)
		}
	}
	if kept == nil {
		return messages
	}
	return kept
}
