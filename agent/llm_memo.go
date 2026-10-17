package agent

import (
	"bytes"
	"sync"
	"unsafe"

	"github.com/MichaelKinsy/PiG/ai"
)

// llmMemo remembers the provider messages of the previous request's context, so a request converts only the messages
// that changed and copies the rest. Pi converts the live message objects on every request (agent.ts defaultConvertToLlm),
// and a hook may write to them in place, so the memo serves a message only while the value it converted still equals the
// value the message holds now. The comparison is shallow and exact for every word the conversion reads: a scalar, a string,
// an interface (its type and data words), and the pointer, length and capacity of a slice, map or pointer field, so a
// replaced field is seen. A write through a shared slice's element data, a map or a pointer needs no comparison: the
// remembered conversion holds the same slice, map or pointer, as the provider request does, and shows the write.
//
// A message whose conversion reads state that can still change (an assistant message backed by a live stream) ends the
// remembered prefix, and so does the first message that differs.
type llmMemo struct {
	mu sync.Mutex
	// out holds the conversion of the remembered messages; ends[i] is its length after the i-th remembered message.
	out  []ai.Message
	ends []int
	// minMessages overrides llmMemoMinMessages when it is not zero.
	minMessages int
}

// defaultLLMMemoMinMessages is the length of a context under which the memo converts every message and keeps nothing: converting that many messages costs a few hundred allocations, and the remembered copies of a short session would add to the heap its every turn leaves behind.
// pig additive (D112): Pi converts every message on every request (agent.ts defaultConvertToLlm); the floor changes only what the memo retains, never the request.
const defaultLLMMemoMinMessages = 64

// llmMemoMinMessages is the minimum an llmMemo without its own minMessages uses; the tests of the package lower it so every test exercises the memo.
var llmMemoMinMessages = defaultLLMMemoMinMessages

// convert returns what ConvertToLLM returns for msgs in a slice the caller owns.
func (c *llmMemo) convert(msgs []AgentMessage) []ai.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	floor := llmMemoMinMessages
	if c.minMessages > 0 {
		floor = c.minMessages
	}
	if len(msgs) < floor {
		clear(c.out)
		c.out, c.ends = nil, nil
		return ConvertToLLM(msgs)
	}
	prefix, outLen := 0, 0
	for prefix < len(msgs) && prefix < len(c.ends) && convertsStably(msgs[prefix]) {
		start := 0
		if prefix > 0 {
			start = c.ends[prefix-1]
		}
		end := c.ends[prefix]
		if msgs[prefix].Custom != nil {
			// A custom message is a map the memo does not compare; it converts again, and the remembered conversion stands only when it still takes the same number of messages.
			var scratch [1]ai.Message
			converted := appendLLM(scratch[:0], msgs[prefix])
			if len(converted) != end-start {
				break
			}
			copy(c.out[start:end], converted)
		} else if end-start != 1 || !stillConverts(msgs[prefix], c.out[start]) {
			break
		}
		prefix++
		outLen = end
	}
	clear(c.out[outLen:])
	c.out, c.ends = c.out[:outLen], c.ends[:prefix]
	next := prefix
	for ; next < len(msgs) && convertsStably(msgs[next]); next++ {
		c.out = appendLLM(c.out, msgs[next])
		c.ends = append(c.ends, len(c.out))
	}
	result := make([]ai.Message, len(c.out), len(msgs))
	copy(result, c.out)
	for ; next < len(msgs); next++ {
		result = appendLLM(result, msgs[next])
	}
	return result
}

// convertsStably reports whether the provider message of m depends only on values m's fields hold.
func convertsStably(m AgentMessage) bool {
	return m.Assistant == nil || m.Assistant.streamView == nil
}

// stillConverts reports whether converting m now gives the message remembered as old: the converted value equals old word
// for word.
func stillConverts(m AgentMessage, old ai.Message) bool {
	switch {
	case m.System != nil:
		previous, ok := old.(ai.SystemMessage)
		return ok && sameWords(m.System, &previous)
	case m.User != nil:
		previous, ok := old.(ai.UserMessage)
		current := m.User.LLMMessage()
		return ok && sameWords(&current, &previous)
	case m.Assistant != nil:
		previous, ok := old.(ai.AssistantMessage)
		current := m.Assistant.LLMMessage()
		return ok && sameWords(&current, &previous)
	case m.ToolResult != nil:
		previous, ok := old.(ai.ToolResultMessage)
		current := toolResultLLM(m.ToolResult)
		return ok && sameWords(&current, &previous)
	}
	return false
}

// sameWords compares two values of one type word for word, including padding: a difference the compare cannot tell from a change only costs a conversion.
func sameWords[T any](a, b *T) bool {
	size := unsafe.Sizeof(*a)
	//nolint:gosec // G103: reads the bytes of two live values of one type within their size; nothing is written and no pointer outlives the call, so the CLI threat model has no input that reaches it. A typed compare of every field of four structs would drift when a field is added, and reflect costs the conversion it saves.
	return bytes.Equal(unsafe.Slice((*byte)(unsafe.Pointer(a)), size), unsafe.Slice((*byte)(unsafe.Pointer(b)), size))
}
