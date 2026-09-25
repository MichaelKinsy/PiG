package pico3

import "context"

const contextPage = 256

// deriveContext projects model context (pico §1.3):
//
//	H       = newest fork-visible entry at or before T with a head
//	from    = H ? H.head : transcript start
//	range   = fork-visible entries from `from` through T
//	edits   = per target, newest edit in range wins
//	entries = H ? [H, ...range without any head entries] : range
//	model   = concat(entries.map(e => edits[e.id] ? apply : e.model)),
//	          then reorder tool results
//
// Display-only entries have no model and contribute nothing.
func deriveContext(ctx context.Context, storage Storage, conversationId Id, at *Id) (ContextView, error) {
	var before *Id
	if at != nil {
		next := *at + 1
		before = &next
	}
	heads, err := storage.ScanEntries(ctx, EntryScan{ConversationId: conversationId, WithHead: true, Before: before, Limit: 1})
	if err != nil {
		return ContextView{}, err
	}
	var head *Entry
	if len(heads) > 0 {
		head = &heads[0]
	}
	rangeEntries, err := scanRange(ctx, storage, conversationId, before, head)
	if err != nil {
		return ContextView{}, err
	}
	edits := map[Id]ContextEdit{}
	for _, entry := range rangeEntries {
		for _, edit := range entry.Edits {
			edits[edit.Target] = edit
		}
	}
	entries := rangeEntries
	if head != nil {
		entries = []Entry{*head}
		for _, entry := range rangeEntries {
			if entry.Head == nil {
				entries = append(entries, entry)
			}
		}
	}
	var messages []JsonObject
	for _, entry := range entries {
		edit, edited := edits[entry.Id]
		switch {
		case edited && edit.Action == "omit":
		case edited && edit.Action == "replace":
			messages = append(messages, edit.Messages...)
		default:
			messages = append(messages, entry.Model...)
		}
	}
	return ContextView{Head: head, Entries: entries, Messages: reorderToolResults(messages)}, nil
}

// scanRange walks newest-first until it passes the head's target and returns
// the range in chronological order.
func scanRange(ctx context.Context, storage Storage, conversationId Id, before *Id, head *Entry) ([]Entry, error) {
	var from *Id
	if head != nil {
		from = head.Head
	}
	var out []Entry
	for {
		page, err := storage.ScanEntries(ctx, EntryScan{ConversationId: conversationId, Before: before, Limit: contextPage})
		if err != nil {
			return nil, err
		}
		done := len(page) < contextPage
		for _, entry := range page {
			if from != nil && entry.Id < *from {
				done = true
				break
			}
			out = append(out, entry)
		}
		if done {
			break
		}
		last := page[len(page)-1].Id
		before = &last
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out, nil
}

// reorderToolResults puts results back in call order after each assistant
// message and synthesizes a result missing after a fork cut.
func reorderToolResults(messages []JsonObject) []JsonObject {
	out := make([]JsonObject, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		message := messages[index]
		out = append(out, message)
		if str(message, "role") != "assistant" {
			continue
		}
		calls := toolCallsOf(message)
		if len(calls) == 0 {
			continue
		}
		results := map[string]JsonObject{}
		next := index + 1
		for next < len(messages) && str(messages[next], "role") == "toolResult" {
			results[str(messages[next], "toolCallId")] = messages[next]
			next++
		}
		for _, call := range calls {
			if result, ok := results[str(call, "id")]; ok {
				out = append(out, result)
				continue
			}
			out = append(out, missingToolResult(call, message["timestamp"]))
		}
		index = next - 1
	}
	return out
}

func toolCallsOf(message JsonObject) []JsonObject {
	var calls []JsonObject
	for _, block := range arr(message, "content") {
		if object, ok := block.(map[string]any); ok && str(object, "type") == "toolCall" {
			calls = append(calls, object)
		}
	}
	return calls
}

func missingToolResult(call JsonObject, timestamp any) JsonObject {
	return JsonObject{
		"role":       "toolResult",
		"toolCallId": str(call, "id"),
		"toolName":   str(call, "name"),
		"content": []any{JsonObject{
			"type": "text",
			"text": "Tool result unavailable: history ends before this call completed.",
		}},
		"isError":   true,
		"details":   JsonObject{"reason": "missing_after_fork"},
		"timestamp": timestamp,
	}
}
