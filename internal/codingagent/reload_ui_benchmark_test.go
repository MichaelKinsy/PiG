package codingagent_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func BenchmarkInteractiveSessionReload(b *testing.B) {
	for _, entries := range []int{0, 1000} {
		b.Run(fmt.Sprint(entries), func(b *testing.B) {
			session, h, _ := reloadOriginalPair(b, func(...any) (any, error) { return nil, nil })
			for range entries {
				if _, err := session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "retained answer"}}}}); err != nil {
					b.Fatal(err)
				}
			}
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
