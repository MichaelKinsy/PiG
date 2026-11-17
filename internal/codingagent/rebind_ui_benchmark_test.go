package codingagent_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func BenchmarkRuntimeInteractiveRebind(b *testing.B) {
	for _, entries := range []int{0, 1000} {
		b.Run(fmt.Sprint(entries), func(b *testing.B) {
			f := newRebindFixture(b, func(*coding.Session, ...any) (any, error) { return nil, nil }, nil)
			options := &extension.NewSessionOptions{Setup: func(manager extension.SessionManager) error {
				for range entries {
					if _, err := manager.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "restored answer"}}}}); err != nil {
						return err
					}
				}
				return nil
			}}
			testenv.EmptyPath(b)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := f.runtime.NewSession(b.Context(), options); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
