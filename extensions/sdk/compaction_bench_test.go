package sdk

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// withStandInDateParser answers Date.parse in process, as a fused extension's host does. An isolated extension makes one host call per timestamped entry
// (custom_message, branch_summary, compaction) in [SessionEntryToContextMessages], which these benchmarks do not measure.
func withStandInDateParser(b *testing.B) {
	b.Helper()
	SetHostHelpers(&HostHelpers{ParseDate: func(text string) (float64, bool) {
		parsed, err := time.Parse(time.RFC3339Nano, text)
		return float64(parsed.UnixMilli()), err == nil
	}})
	b.Cleanup(func() { SetHostHelpers(nil) })
}

// compactionBranch builds n entries shaped like what GetBranch returns (decoded JSON): a compaction entry first, then user, assistant (text and a tool call) and
// tool result turns, with a metadata entry every ninth entry and a custom message every twentieth.
func compactionBranch(tb testing.TB, n int) []map[string]any {
	tb.Helper()
	text := strings.Repeat("lorem ipsum dolor sit amet ", 40)
	entries := make([]map[string]any, 0, n)
	entries = append(entries, map[string]any{"type": "compaction", "id": "c0", "summary": text, "firstKeptEntryId": "c0", "tokensBefore": 1000, "timestamp": "2026-01-01T00:00:00.000Z"})
	for i := 1; len(entries) < n; i++ {
		var entry map[string]any
		switch {
		case i%20 == 19:
			entry = map[string]any{"type": "custom_message", "customType": "note", "content": text, "display": true, "timestamp": "2026-01-01T00:00:00.000Z"}
		case i%9 == 0:
			entry = map[string]any{"type": "model_change", "provider": "p", "modelId": "m"}
		case i%9 == 1 || i%9 == 4 || i%9 == 7:
			entry = map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": text}}
		case i%9 == 2 || i%9 == 5 || i%9 == 8:
			entry = map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
				map[string]any{"type": "text", "text": text},
				map[string]any{"type": "toolCall", "id": "t", "name": "read", "arguments": map[string]any{"path": "/tmp/file.go", "offset": 10, "limit": 200}},
			}}}
		default:
			entry = map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "content": []any{map[string]any{"type": "text", "text": text}}}}
		}
		entry["id"] = fmt.Sprintf("e%d", i)
		entries = append(entries, entry)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		tb.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		tb.Fatal(err)
	}
	return decoded
}

// BenchmarkFindCutPoint and its siblings run the compaction helpers over a branch of 1k, 10k and 100k entries. Pi's findCutPoint scans the range once for cut
// points and walks back once for the budget, so the cost per entry stays flat as the branch grows (keepAll walks the whole range); ns/entry shows a quadratic
// scan as a rising figure.
func BenchmarkFindCutPoint(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		entries := compactionBranch(b, n)
		for _, keep := range []struct {
			name   string
			tokens int
		}{{"keep20k", 20000}, {"keepAll", 1 << 40}} {
			b.Run(fmt.Sprintf("entries=%d/%s", n, keep.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					FindCutPoint(entries, 0, len(entries), keep.tokens)
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/entry")
			})
		}
	}
}

func BenchmarkSessionEntryToContextMessages(b *testing.B) {
	withStandInDateParser(b)
	for _, n := range []int{1_000, 10_000, 100_000} {
		entries := compactionBranch(b, n)
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, entry := range entries {
					if _, err := SessionEntryToContextMessages(entry); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/entry")
		})
	}
}

func BenchmarkEstimateTokens(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		var messages []map[string]any
		for _, entry := range compactionBranch(b, n) {
			if message, ok := entry["message"].(map[string]any); ok {
				messages = append(messages, message)
			}
		}
		b.Run(fmt.Sprintf("messages=%d", len(messages)), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, message := range messages {
					EstimateTokens(message)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(messages)), "ns/message")
		})
	}
}
