package sdk

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// turnEndArgs is the args of the turn_end event of a session of n tool turns: each turn adds an assistant message with a tool call and a tool result of about 120 KB of source code (the Bolt -p scenario reads four fixture files of that size), and the history is in the event three times, as contextEntries messages, contextMessages and llmMessages.
func turnEndArgs(tb testing.TB, turns int) []byte {
	tb.Helper()
	source := strings.Repeat("func (s *Session) run(ctx context.Context) error {\n\tif a < b && c > d {\n\t\treturn fmt.Errorf(\"quoted %q\", s.name)\n\t}\n}\n", 900)
	var messages, llm, entries []any
	messages = append(messages, map[string]any{"role": "user", "content": "Read the four fixture files", "timestamp": float64(1)})
	llm = append(llm, map[string]any{"role": "user", "content": "Read the four fixture files", "timestamp": float64(1)})
	for i := range turns {
		call := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": strings.Repeat("Looking at the code structure ", 20)}, map[string]any{"type": "toolCall", "id": fmt.Sprintf("call_%d", i), "name": "read", "arguments": map[string]any{"path": fmt.Sprintf("fixture/%d.ts", i)}}}, "stopReason": "toolUse", "timestamp": float64(2 + 2*i)}
		result := map[string]any{"role": "toolResult", "toolCallId": fmt.Sprintf("call_%d", i), "toolName": "read", "content": []any{map[string]any{"type": "text", "text": source}}, "isError": false, "timestamp": float64(3 + 2*i)}
		messages = append(messages, call, result)
		llm = append(llm, call, result)
	}
	for i, message := range messages {
		entries = append(entries, map[string]any{"sourceEntry": map[string]any{"type": "message", "id": fmt.Sprintf("e%d", i), "message": message}, "messages": []any{message}})
	}
	args, err := json.Marshal(map[string]any{"type": "turn_end", "turnIndex": float64(turns), "context": map[string]any{"contextEntries": entries, "contextMessages": messages, "llmMessages": llm, "pendingMessages": []any{}, "canContinue": true}})
	if err != nil {
		tb.Fatal(err)
	}
	return args
}

// BenchmarkFusedEventDecode is the cost one fused handler pays to receive a turn_end of a session of n tool turns when the memo has not seen its payload: the trusted decode and the handler's copy. (A second handler of the same payload pays the copy only.)
func BenchmarkFusedEventDecode(b *testing.B) {
	for _, turns := range []int{4, 49} {
		args := turnEndArgs(b, turns)
		b.Run(fmt.Sprintf("turns=%d/%dKB", turns, len(args)>>10), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(args)))
			for b.Loop() {
				var memo eventArgsMemo
				memo.trusted = true
				_, release := memo.decode(args)
				release()
			}
		})
	}
}

// BenchmarkEventArgsValidatingDecode is BenchmarkFusedEventDecode's decode with the validity pass, as a socket frame's args are decoded.
func BenchmarkEventArgsValidatingDecode(b *testing.B) {
	for _, turns := range []int{4, 49} {
		args := turnEndArgs(b, turns)
		b.Run(fmt.Sprintf("turns=%d/%dKB", turns, len(args)>>10), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(args)))
			for b.Loop() {
				var memo eventArgsMemo
				_, release := memo.decode(args)
				release()
			}
		})
	}
}
