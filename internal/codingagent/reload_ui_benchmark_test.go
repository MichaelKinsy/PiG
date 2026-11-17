package codingagent_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// BenchmarkInteractiveSessionReload measures a reload on a machine without fd: each reload resolves fd for the autocomplete provider, and a lookup that finds nothing costs every directory of
// PATH, so the benchmark runs with a PATH of one empty directory (testenv.EmptyPath). Its working directory is three components deep on every machine (reloadOriginalPair).
func BenchmarkInteractiveSessionReload(b *testing.B) {
	for _, entries := range []int{0, 1000} {
		b.Run(fmt.Sprint(entries), func(b *testing.B) {
			session, h, _ := reloadOriginalPair(b, func(...any) (any, error) { return nil, nil })
			for range entries {
				if _, err := session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "retained answer"}}}}); err != nil {
					b.Fatal(err)
				}
			}
			testenv.EmptyPath(b)
			b.ReportAllocs()
			for b.Loop() {
				var err error
				h.Do(func() { err = h.Reload() })
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
