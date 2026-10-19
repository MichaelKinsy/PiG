package harness

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/internal/detach"
)

// assistantCallCacheSize is how many assistant entries' tool calls the scheduler keeps: the rounds in flight.
const assistantCallCacheSize = 8

type assistantCallKey struct {
	conversationId durable.ConversationId
	entryId        durable.EntryId
}

// assistantCalls keeps the tool calls of the assistant entries tool tasks read. An entry is immutable once committed
// and an entry visible from a conversation stays visible (the ancestry of a fork never changes), so what one read
// returned holds for every later one. Each read gets its own copy of the one call it asks for, never of the entry.
type assistantCalls struct {
	mu    sync.Mutex
	calls map[assistantCallKey]map[string]ai.ToolCall
	order []assistantCallKey
}

func (cache *assistantCalls) get(key assistantCallKey, callId string) (call ai.ToolCall, found, cached bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	set, ok := cache.calls[key]
	if !ok {
		return ai.ToolCall{}, false, false
	}
	call, found = set[callId]
	if found {
		call = cloneToolCall(call)
	}
	return call, found, true
}

func (cache *assistantCalls) put(key assistantCallKey, set map[string]ai.ToolCall) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.calls == nil {
		cache.calls = map[assistantCallKey]map[string]ai.ToolCall{}
	}
	if _, ok := cache.calls[key]; !ok {
		if len(cache.order) >= assistantCallCacheSize {
			delete(cache.calls, cache.order[0])
			cache.order = cache.order[1:]
		}
		cache.order = append(cache.order, key)
	}
	cache.calls[key] = set
}

// cloneToolCall is call detached from the cached copy, as the storage returns an entry its caller owns.
func cloneToolCall(call ai.ToolCall) ai.ToolCall {
	detached := detach.Messages([]ai.Message{ai.AssistantMessage{Content: []ai.AssistantContentBlock{call}}})
	return detached[0].(ai.AssistantMessage).Content[0].(ai.ToolCall)
}

// AssistantToolCall implements durable.AssistantCallReader: the first read of an assistant entry copies it from storage,
// as Entry does, and the reads of the other calls it holds are served from that copy.
func (runtime *taskRuntime) AssistantToolCall(ctx context.Context, id durable.EntryId, callId string) (ai.ToolCall, bool, error) {
	cache := &runtime.scheduler.assistantCalls
	key := assistantCallKey{conversationId: runtime.invocation.conversationId, entryId: id}
	if err := runtime.check(); err != nil {
		return ai.ToolCall{}, false, err
	}
	if call, found, cached := cache.get(key, callId); cached {
		return call, found, nil
	}
	record, err := runtime.Entry(ctx, id)
	if err != nil {
		return ai.ToolCall{}, false, err
	}
	entry, err := durable.AssistantEntry.As(record)
	if err != nil || entry == nil {
		return ai.ToolCall{}, false, err
	}
	if len(entry.Model) == 0 {
		return ai.ToolCall{}, false, nil
	}
	set := map[string]ai.ToolCall{}
	if message, ok := entry.Model[0].(ai.AssistantMessage); ok {
		for _, block := range message.Content {
			if call, ok := block.(ai.ToolCall); ok {
				if _, seen := set[call.ID]; !seen {
					set[call.ID] = call
				}
			}
		}
	}
	cache.put(key, set)
	call, found, _ := cache.get(key, callId)
	return call, found, nil
}
